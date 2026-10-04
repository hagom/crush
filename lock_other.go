//go:build !unix

package main

import "context"

func TryAcquireLock() (func(), error) {
	return func() {}, nil
}

func AcquireLockContext(ctx context.Context) (func(), error) {
	return func() {}, nil
}

func AcquireLock() (func(), error) {
	return func() {}, nil
}

