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

	processFn := func(fullPath string) error {
		base := filepath.Base(fullPath)
		if strings.HasPrefix(base, ".") {
			return nil
		}
		if strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".crush_tmp") || strings.Contains(base, ".part") {
			return nil
		}
		if ignoredOutputs.Contains(base) {
			return nil
		}
		if _, loaded := inProgress.LoadOrStore(fullPath, true); loaded {
			return nil
		}
		defer inProgress.Delete(fullPath)

		st, err := os.Stat(fullPath)
		if err != nil {
			return nil
		}

		switch opts.Mode {
		case WatchModeCompress:
			cOpts := opts.CompressOpts
			if cOpts.OutputDir == "" {
				cOpts.OutputDir = opts.Dir
			}
			WriteInfo("[Watcher] Comprimiendo archivo detectado: %s", fullPath)
			outPaths, err := DoCompress([]string{fullPath}, cOpts)
			if err != nil {
				WriteError("[Watcher] Error comprimiendo %s: %v", fullPath, err)
				if opts.OnFileHandled != nil {
					opts.OnFileHandled(fullPath, err)
				}
				return err
			}
			for _, out := range outPaths {
				ignoredOutputs.Add(filepath.Base(out))
				for _, part := range globSplitParts(out) {
					ignoredOutputs.Add(filepath.Base(part))
				}
			}
			WriteSuccess("[Watcher] Compresión finalizada: %s", fullPath)
			if opts.OnFileHandled != nil {
				opts.OnFileHandled(fullPath, nil)
			}
			return nil

		case WatchModeDecompress:
			if st.IsDir() {
				return nil
			}
			if _, err := DetectFormat(fullPath); err != nil {
				return nil
			}
			dOpts := opts.DecompressOpts
			if dOpts.OutputDir == "" {
				dOpts.OutputDir = opts.Dir
			}
			WriteInfo("[Watcher] Descomprimiendo archivo detectado: %s", fullPath)
			err := DoDecompress([]string{fullPath}, dOpts)
			if err != nil {
				WriteError("[Watcher] Error descomprimiendo %s: %v", fullPath, err)
				if opts.OnFileHandled != nil {
					opts.OnFileHandled(fullPath, err)
				}
				return err
			}
			WriteSuccess("[Watcher] Descompresión finalizada: %s", fullPath)
			if opts.OnFileHandled != nil {
				opts.OnFileHandled(fullPath, nil)
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
