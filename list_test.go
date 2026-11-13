package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"os"
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
}
