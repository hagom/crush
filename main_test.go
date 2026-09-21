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

