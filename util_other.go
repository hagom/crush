//go:build !linux

package main

import (
	"errors"
	"io"
)

func setPipeCapacityOS(r io.Reader, w io.Writer, size int) {
}

func getPipeCapacityOS(r io.Reader, w io.Writer) int {
	return -1
}

func disableTerminalEchoOS(fd uintptr) (func(), error) {
	return nil, errors.New("terminal raw mode not supported on this OS")
}

func splicePipe(r io.Reader, w io.Writer) (int64, error) {
	buf := make([]byte, 1024*1024)
	return io.CopyBuffer(w, r, buf)
}
