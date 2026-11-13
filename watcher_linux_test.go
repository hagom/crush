//go:build linux

package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestRunInotifyWatcher_HighFD(t *testing.T) {
	var rlim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlim); err != nil {
		t.Skipf("Getrlimit failed: %v", err)
	}
	const needFDs = 1100
	if rlim.Cur < needFDs {
		if rlim.Max < needFDs {
			t.Skipf("RLIMIT_NOFILE hard limit too low (%d < %d)", rlim.Max, needFDs)
		}
		orig := rlim
		rlim.Cur = needFDs
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rlim); err != nil {
			t.Skipf("cannot raise RLIMIT_NOFILE: %v", err)
		}
		defer func() { _ = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &orig) }()
	}

	var opened []*os.File
	defer func() {
		for _, f := range opened {
			_ = f.Close()
		}
	}()
	for i := 0; i < 1050; i++ {
		f, err := os.Open(os.DevNull)
		if err != nil {
			break
		}
		opened = append(opened, f)
		if f.Fd() >= 1030 {
			break
		}
	}
	if len(opened) == 0 || opened[len(opened)-1].Fd() < 1024 {
		t.Skipf("could not push process FDs above 1024")
	}

	watchDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var events []string
	errCh := make(chan error, 1)

	go func() {
		errCh <- runInotifyWatcher(ctx, WatcherOptions{Dir: watchDir, Mode: WatchModeCompress}, func(path string) error {
			mu.Lock()
			events = append(events, filepath.Base(path))
			mu.Unlock()
			cancel()
			return nil
		})
	}()

	time.Sleep(100 * time.Millisecond)
	target := filepath.Join(watchDir, "highfd.txt")
	if err := os.WriteFile(target, []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("runInotifyWatcher returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatalf("timed out waiting for inotify event on high FD")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 || events[0] != "highfd.txt" {
		t.Errorf("expected event for highfd.txt, got %v", events)
	}
}
