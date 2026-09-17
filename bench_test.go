package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
