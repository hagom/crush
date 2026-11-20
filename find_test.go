package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoFindMatches(t *testing.T) {
	tmpDir := t.TempDir()
	tar1 := filepath.Join(tmpDir, "backup1.tar")
	f1, err := os.Create(tar1)
	if err != nil {
		t.Fatal(err)
	}
	tw1 := tar.NewWriter(f1)
	tw1.WriteHeader(&tar.Header{Name: "database.sql", Mode: 0644, Size: 1024, Typeflag: tar.TypeReg})
	tw1.Write(make([]byte, 1024))
	tw1.WriteHeader(&tar.Header{Name: "index.html", Mode: 0644, Size: 200, Typeflag: tar.TypeReg})
	tw1.Write(make([]byte, 200))
	tw1.Close()
	f1.Close()

	var buf bytes.Buffer
	matches, err := DoFind("*.sql", []string{tar1}, "", &buf)
	if err != nil {
		t.Fatalf("DoFind failed: %v", err)
	}
	if matches != 1 {
		t.Errorf("got %d matches, want 1", matches)
	}
	out := buf.String()
	if !strings.Contains(out, "database.sql") || strings.Contains(out, "index.html") {
		t.Errorf("unexpected output: %s", out)
	}
}

func TestDoFindSubstring(t *testing.T) {
	tmpDir := t.TempDir()
	tar1 := filepath.Join(tmpDir, "backup2.tar")
	f1, err := os.Create(tar1)
	if err != nil {
		t.Fatal(err)
	}
	tw1 := tar.NewWriter(f1)
	tw1.WriteHeader(&tar.Header{Name: "etc/nginx/conf.d/default.conf", Mode: 0644, Size: 50, Typeflag: tar.TypeReg})
	tw1.Write(make([]byte, 50))
	tw1.Close()
	f1.Close()

	var buf bytes.Buffer
	matches, err := DoFind("nginx", []string{tar1}, "", &buf)
	if err != nil {
		t.Fatalf("DoFind failed: %v", err)
	}
	if matches != 1 {
		t.Errorf("got %d matches, want 1", matches)
	}
	if !strings.Contains(buf.String(), "default.conf") {
		t.Errorf("output does not contain match: %s", buf.String())
	}
}

func TestDoFind_TableCasesAndStreaming(t *testing.T) {
	tmpDir := t.TempDir()

	tarPath := filepath.Join(tmpDir, "many.tar")
	{
		f, err := os.Create(tarPath)
		if err != nil {
			t.Fatal(err)
		}
		tw := tar.NewWriter(f)
		_ = tw.WriteHeader(&tar.Header{Name: "target_dir/", Mode: 0755, Typeflag: tar.TypeDir})
		for i := 0; i < 200; i++ {
			name := fmt.Sprintf("noise/file_%03d.log", i)
			_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: 4, Typeflag: tar.TypeReg})
			_, _ = tw.Write([]byte("noop"))
		}
		_ = tw.WriteHeader(&tar.Header{Name: "target_dir/target.json", Mode: 0644, Size: 10, Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(`{"ok":123}`))
		_ = tw.Close()
		_ = f.Close()
	}

	zipPath := filepath.Join(tmpDir, "pack.zip")
	{
		f, err := os.Create(zipPath)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(f)
		w1, _ := zw.Create("cfg/target.json")
		_, _ = w1.Write([]byte("{}"))
		w2, _ := zw.Create("other.txt")
		_, _ = w2.Write([]byte("nope"))
		_ = zw.Close()
		_ = f.Close()
	}

	tarGzPath := filepath.Join(tmpDir, "bundle.tar.gz")
	{
		f, err := os.Create(tarGzPath)
		if err != nil {
			t.Fatal(err)
		}
		gw := gzip.NewWriter(f)
		tw := tar.NewWriter(gw)
		_ = tw.WriteHeader(&tar.Header{Name: "deep/TARGET.JSON", Mode: 0644, Size: 2, Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte("{}"))
		_ = tw.Close()
		_ = gw.Close()
		_ = f.Close()
	}

	missingPath := filepath.Join(tmpDir, "missing.tar")

	tests := []struct {
		name        string
		pattern     string
		files       []string
		wantMatches int
		wantErr     bool
		wantContain []string
		wantExclude []string
	}{
		{
			name:    "empty pattern",
			pattern: "",
			files:   []string{tarPath},
			wantErr: true,
		},
		{
			name:    "empty files",
			pattern: "*.json",
			files:   nil,
			wantErr: true,
		},
		{
			name:        "multi-archive case-insensitive streaming ignores dirs",
			pattern:     "*.json",
			files:       []string{tarPath, zipPath, tarGzPath},
			wantMatches: 3,
			wantContain: []string{"target_dir/target.json", "cfg/target.json", "deep/TARGET.JSON", "3 archivo(s) en 3 archivo(s)"},
			wantExclude: []string{"noise/file_000.log", "other.txt"},
		},
		{
			name:        "no matches",
			pattern:     "*.parquet",
			files:       []string{tarPath, zipPath},
			wantMatches: 0,
			wantContain: []string{"No se encontraron coincidencias"},
		},
		{
			name:        "missing archive mixed with valid archive",
			pattern:     "target.json",
			files:       []string{missingPath, zipPath},
			wantMatches: 1,
			wantContain: []string{"Error buscando en", "cfg/target.json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			got, err := DoFind(tt.pattern, tt.files, "", &buf)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantMatches {
				t.Errorf("got %d matches, want %d (out:\n%s)", got, tt.wantMatches, buf.String())
			}
			out := buf.String()
			for _, sub := range tt.wantContain {
				if !strings.Contains(out, sub) {
					t.Errorf("output missing %q:\n%s", sub, out)
				}
			}
			for _, sub := range tt.wantExclude {
				if strings.Contains(out, sub) {
					t.Errorf("output unexpectedly contains %q:\n%s", sub, out)
				}
			}
		})
	}
}
