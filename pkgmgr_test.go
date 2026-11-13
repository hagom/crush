package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectPkgManager(t *testing.T) {
	mgr := DetectPkgManager()
	if mgr == nil {
		t.Log("No package manager detected (expected in some environments)")
	} else {
		t.Logf("Detected package manager: %s", mgr.Name)
	}
}

func TestFindToolInfo(t *testing.T) {
	requiredTools := []string{"pigz", "xz", "lbzip2", "pbzip2", "bzip3", "zstd", "plzip", "lrzip", "zip", "unzip", "p7zip", "rar", "tar", "lz4", "brotli", "numfmt", "pv", "getconf"}
	for _, name := range requiredTools {
		info := findToolInfo(name)
		if info == nil {
			t.Errorf("findToolInfo(%q) = nil, want valid ToolInfo", name)
		} else if info.Name != name {
			t.Errorf("findToolInfo(%q).Name = %q, want %q", name, info.Name, name)
		}
	}

	info := findToolInfo("nonexistent")
	if info != nil {
		t.Errorf("findToolInfo(nonexistent) = %v, want nil", info)
	}
}

func TestToolPkgSlice(t *testing.T) {
	mgr := PkgManager{Name: "apt-get"}
	info := findToolInfo("pigz")
	if info == nil {
		t.Skip("tool info not found")
	}
	pkg := toolPkgSlice(*info, mgr)
	if pkg != "pigz" {
		t.Errorf("toolPkgSlice(pigz, apt-get) = %q, want pigz", pkg)
	}

	mgr.Name = "pacman"
	pkg = toolPkgSlice(*info, mgr)
	if pkg != "pigz" {
		t.Errorf("toolPkgSlice(pigz, pacman) = %q, want pigz", pkg)
	}
}

func TestContains(t *testing.T) {
	list := []string{"a", "b", "c"}
	if !contains(list, "a") {
		t.Error("contains should find 'a'")
	}
	if contains(list, "d") {
		t.Error("contains should not find 'd'")
	}
}

func TestIsToolInstalled(t *testing.T) {
	if isToolInstalled(nil, "pigz") {
		t.Error("isToolInstalled(nil, pigz) = true, want false")
	}
}

func TestListCompressedBz2(t *testing.T) {
	bin := bzip2Bin()
	if !hasTool(bin) {
		t.Skip("bzip2 no instalado")
	}
	tmp := t.TempDir()
	src := filepath.Join(tmp, "data.txt")
	if err := os.WriteFile(src, []byte("contenido de prueba"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-f", src)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(tmp, "data.txt.bz2"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err = ListCompressed(f)
	os.Stdout = old
	w.Close()
	r.Close()
	if err != nil {
		t.Fatalf("ListCompressed(.bz2): %v", err)
	}
}

func TestListCompressedLz4(t *testing.T) {
	if !hasTool("lz4") {
		t.Skip("lz4 no instalado")
	}
	tmp := t.TempDir()
	src := filepath.Join(tmp, "data.txt")
	if err := os.WriteFile(src, []byte("contenido de prueba"), 0644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(tmp, "data.txt.lz4")
	if err := exec.Command("lz4", "-q", "-f", src, dst).Run(); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err = ListCompressed(f)
	os.Stdout = old
	w.Close()
	r.Close()
	if err != nil {
		t.Fatalf("ListCompressed(.lz4): %v", err)
	}
}

func TestListCompressed(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "*.unknown")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	err = ListCompressed(f)
	if err == nil {
		t.Error("ListCompressed(.unknown) = nil, want error")
	}
}

func TestCompressReadTar(t *testing.T) {
	tmp := t.TempDir()
	txtFile := filepath.Join(tmp, "sample.txt")
	expected := "tar stream content for test"
	if err := os.WriteFile(txtFile, []byte(expected), 0644); err != nil {
		t.Fatal(err)
	}
	tarFile := filepath.Join(tmp, "archive.tar")
	cmd := exec.Command("tar", "-cf", tarFile, "-C", tmp, "sample.txt")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(tarFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	data, err := CompressRead(f)
	if err != nil {
		t.Fatalf("CompressRead(tar) failed: %v", err)
	}
	if strings.TrimSpace(string(data)) != expected {
		t.Errorf("CompressRead(tar) = %q, want %q", string(data), expected)
	}

	// Verify sample.txt was NOT extracted to the working directory
	if _, err := os.Stat("sample.txt"); err == nil {
		_ = os.Remove("sample.txt")
		t.Errorf("CompressRead(tar) leaked extracted file into current directory")
	}
}

func TestCompressReadAndListBz3(t *testing.T) {
	tmp := t.TempDir()
	bz3File := filepath.Join(tmp, "test.bz3")
	if err := os.WriteFile(bz3File, []byte("fake bz3 data"), 0644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(bz3File)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// ListCompressed should succeed without error
	if err := ListCompressed(f); err != nil {
		t.Errorf("ListCompressed(bz3) = %v, want nil", err)
	}
}

func TestEnsureFormatTool(t *testing.T) {
	origLookPath := lookPath
	origAutoInstall := autoInstallDeps
	defer func() {
		lookPath = origLookPath
		autoInstallDeps = origAutoInstall
	}()

	t.Run("ToolAlreadyInstalled", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			if name == "pigz" {
				return "/usr/bin/pigz", nil
			}
			return "", exec.ErrNotFound
		}
		tool, err := EnsureCompressTool(Gz)
		if err != nil {
			t.Fatalf("EnsureCompressTool(Gz) unexpected error: %v", err)
		}
		if tool != "pigz" {
			t.Errorf("got tool %s, want pigz", tool)
		}
	})

	t.Run("MultiToolMissing_AutoInstallSucceeds", func(t *testing.T) {
		installed := false
		lookPath = func(name string) (string, error) {
			if name == "pigz" && installed {
				return "/usr/bin/pigz", nil
			}
			return "", exec.ErrNotFound
		}
		autoInstallDeps = func(tools []string, mgr *PkgManager) []string {
			for _, tool := range tools {
				if tool == "pigz" {
					installed = true
				}
			}
			return nil
		}

		tool, err := EnsureCompressTool(Gz)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tool != "pigz" {
			t.Errorf("got tool %s, want pigz", tool)
		}
	})

	t.Run("MultiToolMissing_FallbackToSequential", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			if name == "gzip" {
				return "/usr/bin/gzip", nil
			}
			return "", exec.ErrNotFound
		}
		autoInstallDeps = func(tools []string, mgr *PkgManager) []string {
			return tools
		}

		tool, err := EnsureCompressTool(Gz)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if tool != "gzip" {
			t.Errorf("got tool %s, want gzip", tool)
		}
	})

	t.Run("BothMultiAndSequentialMissing", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			return "", exec.ErrNotFound
		}
		autoInstallDeps = func(tools []string, mgr *PkgManager) []string {
			return tools
		}

		tool, err := EnsureCompressTool(Gz)
		if err == nil {
			t.Fatalf("expected error when neither tool is available, got tool: %s", tool)
		}
		if !strings.Contains(err.Error(), "pigz") || !strings.Contains(err.Error(), "gzip") {
			t.Errorf("expected error mentioning both pigz and gzip, got: %v", err)
		}
	})

	t.Run("ToolWithoutFallbackMissing", func(t *testing.T) {
		lookPath = func(name string) (string, error) {
			return "", exec.ErrNotFound
		}
		autoInstallDeps = func(tools []string, mgr *PkgManager) []string {
			return tools
		}

		tool, err := EnsureCompressTool(Zst)
		if err == nil {
			t.Fatalf("expected error when zstd is missing, got tool: %s", tool)
		}
		if !strings.Contains(err.Error(), "zstd") {
			t.Errorf("expected error mentioning zstd, got: %v", err)
		}
	})
}

func TestCompressRead_TarDashAndLzipFallback(t *testing.T) {
	t.Run("TarSingleDashFilename", func(t *testing.T) {
		if _, err := exec.LookPath("tar"); err != nil {
			t.Skip("tar no disponible")
		}
		tmp := t.TempDir()
		origWd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(tmp); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chdir(origWd) }()

		if err := os.WriteFile("inner.txt", []byte("dash payload\n"), 0644); err != nil {
			t.Fatal(err)
		}
		dashTar := "-dash.tar"
		if err := exec.Command("tar", "-cf", dashTar, "inner.txt").Run(); err != nil {
			t.Fatal(err)
		}
		// Ensure bare "-" file name also works safely with Format fallback tested via CompressRead
		// For Format detection we use .tar suffix and also verify -f arg passes safeFlagArg via mock
		f, err := os.Open(dashTar)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()

		origExec := execCommand
		defer func() { execCommand = origExec }()
		var tarArgs []string
		execCommand = func(name string, args ...string) *exec.Cmd {
			if name == "tar" {
				tarArgs = append([]string(nil), args...)
			}
			return exec.Command(name, args...)
		}

		data, err := CompressRead(f)
		if err != nil {
			t.Fatalf("CompressRead(%s) failed: %v", dashTar, err)
		}
		if strings.TrimSpace(string(data)) != "dash payload" {
			t.Errorf("CompressRead(%s) = %q, want %q", dashTar, string(data), "dash payload")
		}
		hasSafeArg := false
		for _, a := range tarArgs {
			if a == "./-dash.tar" {
				hasSafeArg = true
			}
		}
		if !hasSafeArg {
			t.Errorf("expected tar args to protect leading dash with ./-dash.tar and go through execCommand, got: %v", tarArgs)
		}
	})

	t.Run("LzipFallbackWhenPlzipMissing", func(t *testing.T) {
		tmp := t.TempDir()
		lzFile := filepath.Join(tmp, "sample.txt.lz")
		if err := os.WriteFile(lzFile, []byte("fake lz"), 0644); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(lzFile)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()

		origLookPath := lookPath
		origExec := execCommand
		defer func() {
			lookPath = origLookPath
			execCommand = origExec
		}()
		lookPath = func(name string) (string, error) {
			if name == "lzip" {
				return "/usr/bin/lzip", nil
			}
			return "", exec.ErrNotFound
		}
		var calledBin string
		execCommand = func(name string, args ...string) *exec.Cmd {
			calledBin = name
			return mockExecCommand("mock-noop")
		}

		if _, err := CompressRead(f); err != nil {
			t.Fatalf("CompressRead(.lz) fallback error: %v", err)
		}
		if calledBin != "lzip" {
			t.Errorf("expected CompressRead(.lz) fallback to lzip via execCommand, got %q", calledBin)
		}
	})

	t.Run("StreamsToWriterDirectly", func(t *testing.T) {
		if _, err := exec.LookPath("tar"); err != nil {
			t.Skip("tar no disponible")
		}
		tmp := t.TempDir()
		src := filepath.Join(tmp, "stream.txt")
		if err := os.WriteFile(src, []byte("streaming writer payload"), 0644); err != nil {
			t.Fatal(err)
		}
		arc := filepath.Join(tmp, "stream.tar")
		if err := exec.Command("tar", "-cf", arc, "-C", tmp, "stream.txt").Run(); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(arc)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()

		var sb strings.Builder
		if err := CompressReadTo(f, &sb); err != nil {
			t.Fatalf("CompressReadTo failed: %v", err)
		}
		if strings.TrimSpace(sb.String()) != "streaming writer payload" {
			t.Errorf("CompressReadTo wrote %q, want %q", sb.String(), "streaming writer payload")
		}
	})
}
