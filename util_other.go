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

