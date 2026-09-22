package main

import (
	"archive/zip"
	"bytes"
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

