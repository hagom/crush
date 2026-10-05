package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoConvertTarGzToTarZst(t *testing.T) {
	if !hasTool("pigz") && !hasTool("gzip") {
		t.Skip("gzip tool not available")
	}
	if !hasTool("zstd") {
		t.Skip("zstd tool not available")
	}

	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "data.txt")
	if err := os.WriteFile(srcFile, []byte("transcoding streaming content pipe test"), 0644); err != nil {
		t.Fatal(err)
	}

	origArchives, err := DoCompress([]string{srcFile}, CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		KeepOrig:  true,
		Combine:   true,
	})
	if err != nil || len(origArchives) == 0 {
		t.Fatalf("setup compress failed: %v", err)
	}

	opts := ConvertOptions{
		KeepOrig: true,
	}
	err = DoConvert(origArchives, Zst, opts)
	if err != nil {
		t.Fatalf("DoConvert failed: %v", err)
	}

	expectedOut := filepath.Join(tmpDir, StripTarSuffix(filepath.Base(origArchives[0]))+".tar.zst")
	if _, err := os.Stat(expectedOut); os.IsNotExist(err) {
		t.Errorf("converted file %s does not exist", expectedOut)
	}

	// Verify test cmd can verify converted file
	if err := DoTest([]string{expectedOut}, TestOptions{}); err != nil {
		t.Errorf("DoTest failed on converted archive: %v", err)
	}
}

func TestDoConvertStreamGzToXz(t *testing.T) {
	if !hasTool("pigz") && !hasTool("gzip") {
		t.Skip("gzip tool not available")
	}
	if !hasTool("xz") {
		t.Skip("xz tool not available")
	}

	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "single.txt")
	content := []byte("plain stream transcoding single file")
	if err := os.WriteFile(srcFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	origArchives, err := DoCompress([]string{srcFile}, CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		KeepOrig:  true,
	})
	if err != nil || len(origArchives) == 0 {
		t.Fatalf("setup compress failed: %v", err)
	}

	opts := ConvertOptions{
		KeepOrig: true,
	}
	err = DoConvert(origArchives, Xz, opts)
	if err != nil {
		t.Fatalf("DoConvert failed: %v", err)
	}

	expectedOut := filepath.Join(tmpDir, "single.txt.xz")
	if _, err := os.Stat(expectedOut); os.IsNotExist(err) {
		t.Errorf("converted file %s does not exist", expectedOut)
	}

	// Verify test cmd on converted file
	if err := DoTest([]string{expectedOut}, TestOptions{}); err != nil {
		t.Errorf("DoTest failed on converted archive: %v", err)
	}
}

func TestDoConvertZipToTarGz(t *testing.T) {
	if !hasTool("unzip") && !hasTool("7z") {
		t.Skip("unzip tool not available")
	}
	if !hasTool("pigz") && !hasTool("gzip") {
		t.Skip("gzip tool not available")
	}

	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "package.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w1, _ := zw.Create("file1.txt")
	w1.Write([]byte("content 1"))
	w2, _ := zw.Create("sub/file2.txt")
	w2.Write([]byte("content 2"))
	zw.Close()
	f.Close()

	opts := ConvertOptions{
		KeepOrig: true,
	}
	err = DoConvert([]string{zipPath}, Gz, opts)
	if err != nil {
		t.Fatalf("DoConvert zip to tar.gz failed: %v", err)
	}

	expectedOut := filepath.Join(tmpDir, "package.tar.gz")
	if _, err := os.Stat(expectedOut); os.IsNotExist(err) {
		t.Fatalf("converted file %s does not exist", expectedOut)
	}

	members, err := ListArchiveMembers(expectedOut, "")
	if err != nil {
		t.Fatalf("listing members failed: %v", err)
	}
	found1, found2 := false, false
	for _, m := range members {
		if strings.Contains(m.Path, "file1.txt") {
			found1 = true
		}
		if strings.Contains(m.Path, "file2.txt") {
			found2 = true
		}
	}
	if !found1 || !found2 {
		t.Errorf("expected members not found in converted tar.gz: %+v", members)
	}
}

func TestDoConvertKeepOrigAndHash(t *testing.T) {
	if !hasTool("pigz") && !hasTool("gzip") {
		t.Skip("gzip tool not available")
	}
	if !hasTool("zstd") {
		t.Skip("zstd tool not available")
	}

	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "item.txt")
	_ = os.WriteFile(srcFile, []byte("delete original and hash test"), 0644)

	origArchives, err := DoCompress([]string{srcFile}, CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		KeepOrig:  true,
		Combine:   true,
	})
	if err != nil || len(origArchives) == 0 {
		t.Fatalf("setup compress failed: %v", err)
	}
	origArchive := origArchives[0]

	opts := ConvertOptions{
		KeepOrig: false,
		Hash:     true,
	}
	err = DoConvert([]string{origArchive}, Zst, opts)
	if err != nil {
		t.Fatalf("DoConvert failed: %v", err)
	}

	// Original archive should be deleted
	if _, err := os.Stat(origArchive); !os.IsNotExist(err) {
		t.Errorf("original archive %s was not deleted when KeepOrig=false", origArchive)
	}

	expectedOut := filepath.Join(tmpDir, StripTarSuffix(filepath.Base(origArchive))+".tar.zst")
	if _, err := os.Stat(expectedOut); os.IsNotExist(err) {
		t.Errorf("converted archive %s does not exist", expectedOut)
	}

	// Check .sha256 file
	shaFile := expectedOut + ".sha256"
	if _, err := os.Stat(shaFile); os.IsNotExist(err) {
		t.Errorf("expected checksum file %s not found", shaFile)
	}
}

func TestDoConvertAlreadyTargetFormatError(t *testing.T) {
	tmpDir := t.TempDir()
	dummy := filepath.Join(tmpDir, "archive.tar.gz")
	_ = os.WriteFile(dummy, []byte("dummy"), 0644)

	err := DoConvert([]string{dummy}, Gz, ConvertOptions{})
	if err == nil {
		t.Error("expected error when converting to same format, got nil")
	}
}
