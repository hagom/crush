package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTestNoFiles(t *testing.T) {
	opts := TestOptions{}
	err := DoTest(nil, opts)
	if err == nil {
		t.Error("DoTest with nil files should error")
	}
}

func TestTestFileTar(t *testing.T) {
	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "sample.txt")
	if err := os.WriteFile(txtFile, []byte("contenido de prueba"), 0644); err != nil {
		t.Fatal(err)
	}

	tarPath := filepath.Join(tmpDir, "archive.tar")
	cmd := exec.Command("tar", "-cf", tarPath, "-C", tmpDir, "sample.txt")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("error creando tar de prueba: %v, out: %s", err, string(out))
	}

	opts := TestOptions{}
	status, err := TestFile(tarPath, opts)
	if err != nil {
		t.Fatalf("TestFile en .tar válido falló: %v (status=%s)", err, status)
	}
	if status != "OK" {
		t.Errorf("esperaba status OK para .tar válido, obtuve: %s", status)
	}

	corruptTar := filepath.Join(tmpDir, "corrupt.tar")
	if err := os.WriteFile(corruptTar, []byte("archivo tar corrupto"), 0644); err != nil {
		t.Fatal(err)
	}
	status, err = TestFile(corruptTar, opts)
	if err == nil {
		t.Errorf("esperaba error para .tar corrupto, obtuve status=%s", status)
	}
}

func TestTestFileGz(t *testing.T) {
	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "sample.txt")
	if err := os.WriteFile(txtFile, []byte("contenido gzip"), 0644); err != nil {
		t.Fatal(err)
	}

	gzPath := filepath.Join(tmpDir, "sample.txt.gz")
	gzTool := "gzip"
	if hasTool("pigz") {
		gzTool = "pigz"
	}
	cmd := exec.Command(gzTool, "-c", txtFile)
	outFile, err := os.Create(gzPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = outFile
	if err := cmd.Run(); err != nil {
		outFile.Close()
		t.Fatal(err)
	}
	outFile.Close()

	opts := TestOptions{}
	status, err := TestFile(gzPath, opts)
	if err != nil {
		t.Fatalf("TestFile en .gz válido falló: %v", err)
	}
	if status != "OK" {
		t.Errorf("esperaba status OK para .gz, obtuve: %s", status)
	}
}

func TestTestFileUnsupported(t *testing.T) {
	tmpDir := t.TempDir()
	dummy := filepath.Join(tmpDir, "archivo.desconocido")
	if err := os.WriteFile(dummy, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	opts := TestOptions{}
	_, err := TestFile(dummy, opts)
	if err == nil {
		t.Error("esperaba error para formato no reconocido")
	}
}

func TestDoTestEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	opts := TestOptions{}

	// Archivos inexistentes
	err := DoTest([]string{filepath.Join(tmpDir, "inexistente.tar")}, opts)
	if err == nil {
		t.Error("esperaba error cuando los archivos no existen")
	}

	// Directorio en vez de archivo
	dirOnly := filepath.Join(tmpDir, "un_directorio")
	if err := os.Mkdir(dirOnly, 0755); err != nil {
		t.Fatal(err)
	}
	err = DoTest([]string{dirOnly}, opts)
	if err == nil {
		t.Error("esperaba error cuando solo se pasan directorios a DoTest")
	}
}
