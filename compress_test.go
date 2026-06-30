package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompressDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{
		Format:    Gz,
		DryRun:    true,
		OutputDir: tmpDir,
	}

	err := DoCompress([]string{testFile}, opts)
	if err != nil {
		t.Errorf("DoCompress dry-run = %v", err)
	}
}

func TestCompressNoFiles(t *testing.T) {
	opts := CompressOptions{
		Format:    Gz,
		OutputDir: ".",
	}
	err := DoCompress(nil, opts)
	if err == nil {
		t.Error("DoCompress with nil files should error")
	}
}

func TestExpandGlobs(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "a.txt")
	f2 := filepath.Join(tmpDir, "b.txt")
	os.WriteFile(f1, []byte("a"), 0644)
	os.WriteFile(f2, []byte("b"), 0644)

	result := expandGlobs([]string{filepath.Join(tmpDir, "*.txt")})
	if len(result) != 2 {
		t.Errorf("expandGlobs got %d files, want 2", len(result))
	}
}

func TestCompressModeDesc(t *testing.T) {
	desc := compressModeDesc(Gz)
	if desc == "" {
		t.Error("compressModeDesc(Gz) = empty")
	}
}

func TestFastOrSlow(t *testing.T) {
	opts := CompressOptions{CompressionOpts: ""}
	lvl := fastOrSlow(opts, 6)
	if lvl != 6 {
		t.Errorf("fastOrSlow with empty opts = %d, want 6", lvl)
	}

	opts.CompressionOpts = "-5"
	lvl = fastOrSlow(opts, 6)
	if lvl != 6 {
		t.Errorf("fastOrSlow with opts = %d, want 6", lvl)
	}
}
