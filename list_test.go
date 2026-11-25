package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDoList_Tar(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "sample.tar")

	tf, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(tf)

	// Agregar carpeta docs/
	tw.WriteHeader(&tar.Header{
		Name:     "docs/",
		Typeflag: tar.TypeDir,
		Mode:     0755,
		ModTime:  time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC),
	})

	// Agregar docs/manual.txt (1024 bytes)
	data := strings.Repeat("A", 1024)
	tw.WriteHeader(&tar.Header{
		Name:     "docs/manual.txt",
		Size:     int64(len(data)),
		Mode:     0644,
		Typeflag: tar.TypeReg,
		ModTime:  time.Date(2026, 11, 5, 12, 30, 0, 0, time.UTC),
	})
	tw.Write([]byte(data))
	tw.Close()
	tf.Close()

	var buf bytes.Buffer
	if err := DoList(tarPath, "", &buf); err != nil {
		t.Fatalf("DoList falló: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "=== Contenido: sample.tar") {
		t.Errorf("encabezado faltante en salida:\n%s", out)
	}
	if !strings.Contains(out, "docs/manual.txt") || !strings.Contains(out, "docs/") {
		t.Errorf("miembros faltantes en salida:\n%s", out)
	}
	if !strings.Contains(out, "1 archivo, 1 carpeta") {
		t.Errorf("resumen faltante en salida:\n%s", out)
	}
	if !strings.Contains(out, "Total sin comprimir: "+FormatSize(int64(len(data)))) {
		t.Errorf("tamaño total formateado incorrecto en salida:\n%s", out)
	}
}

func TestDoList_Zip(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "archive.zip")

	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)

	w, err := zw.Create("hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("HELLO WORLD LIST TEST"))
	zw.Close()
	zf.Close()

	var buf bytes.Buffer
	if err := DoList(zipPath, "", &buf); err != nil {
		t.Fatalf("DoList(zip) falló: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "hello.txt") || !strings.Contains(out, "1 archivo") {
		t.Errorf("salida inesperada:\n%s", out)
	}
}

func TestDoList_Errors(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Archivo inexistente
	var buf1 bytes.Buffer
	if err := DoList("/ruta/inexistente/archivo.tar", "", &buf1); err == nil {
		t.Errorf("esperado error por archivo inexistente")
	}

	// 2. Ruta es directorio
	var buf2 bytes.Buffer
	if err := DoList(tmpDir, "", &buf2); err == nil || !strings.Contains(err.Error(), "es un directorio") {
		t.Errorf("esperado error 'es un directorio', obtenido: %v", err)
	}

	// 3. Formato desconocido no escribe encabezado huérfano
	unknown := filepath.Join(tmpDir, "data.unknown")
	_ = os.WriteFile(unknown, []byte("x"), 0644)
	var buf3 bytes.Buffer
	if err := DoList(unknown, "", &buf3); err == nil {
		t.Errorf("esperado error por formato desconocido")
	}
	if buf3.Len() != 0 {
		t.Errorf("no debía escribir salida en error de formato, escribió: %q", buf3.String())
	}
}

func TestDoList_MatchesRenderListTable(t *testing.T) {
	tmpDir := t.TempDir()
	mod := time.Date(2026, 6, 15, 9, 30, 0, 0, time.UTC)

	tarPath := filepath.Join(tmpDir, "multi.tar")
	{
		f, _ := os.Create(tarPath)
		tw := tar.NewWriter(f)
		_ = tw.WriteHeader(&tar.Header{Name: "src/", Typeflag: tar.TypeDir, Mode: 0755, ModTime: mod})
		_ = tw.WriteHeader(&tar.Header{Name: "src/a.txt", Typeflag: tar.TypeReg, Mode: 0644, Size: 2048, ModTime: mod})
		_, _ = tw.Write(bytes.Repeat([]byte("x"), 2048))
		_ = tw.WriteHeader(&tar.Header{Name: "src/b.txt", Typeflag: tar.TypeReg, Mode: 0644, Size: 512, ModTime: mod})
		_, _ = tw.Write(bytes.Repeat([]byte("y"), 512))
		_ = tw.Close()
		_ = f.Close()
	}

	emptyTar := filepath.Join(tmpDir, "empty.tar")
	{
		f, _ := os.Create(emptyTar)
		tw := tar.NewWriter(f)
		_ = tw.Close()
		_ = f.Close()
	}

	tarGzPath := filepath.Join(tmpDir, "bundle.tar.gz")
	{
		f, _ := os.Create(tarGzPath)
		gw := gzip.NewWriter(f)
		tw := tar.NewWriter(gw)
		_ = tw.WriteHeader(&tar.Header{Name: "README.md", Typeflag: tar.TypeReg, Mode: 0644, Size: 100, ModTime: mod})
		_, _ = tw.Write(bytes.Repeat([]byte("z"), 100))
		_ = tw.Close()
		_ = gw.Close()
		_ = f.Close()
	}

	zipPath := filepath.Join(tmpDir, "pack.zip")
	{
		f, _ := os.Create(zipPath)
		zw := zip.NewWriter(f)
		w, _ := zw.Create("note.txt")
		_, _ = w.Write([]byte("hello zip"))
		_ = zw.Close()
		_ = f.Close()
	}

	gzPath := filepath.Join(tmpDir, "single.log.gz")
	{
		f, _ := os.Create(gzPath)
		gw := gzip.NewWriter(f)
		_, _ = gw.Write([]byte("single stream body"))
		_ = gw.Close()
		_ = f.Close()
	}

	cases := []string{tarPath, emptyTar, tarGzPath, zipPath, gzPath}
	for _, p := range cases {
		t.Run(filepath.Base(p), func(t *testing.T) {
			fi, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			info, err := DetectFormat(p)
			if err != nil {
				t.Fatal(err)
			}
			members, err := ListArchiveMembers(p, "")
			if err != nil {
				t.Fatal(err)
			}

			var want bytes.Buffer
			if err := RenderListTable(p, members, info, fi, &want); err != nil {
				t.Fatal(err)
			}

			var got bytes.Buffer
			if err := DoList(p, "", &got); err != nil {
				t.Fatalf("DoList(%s) failed: %v", p, err)
			}

			if got.String() != want.String() {
				t.Errorf("DoList output differs from RenderListTable:\ngot:\n%s\nwant:\n%s", got.String(), want.String())
			}
		})
	}
}

type streamingGateWriter struct {
	buf      bytes.Buffer
	gateFile string
	opened   bool
}

func (w *streamingGateWriter) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if !w.opened && strings.Contains(w.buf.String(), "first.txt") {
		w.opened = true
		_ = os.WriteFile(w.gateFile, []byte("ok"), 0644)
	}
	return n, err
}

func TestDoList_StreamsRowsIncrementally(t *testing.T) {
	origExec := execCommand
	origLook := lookPath
	defer func() {
		execCommand = origExec
		lookPath = origLook
	}()
	lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }

	tmpDir := t.TempDir()
	fake7z := filepath.Join(tmpDir, "stream.7z")
	if err := os.WriteFile(fake7z, []byte("fake7z"), 0644); err != nil {
		t.Fatal(err)
	}
	gateFile := filepath.Join(tmpDir, "gate")

	execCommand = func(name string, args ...string) *exec.Cmd {
		return streamListMockCmd("7z-slt-gate", gateFile)
	}

	spy := &streamingGateWriter{gateFile: gateFile}
	if err := DoList(fake7z, "", spy); err != nil {
		t.Fatalf("DoList failed: %v", err)
	}
	out := spy.buf.String()
	if strings.Contains(out, "buffered_timeout.txt") || !strings.Contains(out, "second.txt") {
		t.Fatalf("DoList buffered entire archive instead of streaming rows incrementally:\n%s", out)
	}
}

func TestDoList_SanitizesTerminalControlCharsPreservesUTF8(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "ctrl.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	for _, name := range []string{"normal_español_ñ.txt", "evil\x1b[31m_red\r.txt"} {
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0644, Size: 3})
		_, _ = tw.Write([]byte("abc"))
	}
	_ = tw.Close()
	_ = f.Close()

	var buf bytes.Buffer
	if err := DoList(tarPath, "", &buf); err != nil {
		t.Fatalf("DoList failed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "normal_español_ñ.txt") {
		t.Errorf("valid UTF-8 name mangled:\n%s", out)
	}
	if strings.Contains(out, "\x1b[31m") || strings.Contains(out, "\r") {
		t.Errorf("raw terminal control sequence leaked in DoList output:\n%q", out)
	}
}

