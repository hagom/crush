package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
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
		fmt.Fprintf(os.Stderr, "No mock command provided\n")
		os.Exit(2)
	}

	cmd, cmdArgs := args[0], args[1:]
	switch cmd {
	case "free":
		fmt.Println("               total        used        free      shared  buff/cache   available")
		fmt.Println("Mem:        16777216     8388608     4194304      524288     4194304     8388608")
		fmt.Println("Swap:        2097152           0     2097152")
		os.Exit(0)

	case "free-malformed":
		fmt.Println("Mem: invalid")
		os.Exit(0)

	case "free-empty":
		os.Exit(0)

	case "free-fail":
		fmt.Fprintln(os.Stderr, "free: command not found")
		os.Exit(1)

	case "df":
		hasB1 := false
		for _, a := range cmdArgs {
			if a == "-B1" {
				hasB1 = true
				break
			}
		}
		if hasB1 {
			fmt.Println("       Avail")
			fmt.Println("107374182400")
		} else {
			fmt.Println("Filesystem     1K-blocks     Used Available Use% Mounted on")
			fmt.Println("/dev/sda1      209715200 94371840 104857600  48% /")
		}
		os.Exit(0)

	case "df-fail":
		fmt.Fprintln(os.Stderr, "df: cannot access '/nonexistent': No such file or directory")
		os.Exit(1)

	case "df-low-space":
		fmt.Println("       Avail")
		fmt.Println("1024")
		os.Exit(0)

	case "mock-producer":
		for {
			if _, err := fmt.Println("stream payload data"); err != nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		os.Exit(0)

	case "mock-fail-compressor":
		fmt.Fprintln(os.Stderr, "compressor: fatal error: compression stream corrupted")
		os.Exit(1)

	case "mock-consumer":
		buf := make([]byte, 1024)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				break
			}
		}
		os.Exit(0)

	case "mock-noop":
		os.Exit(0)

	default:
		fmt.Fprintf(os.Stderr, "unknown mock command: %s\n", cmd)
		os.Exit(127)
	}
}

func mockExecCommand(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	return cmd
}

func TestPipelineMockIntermediateFailure(t *testing.T) {
	origExec := execCommand
	defer func() { execCommand = origExec }()
	execCommand = mockExecCommand

	cmd1 := execCommand("mock-producer")
	cmd2 := execCommand("mock-fail-compressor")
	cmd3 := execCommand("mock-consumer")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	done := make(chan error, 1)

	go func() {
		done <- pipeline(&stdout, &stderr, cmd1, cmd2, cmd3)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected pipeline to fail when intermediate compressor fails, got nil")
		}
		if !strings.Contains(err.Error(), "pipeline cmd 1") {
			t.Errorf("expected error to mention failing cmd index 1, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("pipeline hung instead of canceling upstream and downstream commands")
	}
}

func TestGetMemFromFreeMock(t *testing.T) {
	origExec := execCommand
	defer func() { execCommand = origExec }()

	tests := []struct {
		name        string
		mockCommand string
		wantMB      int
	}{
		{
			name:        "successful free -k output parses available MB at 70%",
			mockCommand: "free",
			wantMB:      5734, // 8388608 * 70 / 100 / 1024
		},
		{
			name:        "failed free command returns 0",
			mockCommand: "free-fail",
			wantMB:      0,
		},
		{
			name:        "malformed output returns 0",
			mockCommand: "free-malformed",
			wantMB:      0,
		},
		{
			name:        "empty output returns 0",
			mockCommand: "free-empty",
			wantMB:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execCommand = func(name string, args ...string) *exec.Cmd {
				return mockExecCommand(tt.mockCommand, args...)
			}
			got := getMemFromFree()
			if got != tt.wantMB {
				t.Errorf("getMemFromFree() = %d, want %d", got, tt.wantMB)
			}
		})
	}
}

func TestGetAvailBytesMock(t *testing.T) {
	origExec := execCommand
	defer func() { execCommand = origExec }()

	tests := []struct {
		name        string
		mockCommand string
		dir         string
		wantBytes   int64
	}{
		{
			name:        "df with -B1 returns parsed available bytes",
			mockCommand: "df",
			dir:         "/some/mount/point",
			wantBytes:   107374182400,
		},
		{
			name:        "df with empty dir defaults to current dir",
			mockCommand: "df",
			dir:         "",
			wantBytes:   107374182400,
		},
		{
			name:        "failed df command returns 0",
			mockCommand: "df-fail",
			dir:         "/nonexistent",
			wantBytes:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execCommand = func(name string, args ...string) *exec.Cmd {
				return mockExecCommand(tt.mockCommand, args...)
			}
			got := GetAvailBytes(tt.dir)
			if got != tt.wantBytes {
				t.Errorf("GetAvailBytes(%q) = %d, want %d", tt.dir, got, tt.wantBytes)
			}
		})
	}
}

func TestDecompressBatchDiskSpaceCheck(t *testing.T) {
	origExec := execCommand
	defer func() { execCommand = origExec }()

	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "file1.tar")
	f2 := filepath.Join(tmpDir, "file2.tar")
	if err := os.WriteFile(f1, make([]byte, 10*1024), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, make([]byte, 10*1024), 0644); err != nil {
		t.Fatal(err)
	}

	execCommand = func(name string, args ...string) *exec.Cmd {
		return mockExecCommand("df-low-space", args...)
	}

	opts := DecompressOptions{OutputDir: tmpDir}
	err := DoDecompress([]string{f1, f2}, opts)
	if err == nil {
		t.Fatal("DoDecompress debe fallar si no hay suficiente espacio para el lote")
	}
	if !strings.Contains(err.Error(), "Espacio insuficiente para descomprimir") {
		t.Errorf("error inesperado: %v", err)
	}
}

func TestIoPipeDeadlocks_ConsumerFailure(t *testing.T) {
	runWithTimeout := func(t *testing.T, name string, fn func() error) {
		t.Helper()
		done := make(chan error, 1)
		go func() {
			done <- fn()
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s: expected error when compressor fails, got nil", name)
			}
		case <-time.After(1 * time.Second):
			t.Errorf("%s: DEADLOCK timed out after 1s when compressor failed early", name)
		}
	}

	trackCmd := func(t *testing.T, cmd *exec.Cmd) *exec.Cmd {
		t.Cleanup(func() {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		})
		return cmd
	}

	t.Run("compressTarPipe_Progress", func(t *testing.T) {
		origExec := execCommand
		defer func() { execCommand = origExec }()
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "data.txt")
		_ = os.WriteFile(src, []byte("payload"), 0644)
		out := filepath.Join(tmpDir, "out.tar.xz")

		execCommand = func(name string, args ...string) *exec.Cmd {
			if name == "tar" {
				return trackCmd(t, mockExecCommand("mock-producer"))
			}
			return trackCmd(t, mockExecCommand("mock-fail-compressor"))
		}

		pt := NewProgressTracker(100, 1)
		fp := &FileProgress{Name: "data.txt", Size: 100}
		runWithTimeout(t, "compressTarPipe", func() error {
			return compressTarPipe([]string{src}, out, CompressOptions{
				Format:      Xz,
				ThreadLimit: 1,
				Progress:    pt,
			}, fp)
		})
	})

	t.Run("convertStreamPipe", func(t *testing.T) {
		origExec := execCommand
		defer func() { execCommand = origExec }()
		tmpDir := t.TempDir()
		src := filepath.Join(tmpDir, "in.gz")
		_ = os.WriteFile(src, []byte("dummy"), 0644)
		out := filepath.Join(tmpDir, "out.xz")

		execCommand = func(name string, args ...string) *exec.Cmd {
			if name == "pigz" || name == "gzip" {
				return trackCmd(t, mockExecCommand("mock-producer"))
			}
			return trackCmd(t, mockExecCommand("mock-fail-compressor"))
		}

		runWithTimeout(t, "convertStreamPipe", func() error {
			return convertStreamPipe(src, out, FormatInfo{Format: Gz}, Xz, ConvertOptions{ThreadLimit: 1})
		})
	})

	t.Run("convertViaTempDir", func(t *testing.T) {
		origExec := execCommand
		defer func() { execCommand = origExec }()
		tmpDir := t.TempDir()
		srcZip := filepath.Join(tmpDir, "in.zip")
		zf, _ := os.Create(srcZip)
		zw := zip.NewWriter(zf)
		w, _ := zw.Create("inside.txt")
		_, _ = w.Write([]byte("zip body"))
		_ = zw.Close()
		_ = zf.Close()

		outXz := filepath.Join(tmpDir, "in.tar.xz")
		execCommand = func(name string, args ...string) *exec.Cmd {
			if name == "tar" {
				return trackCmd(t, mockExecCommand("mock-producer"))
			}
			if name == "xz" {
				return trackCmd(t, mockExecCommand("mock-fail-compressor"))
			}
			return exec.Command(name, args...)
		}

		runWithTimeout(t, "convertViaTempDir", func() error {
			return convertViaTempDir(srcZip, outXz, FormatInfo{Format: Zip}, Xz, ConvertOptions{ThreadLimit: 1})
		})
	})
}

