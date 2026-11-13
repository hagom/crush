package main

import (
	"archive/zip"
	"compress/gzip"
	"os"
	"os/exec"
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

func TestDoConvertRelativePathToContainers(t *testing.T) {
	if !hasTool("pigz") && !hasTool("gzip") {
		t.Skip("gzip tool not available")
	}

	tests := []struct {
		name      string
		targetFmt Format
		needTool  string
		wantOut   string
		subOutDir string
	}{
		{
			name:      "relative tar.gz to zip in cwd",
			targetFmt: Zip,
			needTool:  "zip",
			wantOut:   "rel.zip",
		},
		{
			name:      "relative tar.gz to 7z with relative OutputDir",
			targetFmt: SevenZ,
			needTool:  sevenzBin(),
			wantOut:   filepath.Join("out", "rel.7z"),
			subOutDir: "out",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !hasTool(tt.needTool) && !(tt.targetFmt == Zip && hasTool(sevenzBin())) {
				t.Skipf("%s tool not available", tt.needTool)
			}

			tmpDir := t.TempDir()
			t.Chdir(tmpDir)

			srcFile := "hello.txt"
			if err := os.WriteFile(srcFile, []byte("relative convert content"), 0644); err != nil {
				t.Fatal(err)
			}

			archives, err := DoCompress([]string{srcFile}, CompressOptions{
				Format:   Gz,
				KeepOrig: true,
				Combine:  true,
			})
			if err != nil || len(archives) == 0 {
				t.Fatalf("setup compress failed: %v", err)
			}
			relArchive := "rel.tar.gz"
			if err := os.Rename(filepath.Base(archives[0]), relArchive); err != nil {
				t.Fatal(err)
			}
			if tt.subOutDir != "" {
				if err := os.MkdirAll(tt.subOutDir, 0755); err != nil {
					t.Fatal(err)
				}
			}

			err = DoConvert([]string{relArchive}, tt.targetFmt, ConvertOptions{
				OutputDir: tt.subOutDir,
				KeepOrig:  false,
			})
			if err != nil {
				t.Fatalf("DoConvert failed: %v", err)
			}

			if _, err := os.Stat(tt.wantOut); err != nil {
				t.Fatalf("converted archive %s missing on disk: %v", tt.wantOut, err)
			}
			if _, err := os.Stat(relArchive); !os.IsNotExist(err) {
				t.Errorf("original %s should be removed only after successful conversion", relArchive)
			}
			members, err := ListArchiveMembers(tt.wantOut, "")
			if err != nil {
				t.Fatalf("ListArchiveMembers(%s) failed: %v", tt.wantOut, err)
			}
			found := false
			for _, m := range members {
				if strings.HasSuffix(m.Path, "hello.txt") {
					found = true
				}
			}
			if !found {
				t.Errorf("converted archive %s missing hello.txt: %+v", tt.wantOut, members)
			}
		})
	}
}

func TestDoConvertDoesNotDeleteOrigIfOutputMissing(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "src.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("a.txt")
	_, _ = w.Write([]byte("abc"))
	zw.Close()
	f.Close()

	origExec := execCommand
	defer func() { execCommand = origExec }()
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == sevenzBin() && len(args) > 0 && args[0] == "a" {
			return mockExecCommand("mock-noop")
		}
		return exec.Command(name, args...)
	}

	err = DoConvert([]string{zipPath}, SevenZ, ConvertOptions{KeepOrig: false})
	if err == nil {
		t.Fatal("expected error when output archive was never created, got nil")
	}
	if _, statErr := os.Stat(zipPath); statErr != nil {
		t.Errorf("original %s was deleted even though output was not created: %v", zipPath, statErr)
	}
}

func TestDoConvertNoForceRejectsBrokenSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	srcGz := filepath.Join(tmpDir, "sample.txt.gz")
	f, err := os.Create(srcGz)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	_, _ = gw.Write([]byte("hello broken symlink"))
	_ = gw.Close()
	_ = f.Close()

	danglingTarget := filepath.Join(tmpDir, "nonexistent_target")
	brokenLink := filepath.Join(tmpDir, "sample.txt.xz")
	if err := os.Symlink(danglingTarget, brokenLink); err != nil {
		t.Skipf("symlinks not supported: %v", err)
	}

	origExec := execCommand
	defer func() { execCommand = origExec }()
	execCommand = func(name string, args ...string) *exec.Cmd {
		return mockExecCommand("mock-noop")
	}

	err = DoConvert([]string{srcGz}, Xz, ConvertOptions{Force: false, KeepOrig: true})
	if err == nil {
		t.Fatalf("expected error when destination is a broken symlink and Force=false")
	}
	if !strings.Contains(err.Error(), "ya existe") {
		t.Fatalf("expected already-exists error, got: %v", err)
	}
	if _, statErr := os.Stat(danglingTarget); !os.IsNotExist(statErr) {
		t.Errorf("broken symlink target %s should not have been created", danglingTarget)
	}
}
