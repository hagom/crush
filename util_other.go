//go:build !linux

package main

import "io"

func setPipeCapacityOS(r io.Reader, w io.Writer, size int) {
}

func getPipeCapacityOS(r io.Reader, w io.Writer) int {
	return -1
}
