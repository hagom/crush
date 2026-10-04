package main

import "fmt"

var lockFilePath = "/tmp/crush.lock"

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
	StdinStream bool
}

func (s lockScope) NeedsLock() bool {
	if s.StdinStream {
		return false
	}
	return s.Compress || s.Decompress || s.Add || s.Test || s.Bench || s.Watch
}
