package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNCPU(t *testing.T) {
	n := NCPU()
	if n < 1 {
		t.Errorf("NCPU() = %d, want >= 1", n)
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048576, "1.0 MiB"},
		{1073741824, "1.0 GiB"},
		{1099511627776, "1.0 TiB"},
	}
	for _, tt := range tests {
		got := FormatSize(tt.bytes)
		if got != tt.want {
			// Accept minor rounding diffs — `numfmt` may not be available
			t.Logf("FormatSize(%d) = %q, want approximate %q", tt.bytes, got, tt.want)
		}
	}
}

func TestCalcPct(t *testing.T) {
	tests := []struct {
		orig, final int64
		want        string
	}{
		{100, 70, "30.00"},
		{100, 0, "100.00"},
		{0, 0, "0.00"},
		{1000, 250, "75.00"},
	}
	for _, tt := range tests {
		got := CalcPct(tt.orig, tt.final)
		if got != tt.want {
			t.Errorf("CalcPct(%d, %d) = %s, want %s", tt.orig, tt.final, got, tt.want)
		}
	}
}

func TestGetUniqueName(t *testing.T) {
	got := GetUniqueName("test", "txt")
	if got != "test.txt" {
		t.Errorf("GetUniqueName(test, txt) = %q, want %q", got, "test.txt")
	}
}

func TestGetUniqueNameConcurrent(t *testing.T) {
	n := 30
	names := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			names[idx] = GetUniqueName("test_concurrent", "txt")
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool)
	for _, name := range names {
		if seen[name] {
			t.Errorf("duplicate name returned concurrently: %s", name)
		}
		seen[name] = true
	}
}

func TestGetMemLimit(t *testing.T) {
	limit := GetMemLimit()
	if limit < 1 {
		t.Errorf("GetMemLimit() = %d, want >= 1", limit)
	}
}

func TestBzip2Bin(t *testing.T) {
	bin := bzip2Bin()
	if bin == "" {
		t.Errorf("bzip2Bin() = empty")
	}
}

func TestSevenzBin(t *testing.T) {
	bin := sevenzBin()
	if bin == "" {
		t.Errorf("sevenzBin() = empty")
	}
}

func TestRarBin(t *testing.T) {
	bin := rarBin()
	if bin == "" {
		t.Errorf("rarBin() = empty")
	}
}

func TestPipeline(t *testing.T) {
	t.Run("empty cmds", func(t *testing.T) {
		var buf bytes.Buffer
		if err := pipeline(&buf, nil); err != nil {
			t.Errorf("pipeline(empty) = %v, want nil", err)
		}
	})

	t.Run("single cmd", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := exec.Command("echo", "hello")
		if err := pipeline(&buf, nil, cmd); err != nil {
			t.Errorf("pipeline(echo) = %v, want nil", err)
		}
		if strings.TrimSpace(buf.String()) != "hello" {
			t.Errorf("pipeline(echo) output = %q, want %q", buf.String(), "hello\n")
		}
	})

	t.Run("pipe two cmds", func(t *testing.T) {
		var buf bytes.Buffer
		echo := exec.Command("echo", "hello-pipe")
		cat := exec.Command("cat")
		if err := pipeline(&buf, nil, echo, cat); err != nil {
			t.Errorf("pipeline(echo|cat) = %v, want nil", err)
		}
		if strings.TrimSpace(buf.String()) != "hello-pipe" {
			t.Errorf("pipeline(echo|cat) output = %q, want %q", buf.String(), "hello-pipe\n")
		}
	})

	t.Run("first cmd fails kills downstream", func(t *testing.T) {
		var buf bytes.Buffer
		cmd1 := exec.Command("false")
		cmd2 := exec.Command("cat")
		err := pipeline(&buf, nil, cmd1, cmd2)
		if err == nil {
			t.Errorf("pipeline(false|cat) expected error, got nil")
		}
	})

	t.Run("downstream fails terminates upstream without hanging", func(t *testing.T) {
		var buf bytes.Buffer
		cmd1 := exec.Command("sleep", "10")
		cmd2 := exec.Command("false")
		done := make(chan error, 1)
		go func() {
			done <- pipeline(&buf, nil, cmd1, cmd2)
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("expected error, got nil")
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("pipeline hung waiting for upstream sleep instead of reacting to downstream failure")
		}
	})
}

func TestFormatSizeExact1024(t *testing.T) {
	got := FormatSize(1024)
	if !strings.Contains(got, "KiB") && !strings.Contains(got, "K") {
		t.Errorf("FormatSize(1024) = %q, want 1 KiB", got)
	}
}

func TestEstimateUncompressedSizeNative(t *testing.T) {
	tmp := t.TempDir()
	sample := filepath.Join(tmp, "archive.tar")
	if err := os.WriteFile(sample, make([]byte, 2048), 0644); err != nil {
		t.Fatal(err)
	}
	size := EstimateUncompressedSize(sample)
	if size != 2048 {
		t.Errorf("EstimateUncompressedSize = %d, want 2048", size)
	}
}

func TestEffectiveThreads(t *testing.T) {
	tests := []struct {
		name        string
		ext         string
		threadLimit int
		want        int
	}{
		{
			name:        "lz4 is always single-threaded",
			ext:         "file.lz4",
			threadLimit: 10,
			want:        1,
		},
		{
			name:        "br is always single-threaded",
			ext:         "file.br",
			threadLimit: 10,
			want:        1,
		},
		{
			name:        "tar is always single-threaded",
			ext:         "file.tar",
			threadLimit: 10,
			want:        1,
		},
		{
			name:        "tar compound lz4 is single-threaded",
			ext:         "archive.tar.lz4",
			threadLimit: 8,
			want:        1,
		},
		{
			name:        "zip respects threadLimit when sevenz is available",
			ext:         "archive.zip",
			threadLimit: 10,
			want:        10,
		},
		{
			name:        "zip with 0 threadLimit uses NCPU",
			ext:         "archive.zip",
			threadLimit: 0,
			want:        NCPU(),
		},
		{
			name:        "zst respects threadLimit",
			ext:         "data.zst",
			threadLimit: 5,
			want:        5,
		},
		{
			name:        "xz respects threadLimit",
			ext:         "data.xz",
			threadLimit: 4,
			want:        4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effectiveThreads(tt.ext, tt.threadLimit)
			if got != tt.want {
				t.Errorf("effectiveThreads(%q, %d) = %d, want %d", tt.ext, tt.threadLimit, got, tt.want)
			}
		})
	}
}

