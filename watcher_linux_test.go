//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"
)

func buildRawInotifyEvent(wd int32, mask, cookie, nameLen uint32, nameBytes []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, wd)
	_ = binary.Write(&b, binary.LittleEndian, mask)
	_ = binary.Write(&b, binary.LittleEndian, cookie)
	_ = binary.Write(&b, binary.LittleEndian, nameLen)
	b.Write(nameBytes)
	return b.Bytes()
}

func TestParseInotifyEvents_BoundsAndOverflow(t *testing.T) {
	padName := func(s string) []byte {
		b := append([]byte(s), 0)
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
		return b
	}

	validA := padName("alpha.txt")
	validB := padName("beta.gz")
	traversalSlash := padName("../escape.txt")
	dotDot := padName("..")

	var multiBuf bytes.Buffer
	multiBuf.Write(buildRawInotifyEvent(1, syscall.IN_CLOSE_WRITE, 0, uint32(len(validA)), validA))
	multiBuf.Write(buildRawInotifyEvent(1, syscall.IN_MOVED_TO, 0, uint32(len(validB)), validB))

	// Truncated name: nameLen claims 64 bytes, only 8 bytes present, followed by another fake event inside those bytes
	var truncBuf bytes.Buffer
	truncBuf.Write(buildRawInotifyEvent(1, syscall.IN_CLOSE_WRITE, 0, 64, validA))
	truncBuf.Write(buildRawInotifyEvent(1, syscall.IN_CLOSE_WRITE, 0, uint32(len(validB)), validB))

	// Overflow uint32 nameLen (0xFFFFFFFF)
	hugeLenBuf := buildRawInotifyEvent(1, syscall.IN_CLOSE_WRITE, 0, 0xFFFFFFFF, validA)

	// IN_Q_OVERFLOW event (wd=-1, mask=IN_Q_OVERFLOW, nameLen=0)
	overflowBuf := buildRawInotifyEvent(-1, syscall.IN_Q_OVERFLOW, 0, 0, nil)

	// Path traversal names
	var travBuf bytes.Buffer
	travBuf.Write(buildRawInotifyEvent(1, syscall.IN_CLOSE_WRITE, 0, uint32(len(traversalSlash)), traversalSlash))
	travBuf.Write(buildRawInotifyEvent(1, syscall.IN_CLOSE_WRITE, 0, uint32(len(dotDot)), dotDot))
	travBuf.Write(buildRawInotifyEvent(1, syscall.IN_CLOSE_WRITE, 0, uint32(len(validA)), validA))

	tests := []struct {
		name         string
		buf          []byte
		wantNames    []string
		wantOverflow bool
	}{
		{
			name:         "empty buffer",
			buf:          nil,
			wantNames:    nil,
			wantOverflow: false,
		},
		{
			name:         "truncated header",
			buf:          []byte{1, 2, 3, 4},
			wantNames:    nil,
			wantOverflow: false,
		},
		{
			name:         "multiple valid events",
			buf:          multiBuf.Bytes(),
			wantNames:    []string{"alpha.txt", "beta.gz"},
			wantOverflow: false,
		},
		{
			name:         "truncated name stops parsing instead of misaligning header",
			buf:          truncBuf.Bytes()[:syscall.SizeofInotifyEvent+len(validA)],
			wantNames:    nil,
			wantOverflow: false,
		},
		{
			name:         "huge uint32 nameLen does not panic or overflow int",
			buf:          hugeLenBuf,
			wantNames:    nil,
			wantOverflow: false,
		},
		{
			name:         "IN_Q_OVERFLOW sets overflow flag",
			buf:          overflowBuf,
			wantNames:    nil,
			wantOverflow: true,
		},
		{
			name:         "rejects path separators and dotdot names",
			buf:          travBuf.Bytes(),
			wantNames:    []string{"alpha.txt"},
			wantOverflow: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			names, overflow := parseInotifyEvents(tc.buf)
			if overflow != tc.wantOverflow {
				t.Errorf("overflow = %v, want %v", overflow, tc.wantOverflow)
			}
			if !reflect.DeepEqual(names, tc.wantNames) {
				t.Errorf("names = %v, want %v", names, tc.wantNames)
			}
		})
	}
}

func TestInotifyBatch_PrunesDeletedAndReprocessesOnOverflow(t *testing.T) {
	dir := t.TempDir()

	// Pre-populate knownBefore with a stale file that no longer exists on disk
	known := map[string]bool{
		"stale_deleted.txt": true,
	}

	// Create job.txt on disk; processFn simulates KeepOrig=false (deletes file after compressing)
	jobPath := filepath.Join(dir, "job.txt")
	if err := os.WriteFile(jobPath, []byte("first payload"), 0644); err != nil {
		t.Fatal(err)
	}

	var processed []string
	processFn := func(fullPath string) error {
		processed = append(processed, filepath.Base(fullPath))
		_ = os.Remove(fullPath)
		return nil
	}

	padName := func(s string) []byte {
		b := append([]byte(s), 0)
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
		return b
	}
	jobBytes := padName("job.txt")
	normalEv := buildRawInotifyEvent(1, syscall.IN_CLOSE_WRITE, 0, uint32(len(jobBytes)), jobBytes)

	// 1. Process normal IN_CLOSE_WRITE event for job.txt (which deletes job.txt)
	handleInotifyBuffer(context.Background(), dir, normalEv, known, processFn)
	if len(processed) != 1 || processed[0] != "job.txt" {
		t.Fatalf("after normal event processed = %v, want [job.txt]", processed)
	}
	if known["job.txt"] {
		t.Errorf("known[job.txt] should be pruned after processFn removed job.txt")
	}

	// 2. Re-create job.txt before an IN_Q_OVERFLOW event
	if err := os.WriteFile(jobPath, []byte("second payload after overflow"), 0644); err != nil {
		t.Fatal(err)
	}
	overflowEv := buildRawInotifyEvent(-1, syscall.IN_Q_OVERFLOW, 0, 0, nil)
	handleInotifyBuffer(context.Background(), dir, overflowEv, known, processFn)

	if len(processed) != 2 || processed[1] != "job.txt" {
		t.Fatalf("after IN_Q_OVERFLOW rescan processed = %v, want [job.txt job.txt]", processed)
	}
	if known["stale_deleted.txt"] {
		t.Errorf("known[stale_deleted.txt] should be pruned during IN_Q_OVERFLOW rescan")
	}
	if len(known) != 0 {
		t.Errorf("known map should be empty after all files removed, got %v", known)
	}
}

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
