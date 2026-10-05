package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type findResult struct {
	archive string
	matches []ArchiveMember
	err     error
}

// DoFind searches for pattern in members of the given archive files.
func DoFind(pattern string, files []string, password string, out io.Writer) (int, error) {
	if out == nil {
		out = os.Stdout
	}
	if pattern == "" {
		return 0, fmt.Errorf("debe especificar un patrón de búsqueda (-find PATRÓN)")
	}
	if len(files) == 0 {
		return 0, fmt.Errorf("debe especificar al menos un archivo comprimido para buscar")
	}

	patLower := strings.ToLower(pattern)
	results := make([]findResult, len(files))

	var wg sync.WaitGroup
	sem := make(chan struct{}, effectiveThreads("", 0))

	for i, file := range files {
		wg.Add(1)
		go func(idx int, path string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			members, err := ListArchiveMembers(path, password)
			if err != nil {
				results[idx] = findResult{archive: path, err: err}
				return
			}

			var matches []ArchiveMember
			for _, m := range members {
				if m.IsDir {
					continue
				}
				mPathLower := strings.ToLower(m.Path)
				baseLower := strings.ToLower(filepath.Base(m.Path))

				matched := false
				if ok, _ := filepath.Match(patLower, baseLower); ok {
					matched = true
				} else if ok, _ := filepath.Match(patLower, mPathLower); ok {
					matched = true
				} else if strings.Contains(mPathLower, patLower) {
					matched = true
				}

				if matched {
					matches = append(matches, m)
				}
			}
			results[idx] = findResult{archive: path, matches: matches}
		}(i, file)
	}

	wg.Wait()

	totalMatches := 0
	matchedArchives := 0

	for _, res := range results {
		if res.err != nil {
			fmt.Fprintf(out, "%s✗ Error buscando en %s: %v%s\n", Red, res.archive, res.err, NC)
			continue
		}
		if len(res.matches) == 0 {
			continue
		}
		matchedArchives++
		totalMatches += len(res.matches)

		fmt.Fprintf(out, "%s%s%s:\n", Bold, res.archive, NC)
		for _, m := range res.matches {
			fmt.Fprintf(out, "  → %s%s%s (%s)\n", Green, m.Path, NC, FormatSize(m.Size))
		}
	}

	if totalMatches == 0 {
		fmt.Fprintf(out, "No se encontraron coincidencias para '%s'\n", pattern)
	} else {
		fmt.Fprintf(out, "\nCoincidencias encontradas: %d archivo(s) en %d archivo(s) comprimido(s)\n", totalMatches, matchedArchives)
	}

	return totalMatches, nil
}
