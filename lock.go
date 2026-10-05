package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

var (
	lockFilePath     = "/tmp/crush.lock"
	lockPollInterval = 250 * time.Millisecond
	lockOutputMu     sync.Mutex
	lockOutput       io.Writer = os.Stderr
)

func setLockOutput(w io.Writer) func() {
	lockOutputMu.Lock()
	old := lockOutput
	lockOutput = w
	lockOutputMu.Unlock()
	return func() {
		lockOutputMu.Lock()
		lockOutput = old
		lockOutputMu.Unlock()
	}
}

func getLockOutput() io.Writer {
	lockOutputMu.Lock()
	defer lockOutputMu.Unlock()
	return lockOutput
}

func printLockWaiting(w io.Writer, pid int) {
	if w == nil {
		return
	}
	if pid > 0 {
		fmt.Fprintf(w, "%s⏳ Otra instancia de crush (PID %d) está en ejecución. Esperando a que termine para continuar...%s\n", Yellow, pid, NC)
	} else {
		fmt.Fprintf(w, "%s⏳ Otra instancia de crush está en ejecución. Esperando a que termine para continuar...%s\n", Yellow, NC)
	}
}

func printLockAcquired(w io.Writer) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, "%s✓ Bloqueo adquirido. Continuando...%s\n", Green, NC)
}

type LockError struct {
	Path string
	PID  int
}

func (e *LockError) Error() string {
	if e.PID > 0 {
		return fmt.Sprintf("otra instancia de crush ya está en ejecución (PID %d); espere a que termine o ciérrela (bloqueo: %s)", e.PID, e.Path)
	}
	return fmt.Sprintf("otra instancia de crush ya está en ejecución; espere a que termine (bloqueo: %s)", e.Path)
}

type lockScope struct {
	Compress    bool
	Decompress  bool
	Add         bool
	Test        bool
	Bench       bool
	Watch       bool
	List        bool
	Read        bool
	Tree        bool
	Find        bool
	Diff        bool
	Convert     bool
	StdinStream bool
}

func (s lockScope) NeedsLock() bool {
	if s.StdinStream {
		return false
	}
	return s.Compress || s.Decompress || s.Add || s.Test || s.Bench || s.Watch || s.Convert
}
