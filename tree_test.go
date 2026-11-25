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

func TestDoTree_WeirdPathsAndChildPreservation(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "weird_tree.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	_ = tw.WriteHeader(&tar.Header{Name: "./", Mode: 0755, Typeflag: tar.TypeDir})
	_ = tw.WriteHeader(&tar.Header{Name: "///src//main.go", Mode: 0644, Size: 10, Typeflag: tar.TypeReg})
	_, _ = tw.Write(make([]byte, 10))
	_ = tw.WriteHeader(&tar.Header{Name: `win\pkg\mod.go`, Mode: 0644, Size: 20, Typeflag: tar.TypeReg})
	_, _ = tw.Write(make([]byte, 20))
	_ = tw.WriteHeader(&tar.Header{Name: "sub/file.txt", Mode: 0644, Size: 30, Typeflag: tar.TypeReg})
	_, _ = tw.Write(make([]byte, 30))
	// Duplicate/conflicting file entry for existing dir "sub" must not hide "file.txt"
	_ = tw.WriteHeader(&tar.Header{Name: "sub", Mode: 0644, Size: 5, Typeflag: tar.TypeReg})
	_, _ = tw.Write(make([]byte, 5))
	_ = tw.WriteHeader(&tar.Header{Name: "evil\x1b[2J.txt", Mode: 0644, Size: 4, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("abcd"))
	_ = tw.Close()
	_ = f.Close()

	var buf bytes.Buffer
	if err := DoTree([]string{tarPath}, "", &buf); err != nil {
		t.Fatalf("DoTree failed: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"src/", "main.go", "win/", "pkg/", "mod.go", "sub/", "file.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("DoTree output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b[2J") {
		t.Errorf("DoTree leaked raw ANSI escape sequence:\n%q", out)
	}
	if strings.Contains(out, "├── ./") || strings.Contains(out, "└── ./") {
		t.Errorf("DoTree rendered bogus '.' node:\n%s", out)
	}
}

