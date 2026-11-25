package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoDiffComparison(t *testing.T) {
	tmpDir := t.TempDir()
	tar1 := filepath.Join(tmpDir, "v1.tar")
	f1, err := os.Create(tar1)
	if err != nil {
		t.Fatal(err)
	}
	tw1 := tar.NewWriter(f1)
	tw1.WriteHeader(&tar.Header{Name: "common.txt", Mode: 0644, Size: 10, Typeflag: tar.TypeReg})
	tw1.Write(make([]byte, 10))
	tw1.WriteHeader(&tar.Header{Name: "removed.txt", Mode: 0644, Size: 20, Typeflag: tar.TypeReg})
	tw1.Write(make([]byte, 20))
	tw1.WriteHeader(&tar.Header{Name: "modified.txt", Mode: 0644, Size: 30, Typeflag: tar.TypeReg})
	tw1.Write(make([]byte, 30))
	tw1.Close()
	f1.Close()

	tar2 := filepath.Join(tmpDir, "v2.tar")
	f2, err := os.Create(tar2)
	if err != nil {
		t.Fatal(err)
	}
	tw2 := tar.NewWriter(f2)
	tw2.WriteHeader(&tar.Header{Name: "common.txt", Mode: 0644, Size: 10, Typeflag: tar.TypeReg})
	tw2.Write(make([]byte, 10))
	tw2.WriteHeader(&tar.Header{Name: "added.txt", Mode: 0644, Size: 40, Typeflag: tar.TypeReg})
	tw2.Write(make([]byte, 40))
	tw2.WriteHeader(&tar.Header{Name: "modified.txt", Mode: 0644, Size: 50, Typeflag: tar.TypeReg})
	tw2.Write(make([]byte, 50))
	tw2.Close()
	f2.Close()

	var buf bytes.Buffer
	err = DoDiff(tar1, tar2, "", &buf)
	if err != nil {
		t.Fatalf("DoDiff failed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "+ added.txt") {
		t.Errorf("missing added item in output: %s", out)
	}
	if !strings.Contains(out, "- removed.txt") {
		t.Errorf("missing removed item in output: %s", out)
	}
	if !strings.Contains(out, "~ modified.txt") {
		t.Errorf("missing modified item in output: %s", out)
	}
	if !strings.Contains(out, "Resumen:") {
		t.Errorf("missing summary line in output: %s", out)
	}
}

func TestDoDiff_NormalizesCrossFormatPaths(t *testing.T) {
	tmpDir := t.TempDir()
	tar1 := filepath.Join(tmpDir, "a.tar")
	tar2 := filepath.Join(tmpDir, "b.tar")

	{
		f, _ := os.Create(tar1)
		tw := tar.NewWriter(f)
		_ = tw.WriteHeader(&tar.Header{Name: "./dir/", Mode: 0755, Typeflag: tar.TypeDir})
		_ = tw.WriteHeader(&tar.Header{Name: "./dir/file.txt", Mode: 0644, Size: 10, Typeflag: tar.TypeReg})
		_, _ = tw.Write(make([]byte, 10))
		_ = tw.WriteHeader(&tar.Header{Name: `sub\a.txt`, Mode: 0644, Size: 20, Typeflag: tar.TypeReg})
		_, _ = tw.Write(make([]byte, 20))
		_ = tw.Close()
		_ = f.Close()
	}
	{
		f, _ := os.Create(tar2)
		tw := tar.NewWriter(f)
		_ = tw.WriteHeader(&tar.Header{Name: "dir", Mode: 0755, Typeflag: tar.TypeDir})
		_ = tw.WriteHeader(&tar.Header{Name: "dir/file.txt", Mode: 0644, Size: 10, Typeflag: tar.TypeReg})
		_, _ = tw.Write(make([]byte, 10))
		_ = tw.WriteHeader(&tar.Header{Name: "sub/a.txt", Mode: 0644, Size: 20, Typeflag: tar.TypeReg})
		_, _ = tw.Write(make([]byte, 20))
		_ = tw.Close()
		_ = f.Close()
	}

	var buf bytes.Buffer
	if err := DoDiff(tar1, tar2, "", &buf); err != nil {
		t.Fatalf("DoDiff failed: %v", err)
	}
	out := buf.String()
	wantSummary := "+0 añadido(s), -0 eliminado(s), ~0 modificado(s), 3 sin cambios"
	if !strings.Contains(out, wantSummary) {
		t.Errorf("expected %q, got:\n%s", wantSummary, out)
	}
}

