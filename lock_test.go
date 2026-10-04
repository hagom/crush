package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func useTempLockPath(t *testing.T) string {
	t.Helper()
	old := lockFilePath
	p := filepath.Join(t.TempDir(), "crush.lock")
	lockFilePath = p
	t.Cleanup(func() { lockFilePath = old })
	return p
}

func TestTryAcquireLockSecondInstanceFails(t *testing.T) {
	useTempLockPath(t)

	release, err := TryAcquireLock()
	if err != nil {
		t.Fatalf("primer TryAcquireLock() error = %v", err)
	}
	defer release()

	_, err = TryAcquireLock()
	if err == nil {
		t.Fatal("segundo TryAcquireLock() debería fallar inmediatamente mientras el primero está activo")
	}
	var le *LockError
	if !errors.As(err, &le) {
		t.Fatalf("error = %T (%v), want *LockError", err, err)
	}
	if le.PID != os.Getpid() {
		t.Errorf("LockError.PID = %d, want %d", le.PID, os.Getpid())
	}
	if !strings.Contains(err.Error(), strconv.Itoa(os.Getpid())) {
		t.Errorf("el mensaje %q debería incluir el PID del titular", err.Error())
	}
}

func TestAcquireLockQueuesAndProceeds(t *testing.T) {
	useTempLockPath(t)

	// Short poll interval for fast test
	oldInterval := lockPollInterval
	lockPollInterval = 20 * time.Millisecond
	defer func() { lockPollInterval = oldInterval }()

	var buf safeBuffer
	restore := setLockOutput(&buf)
	defer restore()

	release1, err := TryAcquireLock()
	if err != nil {
		t.Fatalf("primer TryAcquireLock() error = %v", err)
	}

	acquiredCh := make(chan func(), 1)
	errCh := make(chan error, 1)

	go func() {
		rel, err := AcquireLock()
		if err != nil {
			errCh <- err
			return
		}
		acquiredCh <- rel
	}()

	// Wait enough time for AcquireLock to attempt, detect busy lock, and print waiting message
	time.Sleep(100 * time.Millisecond)

	select {
	case <-acquiredCh:
		t.Fatal("AcquireLock no debió adquirir el bloqueo mientras release1 estaba activo")
	case err := <-errCh:
		t.Fatalf("AcquireLock devolvió error inesperado mientras esperaba: %v", err)
	default:
		// Sigue esperando como debe ser
	}

	outStr := buf.String()
	if !strings.Contains(outStr, "⏳ Otra instancia de crush") {
		t.Errorf("la salida %q debería indicar espera con '⏳ Otra instancia de crush'", outStr)
	}
	if !strings.Contains(outStr, strconv.Itoa(os.Getpid())) {
		t.Errorf("la salida %q debería incluir el PID del titular (%d)", outStr, os.Getpid())
	}

	// Now release lock 1. Instance 2 should acquire it and finish.
	release1()

	select {
	case release2 := <-acquiredCh:
		defer release2()
	case err := <-errCh:
		t.Fatalf("AcquireLock falló tras liberación: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("AcquireLock tardó demasiado en adquirir el bloqueo tras liberación")
	}

	outFinal := buf.String()
	if !strings.Contains(outFinal, "✓ Bloqueo adquirido. Continuando...") {
		t.Errorf("la salida %q debería confirmar la adquisición del bloqueo", outFinal)
	}
}

func TestAcquireLockContextTimeout(t *testing.T) {
	useTempLockPath(t)

	oldInterval := lockPollInterval
	lockPollInterval = 20 * time.Millisecond
	defer func() { lockPollInterval = oldInterval }()

	var buf safeBuffer
	restore := setLockOutput(&buf)
	defer restore()

	release, err := TryAcquireLock()
	if err != nil {
		t.Fatalf("TryAcquireLock() error = %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	_, err = AcquireLockContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("esperado context.DeadlineExceeded, obtenido: %v", err)
	}
}

func TestAcquireLockReleaseAllowsReacquire(t *testing.T) {
	useTempLockPath(t)

	release, err := AcquireLock()
	if err != nil {
		t.Fatalf("AcquireLock() error = %v", err)
	}
	release()
	release()

	release2, err := AcquireLock()
	if err != nil {
		t.Fatalf("AcquireLock() tras release debería funcionar: %v", err)
	}
	release2()
}

func TestAcquireLockIgnoresStaleFile(t *testing.T) {
	p := useTempLockPath(t)
	if err := os.WriteFile(p, []byte("999999\n"), 0o666); err != nil {
		t.Fatal(err)
	}

	release, err := AcquireLock()
	if err != nil {
		t.Fatalf("un archivo de lock huérfano (sin flock activo) no debe bloquear: %v", err)
	}
	release()
}

func TestAcquireLockUnwritablePath(t *testing.T) {
	old := lockFilePath
	lockFilePath = filepath.Join(t.TempDir(), "no-existe", "crush.lock")
	t.Cleanup(func() { lockFilePath = old })

	_, err := AcquireLock()
	if err == nil {
		t.Fatal("AcquireLock() debería devolver error si no puede crear el archivo")
	}
	var le *LockError
	if errors.As(err, &le) {
		t.Errorf("un fallo de E/S no debe reportarse como LockError, got %v", err)
	}
}

func TestAcquireLockAcrossProcessesAndCrash(t *testing.T) {
	p := useTempLockPath(t)

	cmd := exec.Command(os.Args[0], "-test.run=TestLockHolderProcess")
	cmd.Env = append(os.Environ(), "CRUSH_LOCK_HOLDER_PATH="+p)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
	}()

	sc := bufio.NewScanner(stdout)
	ready := false
	for sc.Scan() {
		if sc.Text() == "LOCK-READY" {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("el proceso hijo no adquirió el lock")
	}

	_, err = TryAcquireLock()
	var le *LockError
	if !errors.As(err, &le) {
		t.Fatalf("con otro proceso titular se esperaba *LockError, got %v", err)
	}
	if le.PID != cmd.Process.Pid {
		t.Errorf("LockError.PID = %d, want PID del hijo %d", le.PID, cmd.Process.Pid)
	}

	cmd.Process.Kill()
	cmd.Wait()

	release, err := AcquireLock()
	if err != nil {
		t.Fatalf("tras morir el titular (kill -9) el lock debe liberarse solo: %v", err)
	}
	release()
}

func TestAcquireLockCrossProcessQueue(t *testing.T) {
	p := useTempLockPath(t)

	oldInterval := lockPollInterval
	lockPollInterval = 25 * time.Millisecond
	defer func() { lockPollInterval = oldInterval }()

	cmd := exec.Command(os.Args[0], "-test.run=TestLockHolderProcess")
	cmd.Env = append(os.Environ(), "CRUSH_LOCK_HOLDER_PATH="+p)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	sc := bufio.NewScanner(stdout)
	ready := false
	for sc.Scan() {
		if sc.Text() == "LOCK-READY" {
			ready = true
			break
		}
	}
	if !ready {
		stdin.Close()
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal("el proceso hijo no adquirió el lock")
	}

	go func() {
		time.Sleep(150 * time.Millisecond)
		stdin.Close()
		cmd.Wait()
	}()

	release, err := AcquireLock()
	if err != nil {
		t.Fatalf("AcquireLock() en cola con proceso falló: %v", err)
	}
	release()
}

func TestLockHolderProcess(t *testing.T) {
	p := os.Getenv("CRUSH_LOCK_HOLDER_PATH")
	if p == "" {
		t.Skip("solo se ejecuta como proceso auxiliar")
	}
	lockFilePath = p
	if _, err := AcquireLock(); err != nil {
		os.Stdout.WriteString("LOCK-FAILED " + err.Error() + "\n")
		os.Exit(2)
	}
	os.Stdout.WriteString("LOCK-READY\n")
	buf := make([]byte, 1)
	os.Stdin.Read(buf)
	os.Exit(0)
}

func TestLockScopeNeedsLock(t *testing.T) {
	tests := []struct {
		name  string
		scope lockScope
		want  bool
	}{
		{"compress", lockScope{Compress: true}, true},
		{"decompress", lockScope{Decompress: true}, true},
		{"add", lockScope{Add: true}, true},
		{"test", lockScope{Test: true}, true},
		{"bench", lockScope{Bench: true}, true},
		{"watch", lockScope{Watch: true, Compress: true}, true},
		{"compress+test", lockScope{Compress: true, Test: true}, true},
		{"list no bloquea", lockScope{List: true}, false},
		{"read no bloquea", lockScope{Read: true}, false},
		{"compress stdin stream no bloquea", lockScope{Compress: true, StdinStream: true}, false},
		{"decompress stdin stream no bloquea", lockScope{Decompress: true, StdinStream: true}, false},
		{"sin modo", lockScope{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.scope.NeedsLock(); got != tt.want {
				t.Errorf("NeedsLock() = %v, want %v", got, tt.want)
			}
		})
	}
}
