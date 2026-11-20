package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const maxTestDiagBytes = 32 * 1024

type cappedDiagBuffer struct {
	mu        sync.Mutex
	buf       []byte
	max       int
	truncated bool
}

func newCappedDiagBuffer(max int) *cappedDiagBuffer {
	if max <= 0 {
		max = maxTestDiagBytes
	}
	return &cappedDiagBuffer{max: max}
}

func (b *cappedDiagBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	origLen := len(p)
	if origLen == 0 {
		return 0, nil
	}
	if len(b.buf)+origLen > b.max {
		b.truncated = true
	}
	if origLen >= b.max {
		if cap(b.buf) < b.max {
			b.buf = make([]byte, b.max)
		} else {
			b.buf = b.buf[:b.max]
		}
		copy(b.buf, p[origLen-b.max:])
		return origLen, nil
	}
	if overflow := len(b.buf) + origLen - b.max; overflow > 0 {
		copy(b.buf, b.buf[overflow:])
		b.buf = b.buf[:len(b.buf)-overflow]
	}
	b.buf = append(b.buf, p...)
	return origLen, nil
}

func (b *cappedDiagBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

type TestOptions struct {
	Verbose  bool
	Quick    bool
	Verify   bool
	Password string
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
	status, err := testArchiveIntegrity(file, opts)
	if err != nil {
		return status, err
	}

	if opts.Verify {
		shaPath := file + ".sha256"
		if _, statErr := os.Stat(shaPath); statErr == nil {
			expectedHash, parseErr := ParseSHA256File(shaPath, file)
			if parseErr != nil {
				WriteLogf("%s✗ %s: error leyendo checksum: %v%s\n", Red, file, parseErr, NC)
				return "FAIL", parseErr
			}
			actualHash, hashErr := ComputeSHA256(file)
			if hashErr != nil {
				WriteLogf("%s✗ %s: error calculando SHA-256: %v%s\n", Red, file, hashErr, NC)
				return "FAIL", hashErr
			}
			if actualHash != expectedHash {
				WriteLogf("%s✗ Checksum SHA-256 no coincide para %s (esperado: %s, obtenido: %s)%s\n", Red, file, expectedHash, actualHash, NC)
				return "FAIL", fmt.Errorf("checksum SHA-256 no coincide para %s", file)
			}
			WriteLogf("%s✓ Checksum SHA-256 verificado para %s%s\n", Green, file, NC)
		}
	}

	return status, nil
}

func testArchiveIntegrity(file string, opts TestOptions) (string, error) {
	info, err := DetectFormat(file)
	if err != nil {
		return "", err
	}
	if info.IsContainer() {
		if p, err := GetPasswordManager().ResolvePassword(file, info.Format, opts.Password); err == nil && p != "" {
			opts.Password = p
		}
	}

	var fileSize int64
	if fi, err := os.Stat(file); err == nil {
		fileSize = fi.Size()
	}

	switch info.Format {
	case Gz:
		if hasTool("pigz") {
			return testWith(file, "pigz", "-t", "--", file)
		}
		return testWith(file, "gzip", "-t", "--", file)

	case Xz:
		thStr := threadStr(resolveThreadCap(Xz, fileSize))
		memMB := getMemLimit()
		if memMB < 65 {
			memMB = 65
		}
		return testWith(file, "xz", "-t", "-T"+thStr, fmt.Sprintf("--memlimit-mt-decompress=%dMiB", memMB), "--", file)

	case Bz2:
		return testWith(file, bzip2Bin(), "-t", "--", file)

	case Bz3:
		thStr := threadStr(resolveThreadCap(Bz3, fileSize))
		return testWith(file, "bzip3", "-t", "-j", thStr, "--", file)

	case Zst:
		thStr := threadStr(resolveThreadCap(Zst, fileSize))
		return testWith(file, "zstd", "-t", "-T"+thStr, "--", file)

	case Lz:
		if hasTool("plzip") {
			thStr := threadStr(resolveThreadCap(Lz, fileSize))
			return testWith(file, "plzip", "-t", "--threads="+thStr, "--", file)
		}
		return testWith(file, "lzip", "-t", "--", file)

	case Lrz:
		thStr := threadStr(resolveThreadCap(Lrz, fileSize))
		lrzMem := getMemLimit() / 100
		if lrzMem < 1 {
			lrzMem = 1
		}
		return testWith(file, "lrzip", "-t", "-p", thStr, "-m", strconv.Itoa(lrzMem), "--", file)

	case Lz4:
		return testWith(file, "lz4", "-t", "--", file)

	case Br:
		return testWith(file, "brotli", "-t", "--", file)

	case Tar:
		return testWith(file, "tar", "-tf", safeFlagArg(file), "--")

	case Zip:
		if opts.Password != "" {
			if hasTool(sevenzBin()) {
				return testWith(file, sevenzBin(), "t", "-p"+opts.Password, "--", file)
			}
			return testWith(file, "unzip", "-t", "-P", opts.Password, "--", file)
		}
		return testWith(file, "unzip", "-t", "--", file)

	case SevenZ:
		args := []string{"t"}
		if opts.Password != "" {
			args = append(args, "-p"+opts.Password)
		}
		args = append(args, "--", file)
		return testWith(file, sevenzBin(), args...)

	case Rar:
		args := []string{"t"}
		if opts.Password != "" {
			args = append(args, "-p"+opts.Password)
		}
		args = append(args, "--", file)
		return testWith(file, rarBin(), args...)

	default:
		return "", fmt.Errorf("formato no reconocido: %s", file)
	}
}

func testWith(file, tool string, args ...string) (string, error) {
	if !hasTool(tool) {
		return "WARNING", nil
	}
	cmd := execCommand(tool, args...)
	diagBuf := newCappedDiagBuffer(maxTestDiagBytes)
	if tool == "tar" {
		cmd.Stdout = io.Discard
		cmd.Stderr = diagBuf
	} else {
		cmd.Stdout = diagBuf
		cmd.Stderr = diagBuf
	}
	if err := cmd.Run(); err != nil {
		outStr := strings.TrimSpace(diagBuf.String())
		if outStr != "" {
			return "FAIL", fmt.Errorf("%s: %s", outStr, err)
		}
		return "FAIL", fmt.Errorf("error: %w", err)
	}
	return "OK", nil
}
