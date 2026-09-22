package main

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDecompressNoFiles(t *testing.T) {
	opts := DecompressOptions{}
	err := DoDecompress(nil, opts)
	if err == nil {
		t.Error("DoDecompress with nil files should error")
	}
}

func TestDecompressDryRun(t *testing.T) {
	opts := DecompressOptions{
		DryRun: true,
	}
	err := DoDecompress([]string{"test.tar.gz"}, opts)
	if err != nil {
		t.Errorf("DoDecompress dry-run = %v", err)
	}
}

func makeGz(t *testing.T, src, dst string) {
	t.Helper()
	tool := "pigz"
	if !hasTool(tool) {
		tool = "gzip"
	}
	if !hasTool(tool) {
		t.Skip("ni pigz ni gzip disponibles")
	}
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	cmd := exec.Command(tool, "-c", "-9")
	cmd.Stdin = in
	cmd.Stdout = out
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
}

func TestDecompressSingleCleanupOnError(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "source.txt")
	if err := os.WriteFile(src, []byte(strings.Repeat("contenido repetido ", 1000)), 0644); err != nil {
		t.Fatal(err)
	}
	gz := filepath.Join(tmpDir, "data.txt.gz")
	makeGz(t, src, gz)
	os.Remove(src)

	fi, err := os.Stat(gz)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(gz, fi.Size()*6/10); err != nil {
		t.Fatal(err)
	}

	err = DoDecompress([]string{gz}, DecompressOptions{OutputDir: tmpDir})
	if err == nil {
		t.Fatal("Esperaba error por .gz truncado, no hubo")
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "data.txt.gz" {
		t.Errorf("Tras el fallo debería quedar solo el original data.txt.gz, quedó: %v", entries)
	}
}

func TestDecompressPreExistingKept(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "source.txt")
	if err := os.WriteFile(src, []byte(strings.Repeat("contenido repetido ", 1000)), 0644); err != nil {
		t.Fatal(err)
	}
	gz := filepath.Join(tmpDir, "data.txt.gz")
	makeGz(t, src, gz)
	os.Remove(src)

	pre := filepath.Join(tmpDir, "data.txt")
	if err := os.WriteFile(pre, []byte("datos previos"), 0644); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(gz)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(gz, fi.Size()*6/10); err != nil {
		t.Fatal(err)
	}

	err = DoDecompress([]string{gz}, DecompressOptions{OutputDir: tmpDir})
	if err == nil {
		t.Fatal("Esperaba error por .gz truncado, no hubo")
	}

	if _, statErr := os.Stat(pre); statErr != nil {
		t.Errorf("La salida preexistente %s no debería borrarse: %v", pre, statErr)
	}
	if _, statErr := os.Stat(gz); statErr != nil {
		t.Errorf("El original %s debería conservarse: %v", gz, statErr)
	}
}

func TestDecompressPartFileHint(t *testing.T) {
	tmp := t.TempDir()
	part := filepath.Join(tmp, "big.tar.gz.part")
	err := decompressFile(part, DecompressOptions{}, nil)
	if err == nil {
		t.Fatal("esperaba error para fragmento .part")
	}
	if !strings.Contains(err.Error(), "cat big.tar.gz.part* > big.tar.gz") {
		t.Fatalf("mensaje debería sugerir concatenar correctamente: %v", err)
	}
}

func TestPipeCmdForLz4UpperExt(t *testing.T) {
	tmp := t.TempDir()
	lz4File := filepath.Join(tmp, "x.tar.LZ4")
	if err := os.WriteFile(lz4File, []byte("dummy"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd, closer := pipeCmdFor(FormatInfo{Tool: "lz4", PipeFlags: "-dc"}, lz4File)
	if closer != nil {
		defer closer.Close()
	}
	for _, a := range cmd.Args {
		if strings.Contains(a, "LZ4") {
			t.Fatalf("lz4 no debe recibir el archivo por nombre (extensión mayúscula): %v", cmd.Args)
		}
	}
	if cmd.Stdin == nil {
		t.Fatal("lz4 debe leer el archivo por stdin (magic), no por extensión")
	}

	gzFile := filepath.Join(tmp, "x.gz")
	if err := os.WriteFile(gzFile, []byte("dummy"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd2, c2 := pipeCmdFor(FormatInfo{Tool: "gzip", PipeFlags: "-dc"}, gzFile)
	if c2 != nil {
		defer c2.Close()
	}
	if cmd2.Stdin != nil {
		t.Fatal("gzip debe recibir el archivo por nombre")
	}
}

func TestDecompressRelativeOutputDir(t *testing.T) {
	if !hasTool("tar") {
		t.Skip("tar no disponible")
	}
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldWd)

	src := "source.txt"
	if err := os.WriteFile(src, []byte(strings.Repeat("contenido repetido ", 1000)), 0644); err != nil {
		t.Fatal(err)
	}
	makeGz(t, src, "data.txt.gz")
	if err := os.Mkdir("content", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("content/a.txt", []byte("aaaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("tar", "-cf", "data.tar", "content").Run(); err != nil {
		t.Fatal(err)
	}
	makeGz(t, "data.tar", "data.tar.gz")
	os.Remove("data.tar")

	err = DoDecompress([]string{"data.txt.gz", "data.tar.gz"}, DecompressOptions{OutputDir: "out", KeepOrig: true})
	if err != nil {
		t.Fatalf("DoDecompress con -o relativo = %v", err)
	}

	for _, want := range []string{"out/data.txt", "out/content/a.txt"} {
		if fi, statErr := os.Stat(want); statErr != nil {
			t.Errorf("Salida esperada %s no existe: %v", want, statErr)
		} else if fi.Size() == 0 {
			t.Errorf("Salida %s vacía", want)
		}
	}
}

func TestDecompressLrz(t *testing.T) {
	if !hasTool("lrzip") {
		t.Skip("lrzip no disponible")
	}
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldWd)

	if err := os.WriteFile("data.txt", []byte(strings.Repeat("contenido lrzip repetido ", 500)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("lrzip", "-f", "data.txt").Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("data.txt.lrz"); err != nil {
		t.Fatal("No se creó data.txt.lrz:", err)
	}

	err = DoDecompress([]string{"data.txt.lrz"}, DecompressOptions{OutputDir: "out", KeepOrig: true})
	if err != nil {
		t.Fatalf("DoDecompress .lrz = %v", err)
	}

	if fi, statErr := os.Stat("out/data.txt"); statErr != nil {
		t.Errorf("Salida esperada out/data.txt no existe: %v", statErr)
	} else if fi.Size() == 0 {
		t.Error("Salida out/data.txt vacía")
	}
}

func TestListArchiveOutputsBz3(t *testing.T) {
	if !hasTool("tar") {
		t.Skip("tar no disponible")
	}
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldWd)

	if err := os.Mkdir("content", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("content/a.txt", []byte("aaaa"), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		tool    string
		ext     string
		flags   string
	}{
		{"bz3", "bzip3", ".tar.bz3", "bzip3 -dc"},
		{"lrz", "lrzip", ".tar.lrz", "lrzip -d -p 1 -o -"},
		{"br", "brotli", ".tar.br", "brotli -dc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !hasTool(tt.tool) {
				t.Skipf("%s no disponible", tt.tool)
			}
			archive := "data" + tt.ext
			if err := exec.Command("tar", "-I", tt.tool, "-cf", archive, "content").Run(); err != nil {
				t.Fatal(err)
			}
			outputs := listArchiveOutputs(archive, "out", FormatInfo{})
			want := filepath.Join("out", "content", "a.txt")
			for _, o := range outputs {
				if o == want {
					return
				}
			}
			t.Errorf("listArchiveOutputs(%s) no incluye %s: %v", archive, want, outputs)
		})
	}
}

func TestDecompressTarCleanupOnError(t *testing.T) {
	if !hasTool("tar") {
		t.Skip("tar no disponible")
	}
	tmpDir := t.TempDir()
	contentDir := filepath.Join(tmpDir, "content")
	if err := os.MkdirAll(contentDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "a.txt"), []byte("aaaa"), 0644); err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 2*1024*1024)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "b.bin"), big, 0644); err != nil {
		t.Fatal(err)
	}

	tarPath := filepath.Join(tmpDir, "big.tar")
	cmd := exec.Command("tar", "-cf", tarPath, "content")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(contentDir)

	fi, err := os.Stat(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(tarPath, fi.Size()*6/10); err != nil {
		t.Fatal(err)
	}

	err = DoDecompress([]string{tarPath}, DecompressOptions{OutputDir: tmpDir})
	if err == nil {
		t.Fatal("Esperaba error por .tar truncado, no hubo")
	}

	if _, statErr := os.Stat(tarPath); statErr != nil {
		t.Errorf("El original %s debería conservarse: %v", tarPath, statErr)
	}
	if _, statErr := os.Stat(contentDir); !os.IsNotExist(statErr) {
		t.Errorf("La extracción parcial %s debería haberse eliminado", contentDir)
	}
}

func TestDecompressTarKeepOrigNoLeak(t *testing.T) {
	tmpDir := t.TempDir()
	sampleTxt := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(sampleTxt, []byte("contenido a descomprimir"), 0644); err != nil {
		t.Fatal(err)
	}

	tarGzPath := filepath.Join(tmpDir, "sample.tar.gz")
	cmd := exec.Command("tar", "-czf", tarGzPath, "-C", tmpDir, "file.txt")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(sampleTxt)

	outDir := filepath.Join(tmpDir, "extracted")
	if err := os.Mkdir(outDir, 0755); err != nil {
		t.Fatal(err)
	}

	opts := DecompressOptions{
		OutputDir: outDir,
		KeepOrig:  true,
	}

	if err := DoDecompress([]string{tarGzPath}, opts); err != nil {
		t.Fatalf("DoDecompress falló: %v", err)
	}

	// El original debe existir
	if _, err := os.Stat(tarGzPath); err != nil {
		t.Errorf("el archivo original %s debería conservarse con KeepOrig=true", tarGzPath)
	}

	// El archivo extraído debe existir
	if _, err := os.Stat(filepath.Join(outDir, "file.txt")); err != nil {
		t.Errorf("el archivo extraído debería existir: %v", err)
	}

	// No debe haber ningún archivo .tar residual en outDir
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tar") {
			t.Errorf("fuga de archivo temporal detectada: %s aún existe en el directorio de salida", e.Name())
		}
	}
}

func TestDecompressNoForceRejectsExisting(t *testing.T) {
	tmpDir := t.TempDir()
	txtPath := filepath.Join(tmpDir, "data.txt")
	if err := os.WriteFile(txtPath, []byte("compressed content"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("gzip", "-k", txtPath)
	if err := cmd.Run(); err != nil {
		t.Skip("gzip no disponible")
	}
	gzPath := txtPath + ".gz"

	outDir := filepath.Join(tmpDir, "out")
	if err := os.Mkdir(outDir, 0755); err != nil {
		t.Fatal(err)
	}
	existingOut := filepath.Join(outDir, "data.txt")
	if err := os.WriteFile(existingOut, []byte("pre-existing content"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := DecompressOptions{
		OutputDir: outDir,
		KeepOrig:  true,
		Force:     false,
	}

	err := DoDecompress([]string{gzPath}, opts)
	if err == nil {
		t.Fatalf("se esperaba error al descomprimir sin --force sobre archivo existente")
	}

	// Verificar que el contenido previo no fue sobreescrito
	content, readErr := os.ReadFile(existingOut)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "pre-existing content" {
		t.Errorf("el contenido fue sobreescrito sin --force: %q", string(content))
	}

	// Ahora con Force: true debe sobrescribir
	opts.Force = true
	if err := DoDecompress([]string{gzPath}, opts); err != nil {
		t.Fatalf("falló descompresión con Force=true: %v", err)
	}
	content, _ = os.ReadFile(existingOut)
	if string(content) != "compressed content" {
		t.Errorf("contenido no actualizado con Force=true: %q", string(content))
	}
}

func TestSplitWriterCloseAndCountingWriterCloser(t *testing.T) {
	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "test.part")
	firstFile, err := os.Create(basePath)
	if err != nil {
		t.Fatal(err)
	}
	defer firstFile.Close()

	sw := newSplitWriter(firstFile, 1, basePath)
	var w io.Writer = sw
	pt := NewProgressTracker(100, 1)
	cw := &countingWriter{w: w, pt: pt}

	closer, ok := interface{}(cw).(io.Closer)
	if !ok {
		t.Fatalf("countingWriter does not implement io.Closer")
	}

	data := make([]byte, 1500*1024)
	if _, err := cw.Write(data); err != nil {
		t.Fatal(err)
	}

	if sw.part < 1 {
		t.Fatalf("expected split to occur, but part is %d", sw.part)
	}

	lastFile := sw.file
	if err := closer.Close(); err != nil {
		t.Fatalf("closer.Close() failed: %v", err)
	}

	if _, err := lastFile.Write([]byte("more")); err == nil {
		t.Errorf("expected lastFile to be closed after Close(), but write succeeded")
	}
}

func TestDecompressReportThreadLimit(t *testing.T) {
	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(txtFile, []byte("thread report test data"), 0644); err != nil {
		t.Fatal(err)
	}
	gzFile := filepath.Join(tmpDir, "test.txt.gz")
	makeGz(t, txtFile, gzFile)

	// Test decompressFile with ThreadLimit = 7
	opts := DecompressOptions{
		OutputDir:   filepath.Join(tmpDir, "out"),
		ThreadLimit: 7,
		Force:       true,
		KeepOrig:    true,
	}

	fp := &FileProgress{Name: filepath.Base(gzFile), Size: 100}
	err := decompressFile(gzFile, opts, fp)
	if err != nil {
		t.Fatalf("decompressFile failed: %v", err)
	}

	expectedThreads := 7
	if !hasTool("pigz") {
		expectedThreads = 1
	}
	gotThreads := effectiveThreads(gzFile, opts.ThreadLimit)
	if gotThreads != expectedThreads {
		t.Errorf("effectiveThreads = %d, want %d", gotThreads, expectedThreads)
	}
}

func TestDecompressFileProgressTracking(t *testing.T) {
	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "test.txt")
	content := []byte(strings.Repeat("test progress tracking data in decompress\n", 100))
	if err := os.WriteFile(txtFile, content, 0644); err != nil {
		t.Fatal(err)
	}
	gzFile := filepath.Join(tmpDir, "test.txt.gz")
	makeGz(t, txtFile, gzFile)

	fi, err := os.Stat(gzFile)
	if err != nil {
		t.Fatal(err)
	}

	pt := NewProgressTracker(fi.Size(), 1)
	opts := DecompressOptions{
		OutputDir: filepath.Join(tmpDir, "out"),
		Force:     true,
		KeepOrig:  true,
		Progress:  pt,
	}

	fp := &FileProgress{Name: filepath.Base(gzFile), Size: fi.Size()}
	err = decompressFile(gzFile, opts, fp)
	if err != nil {
		t.Fatalf("decompressFile failed: %v", err)
	}

	if fp.Current.Load() != fi.Size() {
		t.Errorf("decompressFile no actualizó fp.Current al tamaño completo: obtenido %d, esperado %d", fp.Current.Load(), fi.Size())
	}
}

func TestDecompressTarProgressTracking(t *testing.T) {
	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "sample.txt")
	content := []byte(strings.Repeat("data for tar progress tracking\n", 200))
	if err := os.WriteFile(txtFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	compressOpts := CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		KeepOrig:  true,
	}
	outPaths, err := DoCompress([]string{txtFile}, compressOpts)
	if err != nil || len(outPaths) == 0 {
		t.Fatalf("DoCompress failed: %v", err)
	}
	tarGzFile := outPaths[0]

	fi, err := os.Stat(tarGzFile)
	if err != nil {
		t.Fatal(err)
	}

	pt := NewProgressTracker(fi.Size(), 1)
	opts := DecompressOptions{
		OutputDir: filepath.Join(tmpDir, "out_tar"),
		Force:     true,
		KeepOrig:  true,
		Progress:  pt,
	}

	fp := &FileProgress{Name: filepath.Base(tarGzFile), Size: fi.Size()}
	err = decompressFile(tarGzFile, opts, fp)
	if err != nil {
		t.Fatalf("decompressFile tar.gz failed: %v", err)
	}

	if fp.Current.Load() != fi.Size() {
		t.Errorf("decompressTar no actualizó fp.Current al tamaño completo: obtenido %d, esperado %d", fp.Current.Load(), fi.Size())
	}
}

func TestFindDecompressibleFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Crear archivos de prueba válidos
	validFiles := []string{"archive1.zip", "archive2.tar.gz", "archive3.7z", "data.bz2", "photo.tar.xz"}
	for _, f := range validFiles {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte("dummy"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Crear archivos que deben ser ignorados
	ignoredFiles := []string{"readme.txt", "script.sh", ".hidden.gz", "archive.tar.gz.part00", "image.png"}
	for _, f := range ignoredFiles {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte("dummy"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Crear un subdirectorio con nombre de archivo comprimido (debe ser ignorado)
	if err := os.Mkdir(filepath.Join(tmpDir, "subfolder.tar.gz"), 0755); err != nil {
		t.Fatal(err)
	}

	found, err := FindDecompressibleFiles(tmpDir)
	if err != nil {
		t.Fatalf("FindDecompressibleFiles falló: %v", err)
	}

	if len(found) != len(validFiles) {
		t.Fatalf("esperados %d archivos, obtenidos %d: %v", len(validFiles), len(found), found)
	}

	for _, expected := range validFiles {
		expectedPath := filepath.Join(tmpDir, expected)
		matched := false
		for _, f := range found {
			if f == expectedPath || f == expected {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("archivo esperado %s no encontrado en %v", expected, found)
		}
	}
}

func TestFindDecompressibleFilesEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	found, err := FindDecompressibleFiles(tmpDir)
	if err != nil {
		t.Fatalf("FindDecompressibleFiles falló en dir vacío: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("esperado 0 archivos en dir vacío, obtenidos: %v", found)
	}
}

func TestPromptDecompressAll(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		files     []string
		wantOK    bool
		wantInOut string
	}{
		{
			name:      "confirmación con s",
			input:     "s\n",
			files:     []string{"file1.zip", "file2.tar.gz"},
			wantOK:    true,
			wantInOut: "¿Desea descomprimir",
		},
		{
			name:      "confirmación con S mayúscula",
			input:     "S\n",
			files:     []string{"file1.zip"},
			wantOK:    true,
			wantInOut: "¿Desea descomprimir",
		},
		{
			name:      "confirmación con si",
			input:     "si\n",
			files:     []string{"file1.zip"},
			wantOK:    true,
			wantInOut: "¿Desea descomprimir",
		},
		{
			name:      "confirmación con y",
			input:     "y\n",
			files:     []string{"file1.zip"},
			wantOK:    true,
			wantInOut: "¿Desea descomprimir",
		},
		{
			name:      "rechazo con n",
			input:     "n\n",
			files:     []string{"file1.zip"},
			wantOK:    false,
			wantInOut: "¿Desea descomprimir",
		},
		{
			name:      "rechazo con enter vacío",
			input:     "\n",
			files:     []string{"file1.zip"},
			wantOK:    false,
			wantInOut: "¿Desea descomprimir",
		},
		{
			name:      "rechazo por EOF",
			input:     "",
			files:     []string{"file1.zip"},
			wantOK:    false,
			wantInOut: "¿Desea descomprimir",
		},
		{
			name:      "sin archivos devuelve false sin preguntar",
			input:     "s\n",
			files:     nil,
			wantOK:    false,
			wantInOut: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := strings.NewReader(tt.input)
			var out bytes.Buffer
			got, err := PromptDecompressAll(in, &out, tt.files)
			if err != nil {
				t.Fatalf("PromptDecompressAll devolvió error inesperado: %v", err)
			}
			if got != tt.wantOK {
				t.Errorf("PromptDecompressAll() = %v, want %v", got, tt.wantOK)
			}
			if tt.wantInOut != "" && !strings.Contains(out.String(), tt.wantInOut) {
				t.Errorf("salida esperada contenía %q, obtenida: %q", tt.wantInOut, out.String())
			}
		})
	}
}

func TestFindDecompressibleFilesRecursiveAndSplit(t *testing.T) {
	tmpDir := t.TempDir()

	sub1 := filepath.Join(tmpDir, "sub1")
	sub2 := filepath.Join(tmpDir, "sub2", "nested")
	hidden := filepath.Join(tmpDir, ".hidden")
	if err := os.MkdirAll(sub1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub2, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(hidden, 0755); err != nil {
		t.Fatal(err)
	}

	// Archivo en subdirectorio 1
	if err := os.WriteFile(filepath.Join(sub1, "archive.tar.gz"), []byte("dummy"), 0644); err != nil {
		t.Fatal(err)
	}
	// Archivo dividido en subdirectorio 2 (base + partes)
	if err := os.WriteFile(filepath.Join(sub2, "divided.zst"), []byte("dummy base"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub2, "divided.zst.part01"), []byte("part 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub2, "divided.zst.part02"), []byte("part 2"), 0644); err != nil {
		t.Fatal(err)
	}
	// Archivo regular y archivo oculto que deben ignorarse
	if err := os.WriteFile(filepath.Join(sub2, "notes.txt"), []byte("text"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "secret.tar.gz"), []byte("dummy"), 0644); err != nil {
		t.Fatal(err)
	}

	found, err := FindDecompressibleFiles(tmpDir)
	if err != nil {
		t.Fatalf("FindDecompressibleFiles falló: %v", err)
	}

	// Debe encontrar sub1/archive.tar.gz y sub2/nested/divided.zst, ignorando las partes .part*
	if len(found) != 2 {
		t.Fatalf("esperados 2 archivos base, obtenidos %d: %v", len(found), found)
	}

	for _, f := range found {
		if strings.Contains(f, ".part") {
			t.Errorf("FindDecompressibleFiles incluyó fragmento .part: %s", f)
		}
		if strings.Contains(f, ".hidden") {
			t.Errorf("FindDecompressibleFiles incluyó directorio oculto: %s", f)
		}
	}
}

func TestDecompressSplitMultiReader(t *testing.T) {
	tmpDir := t.TempDir()
	originalData := []byte("Antigravity MultiReader split decompression test data payload repeated! " + strings.Repeat("ABCDEF1234567890\n", 50))

	// Comprimir datos a gzip
	var compressedBuf bytes.Buffer
	gw := gzip.NewWriter(&compressedBuf)
	if _, err := gw.Write(originalData); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}

	compBytes := compressedBuf.Bytes()
	if len(compBytes) < 30 {
		t.Fatalf("datos comprimidos demasiado pequeños: %d bytes", len(compBytes))
	}

	// Dividir en 3 partes
	chunkSize := len(compBytes) / 3
	part0 := compBytes[:chunkSize]
	part1 := compBytes[chunkSize : chunkSize*2]
	part2 := compBytes[chunkSize*2:]

	basePath := filepath.Join(tmpDir, "split_archive.gz")
	part1Path := basePath + ".part01"
	part2Path := basePath + ".part02"

	if err := os.WriteFile(basePath, part0, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part1Path, part1, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part2Path, part2, 0644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "out")
	opts := DecompressOptions{
		OutputDir: outDir,
		Force:     true,
		KeepOrig:  true,
	}

	err := decompressFile(basePath, opts, nil)
	if err != nil {
		t.Fatalf("decompressFile para archivo dividido falló: %v", err)
	}

	resultFile := filepath.Join(outDir, "split_archive")
	decompressed, err := os.ReadFile(resultFile)
	if err != nil {
		t.Fatalf("no se encontró archivo descomprimido: %v", err)
	}

	if !bytes.Equal(decompressed, originalData) {
		t.Fatalf("los datos descomprimidos no coinciden con el original (esperado %d bytes, obtenido %d bytes)", len(originalData), len(decompressed))
	}
}

func TestDecompressSplitReportPortions(t *testing.T) {
	tmpDir := t.TempDir()
	originalData := []byte("Portions test data " + strings.Repeat("0123456789", 40))

	var compressedBuf bytes.Buffer
	gw := gzip.NewWriter(&compressedBuf)
	gw.Write(originalData)
	gw.Close()

	compBytes := compressedBuf.Bytes()
	mid := len(compBytes) / 2
	part0 := compBytes[:mid]
	part1 := compBytes[mid:]

	basePath := filepath.Join(tmpDir, "portion_archive.gz")
	part1Path := basePath + ".part01"

	if err := os.WriteFile(basePath, part0, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part1Path, part1, 0644); err != nil {
		t.Fatal(err)
	}

	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w

	outDir := filepath.Join(tmpDir, "out")
	opts := DecompressOptions{
		OutputDir: outDir,
		Force:     true,
		KeepOrig:  true,
	}

	err = decompressFile(basePath, opts, nil)

	w.Close()
	os.Stderr = oldStderr

	if err != nil {
		t.Fatalf("decompressFile falló: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "Porciones:") || !strings.Contains(output, "2 / 2 partes") {
		t.Errorf("reporte de descompresión no contiene 'Porciones:' o '2 / 2 partes': %q", output)
	}
}

func TestDecompressSinglePreservesExtension(t *testing.T) {
	if !hasTool("bzip3") {
		t.Skip("bzip3 no disponible")
	}
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "game.iso")
	originalData := []byte("Antigravity ISO dummy data for decompression test")
	if err := os.WriteFile(src, originalData, 0644); err != nil {
		t.Fatal(err)
	}

	// Comprimir game.iso -> game.iso.bz3
	archive := filepath.Join(tmpDir, "game.iso.bz3")
	cmd := exec.Command("bzip3", "-c", src)
	outFile, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = outFile
	if err := cmd.Run(); err != nil {
		outFile.Close()
		t.Fatalf("falló compresión bzip3: %v", err)
	}
	outFile.Close()

	outDir := filepath.Join(tmpDir, "out")
	opts := DecompressOptions{
		OutputDir: outDir,
		Force:     true,
		KeepOrig:  true,
	}

	err = DoDecompress([]string{archive}, opts)
	if err != nil {
		t.Fatalf("DoDecompress falló: %v", err)
	}

	extracted := filepath.Join(outDir, "game.iso")
	data, err := os.ReadFile(extracted)
	if err != nil {
		t.Fatalf("se esperaba que el archivo descomprimido fuese %s pero no se encontró: %v", extracted, err)
	}
	if !bytes.Equal(data, originalData) {
		t.Fatalf("contenido descomprimido no coincide")
	}
}

func TestDecompressAutoDetectIsoExtension(t *testing.T) {
	if !hasTool("bzip3") {
		t.Skip("bzip3 no disponible")
	}
	tmpDir := t.TempDir()

	// Crear archivo simulando imagen ISO 9660:
	// Tamaño >= 32774 bytes, con magic "CD001" en offset 32769 (0x8001)
	isoData := make([]byte, 33000)
	copy(isoData[32769:], []byte("CD001"))

	src := filepath.Join(tmpDir, "legacy_file") // sin extensión
	if err := os.WriteFile(src, isoData, 0644); err != nil {
		t.Fatal(err)
	}

	// Archivo legacy sin extensión comprimido: legacy_game.bz3
	archive := filepath.Join(tmpDir, "legacy_game.bz3")
	cmd := exec.Command("bzip3", "-c", src)
	outFile, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = outFile
	if err := cmd.Run(); err != nil {
		outFile.Close()
		t.Fatalf("falló compresión bzip3: %v", err)
	}
	outFile.Close()

	outDir := filepath.Join(tmpDir, "out")
	opts := DecompressOptions{
		OutputDir: outDir,
		Force:     true,
		KeepOrig:  true,
	}

	err = DoDecompress([]string{archive}, opts)
	if err != nil {
		t.Fatalf("DoDecompress falló: %v", err)
	}

	expectedIso := filepath.Join(outDir, "legacy_game.iso")
	if _, err := os.Stat(expectedIso); err != nil {
		t.Errorf("se esperaba que legacy_game fuese renombrado a legacy_game.iso por magic CD001, pero no existe: %v", err)
	}
	unwanted := filepath.Join(outDir, "legacy_game")
	if _, err := os.Stat(unwanted); err == nil {
		t.Errorf("el archivo sin extensión %s no debería existir tras el renombramiento a .iso", unwanted)
	}
}

func TestDecompressRecursivePreservesDirectoryPaths(t *testing.T) {
	tmpDir := t.TempDir()

	// Subdirectorio anidado: dir1/dir2
	nestedDir := filepath.Join(tmpDir, "dir1", "dir2")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Archivo comprimido en dir1/dir2/data.txt.gz
	fileNested := filepath.Join(nestedDir, "data.txt")
	if err := os.WriteFile(fileNested, []byte("nested content"), 0644); err != nil {
		t.Fatal(err)
	}
	archNested := filepath.Join(nestedDir, "data.txt.gz")
	makeGz(t, fileNested, archNested)
	os.Remove(fileNested)

	// Subdirectorio con _parts: dir1/mysplit_parts/part.txt.gz
	partsDir := filepath.Join(tmpDir, "dir1", "mysplit_parts")
	if err := os.MkdirAll(partsDir, 0755); err != nil {
		t.Fatal(err)
	}
	fileParts := filepath.Join(partsDir, "part.txt")
	if err := os.WriteFile(fileParts, []byte("parts content"), 0644); err != nil {
		t.Fatal(err)
	}
	archParts := filepath.Join(partsDir, "part.txt.gz")
	makeGz(t, fileParts, archParts)
	os.Remove(fileParts)

	// Descomprimir con OutputDir: "" (como crush -d sin -o)
	opts := DecompressOptions{
		OutputDir: "",
		Force:     true,
		KeepOrig:  true,
	}

	err := DoDecompress([]string{archNested, archParts}, opts)
	if err != nil {
		t.Fatalf("DoDecompress falló: %v", err)
	}

	// 1. archNested debe extraerse en dir1/dir2/data.txt
	expectedNested := filepath.Join(nestedDir, "data.txt")
	if _, err := os.Stat(expectedNested); err != nil {
		t.Errorf("archivo anidado debería extraerse en %s, pero no existe: %v", expectedNested, err)
	}

	// 2. archParts debe extraerse en dir1/part.txt (padre de mysplit_parts), NO dentro de mysplit_parts/
	expectedParts := filepath.Join(tmpDir, "dir1", "part.txt")
	if _, err := os.Stat(expectedParts); err != nil {
		t.Errorf("archivo en _parts debería extraerse en %s (padre del directorio _parts), pero no existe: %v", expectedParts, err)
	}
	unwantedInParts := filepath.Join(partsDir, "part.txt")
	if _, err := os.Stat(unwantedInParts); err == nil {
		t.Errorf("el archivo no debería estar dentro de _parts: %s", unwantedInParts)
	}
}

func TestDecompressTarNoIntermediateDiskFile(t *testing.T) {
	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "payload.txt")
	chunk := []byte(strings.Repeat("Crush streaming pipe decompression test data without temporary tar on disk!\n", 1000))
	f, err := os.Create(txtFile)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if _, err := f.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	tarGzFile := filepath.Join(tmpDir, "archive.tar.gz")
	cmd := exec.Command("tar", "-czf", tarGzFile, "-C", tmpDir, "payload.txt")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "extracted")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(tarGzFile)
	if err != nil {
		t.Fatal(err)
	}

	pt := NewProgressTracker(fi.Size(), 1)
	opts := DecompressOptions{
		OutputDir: outDir,
		Force:     true,
		KeepOrig:  true,
		Progress:  pt,
	}

	var foundIntermediateTar atomic.Bool
	stopWatcher := make(chan struct{})
	watcherDone := make(chan struct{})

	go func() {
		defer close(watcherDone)
		ticker := time.NewTicker(500 * time.Microsecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopWatcher:
				return
			case <-ticker.C:
				matches, err := filepath.Glob(filepath.Join(outDir, "*.tar"))
				if err == nil && len(matches) > 0 {
					foundIntermediateTar.Store(true)
				}
			}
		}
	}()

	fp := &FileProgress{Name: filepath.Base(tarGzFile), Size: fi.Size()}
	decompErr := decompressFile(tarGzFile, opts, fp)
	close(stopWatcher)
	<-watcherDone

	if decompErr != nil {
		t.Fatalf("decompressFile falló: %v", decompErr)
	}

	if foundIntermediateTar.Load() {
		t.Errorf("decompressTar escribió un archivo .tar intermedio a disco en %s", outDir)
	}

	extractedFile := filepath.Join(outDir, "payload.txt")
	extFi, err := os.Stat(extractedFile)
	if err != nil {
		t.Fatalf("archivo extraído no encontrado: %v", err)
	}

	origFi, _ := os.Stat(txtFile)
	if extFi.Size() != origFi.Size() {
		t.Errorf("tamaño extraído = %d, esperado = %d", extFi.Size(), origFi.Size())
	}
}

func TestDecompressTarCorruptArchive(t *testing.T) {
	tmpDir := t.TempDir()
	corruptTarGz := filepath.Join(tmpDir, "corrupt.tar.gz")
	// Write invalid gzip payload (starts with magic 0x1f 0x8b but corrupted data)
	if err := os.WriteFile(corruptTarGz, []byte("\x1f\x8b\x08\x00\x00\x00\x00\x00\x00\xffcorrupt_data_payload_that_fails_gunzip"), 0644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "out")
	opts := DecompressOptions{
		OutputDir: outDir,
		Force:     true,
		KeepOrig:  true,
	}

	err := decompressFile(corruptTarGz, opts, nil)
	if err == nil {
		t.Error("esperaba error al descomprimir tar.gz corrupto, pero no hubo error")
	}

	// Verify no intermediate .tar file is left behind
	matches, _ := filepath.Glob(filepath.Join(outDir, "*.tar"))
	if len(matches) > 0 {
		t.Errorf("se encontraron archivos .tar residuales: %v", matches)
	}
}

func TestDecompressTarSplitStreaming(t *testing.T) {
	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "file.txt")
	content := []byte(strings.Repeat("split streaming data block\n", 5000))
	if err := os.WriteFile(txtFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	fullTarGz := filepath.Join(tmpDir, "split_arch.tar.gz")
	cmd := exec.Command("tar", "-czf", fullTarGz, "-C", tmpDir, "file.txt")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	gzData, err := os.ReadFile(fullTarGz)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(fullTarGz)

	mid := len(gzData) / 2
	part00 := filepath.Join(tmpDir, "split_arch.tar.gz")
	part01 := filepath.Join(tmpDir, "split_arch.tar.gz.part01")
	if err := os.WriteFile(part00, gzData[:mid], 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part01, gzData[mid:], 0644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "out_split")
	opts := DecompressOptions{
		OutputDir: outDir,
		Force:     true,
		KeepOrig:  true,
	}

	var foundIntermediateTar atomic.Bool
	stopWatcher := make(chan struct{})
	watcherDone := make(chan struct{})

	go func() {
		defer close(watcherDone)
		ticker := time.NewTicker(500 * time.Microsecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopWatcher:
				return
			case <-ticker.C:
				matches, err := filepath.Glob(filepath.Join(outDir, "*.tar"))
				if err == nil && len(matches) > 0 {
					foundIntermediateTar.Store(true)
				}
			}
		}
	}()

	err = decompressFile(part00, opts, nil)
	close(stopWatcher)
	<-watcherDone

	if err != nil {
		t.Fatalf("descompresión de split tar.gz falló: %v", err)
	}

	if foundIntermediateTar.Load() {
		t.Errorf("se detectó un archivo .tar intermedio durante la descompresión split")
	}

	extractedFile := filepath.Join(outDir, "file.txt")
	gotData, err := os.ReadFile(extractedFile)
	if err != nil {
		t.Fatalf("no se pudo leer archivo extraído: %v", err)
	}
	if !bytes.Equal(gotData, content) {
		t.Errorf("contenido extraído no coincide con el original")
	}
}




