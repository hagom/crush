package main

import (
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
