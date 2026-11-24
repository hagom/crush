package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGenerateBenchmarkDataset(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("invalid size", func(t *testing.T) {
		p := filepath.Join(tmpDir, "invalid.dat")
		if err := GenerateBenchmarkDataset(0, p); err == nil {
			t.Error("GenerateBenchmarkDataset con tamaño 0 debería fallar")
		}
		if err := GenerateBenchmarkDataset(-10, p); err == nil {
			t.Error("GenerateBenchmarkDataset con tamaño negativo debería fallar")
		}
	})

	t.Run("exact size and determinism", func(t *testing.T) {
		p1 := filepath.Join(tmpDir, "ds1.dat")
		p2 := filepath.Join(tmpDir, "ds2.dat")
		targetSize := int64(64 * 1024)

		if err := GenerateBenchmarkDataset(targetSize, p1); err != nil {
			t.Fatalf("GenerateBenchmarkDataset p1 falló: %v", err)
		}
		if err := GenerateBenchmarkDataset(targetSize, p2); err != nil {
			t.Fatalf("GenerateBenchmarkDataset p2 falló: %v", err)
		}

		fi1, err := os.Stat(p1)
		if err != nil {
			t.Fatalf("stat p1 falló: %v", err)
		}
		if fi1.Size() != targetSize {
			t.Errorf("tamaño p1 = %d, esperado = %d", fi1.Size(), targetSize)
		}

		h1 := hashFile(t, p1)
		h2 := hashFile(t, p2)
		if h1 != h2 {
			t.Errorf("los datasets generados no son deterministas: %s != %s", h1, h2)
		}
	})
}

func TestBenchmarkFormatExecution(t *testing.T) {
	tmpDir := t.TempDir()
	datasetPath := filepath.Join(tmpDir, "dataset.dat")
	if err := GenerateBenchmarkDataset(32*1024, datasetPath); err != nil {
		t.Fatalf("error creando dataset: %v", err)
	}

	res := BenchmarkFormat(datasetPath, Gz, tmpDir)
	if res.Status != "OK" {
		t.Fatalf("benchmark Gz falló: status=%s err=%s", res.Status, res.ErrorMsg)
	}
	if res.CompTime <= 0 {
		t.Errorf("CompTime debería ser > 0, obtenido %v", res.CompTime)
	}
	if res.CompSpeed <= 0 {
		t.Errorf("CompSpeed debería ser > 0, obtenido %f", res.CompSpeed)
	}
	if res.Ratio <= 0 || res.Ratio > 100 {
		t.Errorf("Ratio inesperado: %f", res.Ratio)
	}
	if res.DecompTime <= 0 {
		t.Errorf("DecompTime debería ser > 0, obtenido %v", res.DecompTime)
	}
	if res.DecompSpeed <= 0 {
		t.Errorf("DecompSpeed debería ser > 0, obtenido %f", res.DecompSpeed)
	}
	if res.FormatName != "gz" {
		t.Errorf("FormatName = %s, esperado gz", res.FormatName)
	}
}

func TestBenchmarkMissingTool(t *testing.T) {
	tmpDir := t.TempDir()
	datasetPath := filepath.Join(tmpDir, "dataset.dat")
	if err := GenerateBenchmarkDataset(16*1024, datasetPath); err != nil {
		t.Fatalf("error creando dataset: %v", err)
	}

	// Usar un formato sin herramienta o herramienta inexistente
	// Lz4 no está instalado en este sistema de test
	res := BenchmarkFormat(datasetPath, Lz4, tmpDir)
	if !hasTool("lz4") {
		if res.Status != "OMITIDO" {
			t.Errorf("esperado status OMITIDO para lz4 no instalado, obtenido: %s", res.Status)
		}
	}
}

func TestFormatBenchTableAndSummary(t *testing.T) {
	results := []BenchResult{
		{
			Format:      Gz,
			FormatName:  "gz",
			Compressor:  "pigz",
			CompTime:    100 * time.Millisecond,
			CompSpeed:   100.0,
			OrigSize:    10 * 1024 * 1024,
			CompSize:    3 * 1024 * 1024,
			Ratio:       30.0,
			DecompTime:  50 * time.Millisecond,
			DecompSpeed: 200.0,
			Status:      "OK",
		},
		{
			Format:      Zst,
			FormatName:  "zst",
			Compressor:  "zstd",
			CompTime:    50 * time.Millisecond,
			CompSpeed:   200.0,
			OrigSize:    10 * 1024 * 1024,
			CompSize:    2 * 1024 * 1024,
			Ratio:       20.0,
			DecompTime:  25 * time.Millisecond,
			DecompSpeed: 400.0,
			Status:      "OK",
		},
		{
			Format:     Lz4,
			FormatName: "lz4",
			Compressor: "lz4",
			Status:     "OMITIDO",
			ErrorMsg:   "herramienta no disponible",
		},
		{
			Format:     Br,
			FormatName: "br",
			Compressor: "brotli",
			Status:     "ERROR",
			ErrorMsg:   "fallo de ejecución",
		},
	}

	table := FormatBenchTable(results)
	expectedHeaders := []string{"FORMAT", "COMPRESSOR", "COMP TIME", "COMP SPEED", "RATIO", "DECOMP TIME", "DECOMP SPEED", "STATUS"}
	for _, h := range expectedHeaders {
		if !strings.Contains(table, h) {
			t.Errorf("tabla no contiene cabecera %s", h)
		}
	}
	if !strings.Contains(table, "pigz") || !strings.Contains(table, "zstd") {
		t.Errorf("tabla no contiene nombres de compresores esperados")
	}
	if !strings.Contains(table, "OMITIDO") || !strings.Contains(table, "ERROR") {
		t.Errorf("tabla no contiene estados OMITIDO y ERROR")
	}

	summary := FormatBenchSummary(results, 10*1024*1024)
	if !strings.Contains(summary, "zst") {
		t.Errorf("resumen debería destacar zst como más rápido o mejor ratio: %s", summary)
	}
	if !strings.Contains(summary, "400.0") && !strings.Contains(summary, "200.0") {
		t.Errorf("resumen debería incluir velocidades destacadas: %s", summary)
	}
}

func TestDoBenchCustomFile(t *testing.T) {
	tmpDir := t.TempDir()
	customFile := filepath.Join(tmpDir, "custom.txt")
	content := []byte(strings.Repeat("Crush benchmark test file content line\n", 500))
	if err := os.WriteFile(customFile, content, 0644); err != nil {
		t.Fatalf("error escribiendo custom file: %v", err)
	}

	// Ejecutar DoBench con archivo personalizado
	if err := DoBench(customFile, 0); err != nil {
		t.Fatalf("DoBench con archivo personalizado falló: %v", err)
	}
}

func TestReorderArgsBenchSize(t *testing.T) {
	args := []string{"crush", "--bench", "--bench-size", "5"}
	got := reorderArgs(args)
	want := []string{"crush", "--bench", "--bench-size", "5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reorderArgs(--bench-size 5) = %v, want %v", got, want)
	}

	args2 := []string{"crush", "custom.dat", "-bench-size", "15", "--bench"}
	got2 := reorderArgs(args2)
	want2 := []string{"crush", "-bench-size", "15", "--bench", "custom.dat"}
	if !reflect.DeepEqual(got2, want2) {
		t.Errorf("reorderArgs(-bench-size 15) = %v, want %v", got2, want2)
	}
}

func hashFile(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("abrir %s falló: %v", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("copiar %s a hash falló: %v", path, err)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// --- Standard Go Benchmarks ---

func benchmarkFormat(b *testing.B, f Format) {
	b.Helper()
	tool, avail := getBenchCompressor(f)
	if !avail {
		b.Skipf("herramienta %s para formato %s no disponible", tool, f)
	}

	tmpDir := b.TempDir()
	datasetPath := filepath.Join(tmpDir, "dataset.dat")
	if err := GenerateBenchmarkDataset(1024*1024, datasetPath); err != nil {
		b.Fatalf("error generando dataset: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := BenchmarkFormat(datasetPath, f, tmpDir)
		if res.Status != "OK" {
			b.Fatalf("benchmark %s falló: %s", f, res.ErrorMsg)
		}
	}
}

func BenchmarkCompressGz(b *testing.B) {
	benchmarkFormat(b, Gz)
}

func BenchmarkCompressZstd(b *testing.B) {
	benchmarkFormat(b, Zst)
}

func BenchmarkCompressXz(b *testing.B) {
	benchmarkFormat(b, Xz)
}

func BenchmarkCompressBz2(b *testing.B) {
	benchmarkFormat(b, Bz2)
}

func BenchmarkCompressZip(b *testing.B) {
	benchmarkFormat(b, Zip)
}

func BenchmarkCompress7z(b *testing.B) {
	benchmarkFormat(b, SevenZ)
}

func BenchmarkCompressLz(b *testing.B) {
	benchmarkFormat(b, Lz)
}

func BenchmarkCompressBz3(b *testing.B) {
	benchmarkFormat(b, Bz3)
}

func BenchmarkCompressLz4(b *testing.B) {
	benchmarkFormat(b, Lz4)
}

func BenchmarkCompressBr(b *testing.B) {
	benchmarkFormat(b, Br)
}

func TestBench_MemorySafeFlagsAndBoundedStderr(t *testing.T) {
	origMem := getMemLimit
	defer func() { getMemLimit = origMem }()
	getMemLimit = func() int { return 1024 }

	origExec := execCommand
	defer func() { execCommand = origExec }()

	tmpDir := t.TempDir()
	dataset := filepath.Join(tmpDir, "small.dat")
	if err := os.WriteFile(dataset, []byte(strings.Repeat("bench payload ", 200)), 0644); err != nil {
		t.Fatal(err)
	}
	compFile := filepath.Join(tmpDir, "out.bin")
	if err := os.WriteFile(compFile, []byte("compressed placeholder"), 0644); err != nil {
		t.Fatal(err)
	}
	decompFile := filepath.Join(tmpDir, "out.decomp")
	extractDir := filepath.Join(tmpDir, "extract")

	var capturedName string
	var capturedArgs []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		capturedName = name
		capturedArgs = append([]string(nil), args...)
		return mockExecCommand("mock-noop")
	}

	t.Run("7z compression uses adaptive7zDict and resolveThreadCap", func(t *testing.T) {
		capturedName = ""
		capturedArgs = nil
		if err := runFormatCompression(SevenZ, sevenzBin(), dataset, compFile); err != nil {
			t.Fatalf("runFormatCompression(SevenZ) failed: %v", err)
		}
		if capturedName != sevenzBin() {
			t.Fatalf("expected %s via execCommand, got %q", sevenzBin(), capturedName)
		}
		if !containsArg(capturedArgs, "-md=16m") || containsArg(capturedArgs, "-md=128m") {
			t.Errorf("expected adaptive -md=16m for small dataset, got %v", capturedArgs)
		}
		if !containsArg(capturedArgs, "-mmt=1") {
			t.Errorf("expected memory/size-capped -mmt=1, got %v", capturedArgs)
		}
	})

	t.Run("xz compression uses resolveThreadCap and --memlimit-compress", func(t *testing.T) {
		capturedName = ""
		capturedArgs = nil
		if err := runFormatCompression(Xz, "xz", dataset, compFile); err != nil {
			t.Fatalf("runFormatCompression(Xz) failed: %v", err)
		}
		if !containsArg(capturedArgs, "-T1") || containsArg(capturedArgs, "-T0") {
			t.Errorf("expected -T1 (not -T0) for xz bench compression, got %v", capturedArgs)
		}
		if !containsArg(capturedArgs, "--memlimit-compress=1024MiB") {
			t.Errorf("expected --memlimit-compress=1024MiB, got %v", capturedArgs)
		}
	})

	t.Run("xz decompression uses resolveThreadCap and --memlimit-mt-decompress", func(t *testing.T) {
		capturedName = ""
		capturedArgs = nil
		if err := runFormatDecompression(Xz, "xz", compFile, decompFile, extractDir); err != nil {
			t.Fatalf("runFormatDecompression(Xz) failed: %v", err)
		}
		if !containsArg(capturedArgs, "-T1") || containsArg(capturedArgs, "-T0") {
			t.Errorf("expected -T1 (not -T0) for xz bench decompression, got %v", capturedArgs)
		}
		if !containsArg(capturedArgs, "--memlimit-mt-decompress=1024MiB") {
			t.Errorf("expected --memlimit-mt-decompress=1024MiB, got %v", capturedArgs)
		}
	})

	t.Run("stderr is bounded via limitedBuffer on failure", func(t *testing.T) {
		execCommand = func(name string, args ...string) *exec.Cmd {
			return exec.Command("sh", "-c", "head -c 131072 /dev/zero | tr '\\0' 'E' >&2; exit 1")
		}
		err := runFormatCompression(Gz, "pigz", dataset, compFile)
		if err == nil {
			t.Fatal("expected error from failing command, got nil")
		}
		if len(err.Error()) > maxStderrBytes+512 {
			t.Errorf("error message length %d exceeds bounded buffer limit (%d)", len(err.Error()), maxStderrBytes)
		}
	})
}

func TestBenchAudit_CleanupArgsAndValidation(t *testing.T) {
	t.Run("BenchmarkFormat cleans up per-format temp artifacts in workDir", func(t *testing.T) {
		datasetDir := t.TempDir()
		workDir := t.TempDir()
		datasetPath := filepath.Join(datasetDir, "dataset.dat")
		if err := GenerateBenchmarkDataset(16*1024, datasetPath); err != nil {
			t.Fatalf("GenerateBenchmarkDataset: %v", err)
		}

		res := BenchmarkFormat(datasetPath, Gz, workDir)
		if res.Status != "OK" {
			t.Fatalf("BenchmarkFormat(Gz) failed: %s: %s", res.Status, res.ErrorMsg)
		}
		entries, err := os.ReadDir(workDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Errorf("BenchmarkFormat left temporary artifacts in workDir: %v", names)
		}
	})

	t.Run("unzip decompression uses -d <dir> -- <file>", func(t *testing.T) {
		origLookPath := lookPath
		defer func() { lookPath = origLookPath }()
		lookPath = func(name string) (string, error) {
			if name == "unzip" || name == "zip" {
				return "/usr/bin/" + name, nil
			}
			return "", os.ErrNotExist
		}

		origExec := execCommand
		defer func() { execCommand = origExec }()
		var capturedTool string
		var capturedArgs []string
		execCommand = func(name string, args ...string) *exec.Cmd {
			capturedTool = name
			capturedArgs = append([]string(nil), args...)
			return mockExecCommand("mock-noop")
		}

		tmpDir := t.TempDir()
		compFile := filepath.Join(tmpDir, "bench.zip")
		if err := os.WriteFile(compFile, []byte("zip"), 0644); err != nil {
			t.Fatal(err)
		}
		extractDir := filepath.Join(tmpDir, "extract")

		if err := runFormatDecompression(Zip, "zip", compFile, filepath.Join(tmpDir, "out.decomp"), extractDir); err != nil {
			t.Fatalf("runFormatDecompression(Zip) failed: %v", err)
		}
		if capturedTool != "unzip" {
			t.Fatalf("expected unzip, got %q", capturedTool)
		}
		wantSeq := []string{"-d", extractDir, "--", compFile}
		found := false
		for i := 0; i+len(wantSeq) <= len(capturedArgs); i++ {
			if reflect.DeepEqual(capturedArgs[i:i+len(wantSeq)], wantSeq) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("unzip args %v missing sequence %v", capturedArgs, wantSeq)
		}
	})

	t.Run("runFormatDecompression returns error when extractDir creation fails", func(t *testing.T) {
		origExec := execCommand
		defer func() { execCommand = origExec }()
		called := false
		execCommand = func(name string, args ...string) *exec.Cmd {
			called = true
			return mockExecCommand("mock-noop")
		}

		tmpDir := t.TempDir()
		compFile := filepath.Join(tmpDir, "bench.7z")
		if err := os.WriteFile(compFile, []byte("7z"), 0644); err != nil {
			t.Fatal(err)
		}
		blockingFile := filepath.Join(tmpDir, "not_a_dir")
		if err := os.WriteFile(blockingFile, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		badExtractDir := filepath.Join(blockingFile, "sub")

		if err := runFormatDecompression(SevenZ, sevenzBin(), compFile, filepath.Join(tmpDir, "d"), badExtractDir); err == nil {
			t.Errorf("expected MkdirAll error for blocked extractDir, got nil")
		}
		if called {
			t.Errorf("external extractor should not be invoked when MkdirAll fails")
		}
	})

	t.Run("DoBench rejects non-regular FIFO without blocking", func(t *testing.T) {
		tmpDir := t.TempDir()
		fifo := filepath.Join(tmpDir, "bench_fifo")
		if err := syscall.Mkfifo(fifo, 0600); err != nil {
			t.Skipf("mkfifo unavailable: %v", err)
		}

		errCh := make(chan error, 1)
		go func() {
			errCh <- DoBench(fifo, 0)
		}()

		select {
		case err := <-errCh:
			if err == nil {
				t.Errorf("DoBench(FIFO) expected error, got nil")
			}
		case <-time.After(300 * time.Millisecond):
			if w, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
				_ = w.Close()
			}
			t.Fatalf("DoBench blocked on FIFO instead of rejecting non-regular file")
		}
	})

	t.Run("DoBench rejects overflowing sizeMB", func(t *testing.T) {
		badMBs := []int{math.MaxInt}
		const maxSafeMB = int64(math.MaxInt64) / (1024 * 1024)
		if int64(math.MaxInt) > maxSafeMB {
			badMBs = append(badMBs, int(maxSafeMB+1))
			if int64(math.MaxInt) > (int64(1)<<44)+1 {
				badMBs = append(badMBs, int((int64(1)<<44)+1))
			}
		}
		for _, badMB := range badMBs {
			if err := DoBench("", badMB); err == nil {
				t.Errorf("DoBench with overflowing sizeMB=%d expected error, got nil", badMB)
			}
		}
	})
}

