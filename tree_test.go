package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoTreeHierarchy(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "tree_test.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	tw.WriteHeader(&tar.Header{Name: "src/", Mode: 0755, Typeflag: tar.TypeDir})
	tw.WriteHeader(&tar.Header{Name: "src/main.go", Mode: 0644, Size: 100, Typeflag: tar.TypeReg})
	tw.Write(make([]byte, 100))
	tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0644, Size: 50, Typeflag: tar.TypeReg})
	tw.Write(make([]byte, 50))
	tw.Close()
	f.Close()

	var buf bytes.Buffer
	err = DoTree([]string{tarPath}, "", &buf)
	if err != nil {
		t.Fatalf("DoTree failed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "tree_test.tar") {
		t.Errorf("missing header in output: %s", out)
	}
	if !strings.Contains(out, "src") || !strings.Contains(out, "main.go") {
		t.Errorf("missing tree nodes in output: %s", out)
	}
	if !strings.Contains(out, "1 directorio") || !strings.Contains(out, "2 archivo") {
		t.Errorf("missing summary in output: %s", out)
	}
}
