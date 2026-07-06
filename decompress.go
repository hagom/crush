package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type DecompressOptions struct {
	DryRun    bool
	Verbose   bool
	OutputDir string
	KeepOrig  bool
	Progress  bool
	Force     bool
}

func DoDecompress(files []string, opts DecompressOptions) error {
	if len(files) == 0 {
		return fmt.Errorf("No se especificaron archivos. Use -i archivo o pase archivos como argumento")
	}

	var successes, errors int

	if opts.DryRun {
		for _, file := range files {
			WriteLogf("%s[Simulacro] Descomprimiendo: %s%s\n", Blue, file, NC)
		}
		return nil
	}

	for _, file := range files {
		matches, err := filepath.Glob(file)
		if err != nil || len(matches) == 0 {
			// Try literal
			if _, err := os.Stat(file); err == nil {
				matches = []string{file}
			} else {
				WriteLogf("%s✗ No encontrado: %s%s\n", Red, file, NC)
				errors++
				continue
			}
		}

		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil {
				WriteLogf("%s✗ Error: %s%s\n", Red, err, NC)
				errors++
				continue
			}
			if info.IsDir() {
				WriteLogf("%s✗ Es un directorio: %s%s\n", Red, match, NC)
				errors++
				continue
			}

			err = decompressFile(match, opts)
			if err != nil {
				WriteLogf("%s✗ %s%s\n", Red, err, NC)
				errors++
			} else {
				successes++
			}
		}
	}

	WriteLogf("\n")
	if errors > 0 {
		WriteLogf("%sCompletado con %d errores, %d exitosos%s\n", Yellow, errors, successes, NC)
		return fmt.Errorf("%d error(es) durante la descompresión", errors)
	}
	WriteLogf("%s✓ Descompresión completada (%d archivo(s))%s\n", Green, successes, NC)
	return nil
}

func decompressFile(file string, opts DecompressOptions) error {
	info, err := DetectFormat(file)
	if err != nil {
		return err
	}

	if opts.DryRun {
		WriteLogf("%s[Simulacro] Descomprimiendo: %s%s\n", Blue, file, NC)
		return nil
	}

	startTime := time.Now()
	WriteLogf("%s%s%s\n", Bold, file, NC)

	dir := opts.OutputDir
	if dir == "" {
		dir = filepath.Dir(file)
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("Error creando directorio de salida: %w", err)
	}

	if info.IsTar {
		err = decompressTar(file, dir, info, opts)
	} else {
		err = decompressSingle(file, dir, info, opts)
	}

	if err != nil {
		return err
	}

	if !opts.KeepOrig {
		os.Remove(file)
	}

	elapsed := time.Since(startTime)
	WriteLogf("  %s✓ Hecho (%v)%s\n", Green, elapsed.Round(time.Second), NC)

	return nil
}

func decompressTar(file string, dir string, info FormatInfo, opts DecompressOptions) error {
	if info.Tool == "" {
		return fmt.Errorf("No se detectó herramienta para: %s", file)
	}

	// Check disk space first
	needed := EstimateUncompressedSize(file)
	if needed > 0 {
		if err := CheckDiskSpace(needed*110/100, dir); err != nil {
			return err
		}
	}

	WriteLogf("  → %s/\n", dir)

	if opts.Progress && hasTool("pv") {
		tarExtract := exec.Command("tar", "-xf", "-", "-C", dir)
		// Build decompressor pipe: decompress -> pv -> tar -xf -
		var decompCmd *exec.Cmd
		ext := strings.ToLower(file)
		switch {
		case strings.HasSuffix(ext, ".tar.gz") || strings.HasSuffix(ext, ".tgz"):
			if hasTool("pigz") {
				decompCmd = exec.Command("pigz", "-dc", "--", file)
			} else {
				decompCmd = exec.Command("gzip", "-dc", "--", file)
			}
		case strings.HasSuffix(ext, ".tar.xz") || strings.HasSuffix(ext, ".txz"):
			decompCmd = exec.Command("xz", "-dc", "-T0", "--", file)
		case strings.HasSuffix(ext, ".tar.bz2") || strings.HasSuffix(ext, ".tbz2"):
			decompCmd = exec.Command(bzip2Bin(), "-dc", "--", file)
		case strings.HasSuffix(ext, ".tar.bz3"):
			decompCmd = exec.Command("bzip3", "-dc", "-j", ncpuStr(), "--", file)
		case strings.HasSuffix(ext, ".tar.zst") || strings.HasSuffix(ext, ".tzst"):
			decompCmd = exec.Command("zstd", "-dc", "-T0", "--", file)
		case strings.HasSuffix(ext, ".tar.lz") || strings.HasSuffix(ext, ".tlz"):
			decompCmd = exec.Command("plzip", "-dc", "--threads="+ncpuStr(), "--", file)
		case strings.HasSuffix(ext, ".tar.lrz"):
			decompCmd = exec.Command("lrzip", "-d", "-p", ncpuStr(), "-o", "-", "--", file)
		case strings.HasSuffix(ext, ".tar"):
			return exec.Command("tar", "-xf", file, "-C", dir).Run()
		}

		pvCmd := exec.Command("pv", "-B", "256k")

		if err := pipeline(os.Stdout, os.Stderr, decompCmd, pvCmd, tarExtract); err != nil {
			return fmt.Errorf("Error extrayendo %s: %w", file, err)
		}
	} else {
		// Direct tool invocation
		args := strings.Fields(info.DirectFlags)
		args = append(args, "--", file)
		cmd := exec.Command(info.Tool, args...)
		cmd.Dir = dir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("Error descomprimiendo %s: %w", file, err)
		}

		// For tar-based archives, we need to extract the tar manually if tool didn't do it
		if strings.HasSuffix(file, ".lrz") || strings.HasSuffix(file, ".tar.lrz") {
			// lrzip needs special handling - it produces the tar
			base := file
			if strings.HasSuffix(file, ".lrz") {
				base = strings.TrimSuffix(file, ".lrz")
			} else if strings.HasSuffix(file, ".tar.lrz") {
				base = strings.TrimSuffix(file, ".lrz")
			}
			if _, err := os.Stat(base); err == nil && !strings.HasSuffix(base, ".tar") {
				// It's the lrz decompressor, now extract tar if applicable
				if strings.HasPrefix(info.Tool, "tar") || info.IsTar {
					extractCmd := exec.Command("tar", "-xf", base, "-C", dir)
					extractCmd.Stdout = os.Stdout
					extractCmd.Stderr = os.Stderr
					if err := extractCmd.Run(); err != nil {
						return fmt.Errorf("Error extrayendo tar de %s: %w", base, err)
					}
				}
			} else if strings.HasSuffix(base, ".tar") {
				// Direct tar file - extract it
				extractCmd := exec.Command("tar", "-xf", base, "-C", dir)
				extractCmd.Stdout = os.Stdout
				extractCmd.Stderr = os.Stderr
				if err := extractCmd.Run(); err != nil {
					return fmt.Errorf("Error extrayendo tar de %s: %w", base, err)
				}
			}
		}
	}

	return nil
}

func decompressSingle(file string, dir string, info FormatInfo, opts DecompressOptions) error {
	ext := strings.ToLower(file)

	switch {
	case strings.HasSuffix(ext, ".zip"):
		args := []string{"-o"}
		if !opts.Force {
			args = append(args, "-n")
		} else {
			args = append(args, "-o")
		}
		dirFlag := "-d"
		args = append(args, file, dirFlag, dir)
		cmd := exec.Command("unzip", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()

	case strings.HasSuffix(ext, ".7z"):
		args := []string{"x", file, fmt.Sprintf("-o%s", dir)}
		if opts.Force {
			args = append(args, "-y")
		}
		cmd := exec.Command(info.Tool, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()

	case strings.HasSuffix(ext, ".rar"):
		args := []string{"x", file, fmt.Sprintf("%s/", dir)}
		if opts.Force {
			args = append(args, "-y")
		}
		cmd := exec.Command(info.Tool, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()

	case strings.HasSuffix(ext, ".tar"):
		cmd := exec.Command("tar", "-xf", file, "-C", dir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()

	case strings.HasSuffix(ext, ".lrz"):
		cmd := exec.Command("lrzip", "-d", "-p", ncpuStr(), "-k", "--", file, "-o", dir+"/")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()

	default:
		directArgs := strings.Fields(info.DirectFlags)
		cmd := exec.Command(info.Tool, directArgs...)
		cmd.Args = append(cmd.Args, "--", file)
		cmd.Dir = dir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
}

// splitWriter writes to underlying writer, splitting every splitSize bytes
// and producing sequential files (file.partaa, file.partab, etc.)

type splitWriter struct {
	base     io.Writer
	split    int64
	written  int64
	part     int
	basePath string
	file     *os.File
}

func newSplitWriter(firstFile *os.File, splitSize int, basePath string) *splitWriter {
	return &splitWriter{
		base:     firstFile,
		split:    int64(splitSize) * 1024 * 1024, // MB to bytes
		written:  0,
		part:     0,
		basePath: basePath,
		file:     firstFile,
	}
}

func (w *splitWriter) Write(p []byte) (int, error) {
	totalWritten := 0

	for len(p) > 0 {
		remaining := w.split - w.written
		toWrite := int64(len(p))
		if toWrite > remaining {
			toWrite = remaining
		}

		n, err := w.file.Write(p[:toWrite])
		totalWritten += n
		if err != nil {
			return totalWritten, err
		}

		p = p[toWrite:]
		w.written += toWrite

		if w.written >= w.split && len(p) > 0 {
			// Close current, open next
			w.file.Close()
			w.part++
			partName := fmt.Sprintf("%s.part%02d", w.basePath, w.part)
			file, err := os.Create(partName)
			if err != nil {
				return totalWritten, fmt.Errorf("Error creando parte %s: %w", partName, err)
			}
			w.file = file
			w.written = 0
		}
	}

	return totalWritten, nil
}

func (w *splitWriter) Close() error {
	if w.file != nil && w.file != w.base.(*os.File) {
		return w.file.Close()
	}
	return nil
}
