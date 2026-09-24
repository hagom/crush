//go:build !linux

package main

import "context"

func runPlatformWatcher(ctx context.Context, opts WatcherOptions, processFn func(string) error) error {
	return runPollingWatcher(ctx, opts, processFn)
}
