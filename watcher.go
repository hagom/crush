package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type WatcherMode int

const (
	WatchModeNone WatcherMode = iota
	WatchModeCompress
	WatchModeDecompress
)

const (
	defaultIgnoredTTL = 60 * time.Second
	defaultIgnoredCap = 1024
)

type ttlSet struct {
	mu      sync.Mutex
	entries map[string]time.Time
	ttl     time.Duration
	maxCap  int
	now     func() time.Time
}

func newTTLSet(ttl time.Duration, maxCap int) *ttlSet {
	if ttl <= 0 {
		ttl = defaultIgnoredTTL
	}
	if maxCap <= 0 {
		maxCap = defaultIgnoredCap
	}
	return &ttlSet{
		entries: make(map[string]time.Time),
		ttl:     ttl,
		maxCap:  maxCap,
		now:     time.Now,
	}
}

func (s *ttlSet) pruneLocked(now time.Time) {
	for k, exp := range s.entries {
		if !now.Before(exp) {
			delete(s.entries, k)
		}
	}
}

func (s *ttlSet) Add(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.pruneLocked(now)
	if _, exists := s.entries[key]; !exists {
		for len(s.entries) >= s.maxCap {
			var oldestKey string
			var oldestExp time.Time
			first := true
			for k, exp := range s.entries {
				if first || exp.Before(oldestExp) || (exp.Equal(oldestExp) && k < oldestKey) {
					oldestKey = k
					oldestExp = exp
					first = false
				}
			}
			delete(s.entries, oldestKey)
		}
	}
	s.entries[key] = now.Add(s.ttl)
}

func (s *ttlSet) Contains(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.pruneLocked(now)
	_, ok := s.entries[key]
	return ok
}

func (s *ttlSet) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

type WatcherOptions struct {
	Dir            string
	Mode           WatcherMode
	CompressOpts   CompressOptions
	DecompressOpts DecompressOptions
	PollInterval   time.Duration
	ForcePolling   bool
	OnFileHandled  func(path string, err error)
}

func RunWatcher(ctx context.Context, opts WatcherOptions) error {
	fi, err := os.Stat(opts.Dir)
	if err != nil {
		return fmt.Errorf("directorio a observar no existe: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s no es un directorio", opts.Dir)
	}
	if opts.Mode != WatchModeCompress && opts.Mode != WatchModeDecompress {
		return fmt.Errorf("modo de observador inválido: requiere compresión o descompresión")
	}

	if opts.PollInterval <= 0 {
		opts.PollInterval = 1 * time.Second
	}

	ignoredOutputs := newTTLSet(defaultIgnoredTTL, defaultIgnoredCap)
	var inProgress sync.Map
	cleanWatchDir := filepath.Clean(opts.Dir)

	processFn := func(fullPath string) error {
		cleanFull := filepath.Clean(fullPath)
		if filepath.Dir(cleanFull) != cleanWatchDir {
			return nil
		}
		base := filepath.Base(cleanFull)
		if base == "" || base == "." || base == ".." || strings.HasPrefix(base, ".") {
			return nil
		}
		if strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".crush_tmp") || strings.HasSuffix(base, ".sha256") || strings.Contains(base, ".part") {
			return nil
		}
		if ignoredOutputs.Contains(base) {
			return nil
		}
		if _, loaded := inProgress.LoadOrStore(cleanFull, true); loaded {
			return nil
		}
		defer inProgress.Delete(cleanFull)

		st, err := os.Lstat(cleanFull)
		if err != nil {
			return nil
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		switch opts.Mode {
		case WatchModeCompress:
			if !st.Mode().IsRegular() && !st.IsDir() {
				return nil
			}
			cOpts := opts.CompressOpts
			if cOpts.OutputDir == "" {
				cOpts.OutputDir = opts.Dir
			}
			WriteInfo("[Watcher] Comprimiendo archivo detectado: %s", cleanFull)
			outPaths, err := DoCompress([]string{cleanFull}, cOpts)
			if err != nil {
				WriteError("[Watcher] Error comprimiendo %s: %v", cleanFull, err)
				if opts.OnFileHandled != nil {
					opts.OnFileHandled(cleanFull, err)
				}
				return err
			}
			for _, out := range outPaths {
				outBase := filepath.Base(out)
				ignoredOutputs.Add(outBase)
				ignoredOutputs.Add(outBase + ".sha256")
				for _, part := range globSplitParts(out) {
					partBase := filepath.Base(part)
					ignoredOutputs.Add(partBase)
					ignoredOutputs.Add(partBase + ".sha256")
				}
			}
			WriteSuccess("[Watcher] Compresión finalizada: %s", cleanFull)
			if opts.OnFileHandled != nil {
				opts.OnFileHandled(cleanFull, nil)
			}
			return nil

		case WatchModeDecompress:
			if !st.Mode().IsRegular() {
				return nil
			}
			if _, err := DetectFormat(cleanFull); err != nil {
				return nil
			}
			dOpts := opts.DecompressOpts
			if dOpts.OutputDir == "" {
				dOpts.OutputDir = opts.Dir
			}
			WriteInfo("[Watcher] Descomprimiendo archivo detectado: %s", cleanFull)
			err := DoDecompress([]string{cleanFull}, dOpts)
			if err != nil {
				WriteError("[Watcher] Error descomprimiendo %s: %v", cleanFull, err)
				if opts.OnFileHandled != nil {
					opts.OnFileHandled(cleanFull, err)
				}
				return err
			}
			WriteSuccess("[Watcher] Descompresión finalizada: %s", cleanFull)
			if opts.OnFileHandled != nil {
				opts.OnFileHandled(cleanFull, nil)
			}
			return nil

		default:
			return nil
		}
	}

	return runPlatformWatcher(ctx, opts, processFn)
}

func runPollingWatcher(ctx context.Context, opts WatcherOptions, processFn func(string) error) error {
	interval := opts.PollInterval
	if interval <= 0 {
		interval = 1 * time.Second
	}

	knownFiles := make(map[string]bool)
	entries, err := os.ReadDir(opts.Dir)
	if err == nil {
		for _, e := range entries {
			knownFiles[e.Name()] = true
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			entries, err := os.ReadDir(opts.Dir)
			if err != nil {
				continue
			}
			currentFiles := make(map[string]bool, len(entries))
			for _, e := range entries {
				name := e.Name()
				currentFiles[name] = true
				if !knownFiles[name] {
					knownFiles[name] = true
					fullPath := filepath.Join(opts.Dir, name)
					_ = processFn(fullPath)
				}
			}
			for name := range knownFiles {
				if !currentFiles[name] {
					delete(knownFiles, name)
				}
			}
		}
	}
}
