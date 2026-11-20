package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListArchiveMembersTar(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "sample.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	hdr1 := &tar.Header{Name: "dir/", Mode: 0755, Typeflag: tar.TypeDir}
	tw.WriteHeader(hdr1)
	hdr2 := &tar.Header{Name: "dir/hello.txt", Mode: 0644, Size: 12, Typeflag: tar.TypeReg}
	tw.WriteHeader(hdr2)
	tw.Write([]byte("hello world\n"))
	tw.Close()
	f.Close()

	members, err := ListArchiveMembers(tarPath, "")
	if err != nil {
		t.Fatalf("ListArchiveMembers failed: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("got %d members, want 2", len(members))
	}
	if members[0].Path != "dir/" || !members[0].IsDir {
		t.Errorf("member[0] mismatch: %+v", members[0])
	}
	if members[1].Path != "dir/hello.txt" || members[1].Size != 12 || members[1].IsDir {
		t.Errorf("member[1] mismatch: %+v", members[1])
	}
}

func TestListArchiveMembersZip(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "sample.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("doc.txt")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("documentation"))
	zw.Close()
	f.Close()

	members, err := ListArchiveMembers(zipPath, "")
	if err != nil {
		t.Fatalf("ListArchiveMembers failed: %v", err)
	}
	if len(members) != 1 || members[0].Path != "doc.txt" || members[0].Size != 13 {
		t.Errorf("zip member mismatch: %+v", members)
	}
}

func TestListArchiveMembersTarGz(t *testing.T) {
	tmpDir := t.TempDir()
	tarGzPath := filepath.Join(tmpDir, "sample.tar.gz")
	f, err := os.Create(tarGzPath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	tw.WriteHeader(&tar.Header{Name: "file.bin", Mode: 0644, Size: 100, Typeflag: tar.TypeReg})
	tw.Write(make([]byte, 100))
	tw.Close()
	gw.Close()
	f.Close()

	members, err := ListArchiveMembers(tarGzPath, "")
	if err != nil {
		t.Fatalf("ListArchiveMembers tar.gz failed: %v", err)
	}
	if len(members) != 1 || members[0].Path != "file.bin" || members[0].Size != 100 {
		t.Errorf("tar.gz member mismatch: %+v", members)
	}
}

func TestListArchiveMembersStream(t *testing.T) {
	tmpDir := t.TempDir()
	gzPath := filepath.Join(tmpDir, "single.txt.gz")
	f, err := os.Create(gzPath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	gw.Write([]byte("stream data content"))
	gw.Close()
	f.Close()

	members, err := ListArchiveMembers(gzPath, "")
	if err != nil {
		t.Fatalf("ListArchiveMembers single stream failed: %v", err)
	}
	if len(members) != 1 || members[0].Path != "single.txt" {
		t.Errorf("stream member mismatch: %+v", members)
	}
}

func TestStreamListHelperProcess(t *testing.T) {
	if os.Getenv("CRUSH_STREAM_LIST_HELPER") != "1" {
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

	mode := args[0]
	switch mode {
	case "infinite-tar":
		tw := tar.NewWriter(os.Stdout)
		for i := 0; ; i++ {
			name := fmt.Sprintf("entry_%06d.txt", i)
			if err := tw.WriteHeader(&tar.Header{
				Name:     name,
				Mode:     0644,
				Size:     4,
				Typeflag: tar.TypeReg,
			}); err != nil {
				os.Exit(0)
			}
			if _, err := tw.Write([]byte("data")); err != nil {
				os.Exit(0)
			}
		}

	case "fail-empty":
		os.Exit(1)

	case "tar-long-lines":
		longPath := "deep/" + strings.Repeat("a", 80*1024) + ".txt"
		fmt.Println("dir/")
		fmt.Println(longPath)
		fmt.Println("tail.txt")

	case "infinite-lines":
		for i := 0; ; i++ {
			if _, err := fmt.Printf("item_%06d.txt\n", i); err != nil {
				os.Exit(0)
			}
		}

	case "7z-slt-long":
		longPath := "sub/" + strings.Repeat("b", 80*1024) + ".bin"
		fmt.Println("Path = folder")
		fmt.Println("Size = 0")
		fmt.Println("Modified = 2026-05-01 10:20:30")
		fmt.Println("Folder = +")
		fmt.Println("Attributes = D")
		fmt.Println()
		fmt.Printf("Path = %s\n", longPath)
		fmt.Println("Size = 4096")
		fmt.Println("Modified = 2026-05-01 11:00:00")
		fmt.Println("Folder = -")
		fmt.Println()
		fmt.Println("Path = last.txt")
		fmt.Println("Size = 12")

	case "7z-slt-infinite":
		for i := 0; ; i++ {
			if _, err := fmt.Printf("Path = f_%06d.txt\nSize = 10\nFolder = -\n\n", i); err != nil {
				os.Exit(0)
			}
		}

	case "7z-slt-secret":
		hasSecret := false
		for _, a := range args[1:] {
			if a == "-psecret123" {
				hasSecret = true
				break
			}
		}
		if !hasSecret {
			os.Exit(2)
		}
		fmt.Println("Path = vault.txt")
		fmt.Println("Size = 42")
		fmt.Println("Folder = -")
		fmt.Println()

	case "rar-long-lines":
		longPath := "rar/" + strings.Repeat("c", 80*1024) + ".dat"
		fmt.Println("docs/")
		fmt.Println(longPath)
		fmt.Println("readme.md")

	case "7z-slt-gate":
		gateFile := ""
		if len(args) > 1 {
			gateFile = args[1]
		}
		fmt.Print("Path = first.txt\nSize = 5\nFolder = -\n\n")
		_ = os.Stdout.Sync()
		opened := false
		deadline := time.Now().Add(1500 * time.Millisecond)
		for time.Now().Before(deadline) {
			if gateFile != "" {
				if _, err := os.Stat(gateFile); err == nil {
					opened = true
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		if opened {
			fmt.Print("Path = second.txt\nSize = 6\nFolder = -\n\n")
		} else {
			fmt.Print("Path = buffered_timeout.txt\nSize = 1\nFolder = -\n\n")
		}

	default:
		os.Exit(127)
	}
}

func streamListMockCmd(mode string, extra ...string) *exec.Cmd {
	cs := []string{"-test.run=TestStreamListHelperProcess", "--", mode}
	cs = append(cs, extra...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = append(os.Environ(), "CRUSH_STREAM_LIST_HELPER=1")
	return cmd
}

func TestIterArchiveMembers_LocalFormatsAndEarlyStop(t *testing.T) {
	tmpDir := t.TempDir()

	tarPath := filepath.Join(tmpDir, "multi.tar")
	{
		f, err := os.Create(tarPath)
		if err != nil {
			t.Fatal(err)
		}
		tw := tar.NewWriter(f)
		for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
			_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: 3, Typeflag: tar.TypeReg})
			_, _ = tw.Write([]byte("abc"))
		}
		_ = tw.Close()
		_ = f.Close()
	}

	emptyTarPath := filepath.Join(tmpDir, "empty.tar")
	{
		f, err := os.Create(emptyTarPath)
		if err != nil {
			t.Fatal(err)
		}
		tw := tar.NewWriter(f)
		_ = tw.Close()
		_ = f.Close()
	}

	zipPath := filepath.Join(tmpDir, "multi.zip")
	{
		f, err := os.Create(zipPath)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(f)
		for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
			w, _ := zw.Create(name)
			_, _ = w.Write([]byte("abc"))
		}
		_ = zw.Close()
		_ = f.Close()
	}

	tarGzPath := filepath.Join(tmpDir, "multi.tar.gz")
	{
		f, err := os.Create(tarGzPath)
		if err != nil {
			t.Fatal(err)
		}
		gw := gzip.NewWriter(f)
		tw := tar.NewWriter(gw)
		for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
			_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: 3, Typeflag: tar.TypeReg})
			_, _ = tw.Write([]byte("abc"))
		}
		_ = tw.Close()
		_ = gw.Close()
		_ = f.Close()
	}

	gzPath := filepath.Join(tmpDir, "one.txt.gz")
	{
		f, err := os.Create(gzPath)
		if err != nil {
			t.Fatal(err)
		}
		gw := gzip.NewWriter(f)
		_, _ = gw.Write([]byte("hello"))
		_ = gw.Close()
		_ = f.Close()
	}

	sentinelErr := errors.New("callback boom")

	tests := []struct {
		name      string
		file      string
		stopAt    int
		stopErr   error
		wantCount int
		wantErr   error
		expectErr bool
	}{
		{name: "tar full", file: tarPath, wantCount: 3},
		{name: "tar early stop", file: tarPath, stopAt: 1, stopErr: errStopIter, wantCount: 1},
		{name: "tar callback error", file: tarPath, stopAt: 2, stopErr: sentinelErr, wantCount: 2, wantErr: sentinelErr},
		{name: "tar empty", file: emptyTarPath, wantCount: 0},
		{name: "zip full", file: zipPath, wantCount: 3},
		{name: "zip early stop", file: zipPath, stopAt: 1, stopErr: errStopIter, wantCount: 1},
		{name: "zip callback error", file: zipPath, stopAt: 2, stopErr: sentinelErr, wantCount: 2, wantErr: sentinelErr},
		{name: "tar.gz full", file: tarGzPath, wantCount: 3},
		{name: "tar.gz early stop", file: tarGzPath, stopAt: 1, stopErr: errStopIter, wantCount: 1},
		{name: "tar.gz callback error", file: tarGzPath, stopAt: 2, stopErr: sentinelErr, wantCount: 2, wantErr: sentinelErr},
		{name: "single stream full", file: gzPath, wantCount: 1},
		{name: "single stream early stop", file: gzPath, stopAt: 1, stopErr: errStopIter, wantCount: 1},
		{name: "single stream callback error", file: gzPath, stopAt: 1, stopErr: sentinelErr, wantCount: 1, wantErr: sentinelErr},
		{name: "nonexistent tar", file: filepath.Join(tmpDir, "missing.tar"), expectErr: true},
		{name: "unknown extension", file: filepath.Join(tmpDir, "file.unknown"), expectErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := 0
			err := IterArchiveMembers(tt.file, "", func(m ArchiveMember) error {
				got++
				if tt.stopAt > 0 && got == tt.stopAt {
					return tt.stopErr
				}
				return nil
			})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("got err=%v, want %v", err, tt.wantErr)
				}
			} else if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantCount {
				t.Errorf("visited %d members, want %d", got, tt.wantCount)
			}
		})
	}
}

func TestIterArchiveMembers_PipeTarEarlyStopKillsSubprocess(t *testing.T) {
	origExec := execCommand
	defer func() { execCommand = origExec }()

	tarFallbackCalled := false
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "tar" {
			tarFallbackCalled = true
			return streamListMockCmd("fail-empty")
		}
		return streamListMockCmd("infinite-tar")
	}

	done := make(chan error, 1)
	var visited []string
	go func() {
		done <- IterArchiveMembers("huge.tar.xz", "", func(m ArchiveMember) error {
			visited = append(visited, m.Path)
			if len(visited) == 2 {
				return errStopIter
			}
			return nil
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected nil on errStopIter, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("IterArchiveMembers hung instead of killing subprocess on errStopIter")
	}

	if len(visited) != 2 || visited[0] != "entry_000000.txt" || visited[1] != "entry_000001.txt" {
		t.Errorf("unexpected visited members: %v", visited)
	}
	if tarFallbackCalled {
		t.Errorf("tar fallback must not be called after errStopIter")
	}
}

func TestIterArchiveMembers_TarFallbackLongLineAndEarlyStop(t *testing.T) {
	origExec := execCommand
	defer func() { execCommand = origExec }()

	t.Run("long path and safeFlagArg", func(t *testing.T) {
		var tarArgs []string
		execCommand = func(name string, args ...string) *exec.Cmd {
			if name == "tar" {
				tarArgs = append([]string(nil), args...)
				return streamListMockCmd("tar-long-lines")
			}
			return streamListMockCmd("fail-empty")
		}

		var got []ArchiveMember
		err := IterArchiveMembers("-dash.tar.xz", "", func(m ArchiveMember) error {
			got = append(got, m)
			return nil
		})
		if err != nil {
			t.Fatalf("IterArchiveMembers fallback failed: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d members, want 3", len(got))
		}
		if !got[0].IsDir || got[0].Path != "dir/" {
			t.Errorf("unexpected first member: %+v", got[0])
		}
		if len(got[1].Path) <= 64*1024 {
			t.Errorf("expected >64KiB path to be preserved, got len=%d", len(got[1].Path))
		}
		hasDashDash, hasSafe := false, false
		for _, a := range tarArgs {
			if a == "--" {
				hasDashDash = true
			}
			if a == "./-dash.tar.xz" {
				hasSafe = true
			}
		}
		if !hasDashDash || !hasSafe {
			t.Errorf("tar fallback missing -- or safeFlagArg: %v", tarArgs)
		}
	})

	t.Run("early stop kills infinite tar fallback", func(t *testing.T) {
		execCommand = func(name string, args ...string) *exec.Cmd {
			if name == "tar" {
				return streamListMockCmd("infinite-lines")
			}
			return streamListMockCmd("fail-empty")
		}

		done := make(chan error, 1)
		count := 0
		go func() {
			done <- IterArchiveMembers("big.tar.xz", "", func(m ArchiveMember) error {
				count++
				if count == 3 {
					return errStopIter
				}
				return nil
			})
		}()

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("expected nil on errStopIter, got: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("tar fallback hung on errStopIter")
		}
		if count != 3 {
			t.Errorf("got %d members, want 3", count)
		}
	})
}

func TestIterArchiveMembers_SevenZipStreamingAndPassword(t *testing.T) {
	origExec := execCommand
	origLook := lookPath
	defer func() {
		execCommand = origExec
		lookPath = origLook
	}()
	lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }

	t.Run("long path and metadata", func(t *testing.T) {
		execCommand = func(name string, args ...string) *exec.Cmd {
			return streamListMockCmd("7z-slt-long")
		}
		var got []ArchiveMember
		err := IterArchiveMembers("sample.7z", "", func(m ArchiveMember) error {
			got = append(got, m)
			return nil
		})
		if err != nil {
			t.Fatalf("IterArchiveMembers(.7z) failed: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d members, want 3", len(got))
		}
		if !got[0].IsDir || got[0].Path != "folder" {
			t.Errorf("member[0] mismatch: %+v", got[0])
		}
		if len(got[1].Path) <= 64*1024 || got[1].Size != 4096 || got[1].IsDir {
			t.Errorf("member[1] mismatch: len=%d size=%d isDir=%v", len(got[1].Path), got[1].Size, got[1].IsDir)
		}
		if got[2].Path != "last.txt" || got[2].Size != 12 {
			t.Errorf("member[2] mismatch: %+v", got[2])
		}
	})

	t.Run("early stop kills infinite 7z", func(t *testing.T) {
		execCommand = func(name string, args ...string) *exec.Cmd {
			return streamListMockCmd("7z-slt-infinite")
		}
		done := make(chan error, 1)
		count := 0
		go func() {
			done <- IterArchiveMembers("huge.7z", "", func(m ArchiveMember) error {
				count++
				if count == 2 {
					return errStopIter
				}
				return nil
			})
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("expected nil on errStopIter, got: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("7z listing hung on errStopIter")
		}
		if count != 2 {
			t.Errorf("got %d members, want 2", count)
		}
	})

	t.Run("lazy password resolution when 0 members emitted", func(t *testing.T) {
		pm := NewPasswordManager(filepath.Join(t.TempDir(), "pw.txt"))
		pm.AddSuccessful("secret123")
		restorePM := SetPasswordManager(pm)
		defer restorePM()

		execCommand = func(name string, args ...string) *exec.Cmd {
			return streamListMockCmd("7z-slt-secret", args...)
		}

		var got []ArchiveMember
		err := IterArchiveMembers("locked.7z", "", func(m ArchiveMember) error {
			got = append(got, m)
			return nil
		})
		if err != nil {
			t.Fatalf("lazy password retry failed: %v", err)
		}
		if len(got) != 1 || got[0].Path != "vault.txt" || got[0].Size != 42 {
			t.Errorf("unexpected members after lazy password: %+v", got)
		}
	})
}

func TestIterArchiveMembers_RarStreamingAndEarlyStop(t *testing.T) {
	origExec := execCommand
	origLook := lookPath
	defer func() {
		execCommand = origExec
		lookPath = origLook
	}()
	lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }

	t.Run("long path and safeFlagArg", func(t *testing.T) {
		var rarArgs []string
		execCommand = func(name string, args ...string) *exec.Cmd {
			rarArgs = append([]string(nil), args...)
			return streamListMockCmd("rar-long-lines")
		}

		var got []ArchiveMember
		err := IterArchiveMembers("-dash.rar", "pw1", func(m ArchiveMember) error {
			got = append(got, m)
			return nil
		})
		if err != nil {
			t.Fatalf("IterArchiveMembers(.rar) failed: %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d members, want 3", len(got))
		}
		if !got[0].IsDir || got[0].Path != "docs/" {
			t.Errorf("member[0] mismatch: %+v", got[0])
		}
		if len(got[1].Path) <= 64*1024 {
			t.Errorf("expected >64KiB path, got len=%d", len(got[1].Path))
		}
		hasDashDash, hasSafe, hasPw := false, false, false
		for _, a := range rarArgs {
			if a == "--" {
				hasDashDash = true
			}
			if a == "./-dash.rar" {
				hasSafe = true
			}
			if a == "-ppw1" {
				hasPw = true
			}
		}
		if !hasDashDash || !hasSafe || !hasPw {
			t.Errorf("rar args missing expected flags: %v", rarArgs)
		}
	})

	t.Run("early stop kills infinite rar", func(t *testing.T) {
		execCommand = func(name string, args ...string) *exec.Cmd {
			return streamListMockCmd("infinite-lines")
		}

		done := make(chan error, 1)
		count := 0
		go func() {
			done <- IterArchiveMembers("huge.rar", "", func(m ArchiveMember) error {
				count++
				if count == 2 {
					return errStopIter
				}
				return nil
			})
		}()

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("expected nil on errStopIter, got: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("rar listing hung on errStopIter")
		}
		if count != 2 {
			t.Errorf("got %d members, want 2", count)
		}
	})
}
