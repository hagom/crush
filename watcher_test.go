package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func writeGzTestFile(t *testing.T, targetPath, content string) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(content)); err != nil {
		t.Fatalf("escribiendo gzip: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("cerrando gzip: %v", err)
	}
	if err := os.WriteFile(targetPath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("guardando archivo gzip: %v", err)
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestWatcher_Compress_Inotify(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	var watcherErr error

	opts := WatcherOptions{
		Dir:  tmpDir,
		Mode: WatchModeCompress,
		CompressOpts: CompressOptions{
			Format:    Gz,
			OutputDir: tmpDir,
			KeepOrig:  true,
		},
		PollInterval: 50 * time.Millisecond,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		watcherErr = RunWatcher(ctx, opts)
	}()

	time.Sleep(50 * time.Millisecond)

	testFile := filepath.Join(tmpDir, "sample.txt")
	if err := os.WriteFile(testFile, []byte("contenido de prueba para watcher inotify"), 0644); err != nil {
		t.Fatalf("creando archivo de prueba: %v", err)
	}

	expectedOut := filepath.Join(tmpDir, "sample.txt.gz")
	success := waitForCondition(t, 5*time.Second, func() bool {
		st, err := os.Stat(expectedOut)
		return err == nil && st.Size() > 0
	})
	if !success {
		t.Fatalf("tiempo de espera agotado esperando archivo comprimido %s", expectedOut)
	}

	cancel()
	wg.Wait()

	if watcherErr != nil {
		t.Fatalf("RunWatcher retornó error al cancelar contexto: %v", watcherErr)
	}
}

func TestWatcher_Decompress_Inotify(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	var watcherErr error

	opts := WatcherOptions{
		Dir:  tmpDir,
		Mode: WatchModeDecompress,
		DecompressOpts: DecompressOptions{
			OutputDir: tmpDir,
			KeepOrig:  true,
			Force:     true,
		},
		PollInterval: 50 * time.Millisecond,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		watcherErr = RunWatcher(ctx, opts)
	}()

	time.Sleep(50 * time.Millisecond)

	archivePath := filepath.Join(tmpDir, "archive.txt.gz")
	writeGzTestFile(t, archivePath, "texto descomprimido con inotify")

	expectedOut := filepath.Join(tmpDir, "archive.txt")
	success := waitForCondition(t, 5*time.Second, func() bool {
		st, err := os.Stat(expectedOut)
		return err == nil && st.Size() > 0
	})
	if !success {
		t.Fatalf("tiempo de espera agotado esperando archivo descomprimido %s", expectedOut)
	}

	data, err := os.ReadFile(expectedOut)
	if err != nil {
		t.Fatalf("leyendo archivo descomprimido: %v", err)
	}
	if string(data) != "texto descomprimido con inotify" {
		t.Fatalf("contenido inesperado: got %q, want %q", string(data), "texto descomprimido con inotify")
	}

	cancel()
	wg.Wait()

	if watcherErr != nil {
		t.Fatalf("RunWatcher retornó error al cancelar contexto: %v", watcherErr)
	}
}

func TestWatcher_Polling_Fallback_Compress(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	var watcherErr error

	opts := WatcherOptions{
		Dir:          tmpDir,
		Mode:         WatchModeCompress,
		ForcePolling: true,
		CompressOpts: CompressOptions{
			Format:    Gz,
			OutputDir: tmpDir,
			KeepOrig:  true,
		},
		PollInterval: 40 * time.Millisecond,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		watcherErr = RunWatcher(ctx, opts)
	}()

	time.Sleep(50 * time.Millisecond)

	testFile := filepath.Join(tmpDir, "poll_sample.txt")
	if err := os.WriteFile(testFile, []byte("contenido de prueba polling"), 0644); err != nil {
		t.Fatalf("creando archivo de prueba: %v", err)
	}

	expectedOut := filepath.Join(tmpDir, "poll_sample.txt.gz")
	success := waitForCondition(t, 5*time.Second, func() bool {
		st, err := os.Stat(expectedOut)
		return err == nil && st.Size() > 0
	})
	if !success {
		t.Fatalf("tiempo de espera agotado esperando archivo comprimido por polling %s", expectedOut)
	}

	cancel()
	wg.Wait()

	if watcherErr != nil {
		t.Fatalf("RunWatcher retornó error con polling: %v", watcherErr)
	}
}

func TestWatcher_Polling_Fallback_Decompress(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	var watcherErr error

	opts := WatcherOptions{
		Dir:          tmpDir,
		Mode:         WatchModeDecompress,
		ForcePolling: true,
		DecompressOpts: DecompressOptions{
			OutputDir: tmpDir,
			KeepOrig:  true,
			Force:     true,
		},
		PollInterval: 40 * time.Millisecond,
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		watcherErr = RunWatcher(ctx, opts)
	}()

	time.Sleep(50 * time.Millisecond)

	archivePath := filepath.Join(tmpDir, "poll_archive.txt.gz")
	writeGzTestFile(t, archivePath, "texto descomprimido por polling")

	expectedOut := filepath.Join(tmpDir, "poll_archive.txt")
	success := waitForCondition(t, 5*time.Second, func() bool {
		st, err := os.Stat(expectedOut)
		return err == nil && st.Size() > 0
	})
	if !success {
		t.Fatalf("tiempo de espera agotado esperando archivo descomprimido por polling %s", expectedOut)
	}

	cancel()
	wg.Wait()

	if watcherErr != nil {
		t.Fatalf("RunWatcher retornó error con polling: %v", watcherErr)
	}
}

func TestWatcher_Decompress_IgnoresNonArchive(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	var watcherErr error
	var handledErr error
	handled := false
	var mu sync.Mutex

	opts := WatcherOptions{
		Dir:  tmpDir,
		Mode: WatchModeDecompress,
		DecompressOpts: DecompressOptions{
			OutputDir: tmpDir,
			KeepOrig:  true,
		},
		PollInterval: 40 * time.Millisecond,
		OnFileHandled: func(path string, err error) {
			mu.Lock()
			handled = true
			handledErr = err
			mu.Unlock()
		},
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		watcherErr = RunWatcher(ctx, opts)
	}()

	time.Sleep(50 * time.Millisecond)

	// Escribir un archivo no reconocible como archivo comprimido
	txtFile := filepath.Join(tmpDir, "plain_document.txt")
	if err := os.WriteFile(txtFile, []byte("no soy un archivo comprimido"), 0644); err != nil {
		t.Fatalf("creando archivo de texto: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	if handled {
		t.Errorf("archivo no comprimido no debió ser procesado por descompresor; handledErr = %v", handledErr)
	}
	mu.Unlock()

	cancel()
	wg.Wait()

	if watcherErr != nil {
		t.Fatalf("RunWatcher retornó error inesperado: %v", watcherErr)
	}
}

func TestWatcher_ContextCancelCleanExit(t *testing.T) {
	tmpDir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())

	opts := WatcherOptions{
		Dir:          tmpDir,
		Mode:         WatchModeCompress,
		PollInterval: 50 * time.Millisecond,
	}

	done := make(chan error, 1)
	go func() {
		done <- RunWatcher(ctx, opts)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWatcher retornó error tras cancelación: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunWatcher no terminó limpiamente en el tiempo esperado tras cancelar el contexto")
	}
}

func TestWatcher_InvalidDirOrMode(t *testing.T) {
	ctx := context.Background()

	// Directorio inexistente
	err := RunWatcher(ctx, WatcherOptions{
		Dir:  filepath.Join(t.TempDir(), "nonexistent_directory"),
		Mode: WatchModeCompress,
	})
	if err == nil {
		t.Error("RunWatcher debería fallar si el directorio no existe")
	}

	// Archivo en vez de directorio
	tmpFile := filepath.Join(t.TempDir(), "a_file.txt")
	os.WriteFile(tmpFile, []byte("abc"), 0644)
	err = RunWatcher(ctx, WatcherOptions{
		Dir:  tmpFile,
		Mode: WatchModeCompress,
	})
	if err == nil {
		t.Error("RunWatcher debería fallar si la ruta no es un directorio")
	}

	// Modo inválido
	err = RunWatcher(ctx, WatcherOptions{
		Dir:  t.TempDir(),
		Mode: WatchModeNone,
	})
	if err == nil {
		t.Error("RunWatcher debería fallar con WatchModeNone")
	}
}

func TestWatcher_CLI_Flags(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{
			name:       "watch without -c or -d",
			args:       []string{"-watch", t.TempDir()},
			wantStderr: "-watch requiere -c",
		},
		{
			name:       "watch nonexistent directory",
			args:       []string{"-watch", filepath.Join(t.TempDir(), "nonexistent_dir"), "-c", "-f", "gz"},
			wantStderr: "directorio de observación no válido",
		},
		{
			name:       "watch -c without -f",
			args:       []string{"-watch", t.TempDir(), "-c"},
			wantStderr: "debe especificar formato con -f",
		},
		{
			name:       "watch conflict with bench",
			args:       []string{"-watch", t.TempDir(), "--bench"},
			wantStderr: "--bench no se puede combinar con -watch",
		},
		{
			name:       "watch conflict with install",
			args:       []string{"-watch", t.TempDir(), "--install"},
			wantStderr: "no puede combinarse con",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmdArgs := append([]string{"run", "."}, tt.args...)
			cmd := exec.Command("go", cmdArgs...)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("se esperaba que el comando fallara con error de validación, pero tuvo éxito; salida: %s", string(out))
			}
			if !strings.Contains(string(out), tt.wantStderr) {
				t.Errorf("salida = %q, no contiene %q", string(out), tt.wantStderr)
			}
		})
	}
}

func TestTTLSet_ExpirationAndDefaults(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		ttl          time.Duration
		maxCap       int
		advance      time.Duration
		wantContains bool
		wantLen      int
	}{
		{
			name:         "present before expiry",
			ttl:          10 * time.Second,
			maxCap:       16,
			advance:      9 * time.Second,
			wantContains: true,
			wantLen:      1,
		},
		{
			name:         "expired at exact boundary",
			ttl:          10 * time.Second,
			maxCap:       16,
			advance:      10 * time.Second,
			wantContains: false,
			wantLen:      0,
		},
		{
			name:         "expired after boundary",
			ttl:          10 * time.Second,
			maxCap:       16,
			advance:      25 * time.Second,
			wantContains: false,
			wantLen:      0,
		},
		{
			name:         "non-positive defaults apply",
			ttl:          0,
			maxCap:       0,
			advance:      defaultIgnoredTTL - time.Second,
			wantContains: true,
			wantLen:      1,
		},
		{
			name:         "non-positive defaults expire at default TTL",
			ttl:          -5 * time.Second,
			maxCap:       -1,
			advance:      defaultIgnoredTTL,
			wantContains: false,
			wantLen:      0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := baseTime
			s := newTTLSet(tc.ttl, tc.maxCap)
			s.now = func() time.Time { return now }

			if s.Contains("missing.gz") {
				t.Fatalf("empty set should not contain missing.gz")
			}

			s.Add("output.tar.gz")
			now = now.Add(tc.advance)

			if got := s.Contains("output.tar.gz"); got != tc.wantContains {
				t.Errorf("Contains = %v, want %v", got, tc.wantContains)
			}
			if got := s.Len(); got != tc.wantLen {
				t.Errorf("Len = %d, want %d", got, tc.wantLen)
			}
		})
	}
}

func TestTTLSet_MaxCapEviction(t *testing.T) {
	baseTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		maxCap     int
		inserts    int
		wantKept   []string
		wantEvict  []string
	}{
		{
			name:      "cap 1 keeps only newest",
			maxCap:    1,
			inserts:   4,
			wantKept:  []string{"f3.gz"},
			wantEvict: []string{"f0.gz", "f1.gz", "f2.gz"},
		},
		{
			name:      "cap 3 evicts oldest entries",
			maxCap:    3,
			inserts:   6,
			wantKept:  []string{"f3.gz", "f4.gz", "f5.gz"},
			wantEvict: []string{"f0.gz", "f1.gz", "f2.gz"},
		},
		{
			name:      "cap 10 bounds burst",
			maxCap:    10,
			inserts:   25,
			wantKept:  []string{"f15.gz", "f20.gz", "f24.gz"},
			wantEvict: []string{"f0.gz", "f14.gz"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := baseTime
			s := newTTLSet(60*time.Second, tc.maxCap)
			s.now = func() time.Time { return now }

			for i := 0; i < tc.inserts; i++ {
				s.Add(fmt.Sprintf("f%d.gz", i))
				now = now.Add(time.Second)
				if s.Len() > tc.maxCap {
					t.Fatalf("Len = %d exceeded maxCap %d at insert %d", s.Len(), tc.maxCap, i)
				}
			}

			for _, k := range tc.wantKept {
				if !s.Contains(k) {
					t.Errorf("expected %q to remain in set", k)
				}
			}
			for _, k := range tc.wantEvict {
				if s.Contains(k) {
					t.Errorf("expected %q to be evicted", k)
				}
			}
		})
	}

	t.Run("refresh existing entry updates eviction order", func(t *testing.T) {
		now := baseTime
		s := newTTLSet(60*time.Second, 2)
		s.now = func() time.Time { return now }

		s.Add("a.gz")
		now = now.Add(time.Second)
		s.Add("b.gz")
		now = now.Add(time.Second)
		s.Add("a.gz")
		now = now.Add(time.Second)
		s.Add("c.gz")

		if s.Len() != 2 {
			t.Fatalf("Len = %d, want 2", s.Len())
		}
		if s.Contains("b.gz") {
			t.Errorf("expected b.gz to be evicted after a.gz was refreshed")
		}
		if !s.Contains("a.gz") || !s.Contains("c.gz") {
			t.Errorf("expected a.gz and c.gz to remain")
		}
	})

	t.Run("expired entries pruned before evicting live entries", func(t *testing.T) {
		now := baseTime
		s := newTTLSet(10*time.Second, 2)
		s.now = func() time.Time { return now }

		s.Add("old.gz")
		now = now.Add(5 * time.Second)
		s.Add("live.gz")
		now = now.Add(6 * time.Second)
		s.Add("new.gz")

		if s.Len() != 2 {
			t.Fatalf("Len = %d, want 2", s.Len())
		}
		if s.Contains("old.gz") {
			t.Errorf("expected expired old.gz to be pruned")
		}
		if !s.Contains("live.gz") || !s.Contains("new.gz") {
			t.Errorf("expected live.gz and new.gz to remain")
		}
	})
}

func TestTTLSet_Concurrency(t *testing.T) {
	const maxCap = 32
	s := newTTLSet(20*time.Millisecond, maxCap)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				key := fmt.Sprintf("w%d_f%d.gz", id, i)
				s.Add(key)
				_ = s.Contains(key)
				if n := s.Len(); n > maxCap {
					t.Errorf("Len = %d exceeded maxCap %d", n, maxCap)
				}
			}
		}(g)
	}
	wg.Wait()

	if n := s.Len(); n > maxCap {
		t.Fatalf("final Len = %d exceeded maxCap %d", n, maxCap)
	}
}
