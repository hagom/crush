//go:build linux

package main

import (
	"io"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

func isOwnedByCurrentUserOS(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == os.Geteuid()
}

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

func ioctlTermios(fd, req uintptr, termios *syscall.Termios) syscall.Errno {
	for {
		_, _, errNo := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(unsafe.Pointer(termios)))
		if errNo == syscall.EINTR {
			continue
		}
		return errNo
	}
}

func disableTerminalEchoOS(fd uintptr) (func(), error) {
	if fd == ^uintptr(0) {
		return nil, syscall.EBADF
	}
	var termios syscall.Termios
	if errNo := ioctlTermios(fd, uintptr(syscall.TCGETS), &termios); errNo != 0 {
		return nil, errNo
	}
	newTermios := termios
	newTermios.Lflag &^= syscall.ECHO
	if errNo := ioctlTermios(fd, uintptr(syscall.TCSETS), &newTermios); errNo != 0 {
		return nil, errNo
	}

	sigCh := make(chan os.Signal, 1)
	doneCh := make(chan struct{})
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

	var once sync.Once
	restore := func() {
		once.Do(func() {
			signal.Stop(sigCh)
			close(doneCh)
			_ = ioctlTermios(fd, uintptr(syscall.TCSETS), &termios)
		})
	}

	go func() {
		select {
		case sig := <-sigCh:
			restore()
			_, _ = os.Stderr.WriteString("\n")
			signal.Reset(sig)
			if sysSig, ok := sig.(syscall.Signal); ok {
				_ = syscall.Kill(os.Getpid(), sysSig)
			}
		case <-doneCh:
		}
	}()

	return restore, nil
}

func waitFdReady(epfd *int, fd uintptr, events uint32) (bool, error) {
	if *epfd < 0 {
		created, err := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
		if err != nil {
			return false, err
		}
		ev := syscall.EpollEvent{Events: events, Fd: int32(fd)}
		if ctlErr := syscall.EpollCtl(created, syscall.EPOLL_CTL_ADD, int(fd), &ev); ctlErr != nil {
			_ = syscall.Close(created)
			if ctlErr == syscall.EPERM {
				return false, nil
			}
			return false, ctlErr
		}
		*epfd = created
	}
	var evs [1]syscall.EpollEvent
	for {
		n, err := syscall.EpollWait(*epfd, evs[:], -1)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return true, err
		}
		if n > 0 {
			return true, nil
		}
	}
}

func splicePipe(r io.Reader, w io.Writer) (int64, error) {
	rfd, okR := extractFd(r)
	wfd, okW := extractFd(w)
	if !okR || !okW {
		return copyWithSplicePool(w, r)
	}

	epfdR := -1
	epfdW := -1
	defer func() {
		if epfdR >= 0 {
			_ = syscall.Close(epfdR)
		}
		if epfdW >= 0 {
			_ = syscall.Close(epfdW)
		}
		runtime.KeepAlive(r)
		runtime.KeepAlive(w)
	}()

	var total int64
	chunkSize := uintptr(1024 * 1024)
	for {
		n, _, errno := syscall.Syscall6(syscall.SYS_SPLICE, rfd, 0, wfd, 0, chunkSize, 0)
		if errno != 0 {
			if errno == syscall.EINTR {
				continue
			}
			if errno == syscall.EAGAIN {
				supR, errR := waitFdReady(&epfdR, rfd, syscall.EPOLLIN|syscall.EPOLLHUP|syscall.EPOLLERR)
				if errR != nil {
					rem, err := copyWithSplicePool(w, r)
					return total + rem, err
				}
				supW, errW := waitFdReady(&epfdW, wfd, syscall.EPOLLOUT|syscall.EPOLLHUP|syscall.EPOLLERR)
				if errW != nil || (!supR && !supW) {
					rem, err := copyWithSplicePool(w, r)
					return total + rem, err
				}
				continue
			}
			if errno == syscall.ENOSYS || errno == syscall.EINVAL || errno == syscall.EOPNOTSUPP {
				rem, err := copyWithSplicePool(w, r)
				return total + rem, err
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
