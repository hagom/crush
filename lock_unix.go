//go:build unix

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

func openLockFile(path string) (*os.File, error) {
	for attempt := 0; attempt < 3; attempt++ {
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err == nil {
			return f, nil
		}
		switch {
		case errors.Is(err, fs.ErrPermission):
			return os.OpenFile(path, os.O_RDONLY, 0)
		case errors.Is(err, fs.ErrNotExist):
			f, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
			if err == nil {
				f.Chmod(0o666)
				return f, nil
			}
			if !errors.Is(err, fs.ErrExist) {
				return nil, err
			}
		default:
			return nil, err
		}
	}
	return nil, fmt.Errorf("no se pudo abrir %s tras varios intentos", path)
}

func readLockPID(f *os.File) int {
	buf := make([]byte, 32)
	n, _ := f.ReadAt(buf, 0)
	pid, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

func AcquireLock() (func(), error) {
	f, err := openLockFile(lockFilePath)
	if err != nil {
		return nil, fmt.Errorf("abriendo archivo de bloqueo %s: %w", lockFilePath, err)
	}
	fd := int(f.Fd())
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		pid := readLockPID(f)
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, &LockError{Path: lockFilePath, PID: pid}
		}
		return nil, fmt.Errorf("bloqueando %s: %w", lockFilePath, err)
	}
	if err := f.Truncate(0); err == nil {
		f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			syscall.Flock(fd, syscall.LOCK_UN)
			f.Close()
		})
	}, nil
}
