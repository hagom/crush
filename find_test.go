package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoFindMatches(t *testing.T) {
	tmpDir := t.TempDir()
	tar1 := filepath.Join(tmpDir, "backup1.tar")
	f1, err := os.Create(tar1)
	if err != nil {
		t.Fatal(err)
	}
	tw1 := tar.NewWriter(f1)
	tw1.WriteHeader(&tar.Header{Name: "database.sql", Mode: 0644, Size: 1024, Typeflag: tar.TypeReg})
	tw1.Write(make([]byte, 1024))
	tw1.WriteHeader(&tar.Header{Name: "index.html", Mode: 0644, Size: 200, Typeflag: tar.TypeReg})
	tw1.Write(make([]byte, 200))
	tw1.Close()
	f1.Close()

	var buf bytes.Buffer
	matches, err := DoFind("*.sql", []string{tar1}, "", &buf)
	if err != nil {
		t.Fatalf("DoFind failed: %v", err)
	}
	if matches != 1 {
		t.Errorf("got %d matches, want 1", matches)
	}
	out := buf.String()
	if !strings.Contains(out, "database.sql") || strings.Contains(out, "index.html") {
		t.Errorf("unexpected output: %s", out)
	}
}

func TestDoFindSubstring(t *testing.T) {
	tmpDir := t.TempDir()
	tar1 := filepath.Join(tmpDir, "backup2.tar")
	f1, err := os.Create(tar1)
	if err != nil {
		t.Fatal(err)
	}
	tw1 := tar.NewWriter(f1)
	tw1.WriteHeader(&tar.Header{Name: "etc/nginx/conf.d/default.conf", Mode: 0644, Size: 50, Typeflag: tar.TypeReg})
	tw1.Write(make([]byte, 50))
	tw1.Close()
	f1.Close()

	var buf bytes.Buffer
	matches, err := DoFind("nginx", []string{tar1}, "", &buf)
	if err != nil {
		t.Fatalf("DoFind failed: %v", err)
	}
	if matches != 1 {
		t.Errorf("got %d matches, want 1", matches)
	}
	if !strings.Contains(buf.String(), "default.conf") {
		t.Errorf("output does not contain match: %s", buf.String())
	}
}
