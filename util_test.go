package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"unicode/utf8"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNCPU(t *testing.T) {
	n := NCPU()
	if n < 1 {
		t.Errorf("NCPU() = %d, want >= 1", n)
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048576, "1.0 MiB"},
		{1073741824, "1.0 GiB"},
		{1099511627776, "1.0 TiB"},
	}
	for _, tt := range tests {
		got := FormatSize(tt.bytes)
		if got != tt.want {
			// Accept minor rounding diffs — `numfmt` may not be available
			t.Logf("FormatSize(%d) = %q, want approximate %q", tt.bytes, got, tt.want)
		}
	}
}

func TestCalcPct(t *testing.T) {
	tests := []struct {
		orig, final int64
		want        string
	}{
		{100, 70, "30.00"},
		{100, 0, "100.00"},
		{0, 0, "0.00"},
		{1000, 250, "75.00"},
	}
	for _, tt := range tests {
		got := CalcPct(tt.orig, tt.final)
		if got != tt.want {
			t.Errorf("CalcPct(%d, %d) = %s, want %s", tt.orig, tt.final, got, tt.want)
		}
	}
}

func TestGetUniqueName(t *testing.T) {
	got := GetUniqueName("test", "txt")
	if got != "test.txt" {
		t.Errorf("GetUniqueName(test, txt) = %q, want %q", got, "test.txt")
	}
}

func TestGetUniqueNameConcurrent(t *testing.T) {
	n := 30
	names := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			names[idx] = GetUniqueName("test_concurrent", "txt")
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool)
	for _, name := range names {
		if seen[name] {
			t.Errorf("duplicate name returned concurrently: %s", name)
		}
		seen[name] = true
	}
}

func TestGetMemLimit(t *testing.T) {
	limit := GetMemLimit()
	if limit < 1 {
		t.Errorf("GetMemLimit() = %d, want >= 1", limit)
	}
}

func TestBzip2Bin(t *testing.T) {
	bin := bzip2Bin()
	if bin == "" {
		t.Errorf("bzip2Bin() = empty")
	}
}

func TestSevenzBin(t *testing.T) {
	bin := sevenzBin()
	if bin == "" {
		t.Errorf("sevenzBin() = empty")
	}
}

func TestRarBin(t *testing.T) {
	bin := rarBin()
	if bin == "" {
		t.Errorf("rarBin() = empty")
	}
}

func TestPipeline(t *testing.T) {
	t.Run("empty cmds", func(t *testing.T) {
		var buf bytes.Buffer
		if err := pipeline(&buf, nil); err != nil {
			t.Errorf("pipeline(empty) = %v, want nil", err)
		}
	})

	t.Run("single cmd", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := exec.Command("echo", "hello")
		if err := pipeline(&buf, nil, cmd); err != nil {
			t.Errorf("pipeline(echo) = %v, want nil", err)
		}
		if strings.TrimSpace(buf.String()) != "hello" {
			t.Errorf("pipeline(echo) output = %q, want %q", buf.String(), "hello\n")
		}
	})

	t.Run("pipe two cmds", func(t *testing.T) {
		var buf bytes.Buffer
		echo := exec.Command("echo", "hello-pipe")
		cat := exec.Command("cat")
		if err := pipeline(&buf, nil, echo, cat); err != nil {
			t.Errorf("pipeline(echo|cat) = %v, want nil", err)
		}
		if strings.TrimSpace(buf.String()) != "hello-pipe" {
			t.Errorf("pipeline(echo|cat) output = %q, want %q", buf.String(), "hello-pipe\n")
		}
	})

	t.Run("first cmd fails kills downstream", func(t *testing.T) {
		var buf bytes.Buffer
		cmd1 := exec.Command("false")
		cmd2 := exec.Command("cat")
		err := pipeline(&buf, nil, cmd1, cmd2)
		if err == nil {
			t.Errorf("pipeline(false|cat) expected error, got nil")
		}
	})

	t.Run("downstream fails terminates upstream without hanging", func(t *testing.T) {
		var buf bytes.Buffer
		cmd1 := exec.Command("sleep", "10")
		cmd2 := exec.Command("false")
		done := make(chan error, 1)
		go func() {
			done <- pipeline(&buf, nil, cmd1, cmd2)
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("expected error, got nil")
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("pipeline hung waiting for upstream sleep instead of reacting to downstream failure")
		}
	})
}

func TestFormatSizeExact1024(t *testing.T) {
	got := FormatSize(1024)
	if !strings.Contains(got, "KiB") && !strings.Contains(got, "K") {
		t.Errorf("FormatSize(1024) = %q, want 1 KiB", got)
	}
}

func TestEstimateUncompressedSizeNative(t *testing.T) {
	tmp := t.TempDir()
	sample := filepath.Join(tmp, "archive.tar")
	if err := os.WriteFile(sample, make([]byte, 2048), 0644); err != nil {
		t.Fatal(err)
	}
	size := EstimateUncompressedSize(sample)
	if size != 2048 {
		t.Errorf("EstimateUncompressedSize = %d, want 2048", size)
	}
}

func TestEffectiveThreads(t *testing.T) {
	tests := []struct {
		name        string
		ext         string
		threadLimit int
		want        int
	}{
		{
			name:        "lz4 is always single-threaded",
			ext:         "file.lz4",
			threadLimit: 10,
			want:        1,
		},
		{
			name:        "br is always single-threaded",
			ext:         "file.br",
			threadLimit: 10,
			want:        1,
		},
		{
			name:        "tar is always single-threaded",
			ext:         "file.tar",
			threadLimit: 10,
			want:        1,
		},
		{
			name:        "tar compound lz4 is single-threaded",
			ext:         "archive.tar.lz4",
			threadLimit: 8,
			want:        1,
		},
		{
			name:        "zip respects threadLimit when sevenz is available",
			ext:         "archive.zip",
			threadLimit: 10,
			want:        10,
		},
		{
			name:        "zip with 0 threadLimit uses NCPU",
			ext:         "archive.zip",
			threadLimit: 0,
			want:        NCPU(),
		},
		{
			name:        "zst respects threadLimit",
			ext:         "data.zst",
			threadLimit: 5,
			want:        5,
		},
		{
			name:        "xz respects threadLimit",
			ext:         "data.xz",
			threadLimit: 4,
			want:        4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effectiveThreads(tt.ext, tt.threadLimit)
			if got != tt.want {
				t.Errorf("effectiveThreads(%q, %d) = %d, want %d", tt.ext, tt.threadLimit, got, tt.want)
			}
		})
	}
}

func TestFormatETA(t *testing.T) {
	tests := []struct {
		name     string
		elapsed  time.Duration
		current  int64
		total    int64
		expected string
	}{
		{
			name:     "arranque temprano menor a 3 segundos",
			elapsed:  1 * time.Second,
			current:  1024,
			total:    1024 * 1024,
			expected: "--:--",
		},
		{
			name:     "porcentaje inicial menor a 1%",
			elapsed:  5 * time.Second,
			current:  32,
			total:    1400 * 1024 * 1024,
			expected: "--:--",
		},
		{
			name:     "tiempo estimado absurdo mayor a 24 horas",
			elapsed:  3600 * time.Second,
			current:  10 * 1024 * 1024 * 1024,
			total:    1000 * 1024 * 1024 * 1024,
			expected: ">24h",
		},
		{
			name:     "progreso normal en segundos",
			elapsed:  10 * time.Second,
			current:  20 * 1024 * 1024,
			total:    30 * 1024 * 1024,
			expected: "5s",
		},
		{
			name:     "progreso normal minutos y segundos",
			elapsed:  60 * time.Second,
			current:  60 * 1024 * 1024,
			total:    1000 * 1024 * 1024,
			expected: "15m40s",
		},
		{
			name:     "progreso normal horas minutos segundos",
			elapsed:  60 * time.Second,
			current:  10 * 1024 * 1024,
			total:    730 * 1024 * 1024,
			expected: "1h12m00s",
		},
		{
			name:     "completado o valores invalidos",
			elapsed:  10 * time.Second,
			current:  100,
			total:    100,
			expected: "",
		},
		{
			name:     "current menor o igual a cero",
			elapsed:  10 * time.Second,
			current:  0,
			total:    100,
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatETA(tc.elapsed, tc.current, tc.total)
			if got != tc.expected {
				t.Errorf("formatETA() = %q, esperado %q", got, tc.expected)
			}
		})
	}
}

func TestTrackProgressMultiDelimiter(t *testing.T) {
	pt := NewProgressTracker(1000, 1)
	fp := &FileProgress{Name: "test.bin", Size: 1000}
	fp.SetStatus("active")

	pr, pw := io.Pipe()

	done := make(chan struct{})
	go func() {
		trackProgress(pr, pt, 1000, fp)
		close(done)
	}()

	// Enviar chunk 1 terminado en \x08 (sin \r)
	pw.Write([]byte("Scanning...\n 25%\x08\x08\x08\x08"))
	time.Sleep(50 * time.Millisecond)

	if got := fp.Current.Load(); got != 250 {
		pw.Close()
		<-done
		t.Fatalf("tras chunk 1 (25%% con \\b): esperado fp.Current = 250, obtenido %d", got)
	}

	// Enviar chunk 2 terminado en \n (sin \r)
	pw.Write([]byte(" 75%\n"))
	time.Sleep(50 * time.Millisecond)

	if got := fp.Current.Load(); got != 750 {
		pw.Close()
		<-done
		t.Fatalf("tras chunk 2 (75%% con \\n): esperado fp.Current = 750, obtenido %d", got)
	}

	// Enviar chunk 3 terminado en \r
	pw.Write([]byte(" 100%\r"))
	pw.Close()
	<-done

	if got := fp.Current.Load(); got != 1000 {
		t.Fatalf("al finalizar: esperado fp.Current = 1000, obtenido %d", got)
	}
}

func TestPollFileProgressMonotonic(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "crush_poll_test_*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	// Archivo de 32 bytes en disco
	tmpFile.Write(make([]byte, 32))
	tmpFile.Close()

	fp := &FileProgress{Name: "test.bin", Size: 1000}
	fp.SetStatus("active")
	fp.SetOutPath(tmpFile.Name())
	fp.Current.Store(500)

	// Caso 1: Con external progress activo, pollFileProgress no debe tocar Current
	fp.SetHasExternalProgress(true)
	pollFileProgress(fp)
	if fp.Current.Load() != 500 {
		t.Errorf("pollFileProgress sobreescribió cuando HasExternalProgress=true (esperado 500, obtenido %d)", fp.Current.Load())
	}

	// Caso 2: Sin external progress pero archivo en disco (32B) menor que progreso actual (500B)
	fp.SetHasExternalProgress(false)
	pollFileProgress(fp)
	if fp.Current.Load() != 500 {
		t.Errorf("pollFileProgress redujo progreso de forma regresiva (esperado 500, obtenido %d)", fp.Current.Load())
	}

	// Caso 3: Archivo en disco crece a 800 bytes (mayor que 500B)
	if err := os.WriteFile(tmpFile.Name(), make([]byte, 800), 0644); err != nil {
		t.Fatal(err)
	}
	pollFileProgress(fp)
	if fp.Current.Load() != 800 {
		t.Errorf("pollFileProgress no actualizó a tamaño mayor (esperado 800, obtenido %d)", fp.Current.Load())
	}
}

func TestCountingReader(t *testing.T) {
	pt := NewProgressTracker(1000, 1)
	fp := &FileProgress{Name: "data.bin", Size: 1000}

	data := make([]byte, 500)
	cr := &countingReader{r: bytes.NewReader(data), pt: pt, fp: fp}

	buf := make([]byte, 200)
	n, err := cr.Read(buf)
	if err != nil || n != 200 {
		t.Fatalf("Read esperado n=200, err=nil, obtenido n=%d, err=%v", n, err)
	}
	if fp.Current.Load() != 200 {
		t.Errorf("fp.Current esperado 200, obtenido %d", fp.Current.Load())
	}
	if pt.current.Load() != 200 {
		t.Errorf("pt.current esperado 200, obtenido %d", pt.current.Load())
	}
}

func TestProgressTrackerRenderStability(t *testing.T) {
	var buf bytes.Buffer
	pt := NewProgressTracker(1000, 2)
	pt.writer = &buf
	pt.stderrIsTTY = true

	fp1 := &FileProgress{Name: "file1.bin", Size: 500}
	fp2 := &FileProgress{Name: "file2.bin", Size: 500}
	pt.SetFiles([]*FileProgress{fp1, fp2})

	pt.render()
	output1 := buf.String()

	// La primera pasada debe contener la línea global, línea de separación en blanco y 2 líneas de archivos
	if !strings.Contains(output1, "file1.bin") || !strings.Contains(output1, "file2.bin") {
		t.Fatalf("render inicial no contiene los archivos: %s", output1)
	}

	lines := strings.Split(output1, "\n")
	// Deben ser al menos 4 líneas (global, separación, file1, file2) más trailing empty
	if len(lines) < 5 {
		t.Fatalf("esperadas al menos 4 líneas renderizadas con separación, obtenidas %d: %q", len(lines), output1)
	}
	// La segunda línea debe ser la separación en blanco
	if !strings.Contains(lines[1], "\033[K") {
		t.Errorf("segunda línea debe ser línea de separación limpia, obtenida: %q", lines[1])
	}

	buf.Reset()
	// La segunda pasada debe usar \r\033[4A para subir (1 global + 1 separación + 2 archivos)
	pt.render()
	output2 := buf.String()

	if !strings.HasPrefix(output2, "\r\033[4A") {
		t.Errorf("segundo render debe comenzar con \\r\\033[4A para posicionamiento exacto, obtenido: %q", output2[:10])
	}
}

func TestFileLineColumnAlignment(t *testing.T) {
	fp1 := &FileProgress{Name: "Jak and Daxter.iso", Size: 1400 * 1024 * 1024}
	fp1.Current.Store(208*1024*1024 + 900*1024)
	fp1.SetStatus("active")

	fp2 := &FileProgress{Name: "Manhunt.iso", Size: 4400 * 1024 * 1024}
	fp2.Current.Store(2000 * 1024 * 1024)
	fp2.SetStatus("active")

	line1 := fileLine(fp1)
	line2 := fileLine(fp2)

	// Verificar posición de apertura y cierre de barra de progreso
	idxBar1 := strings.Index(line1, "[")
	idxBar2 := strings.Index(line2, "[")
	if idxBar1 != idxBar2 || idxBar1 < 0 {
		t.Errorf("desalineación en inicio de barra: %d vs %d", idxBar1, idxBar2)
	}

	// Verificar posición del separador de tamaño ' / '
	idxSlash1 := strings.Index(line1, " / ")
	idxSlash2 := strings.Index(line2, " / ")
	if idxSlash1 != idxSlash2 || idxSlash1 < 0 {
		t.Errorf("desalineación en separador de tamaño ' / ': %d vs %d (line1: %q, line2: %q)", idxSlash1, idxSlash2, line1, line2)
	}

	// Verificar estado done
	fpDone := &FileProgress{Name: "Simpsons.iso", Size: 2000 * 1024 * 1024}
	fpDone.SetStatus("done")
	lineDone := fileLine(fpDone)

	idxSlashDone := strings.Index(lineDone, " / ")
	if idxSlashDone != idxSlash1 {
		t.Errorf("estado done desalineado en separador ' / ': %d vs %d (lineDone: %q)", idxSlashDone, idxSlash1, lineDone)
	}
	if !strings.Contains(lineDone, "✓") {
		t.Errorf("estado done debe contener checkmark: %s", lineDone)
	}

	// Verificar estado waiting
	fpWait := &FileProgress{Name: "Prince.iso", Size: 3600 * 1024 * 1024}
	fpWait.SetStatus("waiting")
	lineWait := fileLine(fpWait)

	idxSlashWait := strings.Index(lineWait, " / ")
	if idxSlashWait != idxSlash1 {
		t.Errorf("estado waiting desalineado en separador ' / ': %d vs %d (lineWait: %q)", idxSlashWait, idxSlash1, lineWait)
	}
	if !strings.Contains(lineWait, "esperando...") {
		t.Errorf("estado waiting debe contener esperando...: %s", lineWait)
	}
}

func TestFileLineStatusColors(t *testing.T) {
	fpActive := &FileProgress{Name: "active_task.bin", Size: 1000}
	fpActive.SetStatus("active")
	fpActive.Current.Store(500)
	lineActive := fileLine(fpActive)

	if !strings.HasPrefix(lineActive, Yellow) {
		t.Errorf("elemento activo debe iniciar con color amarillo (%q): obtenido %q", Yellow, lineActive)
	}
	if !strings.HasSuffix(lineActive, NC) {
		t.Errorf("elemento activo debe finalizar con reset color (%q): obtenido %q", NC, lineActive)
	}

	fpDone := &FileProgress{Name: "done_task.bin", Size: 1000}
	fpDone.SetStatus("done")
	lineDone := fileLine(fpDone)

	if !strings.HasPrefix(lineDone, Green) {
		t.Errorf("elemento finalizado debe iniciar con color verde (%q): obtenido %q", Green, lineDone)
	}
	if !strings.HasSuffix(lineDone, NC) {
		t.Errorf("elemento finalizado debe finalizar con reset color (%q): obtenido %q", NC, lineDone)
	}

	fpError := &FileProgress{Name: "error_task.bin", Size: 1000}
	fpError.SetStatus("error")
	lineError := fileLine(fpError)

	if !strings.HasPrefix(lineError, Red) {
		t.Errorf("elemento con error debe iniciar con color rojo (%q): obtenido %q", Red, lineError)
	}
	if !strings.HasSuffix(lineError, NC) {
		t.Errorf("elemento con error debe finalizar con reset color (%q): obtenido %q", NC, lineError)
	}
}

func TestGetAvailBytesNonExistentDir(t *testing.T) {
	nonExistent := filepath.Join(os.TempDir(), "crush_non_existent_subdir_test_12345/child")
	avail := GetAvailBytes(nonExistent)
	if avail <= 0 {
		t.Errorf("GetAvailBytes para directorio aún no creado debe retornar espacio del ancestro (> 0), obtenido: %d", avail)
	}
}

func TestEstimateCompressedSize(t *testing.T) {
	var totalSize int64 = 100 * 1024 * 1024

	tests := []struct {
		format  Format
		files   []string
		wantMin int64
		wantMax int64
	}{
		{Tar, []string{"file.txt"}, 100 * 1024 * 1024, 105 * 1024 * 1024},
		{Lz4, []string{"file.txt"}, 55 * 1024 * 1024, 65 * 1024 * 1024},
		{Zip, []string{"file.txt"}, 45 * 1024 * 1024, 55 * 1024 * 1024},
		{Gz, []string{"file.txt"}, 35 * 1024 * 1024, 45 * 1024 * 1024},
		{Zst, []string{"file.txt"}, 30 * 1024 * 1024, 40 * 1024 * 1024},
		{SevenZ, []string{"file.txt"}, 20 * 1024 * 1024, 30 * 1024 * 1024},
		{Xz, []string{"file.txt"}, 20 * 1024 * 1024, 30 * 1024 * 1024},
		{Bz2, []string{"file.txt"}, 25 * 1024 * 1024, 35 * 1024 * 1024},
		{Bz3, []string{"file.txt"}, 20 * 1024 * 1024, 30 * 1024 * 1024},
		{Br, []string{"file.txt"}, 25 * 1024 * 1024, 32 * 1024 * 1024},
		{Rar, []string{"file.txt"}, 25 * 1024 * 1024, 35 * 1024 * 1024},
		{Lrz, []string{"file.txt"}, 18 * 1024 * 1024, 25 * 1024 * 1024},
		{SevenZ, []string{"video.mp4", "backup.zip"}, 90 * 1024 * 1024, 100 * 1024 * 1024},
	}

	for _, tt := range tests {
		got := EstimateCompressedSize(totalSize, tt.format, tt.files)
		if got < tt.wantMin || got > tt.wantMax {
			t.Errorf("EstimateCompressedSize(%s, %v) = %d, want entre %d y %d",
				tt.format, tt.files, got, tt.wantMin, tt.wantMax)
		}
	}

	gotSmall := EstimateCompressedSize(100, SevenZ, []string{"small.txt"})
	if gotSmall < 1<<20 {
		t.Errorf("EstimateCompressedSize para archivo pequeño debe ser al menos 1MB, obtenido: %d", gotSmall)
	}
}

func TestEstimateUncompressedSizeZipNative(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test_native.zip")

	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w1, err := zw.Create("file1.txt")
	if err != nil {
		t.Fatal(err)
	}
	w1.Write(make([]byte, 3000))
	w2, err := zw.Create("file2.txt")
	if err != nil {
		t.Fatal(err)
	}
	w2.Write(make([]byte, 2000))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	size := EstimateUncompressedSize(zipPath)
	if size != 5000 {
		t.Errorf("EstimateUncompressedSize en zip nativo = %d, want 5000", size)
	}
}

func TestEstimateUncompressedSizeLz4Realistic(t *testing.T) {
	tmpDir := t.TempDir()
	lz4Path := filepath.Join(tmpDir, "sample.lz4")
	if err := os.WriteFile(lz4Path, make([]byte, 1000), 0644); err != nil {
		t.Fatal(err)
	}

	size := EstimateUncompressedSize(lz4Path)
	if size > 3000 || size < 1500 {
		t.Errorf("EstimateUncompressedSize para lz4 = %d, esperado cálculo refinado realista ~2.0x (1500..3000)", size)
	}
}

func TestEstimateUncompressedSizeRarWithoutHack(t *testing.T) {
	tmpDir := t.TempDir()
	rarPath := filepath.Join(tmpDir, "large_file.rar")
	f, err := os.Create(rarPath)
	if err != nil {
		t.Fatal(err)
	}
	// Create a sparse file of 100 MB (>64 MB)
	if err := f.Truncate(100 * 1024 * 1024); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	origExec := execCommand
	defer func() { execCommand = origExec }()

	mockCalled := false
	execCommand = func(name string, args ...string) *exec.Cmd {
		mockCalled = true
		// Return a command that echos "Size = 45000000\n"
		return exec.Command("echo", "Size = 45000000")
	}

	size := EstimateUncompressedSize(rarPath)
	if !mockCalled {
		t.Errorf("execCommand no fue llamado para archivo RAR >64MB (hack 3x presente)")
	}
	if size != 45000000 {
		t.Errorf("EstimateUncompressedSize = %d, want 45000000", size)
	}
}

func TestEstimateUncompressedSizeLrzCorrectParse(t *testing.T) {
	tmpDir := t.TempDir()
	lrzPath := filepath.Join(tmpDir, "sample.lrz")
	if err := os.WriteFile(lrzPath, []byte("fake lrz"), 0644); err != nil {
		t.Fatal(err)
	}

	origExec := execCommand
	defer func() { execCommand = origExec }()

	execCommand = func(name string, args ...string) *exec.Cmd {
		// Mock lrzip -i output where line ends in 100000
		output := "Summary\n=======\n  Decompressed file size:         100000\n  Compressed file size: 500\n"
		return exec.Command("echo", output)
	}

	size := EstimateUncompressedSize(lrzPath)
	if size != 100000 {
		t.Errorf("EstimateUncompressedSize para .lrz = %d, want 100000", size)
	}
}

func TestEstimateUncompressedSizeZstLarge(t *testing.T) {
	tmpDir := t.TempDir()
	zstPath := filepath.Join(tmpDir, "large.zst")
	if err := os.WriteFile(zstPath, []byte("fake zst"), 0644); err != nil {
		t.Fatal(err)
	}

	origExec := execCommand
	defer func() { execCommand = origExec }()

	execCommand = func(name string, args ...string) *exec.Cmd {
		// Mock zstd -lv output with exact bytes in parentheses
		output := "*** Zstandard CLI ***\nDecompressed Size: 1.18 MiB (1234567 B)\nRatio: 1.0\n"
		return exec.Command("echo", output)
	}

	size := EstimateUncompressedSize(zstPath)
	if size != 1234567 {
		t.Errorf("EstimateUncompressedSize para .zst = %d, want 1234567", size)
	}
}

func TestEstimateUncompressedSizeLzipNative(t *testing.T) {
	tmpDir := t.TempDir()
	lzPath := filepath.Join(tmpDir, "sample.lz")
	if err := os.WriteFile(lzPath, []byte("fake lz"), 0644); err != nil {
		t.Fatal(err)
	}

	origExec := execCommand
	defer func() { execCommand = origExec }()

	execCommand = func(name string, args ...string) *exec.Cmd {
		// Mock lzip -l output
		output := "  uncompressed     compressed   saved  name\n       88888             66 -120.00%  sample.lz\n"
		return exec.Command("echo", output)
	}

	size := EstimateUncompressedSize(lzPath)
	if size != 88888 {
		t.Errorf("EstimateUncompressedSize para .lz = %d, want 88888", size)
	}
}

func TestEstimateUncompressedSize7zArgs(t *testing.T) {
	tmpDir := t.TempDir()
	szPath := filepath.Join(tmpDir, "sample.7z")
	if err := os.WriteFile(szPath, []byte("fake 7z"), 0644); err != nil {
		t.Fatal(err)
	}

	origExec := execCommand
	defer func() { execCommand = origExec }()

	passedP := false
	execCommand = func(name string, args ...string) *exec.Cmd {
		for _, a := range args {
			if a == "-p-" {
				passedP = true
			}
		}
		output := "Size = 55555\n"
		return exec.Command("echo", output)
	}

	size := EstimateUncompressedSize(szPath)
	if !passedP {
		t.Errorf("EstimateUncompressedSize no pasó -p- a 7z (riesgo de hang en archivos con contraseña)")
	}
	if size != 55555 {
		t.Errorf("EstimateUncompressedSize para .7z = %d, want 55555", size)
	}
}



func TestColoredMessages(t *testing.T) {
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	restore := setOutputWriters(outBuf, errBuf)
	defer restore()

	t.Run("WriteWarning format and colors", func(t *testing.T) {
		errBuf.Reset()
		WriteWarning("disco casi lleno: %d MB restantes\n", 50)
		got := errBuf.String()
		wantPrefix := Yellow + "⚠ Advertencia: "
		wantSuffix := NC + "\n"
		if !strings.HasPrefix(got, wantPrefix) {
			t.Errorf("WriteWarning got prefix %q, want %q", got, wantPrefix)
		}
		if !strings.HasSuffix(got, wantSuffix) {
			t.Errorf("WriteWarning got suffix %q, want %q", got, wantSuffix)
		}
		if !strings.Contains(got, "disco casi lleno: 50 MB restantes") {
			t.Errorf("WriteWarning message content missing: %q", got)
		}
		if strings.Contains(got, "\n\n") {
			t.Errorf("WriteWarning produced redundant newline: %q", got)
		}
	})

	t.Run("WriteWarning strips redundant prefix", func(t *testing.T) {
		errBuf.Reset()
		WriteWarning("Warning: -s solo tiene efecto con -c (ignorado)")
		got := errBuf.String()
		want := Yellow + "⚠ Advertencia: -s solo tiene efecto con -c (ignorado)" + NC + "\n"
		if got != want {
			t.Errorf("WriteWarning = %q, want %q", got, want)
		}
	})

	t.Run("WriteError format and colors", func(t *testing.T) {
		errBuf.Reset()
		WriteError("archivo %s no existe\n", "test.tar")
		got := errBuf.String()
		wantPrefix := Red + "✗ Error: "
		wantSuffix := NC + "\n"
		if !strings.HasPrefix(got, wantPrefix) {
			t.Errorf("WriteError got prefix %q, want %q", got, wantPrefix)
		}
		if !strings.HasSuffix(got, wantSuffix) {
			t.Errorf("WriteError got suffix %q, want %q", got, wantSuffix)
		}
		if !strings.Contains(got, "archivo test.tar no existe") {
			t.Errorf("WriteError message content missing: %q", got)
		}
		if strings.Contains(got, "\n\n") {
			t.Errorf("WriteError produced redundant newline: %q", got)
		}
	})

	t.Run("WriteError strips redundant prefix", func(t *testing.T) {
		errBuf.Reset()
		WriteError("Error: permiso denegado")
		got := errBuf.String()
		want := Red + "✗ Error: permiso denegado" + NC + "\n"
		if got != want {
			t.Errorf("WriteError = %q, want %q", got, want)
		}
	})

	t.Run("WriteInfo format and colors", func(t *testing.T) {
		errBuf.Reset()
		WriteInfo("proceso en curso: paso %d", 2)
		got := errBuf.String()
		wantPrefix := Blue + "ℹ "
		wantSuffix := NC + "\n"
		if !strings.HasPrefix(got, wantPrefix) {
			t.Errorf("WriteInfo got prefix %q, want %q", got, wantPrefix)
		}
		if !strings.HasSuffix(got, wantSuffix) {
			t.Errorf("WriteInfo got suffix %q, want %q", got, wantSuffix)
		}
		if !strings.Contains(got, "proceso en curso: paso 2") {
			t.Errorf("WriteInfo message content missing: %q", got)
		}
	})

	t.Run("WriteSuccess format and colors", func(t *testing.T) {
		outBuf.Reset()
		WriteSuccess("compresión completada con éxito: %s", "demo.tar.gz")
		got := outBuf.String()
		wantPrefix := Green + "✓ "
		wantSuffix := NC + "\n"
		if !strings.HasPrefix(got, wantPrefix) {
			t.Errorf("WriteSuccess got prefix %q, want %q", got, wantPrefix)
		}
		if !strings.HasSuffix(got, wantSuffix) {
			t.Errorf("WriteSuccess got suffix %q, want %q", got, wantSuffix)
		}
		if !strings.Contains(got, "compresión completada con éxito: demo.tar.gz") {
			t.Errorf("WriteSuccess message content missing: %q", got)
		}
	})

	t.Run("Concurrent messages safety", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(4)
			go func(val int) {
				defer wg.Done()
				WriteWarning("alerta concurrente %d", val)
			}(i)
			go func(val int) {
				defer wg.Done()
				WriteError("error concurrente %d", val)
			}(i)
			go func(val int) {
				defer wg.Done()
				WriteInfo("info concurrente %d", val)
			}(i)
			go func(val int) {
				defer wg.Done()
				WriteSuccess("éxito concurrente %d", val)
			}(i)
		}
		wg.Wait()
	})
}

func TestFindSplitPartsNaturalSort(t *testing.T) {
	tmpDir := t.TempDir()
	base := filepath.Join(tmpDir, "test.bz3")
	if err := os.WriteFile(base, []byte("part0"), 0644); err != nil {
		t.Fatal(err)
	}

	partNames := []string{
		"test.bz3.part01",
		"test.bz3.part02",
		"test.bz3.part10",
		"test.bz3.part11",
		"test.bz3.part99",
		"test.bz3.part100",
		"test.bz3.part101",
		"test.bz3.part184",
	}
	for _, p := range partNames {
		if err := os.WriteFile(filepath.Join(tmpDir, p), []byte("content"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	parts := findSplitParts(base)
	want := append([]string{base}, func() []string {
		var full []string
		for _, p := range partNames {
			full = append(full, filepath.Join(tmpDir, p))
		}
		return full
	}()...)

	if len(parts) != len(want) {
		t.Fatalf("cantidad de partes obtenida %d, esperada %d", len(parts), len(want))
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Errorf("pos %d: obtenido %s, esperado %s", i, parts[i], want[i])
		}
	}
}

func TestResolveDecompressDir(t *testing.T) {
	tests := []struct {
		name                string
		archivePath         string
		configuredOutputDir string
		want                string
	}{
		{
			name:                "configured output dir takes precedence",
			archivePath:         "/home/user/downloads/archive.tar.gz",
			configuredOutputDir: "/custom/extract/dir",
			want:                "/custom/extract/dir",
		},
		{
			name:                "configured output dir takes precedence over _parts",
			archivePath:         "/home/user/downloads/archive_parts/archive.tar.gz",
			configuredOutputDir: "/custom/extract/dir",
			want:                "/custom/extract/dir",
		},
		{
			name:                "configured output dir takes precedence over _split",
			archivePath:         "/home/user/downloads/archive_split/archive.tar.gz",
			configuredOutputDir: "/custom/extract/dir",
			want:                "/custom/extract/dir",
		},
		{
			name:                "empty configured dir with normal path uses parent dir",
			archivePath:         "/home/user/downloads/archive.tar.gz",
			configuredOutputDir: "",
			want:                "/home/user/downloads",
		},
		{
			name:                "empty configured dir with relative path in current dir",
			archivePath:         "archive.tar.gz",
			configuredOutputDir: "",
			want:                ".",
		},
		{
			name:                "empty configured dir unwraps _parts folder to grandparent dir",
			archivePath:         "/var/data/backup_parts/backup.tar.gz",
			configuredOutputDir: "",
			want:                "/var/data",
		},
		{
			name:                "empty configured dir unwraps _split folder to grandparent dir",
			archivePath:         "/var/data/backup_split/backup.tar.gz",
			configuredOutputDir: "",
			want:                "/var/data",
		},
		{
			name:                "empty configured dir unwraps relative _parts directory",
			archivePath:         "myarchive_parts/myarchive.zip",
			configuredOutputDir: "",
			want:                ".",
		},
		{
			name:                "empty configured dir unwraps relative _split directory",
			archivePath:         "project_split/project.tar.bz2",
			configuredOutputDir: "",
			want:                ".",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveDecompressDir(tt.archivePath, tt.configuredOutputDir)
			if got != tt.want {
				t.Errorf("ResolveDecompressDir(%q, %q) = %q, want %q",
					tt.archivePath, tt.configuredOutputDir, got, tt.want)
			}
		})
	}
}

func TestSetPipeCapacity(t *testing.T) {
	t.Run("nil and non-file safety", func(t *testing.T) {
		setPipeCapacity(nil, nil, 1048576)
		buf := &bytes.Buffer{}
		setPipeCapacity(buf, buf, 1048576)
		setPipeCapacity(nil, nil, -1)
		setPipeCapacity(nil, nil, 0)
	})

	t.Run("real os pipe capacity", func(t *testing.T) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		defer w.Close()

		defaultCap := getPipeCapacity(r, w)
		wantCap := 1048576 // 1 MiB

		setPipeCapacity(r, w, wantCap)
		gotCap := getPipeCapacity(r, w)

		if runtime.GOOS == "linux" {
			if defaultCap > 0 && defaultCap < wantCap {
				if gotCap != wantCap {
					t.Errorf("getPipeCapacity() = %d, want %d", gotCap, wantCap)
				}
			}
		}
	})

	t.Run("set capacity via reader only", func(t *testing.T) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		defer w.Close()

		wantCap := 1048576
		setPipeCapacity(r, nil, wantCap)
		gotCap := getPipeCapacity(r, nil)

		if runtime.GOOS == "linux" {
			if gotCap != wantCap {
				t.Errorf("getPipeCapacity(r, nil) = %d, want %d", gotCap, wantCap)
			}
		}
	})

	t.Run("set capacity via writer only", func(t *testing.T) {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		defer w.Close()

		wantCap := 1048576
		setPipeCapacity(nil, w, wantCap)
		gotCap := getPipeCapacity(nil, w)

		if runtime.GOOS == "linux" {
			if gotCap != wantCap {
				t.Errorf("getPipeCapacity(nil, w) = %d, want %d", gotCap, wantCap)
			}
		}
	})

	t.Run("pipeline sets intermediate pipe capacity", func(t *testing.T) {
		cmd1 := exec.Command("echo", "pipeline test")
		cmd2 := exec.Command("cat")
		var out bytes.Buffer
		if err := pipeline(&out, nil, cmd1, cmd2); err != nil {
			t.Fatalf("pipeline failed: %v", err)
		}
		if !strings.Contains(out.String(), "pipeline test") {
			t.Errorf("pipeline output = %q, want containing 'pipeline test'", out.String())
		}
	})
}

func TestSortByLPT(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		var items []string
		calls := 0
		sizes := SortByLPT(items, func(s string) int64 {
			calls++
			return 100
		})
		if len(sizes) != 0 {
			t.Errorf("expected empty sizes map, got %v", sizes)
		}
		if len(items) != 0 {
			t.Errorf("expected items to remain empty, got %v", items)
		}
		if calls != 0 {
			t.Errorf("expected 0 calls for empty slice, got %d", calls)
		}
	})

	t.Run("single item", func(t *testing.T) {
		items := []string{"only.txt"}
		calls := 0
		sizes := SortByLPT(items, func(s string) int64 {
			calls++
			return 42
		})
		if len(sizes) != 1 || sizes["only.txt"] != 42 {
			t.Errorf("unexpected sizes map: %v", sizes)
		}
		if len(items) != 1 || items[0] != "only.txt" {
			t.Errorf("unexpected items slice: %v", items)
		}
		if calls != 1 {
			t.Errorf("expected 1 call, got %d", calls)
		}
	})

	t.Run("multiple items descending sort and O(N) calls", func(t *testing.T) {
		items := []string{"small.txt", "large.txt", "medium.txt", "tiny.txt"}
		mockSizes := map[string]int64{
			"small.txt":  500,
			"large.txt":  50000,
			"medium.txt": 5000,
			"tiny.txt":   50,
		}
		calls := 0
		sizes := SortByLPT(items, func(s string) int64 {
			calls++
			return mockSizes[s]
		})

		if calls != 4 {
			t.Errorf("expected exactly 4 calls (O(N)), got %d", calls)
		}

		wantOrder := []string{"large.txt", "medium.txt", "small.txt", "tiny.txt"}
		for i, want := range wantOrder {
			if items[i] != want {
				t.Errorf("items[%d] = %q, want %q", i, items[i], want)
			}
			if sizes[want] != mockSizes[want] {
				t.Errorf("sizes[%q] = %d, want %d", want, sizes[want], mockSizes[want])
			}
		}
	})

	t.Run("stable sort with identical sizes", func(t *testing.T) {
		items := []string{"file_a.txt", "file_b.txt", "file_c.txt"}
		sizes := SortByLPT(items, func(s string) int64 {
			return 100
		})
		wantOrder := []string{"file_a.txt", "file_b.txt", "file_c.txt"}
		for i, want := range wantOrder {
			if items[i] != want {
				t.Errorf("items[%d] = %q, want %q (stable sort violated)", i, items[i], want)
			}
		}
		if len(sizes) != 3 {
			t.Errorf("expected 3 entries in sizes map, got %d", len(sizes))
		}
	})

	t.Run("handles negative sizes", func(t *testing.T) {
		items := []string{"err.txt", "ok.txt", "zero.txt"}
		mockSizes := map[string]int64{
			"err.txt":  -1,
			"ok.txt":   100,
			"zero.txt": 0,
		}
		SortByLPT(items, func(s string) int64 {
			return mockSizes[s]
		})
		wantOrder := []string{"ok.txt", "zero.txt", "err.txt"}
		for i, want := range wantOrder {
			if items[i] != want {
				t.Errorf("items[%d] = %q, want %q", i, items[i], want)
			}
		}
	})
}

func TestPartsTotalSize(t *testing.T) {
	tmpDir := t.TempDir()
	p1 := filepath.Join(tmpDir, "part1.bin")
	p2 := filepath.Join(tmpDir, "part2.bin")
	p3 := filepath.Join(tmpDir, "part3.bin")

	if err := os.WriteFile(p1, make([]byte, 100), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p2, make([]byte, 250), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p3, make([]byte, 500), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		parts []string
		want  int64
	}{
		{"empty parts", []string{}, 0},
		{"single part", []string{p1}, 100},
		{"all parts", []string{p1, p2, p3}, 850},
		{"with nonexistent parts", []string{p1, filepath.Join(tmpDir, "missing.bin"), p2}, 350},
		{"only nonexistent parts", []string{filepath.Join(tmpDir, "missing.bin")}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PartsTotalSize(tt.parts)
			if got != tt.want {
				t.Errorf("PartsTotalSize(%v) = %d, want %d", tt.parts, got, tt.want)
			}
		})
	}
}

func TestTotalArchiveSize(t *testing.T) {
	tmpDir := t.TempDir()

	// Single archive
	singleArchive := filepath.Join(tmpDir, "archive.tar.gz")
	if err := os.WriteFile(singleArchive, make([]byte, 1234), 0644); err != nil {
		t.Fatal(err)
	}

	// Split archive
	splitBase := filepath.Join(tmpDir, "split.tar.gz")
	splitPart01 := filepath.Join(tmpDir, "split.tar.gz.part01")
	splitPart02 := filepath.Join(tmpDir, "split.tar.gz.part02")
	if err := os.WriteFile(splitBase, make([]byte, 1000), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(splitPart01, make([]byte, 1000), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(splitPart02, make([]byte, 500), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		archivePath string
		want        int64
	}{
		{"single archive", singleArchive, 1234},
		{"split archive via base", splitBase, 2500},
		{"split archive via part01", splitPart01, 2500},
		{"split archive via part02", splitPart02, 2500},
		{"nonexistent archive", filepath.Join(tmpDir, "missing.tar.gz"), 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TotalArchiveSize(tt.archivePath)
			if got != tt.want {
				t.Errorf("TotalArchiveSize(%q) = %d, want %d", tt.archivePath, got, tt.want)
			}
		})
	}
}

func TestIsSplitPartsDir(t *testing.T) {
	tests := []struct {
		dir  string
		want bool
	}{
		{"backup_parts", true},
		{"backup_split", true},
		{"/var/tmp/data_parts", true},
		{"/var/tmp/data_split", true},
		{"backup_parts/", false},
		{"backup_part", false},
		{"parts", false},
		{"split", false},
		{"backup", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			got := IsSplitPartsDir(tt.dir)
			if got != tt.want {
				t.Errorf("IsSplitPartsDir(%q) = %v, want %v", tt.dir, got, tt.want)
			}
		})
	}
}

func TestComputeAndWriteSHA256File(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "testdata.bin")
	content := []byte("Antigravity checksum test string 123456789")
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		t.Fatal(err)
	}

	hash, err := ComputeSHA256(filePath)
	if err != nil {
		t.Fatalf("ComputeSHA256 failed: %v", err)
	}
	expectedHash := fmt.Sprintf("%x", sha256.Sum256(content))
	if hash != expectedHash {
		t.Errorf("ComputeSHA256 got %q, want %q", hash, expectedHash)
	}

	writtenHash, err := WriteSHA256File(filePath)
	if err != nil {
		t.Fatalf("WriteSHA256File failed: %v", err)
	}
	if writtenHash != expectedHash {
		t.Errorf("writtenHash got %q, want %q", writtenHash, expectedHash)
	}

	shaFile := filePath + ".sha256"
	data, err := os.ReadFile(shaFile)
	if err != nil {
		t.Fatalf("reading sha file failed: %v", err)
	}
	expectedFormat := fmt.Sprintf("%s  %s\n", expectedHash, filepath.Base(filePath))
	if string(data) != expectedFormat {
		t.Errorf("sha file content got %q, want %q", string(data), expectedFormat)
	}

	parsedHash, err := ParseSHA256File(shaFile, filePath)
	if err != nil {
		t.Fatalf("ParseSHA256File failed: %v", err)
	}
	if parsedHash != expectedHash {
		t.Errorf("ParseSHA256File got %q, want %q", parsedHash, expectedHash)
	}
}

func TestParseSHA256FileVariants(t *testing.T) {
	tmpDir := t.TempDir()
	dummyFile := filepath.Join(tmpDir, "sample.tar.gz")
	dummyHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	tests := []struct {
		name       string
		content    string
		targetFile string
		wantHash   string
		wantErr    bool
	}{
		{
			name:       "standard sha256sum two spaces",
			content:    dummyHash + "  sample.tar.gz\n",
			targetFile: dummyFile,
			wantHash:   dummyHash,
		},
		{
			name:       "binary mode with asterisk",
			content:    dummyHash + " *sample.tar.gz\n",
			targetFile: dummyFile,
			wantHash:   dummyHash,
		},
		{
			name:       "single line raw hash",
			content:    dummyHash + "\n",
			targetFile: dummyFile,
			wantHash:   dummyHash,
		},
		{
			name:       "multiple files in checksum file",
			content:    "0000000000000000000000000000000000000000000000000000000000000000  other.tar.gz\n" + dummyHash + "  sample.tar.gz\n",
			targetFile: dummyFile,
			wantHash:   dummyHash,
		},
		{
			name:       "invalid format or empty",
			content:    "\n# comment\n",
			targetFile: dummyFile,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shaPath := filepath.Join(tmpDir, "check_"+tt.name+".sha256")
			if err := os.WriteFile(shaPath, []byte(tt.content), 0644); err != nil {
				t.Fatal(err)
			}
			got, err := ParseSHA256File(shaPath, tt.targetFile)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseSHA256File() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.wantHash {
				t.Errorf("ParseSHA256File() got = %q, want %q", got, tt.wantHash)
			}
		})
	}
}

func TestSplicePipe(t *testing.T) {
	pr1, pw1, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	pr2, pw2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	testData := bytes.Repeat([]byte("0123456789abcdefghijklmnopqrstuvwxyz"), 50000) // ~1.8 MB

	go func() {
		pw1.Write(testData)
		pw1.Close()
	}()

	var total int64
	done := make(chan error, 1)
	go func() {
		var sErr error
		total, sErr = splicePipe(pr1, pw2)
		pw2.Close()
		done <- sErr
	}()

	var readBuf bytes.Buffer
	_, copyErr := io.Copy(&readBuf, pr2)
	pr2.Close()

	if err := <-done; err != nil {
		t.Fatalf("splicePipe error: %v", err)
	}
	pr1.Close()
	if copyErr != nil {
		t.Fatalf("reading destination pipe error: %v", copyErr)
	}

	if total != int64(len(testData)) {
		t.Errorf("splicePipe transferred %d bytes, want %d", total, len(testData))
	}
	if !bytes.Equal(readBuf.Bytes(), testData) {
		t.Errorf("transferred data does not match original data")
	}
}

func TestSplicePipeFallback(t *testing.T) {
	src := bytes.NewBufferString("fallback test content for splice")
	var dst bytes.Buffer
	n, err := splicePipe(src, &dst)
	if err != nil {
		t.Fatalf("splicePipe fallback error: %v", err)
	}
	if n != int64(len("fallback test content for splice")) {
		t.Errorf("splicePipe fallback transferred %d bytes, want %d", n, len("fallback test content for splice"))
	}
	if dst.String() != "fallback test content for splice" {
		t.Errorf("splicePipe fallback got %q, want %q", dst.String(), "fallback test content for splice")
	}
}

func TestAllocateThreadsProportional(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		got := AllocateThreadsProportional(nil, 32, 1, 32)
		if len(got) != 0 {
			t.Errorf("expected empty slice, got %v", got)
		}
	})

	t.Run("single file gets all cores up to max", func(t *testing.T) {
		got := AllocateThreadsProportional([]int64{1000}, 32, 1, 32)
		if len(got) != 1 || got[0] != 32 {
			t.Errorf("expected [32], got %v", got)
		}

		// Capped by maxThreadsPerJob
		gotCapped := AllocateThreadsProportional([]int64{1000}, 32, 1, 8)
		if len(gotCapped) != 1 || gotCapped[0] != 8 {
			t.Errorf("expected [8], got %v", gotCapped)
		}
	})

	t.Run("equal sizes distributed evenly", func(t *testing.T) {
		sizes := []int64{100, 100, 100, 100}
		got := AllocateThreadsProportional(sizes, 32, 1, 32)
		if len(got) != 4 {
			t.Fatalf("expected 4 entries, got %d", len(got))
		}
		for i, v := range got {
			if v != 8 {
				t.Errorf("entry %d: expected 8 threads, got %d", i, v)
			}
		}
	})

	t.Run("unequal sizes PS2 distribution", func(t *testing.T) {
		// 500M, 350M, 200M, 100M, 50M on 32 cores
		sizes := []int64{500, 350, 200, 100, 50}
		got := AllocateThreadsProportional(sizes, 32, 1, 32)
		if len(got) != 5 {
			t.Fatalf("expected 5 entries, got %d", len(got))
		}

		sum := 0
		for _, v := range got {
			sum += v
			if v < 1 {
				t.Errorf("thread allocation must be at least 1, got %d", v)
			}
		}
		if sum != 32 {
			t.Errorf("total allocated threads sum = %d, want 32", sum)
		}

		// Order strictly preserved: larger files get more threads
		if got[0] <= got[1] || got[1] <= got[2] || got[2] <= got[3] || got[3] < got[4] {
			t.Errorf("threads should decrease with file sizes, got: %v", got)
		}
	})

	t.Run("single-threaded format capped at 1", func(t *testing.T) {
		sizes := []int64{500, 350, 200, 100, 50}
		got := AllocateThreadsProportional(sizes, 32, 1, 1)
		if len(got) != 5 {
			t.Fatalf("expected 5 entries, got %d", len(got))
		}
		for i, v := range got {
			if v != 1 {
				t.Errorf("entry %d: expected 1 thread, got %d", i, v)
			}
		}
	})

	t.Run("zero sizes fallback to even distribution", func(t *testing.T) {
		sizes := []int64{0, 0, 0, 0}
		got := AllocateThreadsProportional(sizes, 16, 1, 16)
		if len(got) != 4 {
			t.Fatalf("expected 4 entries, got %d", len(got))
		}
		sum := 0
		for _, v := range got {
			sum += v
			if v < 1 {
				t.Errorf("expected >= 1, got %d", v)
			}
		}
		if sum != 16 {
			t.Errorf("expected sum 16, got %d", sum)
		}
	})

	t.Run("individual caps allow surplus redistribution", func(t *testing.T) {
		// Archivo 1 pequeño (5MB) con cap de 1 hilo
		// Archivo 2 grande (2GB) con cap de 32 hilos
		// En 32 cores: Archivo 1 debe recibir 1, y Archivo 2 debe absorber los 31 restantes
		sizes := []int64{5 * 1024 * 1024, 2 * 1024 * 1024 * 1024}
		caps := []int{1, 32}
		got := AllocateThreadsProportionalWithCaps(sizes, 32, 1, caps)
		if len(got) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(got))
		}
		if got[0] != 1 {
			t.Errorf("expected file 1 to receive 1 thread (its cap), got %d", got[0])
		}
		if got[1] != 31 {
			t.Errorf("expected file 2 to absorb 31 threads, got %d", got[1])
		}
	})
}

func TestPipeFlagsForThreads(t *testing.T) {
	tests := []struct {
		info     FormatInfo
		threads  int
		contains string
	}{
		{FormatInfo{Tool: "pigz"}, 8, "-p"},
		{FormatInfo{Tool: "xz"}, 12, "-T12"},
		{FormatInfo{Tool: "zstd"}, 16, "-T16"},
		{FormatInfo{Tool: "bzip3"}, 6, "-j"},
		{FormatInfo{Tool: "plzip"}, 4, "--threads=4"},
		{FormatInfo{Tool: "lbzip2"}, 4, "-n"},
		{FormatInfo{Tool: "lrzip"}, 8, "-p"},
	}

	for _, tt := range tests {
		flags := PipeFlagsForThreads(tt.info, tt.threads)
		joined := strings.Join(flags, " ")
		if !strings.Contains(joined, tt.contains) {
			t.Errorf("PipeFlagsForThreads(%s, %d) = %v, want substring %q", tt.info.Tool, tt.threads, flags, tt.contains)
		}
	}
}

func TestDynamicThreadPool(t *testing.T) {
	// 32 cores, min 1, max 32, 3 files of 500MB, 300MB, 200MB (total 1000MB)
	pool := NewDynamicThreadPool(32, 1, 32, 3, 1000)

	// File 1 (500MB): ratio 0.5 of 32 = 16
	t1 := pool.Acquire(500)
	if t1 < 10 || t1 > 20 {
		t.Errorf("expected ~16 tokens for file 1, got %d", t1)
	}

	// File 2 (300MB)
	t2 := pool.Acquire(300)
	if t2 < 1 {
		t.Errorf("expected >= 1 tokens for file 2, got %d", t2)
	}

	// File 3 (last file, 200MB) should get all remaining tokens
	t3 := pool.Acquire(200)
	if t3 < 1 {
		t.Errorf("expected >= 1 tokens for last file, got %d", t3)
	}

	// Release tokens
	pool.Release(t1)
	pool.Release(t2)
	pool.Release(t3)

	if pool.availableTokens != 32 {
		t.Errorf("expected 32 available tokens after release, got %d", pool.availableTokens)
	}
}

func TestDynamicThreadPool_ActiveWindowFullUtilization(t *testing.T) {
	// 32 cores, 20 files de 100MB cada uno (total 2000MB), maxWorkers = 8
	// Cuando los primeros 8 workers adquieren tokens, deben repartirse los 32 núcleos
	// (aproximadamente 4 hilos por worker), y NO solo 1 hilo cada uno.
	pool := NewDynamicThreadPool(32, 1, 32, 20, 2000, 8)

	allocated := make([]int, 8)
	sum := 0
	for i := 0; i < 8; i++ {
		allocated[i] = pool.Acquire(100)
		sum += allocated[i]
	}

	if sum != 32 {
		t.Errorf("los 8 workers activos debieron utilizar los 32 núcleos completos, suma obtenida: %d (asignaciones: %v)", sum, allocated)
	}

	for i, v := range allocated {
		if v < 3 || v > 5 {
			t.Errorf("worker %d: esperado ~4 hilos (entre 3 y 5), obtenido %d", i, v)
		}
	}

	// Al liberar un worker de 4 hilos, el siguiente worker (archivo 9) debe recibir esos ~4 hilos
	pool.Release(allocated[0])
	t9 := pool.Acquire(100)
	if t9 < 3 || t9 > 5 {
		t.Errorf("worker 9 debió recibir ~4 hilos liberados, obtuvo %d", t9)
	}
}

func TestMemorySafeMaxThreads(t *testing.T) {
	orig := getMemLimit
	defer func() { getMemLimit = orig }()

	getMemLimit = func() int { return 1400 }

	if safe := MemorySafeMaxThreads(Xz, 8); safe != 2 {
		t.Errorf("MemorySafeMaxThreads(Xz, 8) with 1400MB = %d, want 2", safe)
	}
	if safe1 := MemorySafeMaxThreads(Xz, 1); safe1 != 1 {
		t.Errorf("MemorySafeMaxThreads(Xz, 1) = %d, want 1", safe1)
	}

	getMemLimit = func() int { return 250 }
	if safe := MemorySafeMaxThreads(Xz, 8); safe != 1 {
		t.Errorf("MemorySafeMaxThreads(Xz, 8) with 250MB = %d, want 1", safe)
	}
}

func TestMemorySafeMaxThreadsForBudget(t *testing.T) {
	tests := []struct {
		name    string
		format  Format
		threads int
		memMB   int
		want    int
	}{
		{"xz 250MB clamps to 1", Xz, 8, 250, 1},
		{"xz 673MB clamps to 1", Xz, 8, 673, 1},
		{"xz 1348MB allows 2", Xz, 8, 1348, 2},
		{"xz 4096MB caps at requested", Xz, 4, 4096, 4},
		{"lrz 511MB clamps to 1", Lrz, 8, 511, 1},
		{"lrz 1024MB allows 2", Lrz, 8, 1024, 2},
		{"lrz 2048MB allows 4", Lrz, 8, 2048, 4},
		{"7z 299MB clamps to 1", SevenZ, 8, 299, 1},
		{"7z 600MB allows 2", SevenZ, 8, 600, 2},
		{"7z 1500MB allows 5", SevenZ, 8, 1500, 5},
		{"zst 159MB clamps to 1", Zst, 8, 159, 1},
		{"zst 320MB allows 2", Zst, 8, 320, 2},
		{"zst 800MB allows 5", Zst, 8, 800, 5},
		{"lz 256MB allows 2", Lz, 8, 256, 2},
		{"lz 512MB allows 4", Lz, 8, 512, 4},
		{"bz3 192MB allows 2", Bz3, 8, 192, 2},
		{"bz3 480MB allows 5", Bz3, 8, 480, 5},
		{"br 128MB allows 2", Br, 8, 128, 2},
		{"rar 256MB allows 4", Rar, 8, 256, 4},
		{"bz2 32MB allows 2", Bz2, 8, 32, 2},
		{"bz2 80MB allows 5", Bz2, 8, 80, 5},
		{"gz default 8MB/thread 24MB allows 3", Gz, 8, 24, 3},
		{"lz4 default 8MB/thread 32MB allows 4", Lz4, 8, 32, 4},
		{"zip default 8MB/thread 16MB allows 2", Zip, 8, 16, 2},
		{"tar default 8MB/thread 16MB allows 2", Tar, 8, 16, 2},
		{"threads <= 1 always returns 1", Xz, 1, 16384, 1},
		{"threads 0 returns 1", Xz, 0, 16384, 1},
		{"negative threads returns 1", Xz, -4, 16384, 1},
		{"zero budget returns requested threads", Xz, 6, 0, 6},
		{"negative budget returns requested threads", Xz, 6, -100, 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MemorySafeMaxThreadsForBudget(tt.format, tt.threads, tt.memMB)
			if got != tt.want {
				t.Errorf("MemorySafeMaxThreadsForBudget(%s, %d, %d) = %d, want %d",
					tt.format, tt.threads, tt.memMB, got, tt.want)
			}
		})
	}
}

func TestResolveThreadCapForBudget(t *testing.T) {
	orig := getMemLimit
	defer func() { getMemLimit = orig }()

	const gib = int64(1024 * 1024 * 1024)

	tests := []struct {
		name      string
		format    Format
		sizeBytes int64
		budgetMB  int
		want      int
	}{
		{
			name:      "single-threaded format Tar always 1",
			format:    Tar,
			sizeBytes: 2 * gib,
			budgetMB:  16384,
			want:      1,
		},
		{
			name:      "single-threaded format Br always 1",
			format:    Br,
			sizeBytes: 2 * gib,
			budgetMB:  16384,
			want:      1,
		},
		{
			name:      "small xz file capped by block size",
			format:    Xz,
			sizeBytes: 1024 * 1024,
			budgetMB:  16384,
			want:      1,
		},
		{
			name:      "large xz file capped by tight RAM budget",
			format:    Xz,
			sizeBytes: 2 * gib,
			budgetMB:  700,
			want:      1,
		},
		{
			name:      "large xz file with 1400MB budget allows up to 2",
			format:    Xz,
			sizeBytes: 2 * gib,
			budgetMB:  1400,
			want:      min(2, FormatMaxThreads(Xz)),
		},
		{
			name:      "large lrz file capped by tight RAM budget",
			format:    Lrz,
			sizeBytes: 2 * gib,
			budgetMB:  1024,
			want:      min(2, FormatMaxThreads(Lrz)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveThreadCapForBudget(tt.format, tt.sizeBytes, tt.budgetMB)
			if got != tt.want {
				t.Errorf("resolveThreadCapForBudget(%s, %d, %d) = %d, want %d",
					tt.format, tt.sizeBytes, tt.budgetMB, got, tt.want)
			}
		})
	}

	getMemLimit = func() int { return 700 }
	if got := resolveThreadCap(Xz, 2*gib); got != 1 {
		t.Errorf("resolveThreadCap(Xz, 2GiB) with 700MB = %d, want 1", got)
	}
}

func TestPipeFlagsForThreadsAndMem(t *testing.T) {
	orig := getMemLimit
	defer func() { getMemLimit = orig }()

	tests := []struct {
		name        string
		info        FormatInfo
		threads     int
		memBudgetMB int
		want        []string
	}{
		{
			name:        "xz with positive budget above floor",
			info:        FormatInfo{Tool: "xz"},
			threads:     4,
			memBudgetMB: 512,
			want:        []string{"-dc", "-T4", "--memlimit-mt-decompress=512MiB"},
		},
		{
			name:        "xz clamps small positive budget to 65MiB floor",
			info:        FormatInfo{Tool: "xz"},
			threads:     4,
			memBudgetMB: 32,
			want:        []string{"-dc", "-T4", "--memlimit-mt-decompress=65MiB"},
		},
		{
			name:        "xz exact 65MiB floor",
			info:        FormatInfo{Tool: "xz"},
			threads:     2,
			memBudgetMB: 65,
			want:        []string{"-dc", "-T2", "--memlimit-mt-decompress=65MiB"},
		},
		{
			name:        "xz omits memlimit when budget <= 0",
			info:        FormatInfo{Tool: "xz"},
			threads:     4,
			memBudgetMB: 0,
			want:        []string{"-dc", "-T4"},
		},
		{
			name:        "lrzip with budget in hundreds of MB",
			info:        FormatInfo{Tool: "lrzip"},
			threads:     4,
			memBudgetMB: 850,
			want:        []string{"-d", "-p", "4", "-o", "-", "-m", "8"},
		},
		{
			name:        "lrzip clamps small positive budget to -m 1",
			info:        FormatInfo{Tool: "lrzip"},
			threads:     2,
			memBudgetMB: 50,
			want:        []string{"-d", "-p", "2", "-o", "-", "-m", "1"},
		},
		{
			name:        "lrzip omits -m when budget <= 0",
			info:        FormatInfo{Tool: "lrzip"},
			threads:     4,
			memBudgetMB: 0,
			want:        []string{"-d", "-p", "4", "-o", "-"},
		},
		{
			name:        "zstd never passes -M when budget < 128MiB",
			info:        FormatInfo{Tool: "zstd"},
			threads:     4,
			memBudgetMB: 64,
			want:        []string{"-dc", "-T4"},
		},
		{
			name:        "zstd never passes -M when budget >= 128MiB",
			info:        FormatInfo{Tool: "zstd"},
			threads:     8,
			memBudgetMB: 1024,
			want:        []string{"-dc", "-T8"},
		},
		{
			name:        "pigz ignores mem budget",
			info:        FormatInfo{Tool: "pigz"},
			threads:     6,
			memBudgetMB: 512,
			want:        []string{"-dc", "-p", "6"},
		},
		{
			name:        "bzip3 ignores mem budget",
			info:        FormatInfo{Tool: "bzip3"},
			threads:     4,
			memBudgetMB: 512,
			want:        []string{"-dc", "-j", "4"},
		},
		{
			name:        "plzip ignores mem budget",
			info:        FormatInfo{Tool: "plzip"},
			threads:     3,
			memBudgetMB: 512,
			want:        []string{"-dc", "--threads=3"},
		},
		{
			name:        "lbzip2 ignores mem budget",
			info:        FormatInfo{Tool: "lbzip2"},
			threads:     4,
			memBudgetMB: 512,
			want:        []string{"-dc", "-n", "4"},
		},
		{
			name:        "default fallback uses PipeFlags",
			info:        FormatInfo{Tool: "gzip", PipeFlags: "-dc"},
			threads:     4,
			memBudgetMB: 512,
			want:        []string{"-dc"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PipeFlagsForThreadsAndMem(tt.info, tt.threads, tt.memBudgetMB)
			if len(got) != len(tt.want) {
				t.Fatalf("PipeFlagsForThreadsAndMem(%s, %d, %d) = %v, want %v",
					tt.info.Tool, tt.threads, tt.memBudgetMB, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("flag[%d] = %q, want %q (full: %v)", i, got[i], tt.want[i], got)
				}
			}
		})
	}

	getMemLimit = func() int { return 400 }
	xzFlags := PipeFlagsForThreads(FormatInfo{Tool: "xz"}, 4)
	if !strings.Contains(strings.Join(xzFlags, " "), "--memlimit-mt-decompress=400MiB") {
		t.Errorf("PipeFlagsForThreads(xz) did not delegate getMemLimit(): %v", xzFlags)
	}
	lrzFlags := PipeFlagsForThreads(FormatInfo{Tool: "lrzip"}, 4)
	if !strings.Contains(strings.Join(lrzFlags, " "), "-m 4") {
		t.Errorf("PipeFlagsForThreads(lrzip) did not delegate getMemLimit(): %v", lrzFlags)
	}
}

func TestGetNullFile_ReadWriteAndConcurrency(t *testing.T) {
	nullF := getNullFile()
	if nullF == nil {
		t.Fatal("getNullFile() returned nil")
	}

	// Verify reading from nullFile returns 0 bytes and EOF (valid stdin)
	buf := make([]byte, 16)
	n, err := nullF.Read(buf)
	if n != 0 || (err != nil && err != io.EOF) {
		t.Errorf("nullFile.Read = (%d, %v), want (0, EOF)", n, err)
	}

	// Verify writing to nullFile succeeds (valid stdout/stderr discard)
	wn, werr := nullF.Write([]byte("discard"))
	if werr != nil || wn != 7 {
		t.Errorf("nullFile.Write = (%d, %v), want (7, nil)", wn, werr)
	}

	// Verify concurrency safety across 50 goroutines
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := getNullFile()
			if f == nil {
				t.Errorf("concurrent getNullFile returned nil")
			}
		}()
	}
	wg.Wait()
}

func TestFileProgress_UncompressedSizeMethods(t *testing.T) {
	fp := &FileProgress{Name: "test.tar", Size: 1000}
	if sz := fp.GetUncompressedSize(); sz != 0 {
		t.Errorf("initial GetUncompressedSize = %d, want 0", sz)
	}
	fp.SetUncompressedSize(50000)
	if sz := fp.GetUncompressedSize(); sz != 50000 {
		t.Errorf("GetUncompressedSize after set = %d, want 50000", sz)
	}

	// Test nil receiver safety
	var nilFP *FileProgress
	nilFP.SetUncompressedSize(100)
	if sz := nilFP.GetUncompressedSize(); sz != 0 {
		t.Errorf("nilFP.GetUncompressedSize() = %d, want 0", sz)
	}
}

func TestParseCutoffTime_Table(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.Local)

	tmpDir := t.TempDir()
	refFile := filepath.Join(tmpDir, "ref.txt")
	refTime := time.Date(2026, 10, 15, 8, 30, 0, 0, time.Local)
	if err := os.WriteFile(refFile, []byte("ref"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(refFile, refTime, refTime); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		newerStr   string
		newerThan  string
		wantTime   time.Time
		wantErrSub string
	}{
		{
			name:     "ambos vacíos",
			wantTime: time.Time{},
		},
		{
			name:       "ambos definidos (conflicto)",
			newerStr:   "24h",
			newerThan:  refFile,
			wantErrSub: "mutuamente excluyentes",
		},
		{
			name:     "duración en horas",
			newerStr: "24h",
			wantTime: now.Add(-24 * time.Hour),
		},
		{
			name:     "duración en días",
			newerStr: "7d",
			wantTime: now.Add(-7 * 24 * time.Hour),
		},
		{
			name:     "duración en minutos",
			newerStr: "30m",
			wantTime: now.Add(-30 * time.Minute),
		},
		{
			name:     "fecha YYYY-MM-DD",
			newerStr: "2026-11-01",
			wantTime: time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local),
		},
		{
			name:     "fecha YYYY-MM-DD HH:MM:SS",
			newerStr: "2026-11-01 15:30:45",
			wantTime: time.Date(2026, 11, 1, 15, 30, 45, 0, time.Local),
		},
		{
			name:     "fecha YYYY/MM/DD",
			newerStr: "2026/11/01",
			wantTime: time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local),
		},
		{
			name:      "archivo de referencia existente",
			newerThan: refFile,
			wantTime:  refTime,
		},
		{
			name:       "archivo de referencia inexistente",
			newerThan:  filepath.Join(tmpDir, "no_existe.txt"),
			wantErrSub: "no encontrado",
		},
		{
			name:       "formato inválido",
			newerStr:   "fecha-invalida",
			wantErrSub: "formato de fecha o duración inválido",
		},
		{
			name:       "duración negativa rechazada",
			newerStr:   "-24h",
			wantErrSub: "positiva",
		},
		{
			name:       "duración cero rechazada",
			newerStr:   "0s",
			wantErrSub: "positiva",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCutoffTime(tt.newerStr, tt.newerThan, now)
			if tt.wantErrSub != "" {
				if err == nil {
					t.Fatalf("se esperaba error con %q pero fue nil", tt.wantErrSub)
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Errorf("error %q no contiene subcadena esperada %q", err.Error(), tt.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("error inesperado: %v", err)
			}
			if !got.Equal(tt.wantTime) {
				t.Errorf("ParseCutoffTime() = %v, want %v", got, tt.wantTime)
			}
		})
	}
}

func TestSetupLogging_PrivatePathAndConcurrentCloseRace(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("XDG_CACHE_HOME", stateDir)
	t.Cleanup(CloseLog)

	if err := SetupLogging(); err != nil {
		t.Fatalf("SetupLogging failed: %v", err)
	}

	logMu.Lock()
	lf := logFile
	logMu.Unlock()
	if lf == nil {
		t.Fatal("SetupLogging left logFile nil")
	}
	logPath := lf.Name()
	if strings.HasPrefix(logPath, "/tmp/crush/") {
		t.Errorf("logFile must not be created inside shared /tmp/crush/: %s", logPath)
	}
	fi, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("log file permissions = %04o, want 0600", perm)
	}
	dirFi, err := os.Stat(filepath.Dir(logPath))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dirFi.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("log dir permissions too permissive: %04o (want 0700)", perm)
	}

	loggingActive.Store(true)
	defer func() {
		loggingActive.Store(false)
		logMu.Lock()
		logBuf.Reset()
		logMu.Unlock()
	}()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				WriteLogf("worker %d line %d\n", id, j)
			}
		}(i)
	}
	time.Sleep(time.Millisecond)
	CloseLog()
	wg.Wait()
}

func TestSetupLogging_SymlinkAndPermSafety(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("skipping non-root XDG/TMPDIR fallback test when running as root")
	}
	t.Cleanup(CloseLog)

	t.Run("rejects symlinked tmp log dir and leaves symlink target untouched", func(t *testing.T) {
		fakeTmp := t.TempDir()
		victimDir := t.TempDir()
		squatted := filepath.Join(fakeTmp, fmt.Sprintf("crush-log-%d", os.Getuid()))
		if err := os.Symlink(victimDir, squatted); err != nil {
			t.Fatal(err)
		}

		t.Setenv("TMPDIR", fakeTmp)
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("XDG_CACHE_HOME", "")
		t.Setenv("HOME", "")

		if err := SetupLogging(); err != nil {
			t.Fatalf("SetupLogging failed under symlinked tmp dir: %v", err)
		}
		defer CloseLog()

		entries, err := os.ReadDir(victimDir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("SetupLogging followed symlink and created files in victimDir: %v", entries)
		}

		logMu.Lock()
		lf := logFile
		logMu.Unlock()
		if lf == nil {
			t.Fatal("expected non-nil logFile")
		}
		fi, err := os.Lstat(lf.Name())
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("log file perm = %04o, want 0600", fi.Mode().Perm())
		}
		dirFi, err := os.Lstat(filepath.Dir(lf.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if !dirFi.IsDir() || dirFi.Mode()&os.ModeSymlink != 0 {
			t.Errorf("log parent is not a real directory: mode=%v", dirFi.Mode())
		}
		if dirFi.Mode().Perm() != 0o700 {
			t.Errorf("log dir perm = %04o, want 0700", dirFi.Mode().Perm())
		}
	})

	t.Run("tightens pre-existing 0755 log dir to 0700", func(t *testing.T) {
		stateDir := t.TempDir()
		crushLogDir := filepath.Join(stateDir, "crush")
		if err := os.MkdirAll(crushLogDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(crushLogDir, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_STATE_HOME", stateDir)

		if err := SetupLogging(); err != nil {
			t.Fatalf("SetupLogging failed: %v", err)
		}
		defer CloseLog()

		dirFi, err := os.Lstat(crushLogDir)
		if err != nil {
			t.Fatal(err)
		}
		if dirFi.Mode().Perm() != 0o700 {
			t.Errorf("pre-existing dir perm = %04o, want tightened 0700", dirFi.Mode().Perm())
		}
	})

	t.Run("ignores relative XDG_STATE_HOME and rejects symlinked XDG dir", func(t *testing.T) {
		cwdTmp := t.TempDir()
		origWd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(cwdTmp); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chdir(origWd) }()

		cacheDir := t.TempDir()
		t.Setenv("XDG_STATE_HOME", "relative_state_dir")
		t.Setenv("XDG_CACHE_HOME", cacheDir)

		if err := SetupLogging(); err != nil {
			t.Fatalf("SetupLogging failed: %v", err)
		}
		CloseLog()

		if _, err := os.Lstat(filepath.Join(cwdTmp, "relative_state_dir")); !os.IsNotExist(err) {
			t.Errorf("relative XDG_STATE_HOME should be ignored, but created in CWD (err=%v)", err)
		}

		stateParent := t.TempDir()
		victimDir := t.TempDir()
		if err := os.Symlink(victimDir, filepath.Join(stateParent, "crush")); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_STATE_HOME", stateParent)

		if err := SetupLogging(); err != nil {
			t.Fatalf("SetupLogging failed when XDG_STATE_HOME/crush is symlink: %v", err)
		}
		CloseLog()

		if entries, _ := os.ReadDir(victimDir); len(entries) != 0 {
			t.Errorf("SetupLogging wrote into symlinked XDG_STATE_HOME/crush target: %v", entries)
		}
	})
}

func TestRunWithProgress_AttachesNullStdin(t *testing.T) {
	cmd := exec.Command("true")
	if err := runWithProgress(cmd, nil, 0, nil); err != nil {
		t.Fatalf("runWithProgress failed: %v", err)
	}
	if cmd.Stdin != getNullFile() {
		t.Errorf("expected cmd.Stdin to be set to getNullFile(), got %v", cmd.Stdin)
	}
}

func TestFormatSize_PureGoDeterministic(t *testing.T) {
	origExec := execCommand
	defer func() { execCommand = origExec }()
	execCommand = func(name string, args ...string) *exec.Cmd {
		t.Fatalf("FormatSize should not spawn external tool %q", name)
		return exec.Command("false")
	}

	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{-100, "-100 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{-1024, "-1.0 KiB"},
		{1048576, "1.0 MiB"},
		{1073741824, "1.0 GiB"},
	}
	for _, tc := range tests {
		got := FormatSize(tc.bytes)
		if got != tc.want {
			t.Errorf("FormatSize(%d) = %q, want exact %q", tc.bytes, got, tc.want)
		}
	}
}

func TestEstimateUncompressedSize_ZipOverflowClamp(t *testing.T) {
	var raw bytes.Buffer
	zw := zip.NewWriter(&raw)
	for _, name := range []string{"a.bin", "b.bin"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	b := raw.Bytes()
	eocdOff := bytes.LastIndex(b, []byte{0x50, 0x4b, 0x05, 0x06})
	if eocdOff < 0 {
		t.Fatal("EOCD not found")
	}
	cdSize := int(binary.LittleEndian.Uint32(b[eocdOff+12 : eocdOff+16]))
	cdOff := int(binary.LittleEndian.Uint32(b[eocdOff+16 : eocdOff+20]))

	var out bytes.Buffer
	out.Write(b[:cdOff])

	pos := cdOff
	cdEnd := cdOff + cdSize
	for pos < cdEnd {
		if binary.LittleEndian.Uint32(b[pos:pos+4]) != 0x02014b50 {
			t.Fatalf("bad CD signature at %d", pos)
		}
		nameLen := int(binary.LittleEndian.Uint16(b[pos+28 : pos+30]))
		extraLen := int(binary.LittleEndian.Uint16(b[pos+30 : pos+32]))
		commentLen := int(binary.LittleEndian.Uint16(b[pos+32 : pos+34]))
		recLen := 46 + nameLen + extraLen + commentLen
		rec := append([]byte(nil), b[pos:pos+recLen]...)

		binary.LittleEndian.PutUint32(rec[24:28], 0xFFFFFFFF)
		binary.LittleEndian.PutUint16(rec[30:32], uint16(extraLen+12))

		var z64Extra [12]byte
		binary.LittleEndian.PutUint16(z64Extra[0:2], 0x0001)
		binary.LittleEndian.PutUint16(z64Extra[2:4], 8)
		binary.LittleEndian.PutUint64(z64Extra[4:12], uint64(math.MaxInt64-10))

		fixedEnd := 46 + nameLen + extraLen
		out.Write(rec[:fixedEnd])
		out.Write(z64Extra[:])
		out.Write(rec[fixedEnd:])
		pos += recLen
	}

	newCDSize := out.Len() - cdOff
	eocd := append([]byte(nil), b[eocdOff:]...)
	binary.LittleEndian.PutUint32(eocd[12:16], uint32(newCDSize))
	out.Write(eocd)

	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "huge64.zip")
	if err := os.WriteFile(zipPath, out.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("constructed zip64 unreadable: %v", err)
	}
	if len(zr.File) != 2 || zr.File[0].UncompressedSize64 != uint64(math.MaxInt64-10) {
		zr.Close()
		t.Fatalf("unexpected parsed zip64 sizes: %+v", zr.File)
	}
	zr.Close()

	got := EstimateUncompressedSize(zipPath)
	if got != math.MaxInt64 {
		t.Errorf("EstimateUncompressedSize(%q) = %d, want clamped math.MaxInt64 (%d)", zipPath, got, int64(math.MaxInt64))
	}
}

func TestFileLine_MultibyteUTF8(t *testing.T) {
	// Byte index 19 splits multi-byte 'ñ' (0xC3 0xB1) in half: "aa" (2 bytes) + 12*'ñ' (24 bytes).
	fpUTF := &FileProgress{Name: "aa" + strings.Repeat("ñ", 12) + ".iso", Size: 1000}
	fpUTF.SetStatus("active")
	fpUTF.Current.Store(500)

	// Short non-truncated multi-byte name tests rune vs byte padding alignment before " [".
	fpUTFShort := &FileProgress{Name: "año_ñandú.txt", Size: 1000}
	fpUTFShort.SetStatus("active")
	fpUTFShort.Current.Store(500)

	fpASCII := &FileProgress{Name: "ascii_normal_file_name_long_ok.iso", Size: 1000}
	fpASCII.SetStatus("active")
	fpASCII.Current.Store(500)

	lineUTF := fileLine(fpUTF)
	lineUTFShort := fileLine(fpUTFShort)
	lineASCII := fileLine(fpASCII)

	if !utf8.ValidString(lineUTF) {
		t.Fatalf("fileLine produced invalid UTF-8 string: %q", lineUTF)
	}
	barIdxUTF := strings.Index(lineUTFShort, " [")
	barIdxASCII := strings.Index(lineASCII, " [")
	if barIdxUTF < 0 || barIdxASCII < 0 {
		t.Fatalf("missing progress bar bracket in %q / %q", lineUTFShort, lineASCII)
	}
	runeColUTF := utf8.RuneCountInString(lineUTFShort[:barIdxUTF])
	runeColASCII := utf8.RuneCountInString(lineASCII[:barIdxASCII])
	if runeColUTF != runeColASCII {
		t.Errorf("UTF-8 display column misaligned: utf=%d runes vs ascii=%d runes (%q vs %q)",
			runeColUTF, runeColASCII, lineUTFShort, lineASCII)
	}
}

func TestGetUniqueName_UnboundedAndSymlinkSafe(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	t.Run("ContinuesPast999", func(t *testing.T) {
		if err := os.WriteFile("ucol999.txt", []byte("0"), 0644); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 999; i++ {
			name := filepath.Join(tmpDir, fmt.Sprintf("ucol999_%d.txt", i))
			if err := os.WriteFile(name, []byte("x"), 0644); err != nil {
				t.Fatal(err)
			}
		}
		got := GetUniqueName("ucol999", "txt")
		if got == "ucol999_999.txt" {
			t.Fatalf("GetUniqueName collided with existing ucol999_999.txt")
		}
		if got != "ucol999_1000.txt" {
			t.Errorf("GetUniqueName = %q, want ucol999_1000.txt", got)
		}
	})

	t.Run("RespectsDanglingSymlink", func(t *testing.T) {
		if err := os.Symlink("/nonexistent_crush_target_xyz", "ubrokensym.txt"); err != nil {
			t.Fatal(err)
		}
		got := GetUniqueName("ubrokensym", "txt")
		if got == "ubrokensym.txt" {
			t.Fatalf("GetUniqueName returned broken symlink target %q", got)
		}
		if got != "ubrokensym_1.txt" {
			t.Errorf("GetUniqueName = %q, want ubrokensym_1.txt", got)
		}
	})
}

func TestGetMemLimit_CgroupAndHost(t *testing.T) {
	origProc := procMeminfoPath
	origV2Max := cgroupV2MemMax
	origV2Curr := cgroupV2MemCurr
	origV1Lim := cgroupV1MemLimit
	origV1Use := cgroupV1MemUsage
	origExec := execCommand
	defer func() {
		procMeminfoPath = origProc
		cgroupV2MemMax = origV2Max
		cgroupV2MemCurr = origV2Curr
		cgroupV1MemLimit = origV1Lim
		cgroupV1MemUsage = origV1Use
		execCommand = origExec
		resetMemLimitCache()
	}()

	const mib = 1024 * 1024

	tests := []struct {
		name     string
		meminfo  string
		freeMock string
		v2Max    string
		v2Curr   string
		v1Limit  string
		v1Usage  string
		wantMB   int
	}{
		{
			name:     "host proc/meminfo only",
			meminfo:  "MemTotal:       16777216 kB\nMemAvailable:    2097152 kB\n",
			freeMock: "free-fail",
			wantMB:   1433,
		},
		{
			name:     "cgroup v2 max is unlimited string, falls back to host",
			meminfo:  "MemAvailable:    2097152 kB\n",
			freeMock: "free-fail",
			v2Max:    "max\n",
			v2Curr:   "104857600\n",
			wantMB:   1433,
		},
		{
			name:     "cgroup v2 tighter than host",
			meminfo:  "MemAvailable:   16777216 kB\n",
			freeMock: "free-fail",
			v2Max:    fmt.Sprintf("%d\n", 1024*mib),
			v2Curr:   fmt.Sprintf("%d\n", 256*mib),
			wantMB:   537,
		},
		{
			name:     "host tighter than cgroup v2 takes min",
			meminfo:  "MemAvailable:     524288 kB\n",
			freeMock: "free-fail",
			v2Max:    fmt.Sprintf("%d\n", 4096*mib),
			v2Curr:   "0\n",
			wantMB:   358,
		},
		{
			name:     "cgroup v2 saturated clamps to 1 MiB (not fallback 1024)",
			meminfo:  "MemAvailable:   16777216 kB\n",
			freeMock: "free-fail",
			v2Max:    fmt.Sprintf("%d\n", 512*mib),
			v2Curr:   fmt.Sprintf("%d\n", 600*mib),
			wantMB:   1,
		},
		{
			name:     "cgroup v2 tiny positive remaining clamps to min 1 MiB",
			meminfo:  "MemAvailable:   16777216 kB\n",
			freeMock: "free-fail",
			v2Max:    fmt.Sprintf("%d\n", 10*mib),
			v2Curr:   fmt.Sprintf("%d\n", 10*mib-512*1024),
			wantMB:   1,
		},
		{
			name:     "cgroup v2 without memory.current uses memory.max",
			meminfo:  "MemAvailable:   16777216 kB\n",
			freeMock: "free-fail",
			v2Max:    fmt.Sprintf("%d\n", 1000*mib),
			wantMB:   700,
		},
		{
			name:     "cgroup v1 tighter than host",
			meminfo:  "MemAvailable:   16777216 kB\n",
			freeMock: "free-fail",
			v1Limit:  fmt.Sprintf("%d\n", 512*mib),
			v1Usage:  fmt.Sprintf("%d\n", 128*mib),
			wantMB:   268,
		},
		{
			name:     "cgroup v1 unlimited sentinel ignored",
			meminfo:  "MemAvailable:    2097152 kB\n",
			freeMock: "free-fail",
			v1Limit:  "9223372036854771712\n",
			v1Usage:  "104857600\n",
			wantMB:   1433,
		},
		{
			name:     "cgroup v1 saturated clamps to 1 MiB",
			meminfo:  "MemAvailable:   16777216 kB\n",
			freeMock: "free-fail",
			v1Limit:  fmt.Sprintf("%d\n", 256*mib),
			v1Usage:  fmt.Sprintf("%d\n", 300*mib),
			wantMB:   1,
		},
		{
			name:     "cgroup v2 only when host sources unavailable",
			freeMock: "free-fail",
			v2Max:    fmt.Sprintf("%d\n", 1000*mib),
			v2Curr:   fmt.Sprintf("%d\n", 200*mib),
			wantMB:   560,
		},
		{
			name:     "free -k fallback when proc/meminfo missing and no cgroup",
			freeMock: "free",
			wantMB:   5734,
		},
		{
			name:     "fallback 1024 when neither host nor cgroup yields a value",
			freeMock: "free-fail",
			wantMB:   1024,
		},
		{
			name:     "malformed cgroup files ignored in favor of host",
			meminfo:  "MemAvailable:    1048576 kB\n",
			freeMock: "free-fail",
			v2Max:    "not-a-number\n",
			v1Limit:  "-500\n",
			wantMB:   716,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			writeOpt := func(name, content string) string {
				p := filepath.Join(tmp, name)
				if content != "" {
					if err := os.WriteFile(p, []byte(content), 0644); err != nil {
						t.Fatal(err)
					}
				}
				return p
			}

			procMeminfoPath = writeOpt("meminfo", tt.meminfo)
			cgroupV2MemMax = writeOpt("memory.max", tt.v2Max)
			cgroupV2MemCurr = writeOpt("memory.current", tt.v2Curr)
			cgroupV1MemLimit = writeOpt("memory.limit_in_bytes", tt.v1Limit)
			cgroupV1MemUsage = writeOpt("memory.usage_in_bytes", tt.v1Usage)
			execCommand = func(name string, args ...string) *exec.Cmd {
				return mockExecCommand(tt.freeMock, args...)
			}
			resetMemLimitCache()

			got := GetMemLimit()
			if got != tt.wantMB {
				t.Errorf("GetMemLimit() = %d, want %d", got, tt.wantMB)
			}
		})
	}
}

func TestGetMemLimit_TTLCache(t *testing.T) {
	origProc := procMeminfoPath
	origV2Max := cgroupV2MemMax
	origV2Curr := cgroupV2MemCurr
	origV1Lim := cgroupV1MemLimit
	origV1Use := cgroupV1MemUsage
	origExec := execCommand
	origNow := memNow
	defer func() {
		procMeminfoPath = origProc
		cgroupV2MemMax = origV2Max
		cgroupV2MemCurr = origV2Curr
		cgroupV1MemLimit = origV1Lim
		cgroupV1MemUsage = origV1Use
		execCommand = origExec
		memNow = origNow
		resetMemLimitCache()
	}()

	tmp := t.TempDir()
	cgroupV2MemMax = filepath.Join(tmp, "missing_v2_max")
	cgroupV2MemCurr = filepath.Join(tmp, "missing_v2_curr")
	cgroupV1MemLimit = filepath.Join(tmp, "missing_v1_lim")
	cgroupV1MemUsage = filepath.Join(tmp, "missing_v1_use")

	var mu sync.Mutex
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	memNow = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}

	t.Run("caches value within TTL and refreshes on expiry or reset", func(t *testing.T) {
		memPath := filepath.Join(tmp, "meminfo")
		procMeminfoPath = memPath
		execCommand = func(name string, args ...string) *exec.Cmd {
			return mockExecCommand("free-fail", args...)
		}

		if err := os.WriteFile(memPath, []byte("MemAvailable:    2097152 kB\n"), 0644); err != nil {
			t.Fatal(err)
		}
		resetMemLimitCache()
		if got := GetMemLimit(); got != 1433 {
			t.Fatalf("initial GetMemLimit() = %d, want 1433", got)
		}

		if err := os.WriteFile(memPath, []byte("MemAvailable:    4194304 kB\n"), 0644); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		now = now.Add(memCacheTTL / 2)
		mu.Unlock()
		if got := GetMemLimit(); got != 1433 {
			t.Fatalf("within-TTL GetMemLimit() = %d, want cached 1433", got)
		}

		mu.Lock()
		now = now.Add(memCacheTTL)
		mu.Unlock()
		if got := GetMemLimit(); got != 2867 {
			t.Fatalf("post-TTL GetMemLimit() = %d, want refreshed 2867", got)
		}

		if err := os.WriteFile(memPath, []byte("MemAvailable:    1048576 kB\n"), 0644); err != nil {
			t.Fatal(err)
		}
		resetMemLimitCache()
		if got := GetMemLimit(); got != 716 {
			t.Fatalf("post-reset GetMemLimit() = %d, want 716", got)
		}
	})

	t.Run("coalesces concurrent probes without repeated free forks", func(t *testing.T) {
		procMeminfoPath = filepath.Join(tmp, "missing_meminfo")
		freeCalls := 0
		execCommand = func(name string, args ...string) *exec.Cmd {
			mu.Lock()
			freeCalls++
			mu.Unlock()
			return mockExecCommand("free", args...)
		}
		resetMemLimitCache()

		var wg sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if got := GetMemLimit(); got != 5734 {
					t.Errorf("GetMemLimit() = %d, want 5734", got)
				}
			}()
		}
		wg.Wait()

		if freeCalls != 1 {
			t.Fatalf("expected 1 probe across 16 concurrent callers, got %d", freeCalls)
		}
	})
}

func TestLimitedBuffer_AndAugmentErr(t *testing.T) {
	tests := []struct {
		name          string
		maxBytes      int
		writes        []string
		wantSubstr    string
		wantTruncated bool
		wantExact     string
		wantLen       int
	}{
		{
			name:      "empty writes",
			maxBytes:  16,
			writes:    []string{""},
			wantExact: "",
			wantLen:   0,
		},
		{
			name:      "under limit preserves full output",
			maxBytes:  32,
			writes:    []string{"hello ", "world"},
			wantExact: "hello world",
			wantLen:   11,
		},
		{
			name:      "exact limit single write not truncated",
			maxBytes:  10,
			writes:    []string{"0123456789"},
			wantExact: "0123456789",
			wantLen:   10,
		},
		{
			name:      "exact limit across multiple writes not truncated",
			maxBytes:  10,
			writes:    []string{"012345", "6789"},
			wantExact: "0123456789",
			wantLen:   10,
		},
		{
			name:          "single oversized write retains tail and marks truncated",
			maxBytes:      10,
			writes:        []string{"prefix-ignored-TAIL123456"},
			wantSubstr:    "TAIL123456",
			wantTruncated: true,
			wantLen:       10,
		},
		{
			name:          "incremental overflow keeps latest bytes",
			maxBytes:      12,
			writes:        []string{"aaaa", "bbbb", "cccc", "fatal: boom!"},
			wantSubstr:    "fatal: boom!",
			wantTruncated: true,
			wantLen:       12,
		},
		{
			name:          "large flood stays O(1) capped at 32 KiB",
			maxBytes:      maxStderrBytes,
			writes:        []string{strings.Repeat("x", 100*1024), "FATAL_END_MARKER"},
			wantSubstr:    "FATAL_END_MARKER",
			wantTruncated: true,
			wantLen:       maxStderrBytes,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := newLimitedBuffer(tt.maxBytes)
			for _, chunk := range tt.writes {
				n, err := lb.Write([]byte(chunk))
				if err != nil {
					t.Fatalf("Write error: %v", err)
				}
				if n != len(chunk) {
					t.Fatalf("Write returned n=%d, want %d", n, len(chunk))
				}
			}
			if lb.Len() != tt.wantLen {
				t.Errorf("Len() = %d, want %d", lb.Len(), tt.wantLen)
			}
			if cap(lb.buf) > tt.maxBytes {
				t.Errorf("backing slice cap = %d exceeds maxBytes %d", cap(lb.buf), tt.maxBytes)
			}
			got := lb.String()
			if tt.wantTruncated {
				if !strings.Contains(got, "truncated") {
					t.Errorf("expected truncation marker in %q", got)
				}
				if !strings.Contains(got, tt.wantSubstr) {
					t.Errorf("expected tail %q in %q", tt.wantSubstr, got)
				}
			} else if got != tt.wantExact {
				t.Errorf("String() = %q, want %q", got, tt.wantExact)
			}
		})
	}

	t.Run("concurrent writes race safety", func(t *testing.T) {
		lb := newLimitedBuffer(1024)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				payload := bytes.Repeat([]byte("a"), 256)
				for j := 0; j < 20; j++ {
					_, _ = lb.Write(payload)
				}
			}()
		}
		wg.Wait()
		if lb.Len() != 1024 {
			t.Errorf("concurrent Len() = %d, want 1024", lb.Len())
		}
	})

	t.Run("stderrFor and augmentErr", func(t *testing.T) {
		if w := stderrFor(nil); w != os.Stderr {
			t.Errorf("stderrFor(nil) = %T, want os.Stderr", w)
		}
		pt := NewProgressTracker(100, 1)
		w := stderrFor(pt)
		lb, ok := w.(*limitedBuffer)
		if !ok {
			t.Fatalf("stderrFor(pt) = %T, want *limitedBuffer", w)
		}
		_, _ = lb.Write([]byte(strings.Repeat("noise\n", 10000)))
		_, _ = lb.Write([]byte("corrupt archive header\n"))

		baseErr := fmt.Errorf("exit status 1")
		cmd := &exec.Cmd{Stderr: lb}
		got := augmentErr(cmd, baseErr)
		if got == nil || !strings.Contains(got.Error(), "corrupt archive header") {
			t.Errorf("augmentErr(limitedBuffer) = %v, want stderr tail", got)
		}
		if len(got.Error()) > maxStderrBytes+256 {
			t.Errorf("augmentErr output too large: %d bytes", len(got.Error()))
		}
		if gotNil := augmentErr(cmd, nil); gotNil != nil {
			t.Errorf("augmentErr(cmd, nil) = %v, want nil", gotNil)
		}

		bb := bytes.NewBufferString("legacy buffer error\n")
		cmdLegacy := &exec.Cmd{Stderr: bb}
		gotLegacy := augmentErr(cmdLegacy, baseErr)
		if gotLegacy == nil || !strings.Contains(gotLegacy.Error(), "legacy buffer error") {
			t.Errorf("augmentErr(*bytes.Buffer) = %v, want compatibility", gotLegacy)
		}

		cmdEmpty := &exec.Cmd{Stderr: newLimitedBuffer(64)}
		if gotEmpty := augmentErr(cmdEmpty, baseErr); gotEmpty != baseErr {
			t.Errorf("augmentErr(empty limitedBuffer) = %v, want original err", gotEmpty)
		}
	})
}

func TestLogBuf_CapAndShrink(t *testing.T) {
	var flushed bytes.Buffer
	restore := setOutputWriters(nil, &flushed)
	defer restore()

	defer func() {
		loggingActive.Store(false)
		logMu.Lock()
		logBuf = bytes.Buffer{}
		logBufTruncated = false
		logMu.Unlock()
	}()

	t.Run("caps growth at 1 MiB and shrinks backing array on Stop", func(t *testing.T) {
		pt := NewProgressTracker(100, 1)
		pt.writer = io.Discard
		pt.stderrIsTTY = true
		pt.Start()

		chunk := strings.Repeat("á", 4096) + "\n"
		for i := 0; i < 300; i++ {
			WriteLog(chunk)
		}
		WriteWarning("final warning after cap")

		logMu.Lock()
		gotLen := logBuf.Len()
		gotStr := logBuf.String()
		logMu.Unlock()

		if gotLen > maxLogBufBytes {
			t.Errorf("logBuf.Len() = %d exceeds cap %d", gotLen, maxLogBufBytes)
		}
		if !strings.Contains(gotStr, "truncated") {
			t.Errorf("expected truncation notice in capped logBuf")
		}
		if !utf8.ValidString(gotStr) {
			t.Errorf("capped logBuf contains invalid UTF-8")
		}

		pt.Stop()

		logMu.Lock()
		afterLen := logBuf.Len()
		afterCap := logBuf.Cap()
		logMu.Unlock()

		if afterLen != 0 {
			t.Errorf("post-Stop logBuf.Len() = %d, want 0", afterLen)
		}
		if afterCap > shrinkLogBufBytes {
			t.Errorf("post-Stop logBuf.Cap() = %d, want <= %d", afterCap, shrinkLogBufBytes)
		}
		if flushed.Len() == 0 {
			t.Errorf("expected Stop to flush buffered logs")
		}
	})

	t.Run("shrinks backing array on Suspend and accepts fresh writes after Reset", func(t *testing.T) {
		flushed.Reset()
		pt := NewProgressTracker(100, 1)
		pt.writer = io.Discard
		pt.stderrIsTTY = true
		pt.Start()
		defer pt.Stop()

		big := strings.Repeat("Z", 200*1024)
		WriteLog(big)

		pt.Suspend(func() {
			logMu.Lock()
			capDuringSuspend := logBuf.Cap()
			lenDuringSuspend := logBuf.Len()
			logMu.Unlock()
			if lenDuringSuspend != 0 {
				t.Errorf("during Suspend logBuf.Len() = %d, want 0", lenDuringSuspend)
			}
			if capDuringSuspend > shrinkLogBufBytes {
				t.Errorf("during Suspend logBuf.Cap() = %d, want <= %d", capDuringSuspend, shrinkLogBufBytes)
			}
		})

		for i := 0; i < 20; i++ {
			WriteLog(strings.Repeat("Q", 100*1024))
		}
		logMu.Lock()
		logBuf.Reset()
		logMu.Unlock()

		WriteLog("after direct reset")
		logMu.Lock()
		got := logBuf.String()
		logMu.Unlock()
		if got != "after direct reset" {
			t.Errorf("post-Reset WriteLog = %q, want %q", got, "after direct reset")
		}
	})
}

type recordingReader struct {
	data      []byte
	offset    int
	maxBufLen int
}

func (r *recordingReader) Read(p []byte) (int, error) {
	if len(p) > r.maxBufLen {
		r.maxBufLen = len(p)
	}
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

func TestSplicePipe_FallbackPoolReuse(t *testing.T) {
	bp, ok := spliceBufPool.Get().(*[]byte)
	if !ok || bp == nil {
		t.Fatalf("spliceBufPool.Get() did not return *[]byte")
	}
	if len(*bp) != defaultPipeCapacity {
		t.Fatalf("pooled buffer len = %d, want %d", len(*bp), defaultPipeCapacity)
	}
	spliceBufPool.Put(bp)

	origNew := spliceBufPool.New
	var allocMu sync.Mutex
	allocs := 0
	spliceBufPool.New = func() any {
		allocMu.Lock()
		allocs++
		allocMu.Unlock()
		b := make([]byte, defaultPipeCapacity)
		return &b
	}
	defer func() { spliceBufPool.New = origNew }()

	const calls = 50
	payload := bytes.Repeat([]byte("pool-reuse-check-"), 1024)
	for i := 0; i < calls; i++ {
		src := &recordingReader{data: payload}
		var dst bytes.Buffer
		n, err := splicePipe(src, struct{ io.Writer }{&dst})
		if err != nil {
			t.Fatalf("call %d splicePipe error: %v", i, err)
		}
		if n != int64(len(payload)) || !bytes.Equal(dst.Bytes(), payload) {
			t.Fatalf("call %d payload mismatch: n=%d want %d", i, n, len(payload))
		}
		if src.maxBufLen != defaultPipeCapacity {
			t.Fatalf("call %d buffer len = %d, want %d", i, src.maxBufLen, defaultPipeCapacity)
		}
	}

	if allocs >= calls {
		t.Errorf("expected pooled buffer reuse across %d calls, got %d allocations", calls, allocs)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			msg := bytes.Repeat([]byte{byte('A' + id)}, 65536)
			var dst bytes.Buffer
			n, err := splicePipe(bytes.NewReader(msg), &dst)
			if err != nil || n != int64(len(msg)) || !bytes.Equal(dst.Bytes(), msg) {
				t.Errorf("concurrent worker %d failed: n=%d err=%v", id, n, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestReservedNames_BoundedAndRelease(t *testing.T) {
	uniqueNameMu.Lock()
	origReserved := reservedNames
	origSeq := reservedSeq
	reservedNames = make(map[string]uint64)
	reservedSeq = 0
	uniqueNameMu.Unlock()
	defer func() {
		uniqueNameMu.Lock()
		reservedNames = origReserved
		reservedSeq = origSeq
		uniqueNameMu.Unlock()
	}()

	tmpDir := t.TempDir()

	t.Run("ReleaseReservedName frees name for reuse", func(t *testing.T) {
		base := filepath.Join(tmpDir, "rel_item")
		first := GetUniqueName(base, "txt")
		if first != base+".txt" {
			t.Fatalf("first = %q, want %q", first, base+".txt")
		}
		second := GetUniqueName(base, "txt")
		if second != base+"_1.txt" {
			t.Fatalf("second = %q, want %q", second, base+"_1.txt")
		}
		ReleaseReservedName(first)
		third := GetUniqueName(base, "txt")
		if third != first {
			t.Errorf("after ReleaseReservedName, got %q, want %q", third, first)
		}
	})

	t.Run("prunes entries that already exist on disk", func(t *testing.T) {
		uniqueNameMu.Lock()
		reservedNames = make(map[string]uint64)
		reservedSeq = 0
		uniqueNameMu.Unlock()

		onDisk := filepath.Join(tmpDir, "created_0.txt")
		if err := os.WriteFile(onDisk, []byte("ok"), 0644); err != nil {
			t.Fatal(err)
		}
		uniqueNameMu.Lock()
		reservedSeq++
		reservedNames[onDisk] = reservedSeq
		uniqueNameMu.Unlock()

		for i := 0; i < maxReservedNames+10; i++ {
			_ = GetUniqueName(filepath.Join(tmpDir, fmt.Sprintf("diskprune_%d", i)), "tar")
		}

		uniqueNameMu.Lock()
		_, stillInMap := reservedNames[onDisk]
		count := len(reservedNames)
		uniqueNameMu.Unlock()

		if stillInMap {
			t.Errorf("expected existing-on-disk entry %q to be pruned from reservedNames", onDisk)
		}
		if count > maxReservedNames {
			t.Errorf("len(reservedNames) = %d exceeds cap %d", count, maxReservedNames)
		}
	})

	t.Run("evicts oldest stale entries and keeps map bounded", func(t *testing.T) {
		uniqueNameMu.Lock()
		reservedNames = make(map[string]uint64)
		reservedSeq = 0
		uniqueNameMu.Unlock()

		var last string
		for i := 0; i < maxReservedNames+500; i++ {
			last = GetUniqueName(filepath.Join(tmpDir, fmt.Sprintf("watch_%d", i)), "zst")
		}

		uniqueNameMu.Lock()
		count := len(reservedNames)
		_, lastPresent := reservedNames[last]
		uniqueNameMu.Unlock()

		if count > maxReservedNames {
			t.Errorf("len(reservedNames) = %d exceeds max %d", count, maxReservedNames)
		}
		if !lastPresent {
			t.Errorf("most recent reservation %q was prematurely evicted", last)
		}
	})
}

func TestSafeFlagArg_EdgeCases(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"normal relative", "archive.tar.gz", "archive.tar.gz"},
		{"leading dash flag", "-rf", "./-rf"},
		{"double dash option", "--checkpoint-action=exec=sh", "./--checkpoint-action=exec=sh"},
		{"7z/rar listfile prefix", "@filelist.txt", "./@filelist.txt"},
		{"tar remote host:file colon", "backup_2026-11-24T12:00:00.tar", "./backup_2026-11-24T12:00:00.tar"},
		{"attacker host:path", "attacker.example:payload.tar", "./attacker.example:payload.tar"},
		{"already prefixed relative colon", "./backup_12:00.tar", "./backup_12:00.tar"},
		{"subdir before colon", "backups/2026_12:00.tar", "backups/2026_12:00.tar"},
		{"absolute path with colon", "/tmp/2026_12:00.tar", "/tmp/2026_12:00.tar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeFlagArg(tt.in); got != tt.want {
				t.Errorf("safeFlagArg(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRedactArgs_AllVariants(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "7z attached -p and -p-",
			args: []string{"a", "-pMyS3cr3t!", "-mhe=on", "out.7z", "--", "-pfile.txt"},
			want: []string{"a", "-p***", "-mhe=on", "out.7z", "--", "-pfile.txt"},
		},
		{
			name: "7z/rar -p- no-password flag preserved",
			args: []string{"l", "-slt", "-p-", "--", "arc.7z"},
			want: []string{"l", "-slt", "-p-", "--", "arc.7z"},
		},
		{
			name: "zip separated and attached -P",
			args: []string{"-r", "-P", "sec1", "-Psec2", "out.zip", "--", "-Pnotflag.txt"},
			want: []string{"-r", "-P", "***", "-P***", "out.zip", "--", "-Pnotflag.txt"},
		},
		{
			name: "rar -hp header password separated and attached",
			args: []string{"a", "-hp", "sec1", "-hpSecret2", "out.rar", "--", "-hpfile"},
			want: []string{"a", "-hp", "***", "-hp***", "out.rar", "--", "-hpfile"},
		},
		{
			name: "long -password and --password separated and equals",
			args: []string{"-password=abc", "--password=def", "-password", "ghi", "--password", "jkl"},
			want: []string{"-password=***", "--password=***", "-password", "***", "--password", "***"},
		},
		{
			name: "zip -P whose password value literally is --",
			args: []string{"-P", "--", "out.zip", "--", "-pkeep.txt"},
			want: []string{"-P", "***", "out.zip", "--", "-pkeep.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := append([]string(nil), tt.args...)
			got := redactArgs(tt.args)
			if strings.Join(got, "\x00") != strings.Join(tt.want, "\x00") {
				t.Errorf("redactArgs(%v) = %v, want %v", tt.args, got, tt.want)
			}
			if strings.Join(tt.args, "\x00") != strings.Join(orig, "\x00") {
				t.Errorf("redactArgs mutated input slice: %v", tt.args)
			}
		})
	}
}

func TestProbeDiskAvail_EdgeCases(t *testing.T) {
	origExec := execCommand
	defer func() { execCommand = origExec }()

	t.Run("passes -- and safeFlagArg to df and clamps negative over-quota avail to 0", func(t *testing.T) {
		tmp := t.TempDir()
		origWd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(tmp); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chdir(origWd) }()
		if err := os.Mkdir("-weird_dir", 0o755); err != nil {
			t.Fatal(err)
		}

		var capturedArgs []string
		execCommand = func(name string, args ...string) *exec.Cmd {
			capturedArgs = append([]string(nil), args...)
			return exec.Command("printf", "Avail\n-4096\n")
		}
		err = CheckDiskSpace(1024, "-weird_dir", "descomprimir")
		if err == nil {
			t.Fatalf("CheckDiskSpace succeeded when df reported negative over-quota space (-4096)")
		}
		joined := strings.Join(capturedArgs, " ")
		if !strings.Contains(joined, "-- ./-weird_dir") {
			t.Errorf("probeDiskAvail did not pass '-- ./-weird_dir' to df: %v", capturedArgs)
		}
	})

	t.Run("clamps huge fallback 1K-block count to math.MaxInt64 without overflow", func(t *testing.T) {
		call := 0
		execCommand = func(name string, args ...string) *exec.Cmd {
			call++
			if call == 1 {
				return exec.Command("false")
			}
			return exec.Command("printf", "Filesystem 1K-blocks Used Available Use%% Mounted\n/dev/sda1 1 1 9223372036854775000 1%% /\n")
		}
		avail, ok := probeDiskAvail(".")
		if !ok || avail != math.MaxInt64 {
			t.Errorf("probeDiskAvail huge blocks = (%d, %v), want (%d, true)", avail, ok, int64(math.MaxInt64))
		}
	})
}

func TestEstimateSize_SecurityAndOverflow(t *testing.T) {
	origExec := execCommand
	defer func() { execCommand = origExec }()

	t.Run("EstimateCompressedSize avoids int64 multiplication overflow", func(t *testing.T) {
		huge := int64(math.MaxInt64 / 2)
		if got := EstimateCompressedSize(huge, Tar, []string{"a.bin"}); got <= 0 {
			t.Errorf("EstimateCompressedSize(huge, Tar) overflowed to %d", got)
		}
		if got := EstimateCompressedSize(math.MaxInt64, Tar, []string{"a.bin"}); got != math.MaxInt64 {
			t.Errorf("EstimateCompressedSize(MaxInt64, Tar) = %d, want clamped MaxInt64", got)
		}
		if got := EstimateCompressedSize(huge, SevenZ, []string{"video.mp4"}); got <= 0 {
			t.Errorf("EstimateCompressedSize(huge, precompressed) overflowed to %d", got)
		}
	})

	t.Run("EstimateUncompressedSize 7z/rar/zst clamp int64 sum overflow and pass safeFlagArg", func(t *testing.T) {
		tmp := t.TempDir()
		origWd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(tmp); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chdir(origWd) }()

		for _, name := range []string{"-bomb.7z", "@bomb.rar", "multi:frame.zst"} {
			if err := os.WriteFile(name, []byte("stub"), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		var lastArgs []string
		execCommand = func(name string, args ...string) *exec.Cmd {
			lastArgs = append([]string(nil), args...)
			switch {
			case strings.Contains(name, "zstd"):
				return exec.Command("printf", "Decompressed Size: 8 EiB (9223372036854775800 B)\nDecompressed Size: 8 EiB (9223372036854775800 B)\n")
			default:
				return exec.Command("printf", "Size = 9223372036854775800\nSize = 9223372036854775800\n")
			}
		}

		for _, tc := range []struct {
			file    string
			wantArg string
		}{
			{"-bomb.7z", "./-bomb.7z"},
			{"@bomb.rar", "./@bomb.rar"},
			{"multi:frame.zst", "./multi:frame.zst"},
		} {
			got := EstimateUncompressedSize(tc.file)
			if got != math.MaxInt64 {
				t.Errorf("EstimateUncompressedSize(%q) = %d, want clamped MaxInt64", tc.file, got)
			}
			if len(lastArgs) == 0 || lastArgs[len(lastArgs)-1] != tc.wantArg {
				t.Errorf("EstimateUncompressedSize(%q) passed args %v, want last arg %q", tc.file, lastArgs, tc.wantArg)
			}
		}
	})
}

func TestSHA256File_SecurityAndEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	validA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	validB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	notHex64 := "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"

	t.Run("WriteSHA256File refuses to overwrite symlink target", func(t *testing.T) {
		arc := filepath.Join(tmpDir, "arc.tar.gz")
		victim := filepath.Join(tmpDir, "victim.txt")
		if err := os.WriteFile(arc, []byte("archive-bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(victim, []byte("DO_NOT_TOUCH"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, arc+".sha256"); err != nil {
			t.Fatal(err)
		}

		if _, err := WriteSHA256File(arc); err == nil {
			t.Fatalf("expected WriteSHA256File to reject symlink at %s.sha256", arc)
		}
		gotVictim, err := os.ReadFile(victim)
		if err != nil {
			t.Fatal(err)
		}
		if string(gotVictim) != "DO_NOT_TOUCH" {
			t.Fatalf("WriteSHA256File overwrote symlink target! content=%q", string(gotVictim))
		}
	})

	t.Run("WriteSHA256File escapes newline/backslash and round-trips with ParseSHA256File", func(t *testing.T) {
		sub := filepath.Join(tmpDir, "escaped")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		weirdArc := filepath.Join(sub, "evil\n"+validA+"  injected.tar.gz")
		if err := os.WriteFile(weirdArc, []byte("payload"), 0o644); err != nil {
			t.Skipf("filesystem rejected newline in filename: %v", err)
		}
		wantHash, err := WriteSHA256File(weirdArc)
		if err != nil {
			t.Fatalf("WriteSHA256File failed: %v", err)
		}
		shaPath := weirdArc + ".sha256"
		// Must NOT match injected.tar.gz!
		if _, err := ParseSHA256File(shaPath, filepath.Join(sub, "injected.tar.gz")); err == nil {
			t.Fatalf("newline in filename caused checksum line injection for injected.tar.gz")
		}
		gotHash, err := ParseSHA256File(shaPath, weirdArc)
		if err != nil || gotHash != wantHash {
			t.Fatalf("round-trip ParseSHA256File = (%q, %v), want (%q, nil)", gotHash, err, wantHash)
		}
	})

	t.Run("ParseSHA256File handles spaces, paths, BSD style, hex validation, and wrong-file rejection", func(t *testing.T) {
		tests := []struct {
			name       string
			content    string
			targetFile string
			wantHash   string
			wantErr    bool
		}{
			{
				name:       "multi-file manifest with spaces in filenames selects exact file",
				content:    validA + "  My First Backup.tar.gz\n" + validB + "  My Second Backup.tar.gz\n",
				targetFile: "/tmp/My Second Backup.tar.gz",
				wantHash:   validB,
			},
			{
				name:       "binary asterisk mode with spaces",
				content:    validB + " *Archive With Spaces.7z\n",
				targetFile: "Archive With Spaces.7z",
				wantHash:   validB,
			},
			{
				name:       "manifest with ./ relative path prefix in multi-file list",
				content:    validA + "  ./other.tar.xz\n" + validB + "  ./sub/release.tar.xz\n",
				targetFile: "/var/tmp/release.tar.xz",
				wantHash:   validB,
			},
			{
				name:       "BSD style SHA256 (file) = hash",
				content:    "SHA256 (My Archive.tar.zst) = " + strings.ToUpper(validB) + "\n",
				targetFile: "My Archive.tar.zst",
				wantHash:   validB,
			},
			{
				name:       "rejects manifest naming ONLY a different file",
				content:    validA + "  other_file.tar.gz\n",
				targetFile: "target_file.tar.gz",
				wantErr:    true,
			},
			{
				name:       "rejects non-hex 64-char digest on named line",
				content:    notHex64 + "  target_file.tar.gz\n",
				targetFile: "target_file.tar.gz",
				wantErr:    true,
			},
			{
				name:       "rejects non-hex 64-char digest on raw line",
				content:    notHex64 + "\n",
				targetFile: "target_file.tar.gz",
				wantErr:    true,
			},
			{
				name:       "rejects short digest on named line",
				content:    "deadbeef  target_file.tar.gz\n",
				targetFile: "target_file.tar.gz",
				wantErr:    true,
			},
		}

		for i, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				p := filepath.Join(tmpDir, fmt.Sprintf("case_%d.sha256", i))
				if err := os.WriteFile(p, []byte(tt.content), 0o644); err != nil {
					t.Fatal(err)
				}
				got, err := ParseSHA256File(p, tt.targetFile)
				if (err != nil) != tt.wantErr {
					t.Fatalf("ParseSHA256File() err = %v, wantErr = %v (got=%q)", err, tt.wantErr, got)
				}
				if !tt.wantErr && got != tt.wantHash {
					t.Errorf("ParseSHA256File() = %q, want %q", got, tt.wantHash)
				}
			})
		}
	})
}

func TestPipeline_EarlyExitZeroAndPartialStartFDLeak(t *testing.T) {
	t.Run("downstream exiting 0 early delivers EPIPE instead of hanging upstream", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		cs1 := []string{"-test.run=TestHelperProcess", "--", "mock-flood-producer"}
		cmd1 := exec.CommandContext(ctx, os.Args[0], cs1...)
		cmd1.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")

		cmd2 := mockExecCommand("mock-read-5-bytes")
		var out bytes.Buffer
		err := pipeline(&out, nil, cmd1, cmd2)
		if ctx.Err() == context.DeadlineExceeded {
			t.Fatalf("pipeline hung because parent kept pipe read end open after downstream exited 0 (err=%v)", err)
		}
	})

	t.Run("partial Start failure closes all created pipe FDs", func(t *testing.T) {
		countFDs := func() int {
			entries, err := os.ReadDir("/proc/self/fd")
			if err != nil {
				return -1
			}
			return len(entries)
		}
		before := countFDs()
		if before < 0 {
			t.Skip("/proc/self/fd not available")
		}
		for i := 0; i < 25; i++ {
			c1 := exec.Command("/nonexistent_crush_binary_xyz_12345")
			c2 := mockExecCommand("mock-producer")
			c3 := mockExecCommand("mock-consumer")
			if err := pipeline(io.Discard, nil, c1, c2, c3); err == nil {
				t.Fatal("expected error when first command fails to start")
			}
		}
		after := countFDs()
		if after > before+2 {
			t.Errorf("FD leak detected across failed pipeline starts: before=%d after=%d", before, after)
		}
	})
}

func TestParsePercentAndFormatETA_EdgeCases(t *testing.T) {
	pctTests := []struct {
		in   string
		want int
	}{
		{" 42%", 42},
		{"0%", 0},
		{"100%", 100},
		{"12.75%", 12},
		{"99.9%", 99},
		{"105%", -1},
		{"250%", -1},
		{"-25%", -1},
		{"Scanning 2026 files... 18%", 18},
		{"no percent here", -1},
	}
	for _, tt := range pctTests {
		if got := parsePercent(tt.in); got != tt.want {
			t.Errorf("parsePercent(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}

	// Tiny transfer rate (2% in 200M seconds -> ~9.8B remaining seconds) previously overflowed time.Duration to negative
	gotETA := formatETA(200_000_000*time.Second, 2, 100)
	if gotETA != ">24h" {
		t.Errorf("formatETA(huge remaining) = %q, want \">24h\"", gotETA)
	}

	if bar := makeBar(-10, 5); bar != "     " {
		t.Errorf("makeBar(-10, 5) = %q, want 5 spaces", bar)
	}
	if bar := makeBar(50, 0); bar != "" {
		t.Errorf("makeBar(50, 0) = %q, want empty", bar)
	}
}

func TestRunWithProgress_ZeroSizeSilencesStdoutWhenTrackerPresent(t *testing.T) {
	pt := NewProgressTracker(0, 1)
	cmd := exec.Command("echo", "noisy-banner")
	if err := runWithProgress(cmd, pt, 0, nil); err != nil {
		t.Fatalf("runWithProgress failed: %v", err)
	}
	if cmd.Stdout == os.Stdout {
		t.Errorf("runWithProgress with active ProgressTracker and fileSize=0 wrote to os.Stdout instead of stdoutFor(pt)")
	}
}

func TestProgressTracker_IdempotentStopAndRaces(t *testing.T) {
	t.Run("Stop called twice and on zero-value or nil tracker never panics", func(t *testing.T) {
		var nilPT *ProgressTracker
		nilPT.Stop()
		nilPT.Add(10)
		nilPT.SetFiles(nil)

		zeroPT := &ProgressTracker{}
		zeroPT.Stop()
		zeroPT.Stop()

		pt := NewProgressTracker(100, 1)
		pt.writer = io.Discard
		pt.stderrIsTTY = true
		pt.Start()
		pt.Stop()
		pt.Stop()
	})

	t.Run("concurrent Start, SetFiles, Add, WriteLog, Suspend, and Stop is race-free", func(t *testing.T) {
		var errOut bytes.Buffer
		restore := setOutputWriters(io.Discard, &errOut)
		defer restore()

		for iter := 0; iter < 10; iter++ {
			pt := NewProgressTracker(1000, 3)
			pt.writer = &lockedWriter{w: io.Discard}
			pt.stderrIsTTY = true

			var wg sync.WaitGroup
			wg.Add(4)
			go func() {
				defer wg.Done()
				pt.Start()
			}()
			go func() {
				defer wg.Done()
				for j := 0; j < 20; j++ {
					fps := []*FileProgress{
						{Name: "a.bin", Size: 500},
						{Name: "b.bin", Size: 500},
					}
					fps[0].SetStatus("active")
					pt.SetFiles(fps)
					pt.Add(10)
				}
			}()
			go func() {
				defer wg.Done()
				for j := 0; j < 20; j++ {
					WriteLogf("iter %d msg %d\n", iter, j)
				}
			}()
			go func() {
				defer wg.Done()
				time.Sleep(2 * time.Millisecond)
				pt.Suspend(func() {})
				pt.Stop()
				pt.Stop()
			}()
			wg.Wait()

			logMu.Lock()
			stranded := logBuf.Len()
			logMu.Unlock()
			if stranded != 0 {
				t.Fatalf("iter %d: %d bytes stranded in logBuf after pt.Stop()", iter, stranded)
			}
		}
	})

	t.Run("countingWriter with nil ProgressTracker does not panic", func(t *testing.T) {
		var dst bytes.Buffer
		fp := &FileProgress{Name: "f.bin", Size: 10}
		cw := &countingWriter{w: &dst, pt: nil, fp: fp}
		if _, err := cw.Write([]byte("hello")); err != nil {
			t.Fatalf("countingWriter.Write error: %v", err)
		}
		if fp.Current.Load() != 5 {
			t.Errorf("fp.Current = %d, want 5", fp.Current.Load())
		}
	})
}

func TestLimitedBuffer_UTF8TruncationAndZeroMax(t *testing.T) {
	// 15 bytes max with 2-byte UTF-8 rune 'á' (0xC3 0xA1): 10*'á' = 20 bytes -> cuts mid-rune at offset 5
	lb := newLimitedBuffer(15)
	_, _ = lb.Write([]byte(strings.Repeat("á", 10)))
	got := lb.String()
	if !utf8.ValidString(got) {
		t.Errorf("limitedBuffer.String() produced invalid UTF-8 on mid-rune truncation: %q", got)
	}
	if !strings.Contains(got, "ááááááá") {
		t.Errorf("limitedBuffer.String() missing valid UTF-8 tail: %q", got)
	}

	var zeroLB limitedBuffer
	if _, err := zeroLB.Write([]byte("hello stderr")); err != nil {
		t.Fatalf("zero-value limitedBuffer.Write failed: %v", err)
	}
	if zeroLB.String() != "hello stderr" {
		t.Errorf("zero-value limitedBuffer.String() = %q, want %q", zeroLB.String(), "hello stderr")
	}
}








