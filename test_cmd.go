package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type TestOptions struct {
	Verbose bool
	Quick   bool
}

func DoTest(files []string, opts TestOptions) error {
	if len(files) == 0 {
		return fmt.Errorf("No se especificaron archivos para verificar")
	}

	var successes, failures, warnings int

	for _, pattern := range files {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			if _, err := os.Stat(pattern); err == nil {
				matches = []string{pattern}
			} else {
				WriteLogf("%s✗ No encontrado: %s%s\n", Red, pattern, NC)
				failures++
				continue
			}
		}

		for _, file := range matches {
			info, err := os.Stat(file)
			if err != nil {
				WriteLogf("%s✗ Error: %s%s\n", Red, err, NC)
				failures++
				continue
			}
			if info.IsDir() {
				WriteLogf("%s✗ Es un directorio: %s%s\n", Red, file, NC)
				failures++
				continue
			}

			result, err := TestFile(file, opts)
			if err != nil {
				WriteLogf("%s✗ %s: %s%s\n", Red, file, err, NC)
				failures++
			} else if result == "OK" {
				WriteLogf("%s✓ %s%s\n", Green, file, NC)
				successes++
			} else if result == "WARNING" {
				WriteLogf("%s⚠ %s%s\n", Yellow, file, NC)
				warnings++
			}
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
