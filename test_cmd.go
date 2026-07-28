package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type TestOptions struct {
	Verbose bool
	Quick   bool
}

func DoTest(files []string, opts TestOptions) error {
	if len(files) == 0 {
		return fmt.Errorf("No se especificaron archivos para verificar")
	}

	var allFiles []string
	for _, pattern := range files {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			if _, err := os.Stat(pattern); err == nil {
				matches = []string{pattern}
			} else {
				WriteLogf("%s✗ No encontrado: %s%s\n", Red, pattern, NC)
				continue
			}
		}
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil {
				WriteLogf("%s✗ Error: %s%s\n", Red, err, NC)
				continue
			}
			if info.IsDir() {
				WriteLogf("%s✗ Es un directorio: %s%s\n", Red, m, NC)
				continue
			}
			allFiles = append(allFiles, m)
		}
	}

	if len(allFiles) == 0 {
		return fmt.Errorf("No hay archivos válidos para verificar")
	}

	type testResult struct {
		file   string
		status string
		err    error
	}

	sem := make(chan struct{}, NCPU())
	resultCh := make(chan testResult, len(allFiles))
	var wg sync.WaitGroup

	for _, f := range allFiles {
		wg.Add(1)
		go func(file string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			status, err := TestFile(file, opts)
			resultCh <- testResult{file, status, err}
		}(f)
	}

	wg.Wait()
	close(resultCh)

	var successes, failures, warnings int
	for r := range resultCh {
		switch {
		case r.err != nil:
			WriteLogf("%s✗ %s: %s%s\n", Red, r.file, r.err, NC)
			failures++
		case r.status == "OK":
			WriteLogf("%s✓ %s%s\n", Green, r.file, NC)
			successes++
		case r.status == "WARNING":
			WriteLogf("%s⚠ %s%s\n", Yellow, r.file, NC)
			warnings++
		}
	}

	WriteLogf("\n")
	WriteLogf("Resumen: %d correctos, %d con advertencias, %d fallos\n", successes, warnings, failures)

	if failures > 0 {
		return fmt.Errorf("%d archivo(s) con errores", failures)
	}
	return nil
}

func TestFile(file string, opts TestOptions) (string, error) {
	ext := strings.ToLower(file)

	switch {
	case strings.HasSuffix(ext, ".tar.gz") || strings.HasSuffix(ext, ".tgz"):
		if hasTool("pigz") {
			return testWith(file, "pigz", "-t", "--", file)
		}
		return testWith(file, "gzip", "-t", "--", file)

	case strings.HasSuffix(ext, ".tar.xz") || strings.HasSuffix(ext, ".txz"):
		return testWith(file, "xz", "-t", "-T0", "--", file)

	case strings.HasSuffix(ext, ".tar.bz2") || strings.HasSuffix(ext, ".tbz2"):
		return testWith(file, bzip2Bin(), "-t", "--", file)

	case strings.HasSuffix(ext, ".tar.bz3"):
		return testWith(file, "bzip3", "-t", "-j", ncpuStr(), "--", file)

	case strings.HasSuffix(ext, ".tar.zst") || strings.HasSuffix(ext, ".tzst"):
		return testWith(file, "zstd", "-t", "-T0", "--", file)

	case strings.HasSuffix(ext, ".tar.lz") || strings.HasSuffix(ext, ".tlz"):
		return testWith(file, "plzip", "-t", "--threads="+ncpuStr(), "--", file)

	case strings.HasSuffix(ext, ".tar.lrz"):
		return testWith(file, "lrzip", "-t", "-p", ncpuStr(), "--", file)

	case strings.HasSuffix(ext, ".tar.lz4"):
		return testWith(file, "lz4", "-t", "--", file)

	case strings.HasSuffix(ext, ".tar.br"):
		return testWith(file, "brotli", "-t", "--", file)

	case strings.HasSuffix(ext, ".tar"):
		return testWith(file, "tar", "-tf", "--", file)

	case strings.HasSuffix(ext, ".gz"):
		return testWith(file, "pigz", "-t", "--", file)

	case strings.HasSuffix(ext, ".xz"):
		return testWith(file, "xz", "-t", "--", file)

	case strings.HasSuffix(ext, ".bz2"):
		return testWith(file, bzip2Bin(), "-t", "--", file)

	case strings.HasSuffix(ext, ".bz3"):
		return testWith(file, "bzip3", "-t", "--", file)

	case strings.HasSuffix(ext, ".zst"):
		return testWith(file, "zstd", "-t", "--", file)

	case strings.HasSuffix(ext, ".lz"):
		return testWith(file, "plzip", "-t", "--", file)

	case strings.HasSuffix(ext, ".lrz"):
		return testWith(file, "lrzip", "-t", "--", file)

	case strings.HasSuffix(ext, ".zip"):
		return testWith(file, "unzip", "-t", "--", file)

	case strings.HasSuffix(ext, ".7z"):
		return testWith(file, sevenzBin(), "t", "--", file)

	case strings.HasSuffix(ext, ".rar"):
		return testWith(file, rarBin(), "t", "--", file)

	case strings.HasSuffix(ext, ".lz4"):
		return testWith(file, "lz4", "-t", "--", file)

	case strings.HasSuffix(ext, ".br"):
		return testWith(file, "brotli", "-t", "--", file)

	default:
		return "", fmt.Errorf("formato no reconocido: %s", file)
	}
}

func testWith(file, tool string, args ...string) (string, error) {
	if !hasTool(tool) {
		return "WARNING", nil
	}
	cmd := exec.Command(tool, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Return combined output for diagnostics
		outStr := strings.TrimSpace(string(out))
		if outStr != "" {
			return "FAIL", fmt.Errorf("%s: %s", outStr, err)
		}
		return "FAIL", fmt.Errorf("error: %w", err)
	}
	return "OK", nil
}
