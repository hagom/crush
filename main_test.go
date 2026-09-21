package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReorderArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "combined short flags",
			args: []string{"crush", "-c", "-f", "gz", "file.txt", "-tkv"},
			want: []string{"crush", "-c", "-f", "gz", "-t", "-k", "-v", "file.txt"},
		},
		{
			name: "flags after positional args",
			args: []string{"crush", "-c", "-f", "7z", "file.txt", "-k", "-v"},
			want: []string{"crush", "-c", "-f", "7z", "-k", "-v", "file.txt"},
		},
		{
			name: "no args",
			args: []string{"crush"},
			want: []string{"crush"},
		},
		{
			name: "no flags",
			args: []string{"crush", "file.txt"},
			want: []string{"crush", "file.txt"},
		},
		{
			name: "combined with single flag",
			args: []string{"crush", "-c", "-f", "gz", "file.txt", "-tv"},
			want: []string{"crush", "-c", "-f", "gz", "-t", "-v", "file.txt"},
		},
		{
			name: "long flags untouched",
			args: []string{"crush", "--force", "file.txt"},
			want: []string{"crush", "--force", "file.txt"},
		},
		{
			name: "flag with value preserved",
			args: []string{"crush", "-f", "gz", "-o", "/tmp/out", "file.txt"},
			want: []string{"crush", "-f", "gz", "-o", "/tmp/out", "file.txt"},
		},
		{
			name: "split flag with value",
			args: []string{"crush", "-c", "-f", "gz", "-s", "10", "file.txt"},
			want: []string{"crush", "-c", "-f", "gz", "-s", "10", "file.txt"},
		},
		{
			name: "completion flag with value after positional",
			args: []string{"crush", "file.txt", "-completion", "bash"},
			want: []string{"crush", "-completion", "bash", "file.txt"},
		},
		{
			name: "input list flag -i with value",
			args: []string{"crush", "-c", "-f", "gz", "-i", "list.txt"},
			want: []string{"crush", "-c", "-f", "gz", "-i", "list.txt"},
		},
		{
			name: "input list flag -i after positional",
			args: []string{"crush", "extra.txt", "-i", "list.txt"},
			want: []string{"crush", "-i", "list.txt", "extra.txt"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reorderArgs(tt.args)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("reorderArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMainEmptyFilesValidation(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "-l")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("crush -l without files expected error exit code, got 0")
	}
	if !strings.Contains(string(out), "debe especificar archivos") {
		t.Errorf("crush -l without files output = %q, want 'debe especificar archivos'", string(out))
	}
}

func TestInstallHelpText(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "-h")
	out, _ := cmd.CombinedOutput()
	help := string(out)
	found := false
	for _, line := range strings.Split(help, "\n") {
		if strings.Contains(line, "--install") && strings.Contains(line, "Instalar") && !strings.Contains(line, "--install-deps") {
			found = true
			if strings.Contains(line, "herramientas faltantes") {
				t.Errorf("--install help text still mentions 'herramientas faltantes': %s", line)
			}
			if !strings.Contains(line, "/usr/local/bin") {
				t.Errorf("--install help text should mention /usr/local/bin: %s", line)
			}
		}
	}
	if !found {
		t.Errorf("--install flag description not found in help output")
	}
}

func TestInstallBinaryTo(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "crush_dummy")
	if err := os.WriteFile(src, []byte("#!/bin/sh\necho test\n"), 0755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(tmpDir, "bin", "crush")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := installBinaryTo(src, dest); err != nil {
		t.Fatalf("installBinaryTo failed: %v", err)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat dest failed: %v", err)
	}
	if fi.Mode()&0111 == 0 {
		t.Errorf("dest permissions not executable: %v", fi.Mode())
	}
}

func TestMainDecompressScanNoFiles(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "crush_bin")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build falló: %v, salida: %s", err, string(out))
	}

	tmpDir := t.TempDir()
	cmd := exec.Command(binPath, "-d")
	cmd.Dir = tmpDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("crush -d en dir vacío falló: %v, salida: %s", err, string(out))
	}
	if !strings.Contains(string(out), "No se encontraron archivos comprimidos") {
		t.Errorf("salida esperada contenía 'No se encontraron archivos comprimidos', obtenida: %s", string(out))
	}
}

func TestMainDecompressScanCancel(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "crush_bin")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build falló: %v, salida: %s", err, string(out))
	}

	tmpDir := t.TempDir()
	zipFile := filepath.Join(tmpDir, "sample.zip")
	if err := os.WriteFile(zipFile, []byte("dummy zip content"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "-d")
	cmd.Dir = tmpDir
	cmd.Stdin = strings.NewReader("n\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("crush -d cancelado falló con error: %v, salida: %s", err, string(out))
	}
	if !strings.Contains(string(out), "Operación cancelada") {
		t.Errorf("salida esperada contenía 'Operación cancelada', obtenida: %s", string(out))
	}
}

func TestMainDecompressScanConfirm(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "crush_bin")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build falló: %v, salida: %s", err, string(out))
	}

	tmpDir := t.TempDir()
	txtPath := filepath.Join(tmpDir, "hello.txt")
	if err := os.WriteFile(txtPath, []byte("contenido de prueba"), 0644); err != nil {
		t.Fatal(err)
	}
	tarPath := filepath.Join(tmpDir, "hello.tar")
	cmdTar := exec.Command("tar", "-cf", tarPath, "-C", tmpDir, "hello.txt")
	if err := cmdTar.Run(); err != nil {
		t.Skip("tar no disponible para test")
	}
	// Eliminar original para comprobar que se extrae
	os.Remove(txtPath)

	cmd := exec.Command(binPath, "-d")
	cmd.Dir = tmpDir
	cmd.Stdin = strings.NewReader("s\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("crush -d confirmado falló: %v, salida: %s", err, string(out))
	}

	// Verificar que hello.txt fue extraído
	if _, err := os.Stat(txtPath); err != nil {
		t.Errorf("archivo esperado %s no fue extraído tras confirmación: %v, salida: %s", txtPath, err, string(out))
	}
}

