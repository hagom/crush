//go:build linux

package main

import (
	"io"
	"syscall"
)

const (
	cmdSetPipeSz = 1031
	cmdGetPipeSz = 1032
)

func extractFd(v any) (uintptr, bool) {
	if v == nil {
		return 0, false
	}
	if f, ok := v.(interface{ Fd() uintptr }); ok {
		fd := f.Fd()
		if fd != ^uintptr(0) {
			return fd, true
		}
	}
	return 0, false
}

func setPipeCapacityOS(r io.Reader, w io.Writer, size int) {
	var fd uintptr
	var ok bool
	if fd, ok = extractFd(w); !ok {
		if fd, ok = extractFd(r); !ok {
			return
		}
	}
	_, _, _ = syscall.Syscall(syscall.SYS_FCNTL, fd, uintptr(cmdSetPipeSz), uintptr(size))
}

func getPipeCapacityOS(r io.Reader, w io.Writer) int {
	var fd uintptr
	var ok bool
	if fd, ok = extractFd(w); !ok {
		if fd, ok = extractFd(r); !ok {
			return -1
		}
	}
	res, _, err := syscall.Syscall(syscall.SYS_FCNTL, fd, uintptr(cmdGetPipeSz), 0)
	if err != 0 {
		return -1
	}
	return int(res)
}
