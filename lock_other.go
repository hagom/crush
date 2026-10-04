//go:build !unix

package main

func AcquireLock() (func(), error) {
	return func() {}, nil
}
