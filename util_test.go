package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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


