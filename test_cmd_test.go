package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestTestNoFiles(t *testing.T) {
	opts := TestOptions{}
	err := DoTest(nil, opts)
	if err == nil {
		t.Error("DoTest with nil files should error")
	}
}

func TestTestFileTar(t *testing.T) {
	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "sample.txt")
	if err := os.WriteFile(txtFile, []byte("contenido de prueba"), 0644); err != nil {
		t.Fatal(err)
	}

	tarPath := filepath.Join(tmpDir, "archive.tar")
	cmd := exec.Command("tar", "-cf", tarPath, "-C", tmpDir, "sample.txt")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("error creando tar de prueba: %v, out: %s", err, string(out))
	}

	opts := TestOptions{}
	status, err := TestFile(tarPath, opts)
	if err != nil {
		t.Fatalf("TestFile en .tar válido falló: %v (status=%s)", err, status)
	}
	if status != "OK" {
		t.Errorf("esperaba status OK para .tar válido, obtuve: %s", status)
	}

	corruptTar := filepath.Join(tmpDir, "corrupt.tar")
	if err := os.WriteFile(corruptTar, []byte("archivo tar corrupto"), 0644); err != nil {
		t.Fatal(err)
	}
	status, err = TestFile(corruptTar, opts)
	if err == nil {
		t.Errorf("esperaba error para .tar corrupto, obtuve status=%s", status)
	}
}

func TestTestFileGz(t *testing.T) {
	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "sample.txt")
	if err := os.WriteFile(txtFile, []byte("contenido gzip"), 0644); err != nil {
		t.Fatal(err)
	}

	gzPath := filepath.Join(tmpDir, "sample.txt.gz")
	gzTool := "gzip"
	if hasTool("pigz") {
		gzTool = "pigz"
	}
	cmd := exec.Command(gzTool, "-c", txtFile)
	outFile, err := os.Create(gzPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = outFile
	if err := cmd.Run(); err != nil {
		outFile.Close()
		t.Fatal(err)
	}
	outFile.Close()

	opts := TestOptions{}
	status, err := TestFile(gzPath, opts)
	if err != nil {
		t.Fatalf("TestFile en .gz válido falló: %v", err)
	}
	if status != "OK" {
		t.Errorf("esperaba status OK para .gz, obtuve: %s", status)
	}
}

func TestTestFileUnsupported(t *testing.T) {
	tmpDir := t.TempDir()
	dummy := filepath.Join(tmpDir, "archivo.desconocido")
	if err := os.WriteFile(dummy, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	opts := TestOptions{}
	_, err := TestFile(dummy, opts)
	if err == nil {
		t.Error("esperaba error para formato no reconocido")
	}
}

func TestDoTestEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	opts := TestOptions{}

	// Archivos inexistentes
	err := DoTest([]string{filepath.Join(tmpDir, "inexistente.tar")}, opts)
	if err == nil {
		t.Error("esperaba error cuando los archivos no existen")
	}

	// Directorio en vez de archivo
	dirOnly := filepath.Join(tmpDir, "un_directorio")
	if err := os.Mkdir(dirOnly, 0755); err != nil {
		t.Fatal(err)
	}
	err = DoTest([]string{dirOnly}, opts)
	if err == nil {
		t.Error("esperaba error cuando solo se pasan directorios a DoTest")
	}
}

func TestDoTestVerifyMatching(t *testing.T) {
	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(txtFile, []byte("verificación de hash válida"), 0644); err != nil {
		t.Fatal(err)
	}
	gzFile := filepath.Join(tmpDir, "file.txt.gz")
	cmd := exec.Command("gzip", "-c", txtFile)
	outF, err := os.Create(gzFile)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = outF
	if err := cmd.Run(); err != nil {
		outF.Close()
		t.Fatal(err)
	}
	outF.Close()

	// Create matching .sha256 file
	gzBytes, err := os.ReadFile(gzFile)
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(gzBytes))
	shaFile := gzFile + ".sha256"
	shaContent := fmt.Sprintf("%s  %s\n", hash, filepath.Base(gzFile))
	if err := os.WriteFile(shaFile, []byte(shaContent), 0644); err != nil {
		t.Fatal(err)
	}

	opts := TestOptions{Verify: true}
	err = DoTest([]string{gzFile}, opts)
	if err != nil {
		t.Fatalf("DoTest con -verify y hash coincidente falló: %v", err)
	}
}

func TestDoTestVerifyMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(txtFile, []byte("verificación con hash erróneo"), 0644); err != nil {
		t.Fatal(err)
	}
	gzFile := filepath.Join(tmpDir, "file.txt.gz")
	cmd := exec.Command("gzip", "-c", txtFile)
	outF, err := os.Create(gzFile)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = outF
	if err := cmd.Run(); err != nil {
		outF.Close()
		t.Fatal(err)
	}
	outF.Close()

	// Create mismatching .sha256 file
	shaFile := gzFile + ".sha256"
	fakeHash := "0000000000000000000000000000000000000000000000000000000000000000"
	shaContent := fmt.Sprintf("%s  %s\n", fakeHash, filepath.Base(gzFile))
	if err := os.WriteFile(shaFile, []byte(shaContent), 0644); err != nil {
		t.Fatal(err)
	}

	opts := TestOptions{Verify: true}
	err = DoTest([]string{gzFile}, opts)
	if err == nil {
		t.Fatal("DoTest con hash no coincidente debería haber fallado")
	}
	if !strings.Contains(err.Error(), "con errores") {
		t.Errorf("error inesperado: %v", err)
	}
}

func TestDoTestVerifyNoShaFile(t *testing.T) {
	tmpDir := t.TempDir()
	txtFile := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(txtFile, []byte("sin archivo sha256"), 0644); err != nil {
		t.Fatal(err)
	}
	gzFile := filepath.Join(tmpDir, "file.txt.gz")
	cmd := exec.Command("gzip", "-c", txtFile)
	outF, err := os.Create(gzFile)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = outF
	if err := cmd.Run(); err != nil {
		outF.Close()
		t.Fatal(err)
	}
	outF.Close()

	opts := TestOptions{Verify: true}
	err = DoTest([]string{gzFile}, opts)
	if err != nil {
		t.Fatalf("DoTest sin .sha256 debe verificar integridad sin error: %v", err)
	}
}

func TestTestFile_TarDashAndUsesExecCommand(t *testing.T) {
	origExec := execCommand
	origLook := lookPath
	defer func() {
		execCommand = origExec
		lookPath = origLook
	}()

	lookPath = func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}

	var tarArgs []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "tar" {
			tarArgs = append([]string(nil), args...)
		}
		return mockExecCommand("mock-noop")
	}

	status, err := TestFile("-dash.tar", TestOptions{})
	if err != nil || status != "OK" {
		t.Fatalf("TestFile(-dash.tar) returned status=%q err=%v", status, err)
	}
	hasDashDash, hasSafe := false, false
	for _, a := range tarArgs {
		if a == "--" {
			hasDashDash = true
		}
		if a == "./-dash.tar" {
			hasSafe = true
		}
	}
	if !hasDashDash || !hasSafe {
		t.Errorf("TestFile(-dash.tar) expected -- and ./-dash.tar via execCommand, got: %v", tarArgs)
	}
}

func TestCmdMockHelperProcess(t *testing.T) {
	if os.Getenv("CRUSH_TEST_CMD_HELPER") != "1" {
		return
	}
	defer os.Exit(0)

	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}
	if len(args) == 0 {
		os.Exit(2)
	}

	switch args[0] {
	case "huge-fail-stderr":
		_, _ = os.Stdout.WriteString(strings.Repeat("A", 200*1024))
		_, _ = os.Stderr.WriteString("\nFINAL_ERROR_MARKER\n")
		os.Exit(1)

	case "huge-fail-stdout":
		_, _ = os.Stdout.WriteString(strings.Repeat("testing: ok_entry_line\n", 10000))
		_, _ = os.Stdout.WriteString("CRC_FAIL_AT_END\n")
		os.Exit(1)

	case "tar-huge-ok":
		_, _ = os.Stdout.WriteString(strings.Repeat("dir/member_entry_name.txt\n", 10000))
		os.Exit(0)

	case "tar-huge-fail":
		_, _ = os.Stdout.WriteString(strings.Repeat("STDOUT_NOISE_LINE\n", 10000))
		_, _ = os.Stderr.WriteString("tar: archive corrupted\n")
		os.Exit(1)

	default:
		os.Exit(127)
	}
}

func testCmdMock(mode string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=TestCmdMockHelperProcess", "--", mode)
	cmd.Env = append(os.Environ(), "CRUSH_TEST_CMD_HELPER=1")
	return cmd
}

func TestCappedDiagBuffer(t *testing.T) {
	tests := []struct {
		name          string
		max           int
		writes        []string
		want          string
		wantTruncated bool
	}{
		{
			name:          "empty",
			max:           16,
			writes:        nil,
			want:          "",
			wantTruncated: false,
		},
		{
			name:          "under cap",
			max:           16,
			writes:        []string{"hello ", "world"},
			want:          "hello world",
			wantTruncated: false,
		},
		{
			name:          "exact cap",
			max:           10,
			writes:        []string{"01234", "56789"},
			want:          "0123456789",
			wantTruncated: false,
		},
		{
			name:          "exact cap single write",
			max:           10,
			writes:        []string{"0123456789"},
			want:          "0123456789",
			wantTruncated: false,
		},
		{
			name:          "multi-chunk overflow keeps tail",
			max:           8,
			writes:        []string{"abcd", "efgh", "ijkl"},
			want:          "efghijkl",
			wantTruncated: true,
		},
		{
			name:          "single chunk larger than cap keeps tail",
			max:           6,
			writes:        []string{"0123456789ABCDEF"},
			want:          "ABCDEF",
			wantTruncated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := newCappedDiagBuffer(tt.max)
			for _, w := range tt.writes {
				n, err := buf.Write([]byte(w))
				if err != nil {
					t.Fatalf("Write returned error: %v", err)
				}
				if n != len(w) {
					t.Fatalf("Write returned n=%d, want %d", n, len(w))
				}
			}
			if got := buf.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
			if buf.truncated != tt.wantTruncated {
				t.Errorf("truncated = %v, want %v", buf.truncated, tt.wantTruncated)
			}
		})
	}

	t.Run("concurrent writes race-safe and bounded", func(t *testing.T) {
		buf := newCappedDiagBuffer(256)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				payload := []byte(strings.Repeat("x", 100))
				for j := 0; j < 50; j++ {
					_, _ = buf.Write(payload)
				}
			}()
		}
		wg.Wait()
		if got := len(buf.String()); got > 256 {
			t.Errorf("buffer grew beyond cap: %d > 256", got)
		}
	})
}

func TestTestWith_CapsHugeOutputAndDiscardsTarStdout(t *testing.T) {
	origExec := execCommand
	origLook := lookPath
	defer func() {
		execCommand = origExec
		lookPath = origLook
	}()
	lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }

	t.Run("caps huge combined output and keeps tail error", func(t *testing.T) {
		execCommand = func(name string, args ...string) *exec.Cmd {
			return testCmdMock("huge-fail-stderr")
		}
		status, err := testWith("bad.gz", "gzip", "-t", "--", "bad.gz")
		if status != "FAIL" || err == nil {
			t.Fatalf("expected FAIL+err, got status=%q err=%v", status, err)
		}
		if len(err.Error()) > maxTestDiagBytes+128 {
			t.Errorf("error message unbounded: len=%d (limit ~%d)", len(err.Error()), maxTestDiagBytes)
		}
		if !strings.Contains(err.Error(), "FINAL_ERROR_MARKER") {
			t.Errorf("tail error marker lost: %q", err.Error())
		}
	})

	t.Run("caps huge stdout-only error output and keeps tail", func(t *testing.T) {
		execCommand = func(name string, args ...string) *exec.Cmd {
			return testCmdMock("huge-fail-stdout")
		}
		status, err := testWith("bad.zip", "unzip", "-t", "--", "bad.zip")
		if status != "FAIL" || err == nil {
			t.Fatalf("expected FAIL+err, got status=%q err=%v", status, err)
		}
		if len(err.Error()) > maxTestDiagBytes+128 {
			t.Errorf("error message unbounded: len=%d", len(err.Error()))
		}
		if !strings.Contains(err.Error(), "CRC_FAIL_AT_END") {
			t.Errorf("tail error marker lost: %q", err.Error())
		}
	})

	t.Run("tar -tf discards huge stdout on success", func(t *testing.T) {
		execCommand = func(name string, args ...string) *exec.Cmd {
			return testCmdMock("tar-huge-ok")
		}
		status, err := testWith("big.tar", "tar", "-tf", "big.tar", "--")
		if status != "OK" || err != nil {
			t.Fatalf("expected OK, got status=%q err=%v", status, err)
		}
	})

	t.Run("tar -tf discards stdout and captures only stderr on failure", func(t *testing.T) {
		execCommand = func(name string, args ...string) *exec.Cmd {
			return testCmdMock("tar-huge-fail")
		}
		status, err := testWith("corrupt.tar", "tar", "-tf", "corrupt.tar", "--")
		if status != "FAIL" || err == nil {
			t.Fatalf("expected FAIL+err, got status=%q err=%v", status, err)
		}
		if strings.Contains(err.Error(), "STDOUT_NOISE_LINE") {
			t.Errorf("tar -tf stdout should be discarded, but leaked into error: len=%d", len(err.Error()))
		}
		if !strings.Contains(err.Error(), "tar: archive corrupted") {
			t.Errorf("expected tar stderr in error, got: %v", err)
		}
	})

	t.Run("missing tool returns WARNING", func(t *testing.T) {
		lookPath = func(name string) (string, error) { return "", exec.ErrNotFound }
		defer func() { lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil } }()

		status, err := testWith("a.xz", "xz", "-t", "--", "a.xz")
		if status != "WARNING" || err != nil {
			t.Fatalf("expected WARNING, nil; got %q, %v", status, err)
		}
	})
}

func TestTestArchiveIntegrity_MemorySafeThreadsAndLimits(t *testing.T) {
	origExec := execCommand
	origLook := lookPath
	origMem := getMemLimit
	defer func() {
		execCommand = origExec
		lookPath = origLook
		getMemLimit = origMem
	}()

	lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }

	tests := []struct {
		name     string
		file     string
		memMB    int
		wantTool string
		wantArgs func(file string) []string
	}{
		{
			name:     "tar.xz low memory caps threads=1 and floor 65MiB",
			file:     "archive.tar.xz",
			memMB:    4,
			wantTool: "xz",
			wantArgs: func(f string) []string {
				return []string{"-t", "-T1", "--memlimit-mt-decompress=65MiB", "--", f}
			},
		},
		{
			name:     "single xz low memory caps threads=1 and floor 65MiB",
			file:     "data.xz",
			memMB:    4,
			wantTool: "xz",
			wantArgs: func(f string) []string {
				return []string{"-t", "-T1", "--memlimit-mt-decompress=65MiB", "--", f}
			},
		},
		{
			name:     "txz high memory uses getMemLimit MiB and resolved threads",
			file:     "archive.txz",
			memMB:    512,
			wantTool: "xz",
			wantArgs: func(f string) []string {
				return []string{"-t", "-T" + threadStr(resolveThreadCap(Xz, 0)), "--memlimit-mt-decompress=512MiB", "--", f}
			},
		},
		{
			name:     "tar.lrz low memory uses -p 1 and -m 1",
			file:     "archive.tar.lrz",
			memMB:    4,
			wantTool: "lrzip",
			wantArgs: func(f string) []string {
				return []string{"-t", "-p", "1", "-m", "1", "--", f}
			},
		},
		{
			name:     "single lrz high memory uses -m memMB/100",
			file:     "data.lrz",
			memMB:    512,
			wantTool: "lrzip",
			wantArgs: func(f string) []string {
				return []string{"-t", "-p", threadStr(resolveThreadCap(Lrz, 0)), "-m", "5", "--", f}
			},
		},
		{
			name:     "tar.bz3 low memory caps threads=1",
			file:     "archive.tar.bz3",
			memMB:    4,
			wantTool: "bzip3",
			wantArgs: func(f string) []string {
				return []string{"-t", "-j", "1", "--", f}
			},
		},
		{
			name:     "single bz3 low memory caps threads=1",
			file:     "data.bz3",
			memMB:    4,
			wantTool: "bzip3",
			wantArgs: func(f string) []string {
				return []string{"-t", "-j", "1", "--", f}
			},
		},
		{
			name:     "tar.zst low memory caps threads=1 without -T0",
			file:     "archive.tar.zst",
			memMB:    4,
			wantTool: "zstd",
			wantArgs: func(f string) []string {
				return []string{"-t", "-T1", "--", f}
			},
		},
		{
			name:     "single zst low memory caps threads=1",
			file:     "data.zst",
			memMB:    4,
			wantTool: "zstd",
			wantArgs: func(f string) []string {
				return []string{"-t", "-T1", "--", f}
			},
		},
		{
			name:     "tar.lz low memory caps plzip threads=1",
			file:     "archive.tar.lz",
			memMB:    4,
			wantTool: "plzip",
			wantArgs: func(f string) []string {
				return []string{"-t", "--threads=1", "--", f}
			},
		},
		{
			name:     "single lz low memory caps plzip threads=1",
			file:     "data.lz",
			memMB:    4,
			wantTool: "plzip",
			wantArgs: func(f string) []string {
				return []string{"-t", "--threads=1", "--", f}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getMemLimit = func() int { return tt.memMB }
			var gotTool string
			var gotArgs []string
			execCommand = func(name string, args ...string) *exec.Cmd {
				gotTool = name
				gotArgs = append([]string(nil), args...)
				return mockExecCommand("mock-noop")
			}

			status, err := testArchiveIntegrity(tt.file, TestOptions{})
			if status != "OK" || err != nil {
				t.Fatalf("testArchiveIntegrity(%q) = %q, %v; want OK, nil", tt.file, status, err)
			}
			if gotTool != tt.wantTool {
				t.Errorf("tool = %q, want %q", gotTool, tt.wantTool)
			}
			want := tt.wantArgs(tt.file)
			if !reflect.DeepEqual(gotArgs, want) {
				t.Errorf("args = %v, want %v", gotArgs, want)
			}
		})
	}

	t.Run("tar.lz falls back to lzip when plzip absent", func(t *testing.T) {
		getMemLimit = func() int { return 512 }
		lookPath = func(name string) (string, error) {
			if name == "plzip" {
				return "", exec.ErrNotFound
			}
			return "/usr/bin/" + name, nil
		}
		defer func() { lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil } }()

		var gotTool string
		var gotArgs []string
		execCommand = func(name string, args ...string) *exec.Cmd {
			gotTool = name
			gotArgs = append([]string(nil), args...)
			return mockExecCommand("mock-noop")
		}

		status, err := testArchiveIntegrity("archive.tar.lz", TestOptions{})
		if status != "OK" || err != nil {
			t.Fatalf("got status=%q err=%v", status, err)
		}
		want := []string{"-t", "--", "archive.tar.lz"}
		if gotTool != "lzip" || !reflect.DeepEqual(gotArgs, want) {
			t.Errorf("got %s %v, want lzip %v", gotTool, gotArgs, want)
		}
	})

	t.Run("small existing tar.xz file uses size-aware thread cap=1", func(t *testing.T) {
		getMemLimit = func() int { return 65536 }
		smallXz := filepath.Join(t.TempDir(), "tiny.tar.xz")
		if err := os.WriteFile(smallXz, make([]byte, 1024), 0644); err != nil {
			t.Fatal(err)
		}
		var gotArgs []string
		execCommand = func(name string, args ...string) *exec.Cmd {
			gotArgs = append([]string(nil), args...)
			return mockExecCommand("mock-noop")
		}
		status, err := testArchiveIntegrity(smallXz, TestOptions{})
		if status != "OK" || err != nil {
			t.Fatalf("got status=%q err=%v", status, err)
		}
		want := []string{"-t", "-T1", "--memlimit-mt-decompress=65536MiB", "--", smallXz}
		if !reflect.DeepEqual(gotArgs, want) {
			t.Errorf("args = %v, want %v", gotArgs, want)
		}
	})
}

func TestTestFileXzIntegration(t *testing.T) {
	if !hasTool("xz") {
		t.Skip("xz not installed")
	}
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "data.txt")
	if err := os.WriteFile(src, []byte(strings.Repeat("crush xz integrity check\n", 100)), 0644); err != nil {
		t.Fatal(err)
	}
	xzPath := src + ".xz"
	cmd := exec.Command("xz", "-k", "--", src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("xz create failed: %v (%s)", err, string(out))
	}
	status, err := TestFile(xzPath, TestOptions{})
	if status != "OK" || err != nil {
		t.Fatalf("TestFile(%s) failed: status=%q err=%v", xzPath, status, err)
	}
}

func TestTestFileLrzipIntegration(t *testing.T) {
	if !hasTool("lrzip") {
		t.Skip("lrzip not installed")
	}
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "data.txt")
	if err := os.WriteFile(src, []byte(strings.Repeat("crush lrzip integrity check\n", 100)), 0644); err != nil {
		t.Fatal(err)
	}
	lrzPath := src + ".lrz"
	cmd := exec.Command("lrzip", "-o", lrzPath, "--", src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("lrzip create failed: %v (%s)", err, string(out))
	}
	status, err := TestFile(lrzPath, TestOptions{})
	if status != "OK" || err != nil {
		t.Fatalf("TestFile(%s) failed: status=%q err=%v", lrzPath, status, err)
	}
}

func TestTestArchiveIntegrity_NonInteractivePasswordFlagsAndSafeArg(t *testing.T) {
	origExec := execCommand
	origLook := lookPath
	defer func() {
		execCommand = origExec
		lookPath = origLook
	}()

	t.Run("sets null stdin, non-interactive password flags, and safeFlagArg", func(t *testing.T) {
		tests := []struct {
			name      string
			file      string
			onlyUnzip bool
			wantFlag  string
			wantSafe  string
		}{
			{name: "7z dash file", file: "-.7z", wantFlag: "-p-", wantSafe: "./-.7z"},
			{name: "rar dash file", file: "-.rar", wantFlag: "-p-", wantSafe: "./-.rar"},
			{name: "zip via unzip dash file", file: "-.zip", onlyUnzip: true, wantFlag: "-P", wantSafe: "./-.zip"},
			{name: "gz dash file", file: "-.gz", wantFlag: "--", wantSafe: "./-.gz"},
			{name: "xz dash file", file: "-.xz", wantFlag: "--", wantSafe: "./-.xz"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if tt.onlyUnzip {
					lookPath = func(name string) (string, error) {
						if name == "unzip" {
							return "/usr/bin/unzip", nil
						}
						return "", exec.ErrNotFound
					}
				} else {
					lookPath = func(name string) (string, error) {
						return "/usr/bin/" + name, nil
					}
				}

				var gotArgs []string
				var capturedCmd *exec.Cmd
				execCommand = func(name string, args ...string) *exec.Cmd {
					gotArgs = append([]string(nil), args...)
					capturedCmd = mockExecCommand("mock-noop")
					return capturedCmd
				}

				status, err := testArchiveIntegrity(tt.file, TestOptions{})
				if status != "OK" || err != nil {
					t.Fatalf("testArchiveIntegrity(%q) = %q, %v; want OK, nil", tt.file, status, err)
				}
				if capturedCmd == nil || capturedCmd.Stdin == nil {
					t.Errorf("expected testWith to set non-nil Stdin (null file)")
				}
				hasFlag, hasSafe := false, false
				for _, a := range gotArgs {
					if a == tt.wantFlag {
						hasFlag = true
					}
					if a == tt.wantSafe {
						hasSafe = true
					}
				}
				if !hasFlag {
					t.Errorf("expected flag %q in args %v", tt.wantFlag, gotArgs)
				}
				if !hasSafe {
					t.Errorf("expected safeFlagArg %q in args %v", tt.wantSafe, gotArgs)
				}
			})
		}
	})

	t.Run("wrong password returns FAIL without running tool", func(t *testing.T) {
		if !hasTool(sevenzBin()) {
			t.Skip("7z not installed")
		}
		execCommand = origExec
		lookPath = origLook

		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "s.txt")
		_ = os.WriteFile(src, []byte("secret"), 0644)
		arch := filepath.Join(tmpDir, "locked.7z")
		if out, err := exec.Command(sevenzBin(), "a", "-pright", "-mhe=on", arch, src).CombinedOutput(); err != nil {
			t.Fatalf("7z create failed: %v %s", err, string(out))
		}

		status, err := testArchiveIntegrity(arch, TestOptions{Password: "wrong"})
		if status != "FAIL" || err == nil || !strings.Contains(err.Error(), "contraseña incorrecta") {
			t.Fatalf("expected FAIL with wrong-password error, got status=%q err=%v", status, err)
		}
	})
}

func TestDoTest_MemoryBudgetSplitsAcrossWorkers(t *testing.T) {
	origExec := execCommand
	origLook := lookPath
	origMem := getMemLimit
	defer func() {
		execCommand = origExec
		lookPath = origLook
		getMemLimit = origMem
	}()
	lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	getMemLimit = func() int { return 1200 }

	tmpDir := t.TempDir()
	var files []string
	for _, name := range []string{"a.xz", "b.xz"} {
		p := filepath.Join(tmpDir, name)
		if err := os.WriteFile(p, make([]byte, 1024*1024), 0644); err != nil {
			t.Fatal(err)
		}
		files = append(files, p)
	}

	var mu sync.Mutex
	var captured [][]string
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "xz" {
			mu.Lock()
			captured = append(captured, append([]string(nil), args...))
			mu.Unlock()
		}
		return mockExecCommand("mock-noop")
	}

	if err := DoTest(files, TestOptions{}); err != nil {
		t.Fatalf("DoTest failed: %v", err)
	}
	if len(captured) != 2 {
		t.Fatalf("expected 2 xz invocations, got %d", len(captured))
	}
	for i, args := range captured {
		hasMemLimit := false
		for _, a := range args {
			if a == "--memlimit-mt-decompress=600MiB" {
				hasMemLimit = true
			}
		}
		if !hasMemLimit {
			t.Errorf("worker %d expected split worker budget --memlimit-mt-decompress=600MiB, got %v", i, args)
		}
	}
}

