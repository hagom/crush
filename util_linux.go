//go:build linux

package main

import (
	"io"
	"syscall"
	"unsafe"
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

func disableTerminalEchoOS(fd uintptr) (func(), error) {
	var termios syscall.Termios
	_, _, errNo := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&termios)))
	if errNo != 0 {
		return nil, errNo
	}
	newTermios := termios
	newTermios.Lflag &^= syscall.ECHO
	_, _, errNo = syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&newTermios)))
	if errNo != 0 {
		return nil, errNo
	}
	restore := func() {
		_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&termios)))
	}
	return restore, nil
}

func splicePipe(r io.Reader, w io.Writer) (int64, error) {
	rfd, okR := extractFd(r)
	wfd, okW := extractFd(w)
	if !okR || !okW {
		buf := make([]byte, 1024*1024)
		return io.CopyBuffer(w, r, buf)
	}

	var total int64
	chunkSize := uintptr(1024 * 1024)
	for {
		n, _, errno := syscall.Syscall6(syscall.SYS_SPLICE, rfd, 0, wfd, 0, chunkSize, 0)
		if errno != 0 {
			if errno == syscall.EINTR {
				continue
			}
			if total == 0 && (errno == syscall.ENOSYS || errno == syscall.EINVAL) {
				buf := make([]byte, 1024*1024)
				return io.CopyBuffer(w, r, buf)
			}
			return total, errno
		}
		if n == 0 {
			break
		}
		total += int64(n)
	}
	return total, nil
}
