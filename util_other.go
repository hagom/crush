//go:build !linux

package main

import (
	"errors"
	"io"
	"os"
)

func isOwnedByCurrentUserOS(fi os.FileInfo) bool {
	return fi != nil
}

func setPipeCapacityOS(r io.Reader, w io.Writer, size int) {
}

func getPipeCapacityOS(r io.Reader, w io.Writer) int {
	return -1
}

func disableTerminalEchoOS(fd uintptr) (func(), error) {
	return nil, errors.New("terminal raw mode not supported on this OS")
}

func splicePipe(r io.Reader, w io.Writer) (int64, error) {
	return copyWithSplicePool(w, r)
}
