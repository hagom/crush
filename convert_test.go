package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
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

func TestBuildDecompressCmdForFile_MemorySafeFlags(t *testing.T) {
	origMem := getMemLimit
	defer func() { getMemLimit = origMem }()
	getMemLimit = func() int { return 800 }

	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "archive.dat")
	if err := os.WriteFile(src, []byte("small archive"), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		info     FormatInfo
		threads  int
		budgetMB int
		wantArgs []string
	}{
		{
			name:     "xz decompression applies thread cap and --memlimit-mt-decompress",
			info:     FormatInfo{Format: Xz, Tool: "xz"},
			threads:  16,
			budgetMB: 800,
			wantArgs: []string{"-dc", "-T1", "--memlimit-mt-decompress=800MiB"},
		},
		{
			name:     "lrzip decompression applies thread cap and -m in hundreds of MB",
			info:     FormatInfo{Format: Lrz, Tool: "lrzip"},
			threads:  16,
			budgetMB: 800,
			wantArgs: []string{"-d", "-p", "1", "-o", "-", "-m", "8"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := buildDecompressCmdForFileWithMem(src, tt.info, tt.threads, tt.budgetMB)
			for _, w := range tt.wantArgs {
				if !containsArg(cmd.Args, w) {
					t.Errorf("args = %v, missing %q", cmd.Args, w)
				}
			}
		})
	}
}

func TestConvertStreamPipe_SplitsMemoryBudgetBetweenStages(t *testing.T) {
	origMem := getMemLimit
	defer func() { getMemLimit = origMem }()
	getMemLimit = func() int { return 1024 } // 2 concurrent stages -> 512MB per stage

	origExec := execCommand
	defer func() { execCommand = origExec }()

	var xzDecompArgs, zstCompArgs []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		switch name {
		case "xz":
			xzDecompArgs = append([]string(nil), args...)
			return mockExecCommand("mock-noop")
		case "zstd":
			zstCompArgs = append([]string(nil), args...)
			return mockExecCommand("mock-noop")
		}
		return exec.Command(name, args...)
	}

	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "archive.tar.xz")
	if err := os.WriteFile(src, []byte("compressed content"), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(tmpDir, "archive.tar.zst")

	err := convertStreamPipe(src, out, FormatInfo{Format: Xz, Tool: "xz", IsTar: true}, Zst, ConvertOptions{
		ThreadLimit: 16,
	})
	if err != nil {
		t.Fatalf("convertStreamPipe failed: %v", err)
	}

	if !containsArg(xzDecompArgs, "--memlimit-mt-decompress=512MiB") || !containsArg(xzDecompArgs, "-T1") {
		t.Errorf("decompressor stage expected 512MiB budget and -T1, got %v", xzDecompArgs)
	}
	if !containsArg(zstCompArgs, "-19") || containsArg(zstCompArgs, "--ultra") || !containsArg(zstCompArgs, "-T1") {
		t.Errorf("compressor stage expected 512MiB budget (zstd -19 without --ultra and -T1), got %v", zstCompArgs)
	}
}

func TestConvertAudit_SecurityForceAndPipeCleanup(t *testing.T) {
	t.Run("DoConvert rejects Auto target format without deleting original", func(t *testing.T) {
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "data.txt.gz")
		f, err := os.Create(src)
		if err != nil {
			t.Fatal(err)
		}
		gw := gzip.NewWriter(f)
		_, _ = gw.Write([]byte("payload"))
		_ = gw.Close()
		_ = f.Close()

		if err := DoConvert([]string{src}, Auto, ConvertOptions{KeepOrig: false}); err == nil {
			t.Fatalf("DoConvert(..., Auto) expected error, got nil")
		}
		if _, err := os.Stat(src); err != nil {
			t.Errorf("original %s should remain intact on invalid format: %v", src, err)
		}
	})

	t.Run("Force overwrite replaces existing zip instead of merging stale members", func(t *testing.T) {
		if !hasTool("zip") && !hasTool(sevenzBin()) {
			t.Skip("zip/7z not available")
		}
		tmpDir := t.TempDir()

		// Create source tar.gz containing only fresh.txt
		fresh := filepath.Join(tmpDir, "fresh.txt")
		if err := os.WriteFile(fresh, []byte("new content"), 0644); err != nil {
			t.Fatal(err)
		}
		srcTarGz := filepath.Join(tmpDir, "bundle.tar.gz")
		out, err := DoCompress([]string{fresh}, CompressOptions{Format: Gz, OutputDir: tmpDir, Combine: true, KeepOrig: true})
		if err != nil || len(out) == 0 {
			t.Fatalf("setup compress failed: %v", err)
		}
		if err := os.Rename(out[0], srcTarGz); err != nil {
			t.Fatal(err)
		}

		// Pre-create destination bundle.zip with an unrelated stale.txt
		destZip := filepath.Join(tmpDir, "bundle.zip")
		zf, err := os.Create(destZip)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(zf)
		w, _ := zw.Create("stale.txt")
		_, _ = w.Write([]byte("old stale data"))
		_ = zw.Close()
		_ = zf.Close()

		if err := DoConvert([]string{srcTarGz}, Zip, ConvertOptions{Force: true, KeepOrig: true}); err != nil {
			t.Fatalf("DoConvert -force failed: %v", err)
		}

		members, err := ListArchiveMembers(destZip, "")
		if err != nil {
			t.Fatalf("ListArchiveMembers failed: %v", err)
		}
		var names []string
		for _, m := range members {
			names = append(names, m.Path)
			if strings.Contains(m.Path, "stale.txt") {
				t.Errorf("Force overwrite merged stale.txt from previous archive instead of replacing: %v", members)
			}
		}
		if !strings.Contains(strings.Join(names, " "), "fresh.txt") {
			t.Errorf("converted zip missing fresh.txt: %v", names)
		}
	})

	t.Run("convertViaTempDir passes -- separator and uses sevenzBin and rar", func(t *testing.T) {
		tmpDir := t.TempDir()
		srcZip := filepath.Join(tmpDir, "in.zip")
		zf, err := os.Create(srcZip)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(zf)
		w, _ := zw.Create("a.txt")
		_, _ = w.Write([]byte("hello"))
		_ = zw.Close()
		_ = zf.Close()

		t.Run("tar uses --", func(t *testing.T) {
			origExec := execCommand
			defer func() { execCommand = origExec }()
			var tarArgs []string
			execCommand = func(name string, args ...string) *exec.Cmd {
				if name == "tar" && len(args) > 0 && args[0] == "-cf" {
					tarArgs = append([]string(nil), args...)
				}
				return origExec(name, args...)
			}
			outTar := filepath.Join(tmpDir, "out.tar")
			if err := convertViaTempDir(srcZip, outTar, FormatInfo{Format: Zip}, Tar, ConvertOptions{Force: true}); err != nil {
				t.Fatalf("convertViaTempDir(Tar) failed: %v", err)
			}
			if !containsArg(tarArgs, "--") {
				t.Errorf("tar args missing '--': %v", tarArgs)
			}
		})

		t.Run("7z uses --", func(t *testing.T) {
			origExec := execCommand
			defer func() { execCommand = origExec }()
			var szArgs []string
			execCommand = func(name string, args ...string) *exec.Cmd {
				if name == sevenzBin() && len(args) > 0 && args[0] == "a" {
					szArgs = append([]string(nil), args...)
					return mockExecCommand("mock-noop")
				}
				return origExec(name, args...)
			}
			out7z := filepath.Join(tmpDir, "out.7z")
			_ = convertViaTempDir(srcZip, out7z, FormatInfo{Format: Zip}, SevenZ, ConvertOptions{Force: true})
			if !containsArg(szArgs, "--") {
				t.Errorf("7z args missing '--': %v", szArgs)
			}
		})

		t.Run("rar invokes rar binary (not unrar) with --", func(t *testing.T) {
			origLookPath := lookPath
			defer func() { lookPath = origLookPath }()
			lookPath = func(name string) (string, error) {
				if name == "rar" || name == "unzip" || name == sevenzBin() {
					return "/usr/bin/" + name, nil
				}
				return "", os.ErrNotExist
			}
			origExec := execCommand
			defer func() { execCommand = origExec }()
			var rarTool string
			var rarArgs []string
			execCommand = func(name string, args ...string) *exec.Cmd {
				if len(args) > 0 && args[0] == "a" && (name == "rar" || name == "unrar") {
					rarTool = name
					rarArgs = append([]string(nil), args...)
					return mockExecCommand("mock-noop")
				}
				return origExec(name, args...)
			}
			outRar := filepath.Join(tmpDir, "out.rar")
			_ = convertViaTempDir(srcZip, outRar, FormatInfo{Format: Zip}, Rar, ConvertOptions{Force: true})
			if rarTool != "rar" {
				t.Errorf("expected rar tool for creation, got %q", rarTool)
			}
			if !containsArg(rarArgs, "--") {
				t.Errorf("rar args missing '--': %v", rarArgs)
			}
		})
	})

	t.Run("convertStreamPipe reaps producer when compressor Start fails", func(t *testing.T) {
		origExec := execCommand
		defer func() { execCommand = origExec }()

		var prodCmd *exec.Cmd
		execCommand = func(name string, args ...string) *exec.Cmd {
			if name == "xz" {
				prodCmd = mockExecCommand("mock-producer")
				return prodCmd
			}
			return exec.Command("/nonexistent-compressor-binary-for-test")
		}

		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "a.tar.xz")
		if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(tmpDir, "a.tar.zst")

		err := convertStreamPipe(src, out, FormatInfo{Format: Xz, Tool: "xz", IsTar: true}, Zst, ConvertOptions{})
		if err == nil {
			t.Fatal("expected error when compressor fails to start")
		}
		if prodCmd == nil || prodCmd.ProcessState == nil {
			t.Errorf("producer process was not reaped via Wait() after consumer Start failure (zombie leak)")
		}
		if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
			t.Errorf("partial output %s should not exist after Start failure", out)
		}
	})
}

func TestSelectConvertTempBase(t *testing.T) {
	tmpDir := t.TempDir()
	fakeTmp := filepath.Join(tmpDir, "fake_tmp")
	outDir := filepath.Join(tmpDir, "dest_out")
	_ = os.MkdirAll(fakeTmp, 0o755)
	_ = os.MkdirAll(outDir, 0o755)

	zipPath := filepath.Join(tmpDir, "data.zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, _ := zw.Create("big.txt")
	_, _ = w.Write(make([]byte, 4096))
	_ = zw.Close()
	_ = zf.Close()

	outPath := filepath.Join(outDir, "data.tar")

	tests := []struct {
		name     string
		tmpAvail int64
		wantBase string
	}{
		{
			name:     "uses convertTempDir when space exceeds estimated size plus 10 percent margin",
			tmpAvail: 1 << 20,
			wantBase: fakeTmp,
		},
		{
			name:     "falls back to output dir when tmp space is less than estimated size plus margin",
			tmpAvail: 4200, // < 4096 + 409 (4505)
			wantBase: outDir,
		},
		{
			name:     "keeps convertTempDir when getDiskAvail returns -1",
			tmpAvail: -1,
			wantBase: fakeTmp,
		},
		{
			name:     "falls back to output dir when tmp disk is full (0 bytes)",
			tmpAvail: 0,
			wantBase: outDir,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origTmp := convertTempDir
			origAvail := getDiskAvail
			defer func() {
				convertTempDir = origTmp
				getDiskAvail = origAvail
			}()
			convertTempDir = func() string { return fakeTmp }
			getDiskAvail = func(dir string) int64 {
				if dir == fakeTmp {
					return tt.tmpAvail
				}
				return 1 << 30
			}

			got := selectConvertTempBase(zipPath, outPath)
			if got != tt.wantBase {
				t.Errorf("selectConvertTempBase() = %q, want %q", got, tt.wantBase)
			}
		})
	}
}

func TestConvertViaTempDir_FallbackDirWhenTmpLowSpace(t *testing.T) {
	if !hasTool("unzip") && !hasTool(sevenzBin()) {
		t.Skip("unzip/7z not available")
	}
	tmpDir := t.TempDir()
	fakeTmp := filepath.Join(tmpDir, "tiny_tmp")
	outDir := filepath.Join(tmpDir, "big_disk")
	_ = os.MkdirAll(fakeTmp, 0o755)
	_ = os.MkdirAll(outDir, 0o755)

	zipPath := filepath.Join(tmpDir, "archive.zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, _ := zw.Create("item.txt")
	_, _ = w.Write([]byte("disk aware staging test payload"))
	_ = zw.Close()
	_ = zf.Close()

	origTmp := convertTempDir
	origAvail := getDiskAvail
	origExec := execCommand
	defer func() {
		convertTempDir = origTmp
		getDiskAvail = origAvail
		execCommand = origExec
	}()
	convertTempDir = func() string { return fakeTmp }
	getDiskAvail = func(dir string) int64 {
		if dir == fakeTmp {
			return 10
		}
		return 1 << 30
	}

	var capturedStagingDir string
	var capturedPerm os.FileMode
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "tar" {
			for i, a := range args {
				if a == "-C" && i+1 < len(args) {
					capturedStagingDir = args[i+1]
					if fi, sErr := os.Stat(capturedStagingDir); sErr == nil {
						capturedPerm = fi.Mode().Perm()
					}
				}
			}
		}
		return origExec(name, args...)
	}

	outTar := filepath.Join(outDir, "archive.tar")
	if err := convertViaTempDir(zipPath, outTar, FormatInfo{Format: Zip}, Tar, ConvertOptions{Force: true}); err != nil {
		t.Fatalf("convertViaTempDir failed: %v", err)
	}
	if _, err := os.Stat(outTar); err != nil {
		t.Fatalf("output tar missing: %v", err)
	}
	if !strings.HasPrefix(capturedStagingDir, outDir+string(filepath.Separator)) {
		t.Errorf("expected staging dir inside %s, got %q", outDir, capturedStagingDir)
	}
	if capturedPerm != 0o700 {
		t.Errorf("staging dir permissions = %o, want 0700", capturedPerm)
	}
	entries, _ := os.ReadDir(fakeTmp)
	if len(entries) != 0 {
		t.Errorf("fakeTmp should remain untouched, got entries: %v", entries)
	}
	if _, err := os.Stat(capturedStagingDir); !os.IsNotExist(err) {
		t.Errorf("staging dir %s should be cleaned up", capturedStagingDir)
	}
}

func readTarEntriesFromArchive(t *testing.T, path string, format Format) map[string]struct {
	typeflag byte
	mode     int64
	linkname string
	data     []byte
} {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	var r io.Reader = f
	switch format {
	case Gz:
		gr, gErr := gzip.NewReader(f)
		if gErr != nil {
			t.Fatalf("gzip reader %s: %v", path, gErr)
		}
		defer gr.Close()
		r = gr
	case Zst:
		cmd := exec.Command("zstd", "-dc", "--", path)
		out, zErr := cmd.Output()
		if zErr != nil {
			t.Fatalf("zstd -dc %s: %v", path, zErr)
		}
		r = bytes.NewReader(out)
	}

	tr := tar.NewReader(r)
	got := make(map[string]struct {
		typeflag byte
		mode     int64
		linkname string
		data     []byte
	})
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next %s: %v", path, err)
		}
		var data []byte
		if hdr.Typeflag == tar.TypeReg {
			data, err = io.ReadAll(tr)
			if err != nil {
				t.Fatalf("read tar member %s: %v", hdr.Name, err)
			}
		}
		got[strings.TrimSuffix(hdr.Name, "/")] = struct {
			typeflag byte
			mode     int64
			linkname string
			data     []byte
		}{
			typeflag: hdr.Typeflag,
			mode:     hdr.Mode & 0o777,
			linkname: hdr.Linkname,
			data:     data,
		}
	}
	return got
}

func TestDoConvert_StreamZipToTarVariants(t *testing.T) {
	tests := []struct {
		name         string
		targetFormat Format
		wantExt      string
		needTool     string
	}{
		{name: "zip to plain tar", targetFormat: Tar, wantExt: ".tar"},
		{name: "zip to tar.gz", targetFormat: Gz, wantExt: ".tar.gz", needTool: "gzip"},
		{name: "zip to tar.zst", targetFormat: Zst, wantExt: ".tar.zst", needTool: "zstd"},
	}

	largePayload := bytes.Repeat([]byte("ZIP2TAR-STREAM-BLOCK-"), 16*1024) // ~336 KiB > 256 KiB buf

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.needTool == "gzip" && !hasTool("pigz") && !hasTool("gzip") {
				t.Skip("gzip/pigz not available")
			} else if tt.needTool != "" && tt.needTool != "gzip" && !hasTool(tt.needTool) {
				t.Skipf("%s not available", tt.needTool)
			}

			tmpDir := t.TempDir()
			zipPath := filepath.Join(tmpDir, "bundle.zip")
			zf, err := os.Create(zipPath)
			if err != nil {
				t.Fatal(err)
			}
			zw := zip.NewWriter(zf)

			// 1. Directory
			dh := &zip.FileHeader{Name: "empty_dir/", Method: zip.Store}
			dh.SetMode(os.ModeDir | 0o750)
			if _, err := zw.CreateHeader(dh); err != nil {
				t.Fatal(err)
			}
			// 2. Regular text file
			fh1 := &zip.FileHeader{Name: "hello.txt", Method: zip.Deflate}
			fh1.SetMode(0o644)
			w1, _ := zw.CreateHeader(fh1)
			_, _ = w1.Write([]byte("hello stream zip->tar\n"))
			// 3. Executable nested file
			fh2 := &zip.FileHeader{Name: "bin/run.sh", Method: zip.Deflate}
			fh2.SetMode(0o755)
			w2, _ := zw.CreateHeader(fh2)
			_, _ = w2.Write([]byte("#!/bin/sh\necho ok\n"))
			// 4. Symlink
			sh := &zip.FileHeader{Name: "link.txt", Method: zip.Store}
			sh.SetMode(os.ModeSymlink | 0o777)
			ws, _ := zw.CreateHeader(sh)
			_, _ = ws.Write([]byte("hello.txt"))
			// 5. Large payload > streamBufSize
			fhBig := &zip.FileHeader{Name: "big.bin", Method: zip.Deflate}
			fhBig.SetMode(0o640)
			wb, _ := zw.CreateHeader(fhBig)
			_, _ = wb.Write(largePayload)

			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}
			_ = zf.Close()

			origExec := execCommand
			defer func() { execCommand = origExec }()
			execCommand = func(name string, args ...string) *exec.Cmd {
				if name == "unzip" || name == "tar" || name == "7z" || name == "7zz" || name == "7za" {
					t.Errorf("streaming zip->tar must NOT invoke external %q (args=%v)", name, args)
				}
				return origExec(name, args...)
			}

			outPath := filepath.Join(tmpDir, "bundle"+tt.wantExt)
			if err := DoConvert([]string{zipPath}, tt.targetFormat, ConvertOptions{KeepOrig: true}); err != nil {
				t.Fatalf("DoConvert zip -> %s failed: %v", tt.targetFormat, err)
			}
			if _, err := os.Stat(outPath); err != nil {
				t.Fatalf("expected output %s missing: %v", outPath, err)
			}

			entries := readTarEntriesFromArchive(t, outPath, tt.targetFormat)
			if e, ok := entries["empty_dir"]; !ok || e.typeflag != tar.TypeDir || e.mode != 0o750 {
				t.Errorf("empty_dir mismatch: ok=%v got=%+v", ok, e)
			}
			if e, ok := entries["hello.txt"]; !ok || e.typeflag != tar.TypeReg || string(e.data) != "hello stream zip->tar\n" || e.mode != 0o644 {
				t.Errorf("hello.txt mismatch: ok=%v type=%v mode=%o", ok, e.typeflag, e.mode)
			}
			if e, ok := entries["bin/run.sh"]; !ok || e.typeflag != tar.TypeReg || string(e.data) != "#!/bin/sh\necho ok\n" || e.mode != 0o755 {
				t.Errorf("bin/run.sh mismatch: ok=%v type=%v mode=%o", ok, e.typeflag, e.mode)
			}
			if e, ok := entries["link.txt"]; !ok || e.typeflag != tar.TypeSymlink || e.linkname != "hello.txt" {
				t.Errorf("link.txt mismatch: ok=%v got=%+v", ok, e)
			}
			if e, ok := entries["big.bin"]; !ok || !bytes.Equal(e.data, largePayload) || e.mode != 0o640 {
				t.Errorf("big.bin mismatch: ok=%v len=%d want=%d mode=%o", ok, len(e.data), len(largePayload), e.mode)
			}
		})
	}
}

func TestDoConvert_StreamTarToZipVariants(t *testing.T) {
	tests := []struct {
		name      string
		srcFormat Format
		srcExt    string
		needTool  string
	}{
		{name: "plain tar to zip", srcFormat: Tar, srcExt: ".tar"},
		{name: "tar.gz to zip", srcFormat: Gz, srcExt: ".tar.gz", needTool: "gzip"},
	}

	largePayload := bytes.Repeat([]byte("TAR2ZIP-STREAM-DATA-"), 16*1024)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.needTool == "gzip" && !hasTool("pigz") && !hasTool("gzip") {
				t.Skip("gzip/pigz not available")
			}

			tmpDir := t.TempDir()
			srcPath := filepath.Join(tmpDir, "pkg"+tt.srcExt)
			sf, err := os.Create(srcPath)
			if err != nil {
				t.Fatal(err)
			}

			var rawW io.Writer = sf
			var gw *gzip.Writer
			if tt.srcFormat == Gz {
				gw = gzip.NewWriter(sf)
				rawW = gw
			}
			tw := tar.NewWriter(rawW)

			// Root "." entry should be skipped cleanly, nested sub/ dir preserved
			_ = tw.WriteHeader(&tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755})
			_ = tw.WriteHeader(&tar.Header{Name: "sub/", Typeflag: tar.TypeDir, Mode: 0o750})
			_ = tw.WriteHeader(&tar.Header{Name: "alpha.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 5})
			_, _ = tw.Write([]byte("alpha"))
			_ = tw.WriteHeader(&tar.Header{Name: "sub/tool", Typeflag: tar.TypeReg, Mode: 0o755, Size: 6})
			_, _ = tw.Write([]byte("script"))
			_ = tw.WriteHeader(&tar.Header{Name: "sym", Typeflag: tar.TypeSymlink, Linkname: "alpha.txt", Mode: 0o777})
			_ = tw.WriteHeader(&tar.Header{Name: "blob.bin", Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(largePayload))})
			_, _ = tw.Write(largePayload)

			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if gw != nil {
				_ = gw.Close()
			}
			_ = sf.Close()

			origExec := execCommand
			defer func() { execCommand = origExec }()
			execCommand = func(name string, args ...string) *exec.Cmd {
				if name == "zip" || name == "tar" || name == "7z" || name == "7zz" || name == "7za" {
					t.Errorf("streaming tar->zip must NOT invoke external %q (args=%v)", name, args)
				}
				return origExec(name, args...)
			}

			outZip := filepath.Join(tmpDir, "pkg.zip")
			if err := DoConvert([]string{srcPath}, Zip, ConvertOptions{KeepOrig: true}); err != nil {
				t.Fatalf("DoConvert %s -> zip failed: %v", tt.srcExt, err)
			}

			zr, err := zip.OpenReader(outZip)
			if err != nil {
				t.Fatalf("zip.OpenReader(%s): %v", outZip, err)
			}
			defer zr.Close()

			got := make(map[string]*zip.File)
			for _, f := range zr.File {
				got[f.Name] = f
			}

			if f, ok := got["sub/"]; !ok || !f.FileInfo().IsDir() || f.Mode().Perm() != 0o750 {
				t.Errorf("sub/ mismatch: ok=%v", ok)
			}
			readZipEntry := func(name string) ([]byte, os.FileMode) {
				f, ok := got[name]
				if !ok {
					t.Fatalf("missing zip entry %q", name)
				}
				rc, rErr := f.Open()
				if rErr != nil {
					t.Fatalf("open zip entry %q: %v", name, rErr)
				}
				defer rc.Close()
				b, _ := io.ReadAll(rc)
				return b, f.Mode()
			}

			if data, mode := readZipEntry("alpha.txt"); string(data) != "alpha" || mode.Perm() != 0o644 {
				t.Errorf("alpha.txt mismatch: data=%q mode=%o", data, mode.Perm())
			}
			if data, mode := readZipEntry("sub/tool"); string(data) != "script" || mode.Perm() != 0o755 {
				t.Errorf("sub/tool mismatch: data=%q mode=%o", data, mode.Perm())
			}
			if data, mode := readZipEntry("sym"); mode&os.ModeSymlink == 0 || string(data) != "alpha.txt" {
				t.Errorf("sym mismatch: mode=%v data=%q", mode, data)
			}
			if data, _ := readZipEntry("blob.bin"); !bytes.Equal(data, largePayload) {
				t.Errorf("blob.bin len=%d, want=%d", len(data), len(largePayload))
			}
		})
	}
}

func TestConvertStreaming_FallbacksAndErrors(t *testing.T) {
	t.Run("password causes fallback sentinel for both directions", func(t *testing.T) {
		tmpDir := t.TempDir()
		err := convertZipToTarStream(filepath.Join(tmpDir, "a.zip"), filepath.Join(tmpDir, "a.tar"), Tar, ConvertOptions{Password: "secret"})
		if !errors.Is(err, errFallbackTempConvert) {
			t.Errorf("convertZipToTarStream with password = %v, want errFallbackTempConvert", err)
		}
		err = convertTarToZipStream(filepath.Join(tmpDir, "a.tar"), filepath.Join(tmpDir, "a.zip"), FormatInfo{Format: Tar}, ConvertOptions{Password: "secret"})
		if !errors.Is(err, errFallbackTempConvert) {
			t.Errorf("convertTarToZipStream with password = %v, want errFallbackTempConvert", err)
		}
	})

	t.Run("encrypted zip flag bit triggers fallback", func(t *testing.T) {
		tmpDir := t.TempDir()
		zipPath := filepath.Join(tmpDir, "enc.zip")
		zf, _ := os.Create(zipPath)
		zw := zip.NewWriter(zf)
		w, _ := zw.Create("a.txt")
		_, _ = w.Write([]byte("payload"))
		_ = zw.Close()
		_ = zf.Close()

		// Flip bit 0 (encrypted) in both local file header and central directory header for valid test detection.
		raw, _ := os.ReadFile(zipPath)
		raw[6] |= 0x01
		cdIdx := bytes.Index(raw, []byte("PK\x01\x02"))
		if cdIdx < 0 {
			t.Fatal("central directory not found")
		}
		raw[cdIdx+8] |= 0x01
		if err := os.WriteFile(zipPath, raw, 0o644); err != nil {
			t.Fatal(err)
		}

		err := convertZipToTarStream(zipPath, filepath.Join(tmpDir, "enc.tar"), Tar, ConvertOptions{})
		if !errors.Is(err, errFallbackTempConvert) {
			t.Errorf("encrypted zip flag expected errFallbackTempConvert, got %v", err)
		}
	})

	t.Run("unsupported zip compression method triggers fallback", func(t *testing.T) {
		tmpDir := t.TempDir()
		zipPath := filepath.Join(tmpDir, "custom_method.zip")
		zf, _ := os.Create(zipPath)
		zw := zip.NewWriter(zf)
		zw.RegisterCompressor(99, func(w io.Writer) (io.WriteCloser, error) {
			return nopWriteCloser{w}, nil
		})
		h := &zip.FileHeader{Name: "a.txt", Method: 99}
		w, _ := zw.CreateHeader(h)
		_, _ = w.Write([]byte("custom"))
		_ = zw.Close()
		_ = zf.Close()

		err := convertZipToTarStream(zipPath, filepath.Join(tmpDir, "custom.tar"), Tar, ConvertOptions{})
		if !errors.Is(err, errFallbackTempConvert) {
			t.Errorf("unsupported zip method expected errFallbackTempConvert, got %v", err)
		}
	})

	t.Run("tar hardlink triggers fallback and DoConvert completes via temp dir", func(t *testing.T) {
		if !hasTool("zip") && !hasTool(sevenzBin()) {
			t.Skip("zip/7z not available")
		}
		tmpDir := t.TempDir()
		tarPath := filepath.Join(tmpDir, "hardlink.tar")
		tf, _ := os.Create(tarPath)
		tw := tar.NewWriter(tf)
		_ = tw.WriteHeader(&tar.Header{Name: "orig.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 4})
		_, _ = tw.Write([]byte("body"))
		_ = tw.WriteHeader(&tar.Header{Name: "hard.txt", Typeflag: tar.TypeLink, Linkname: "orig.txt", Mode: 0o644})
		_ = tw.Close()
		_ = tf.Close()

		outZip := filepath.Join(tmpDir, "hardlink.zip")
		err := convertTarToZipStream(tarPath, outZip, FormatInfo{Format: Tar}, ConvertOptions{})
		if !errors.Is(err, errFallbackTempConvert) {
			t.Fatalf("tar hardlink expected errFallbackTempConvert, got %v", err)
		}

		if err := DoConvert([]string{tarPath}, Zip, ConvertOptions{KeepOrig: true}); err != nil {
			t.Fatalf("DoConvert fallback with hardlink failed: %v", err)
		}
		if _, err := os.Stat(outZip); err != nil {
			t.Fatalf("fallback zip missing: %v", err)
		}
	})

	t.Run("compressor failure in zip->tar stream cleans up and keeps original", func(t *testing.T) {
		if !hasTool("xz") {
			t.Skip("xz not available")
		}
		tmpDir := t.TempDir()
		zipPath := filepath.Join(tmpDir, "fail.zip")
		zf, _ := os.Create(zipPath)
		zw := zip.NewWriter(zf)
		w, _ := zw.Create("a.txt")
		_, _ = w.Write([]byte("data"))
		_ = zw.Close()
		_ = zf.Close()

		origExec := execCommand
		defer func() { execCommand = origExec }()
		var compCmd *exec.Cmd
		execCommand = func(name string, args ...string) *exec.Cmd {
			compCmd = mockExecCommand("mock-fail-compressor")
			return compCmd
		}

		outPath := filepath.Join(tmpDir, "fail.tar.xz")
		err := convertZipToTarStream(zipPath, outPath, Xz, ConvertOptions{})
		if err == nil {
			t.Fatal("expected error when stream compressor fails, got nil")
		}
		if _, sErr := os.Stat(outPath); !os.IsNotExist(sErr) {
			t.Errorf("partial output %s should not exist", outPath)
		}
		if compCmd != nil && compCmd.ProcessState == nil {
			t.Errorf("compressor child was not reaped")
		}
	})

	t.Run("corrupt tar.gz in tar->zip stream errors cleanly without partial output", func(t *testing.T) {
		tmpDir := t.TempDir()
		badTarGz := filepath.Join(tmpDir, "corrupt.tar.gz")
		_ = os.WriteFile(badTarGz, []byte("not-a-valid-gzip-stream"), 0o644)

		outZip := filepath.Join(tmpDir, "corrupt.zip")
		err := convertTarToZipStream(badTarGz, outZip, FormatInfo{Format: Gz, IsTar: true}, ConvertOptions{})
		if err == nil {
			t.Fatal("expected error on corrupt tar.gz input, got nil")
		}
		if _, sErr := os.Stat(outZip); !os.IsNotExist(sErr) {
			t.Errorf("partial zip %s should not exist", outZip)
		}
	})
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }



