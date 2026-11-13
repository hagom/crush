//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"syscall"
)

func runPlatformWatcher(ctx context.Context, opts WatcherOptions, processFn func(string) error) error {
	if opts.ForcePolling {
		return runPollingWatcher(ctx, opts, processFn)
	}
	err := runInotifyWatcher(ctx, opts, processFn)
	if err != nil {
		WriteWarning("fallo inicializando inotify (%v); usando observador por sondeo (polling)", err)
		return runPollingWatcher(ctx, opts, processFn)
	}
	return nil
}

func runInotifyWatcher(ctx context.Context, opts WatcherOptions, processFn func(string) error) error {
	fd, err := syscall.InotifyInit()
	if err != nil {
		return fmt.Errorf("inotify_init: %w", err)
	}
	defer syscall.Close(fd)

	wd, err := syscall.InotifyAddWatch(fd, opts.Dir, syscall.IN_CLOSE_WRITE|syscall.IN_MOVED_TO)
	if err != nil {
		return fmt.Errorf("inotify_add_watch: %w", err)
	}
	defer syscall.InotifyRmWatch(fd, uint32(wd))

	epfd, err := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
	if err != nil {
		return fmt.Errorf("epoll_create1: %w", err)
	}
	defer syscall.Close(epfd)

	ev := syscall.EpollEvent{Events: syscall.EPOLLIN, Fd: int32(fd)}
	if err := syscall.EpollCtl(epfd, syscall.EPOLL_CTL_ADD, fd, &ev); err != nil {
		return fmt.Errorf("epoll_ctl: %w", err)
	}

	var buf [4096]byte
	var events [1]syscall.EpollEvent
	for {
		if ctx.Err() != nil {
			return nil
		}

		ready, err := syscall.EpollWait(epfd, events[:], 100)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("epoll_wait: %w", err)
		}

		if ready <= 0 {
			continue
		}

		n, err := syscall.Read(fd, buf[:])
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("inotify read: %w", err)
		}
		if n <= 0 {
			continue
		}

		offset := 0
		for offset+syscall.SizeofInotifyEvent <= n {
			mask := binary.LittleEndian.Uint32(buf[offset+4 : offset+8])
			nameLen := binary.LittleEndian.Uint32(buf[offset+12 : offset+16])
			offset += syscall.SizeofInotifyEvent

			var name string
			if nameLen > 0 && offset+int(nameLen) <= n {
				nameBytes := buf[offset : offset+int(nameLen)]
				if idx := bytes.IndexByte(nameBytes, 0); idx != -1 {
					name = string(nameBytes[:idx])
				} else {
					name = string(nameBytes)
				}
				offset += int(nameLen)
			}

			if name == "" || name == "." || name == ".." {
				continue
			}

			if mask&(syscall.IN_CLOSE_WRITE|syscall.IN_MOVED_TO) != 0 {
				fullPath := filepath.Join(opts.Dir, name)
				_ = processFn(fullPath)
			}
		}
	}
}
