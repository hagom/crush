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
		if err := os.Remove(file); err != nil {
			WriteLogf("  %s⚠ No se pudo eliminar %s: %v%s\n", Yellow, file, err, NC)
		}
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
		case strings.HasSuffix(ext, ".tar.lz4"):
			decompCmd = exec.Command("lz4", "-dc", "--", file)
		case strings.HasSuffix(ext, ".tar.br"):
			decompCmd = exec.Command("brotli", "-dc", "--", file)
		case strings.HasSuffix(ext, ".tar"):
			return exec.Command("tar", "-xf", file, "-C", dir).Run()
		}

		pvArgs := []string{"-B", "256k"}
		if needed > 0 {
			pvArgs = append(pvArgs, "-s", fmt.Sprintf("%d", needed))
		}
		pvCmd := exec.Command("pv", pvArgs...)

		if err := pipeline(os.Stdout, os.Stderr, decompCmd, pvCmd, tarExtract); err != nil {
			return fmt.Errorf("Error extrayendo %s: %w", file, err)
		}
	} else {
		// Direct tool invocation — decompress the compression layer
		args := strings.Fields(info.DirectFlags)
		args = append(args, "--", file)
		cmd := exec.Command(info.Tool, args...)
		cmd.Dir = dir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("Error descomprimiendo %s: %w", file, err)
		}

		// For tar-based archives, extract the resulting tar
		if info.IsTar {
			base := stripTarExt(file)
			tarName := base + ".tar"
			if _, err := os.Stat(tarName); err == nil {
				extractCmd := exec.Command("tar", "-xf", tarName, "-C", dir)
				extractCmd.Stdout = os.Stdout
				extractCmd.Stderr = os.Stderr
				if err := extractCmd.Run(); err != nil {
					return fmt.Errorf("Error extrayendo tar de %s: %w", tarName, err)
				}
				if !opts.KeepOrig {
						if err := os.Remove(tarName); err != nil {
							WriteLogf("  %s⚠ No se pudo eliminar %s: %v%s\n", Yellow, tarName, err, NC)
						}
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
		args := []string{}
		if opts.Force {
			args = append(args, "-o")
		} else {
			args = append(args, "-n")
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
		outputPath := filepath.Join(dir, strings.TrimSuffix(filepath.Base(file), ".lrz"))
		if opts.Progress && hasTool("pv") {
			pipeFlags := strings.Fields(info.PipeFlags)
			decompCmd := exec.Command(info.Tool, pipeFlags...)
			decompCmd.Args = append(decompCmd.Args, "--", file)
			outFile, err := os.Create(outputPath)
			if err != nil {
				return fmt.Errorf("Error creando archivo de salida: %w", err)
			}
			defer outFile.Close()
			pvArgs := []string{"-B", "256k"}
			if size := EstimateUncompressedSize(file); size > 0 {
				pvArgs = append(pvArgs, "-s", fmt.Sprintf("%d", size))
			}
			return pipeline(outFile, os.Stderr, decompCmd, exec.Command("pv", pvArgs...))
		}
		args := []string{"-d", "-p", ncpuStr(), "-k", "--", file, "-o", outputPath}
		cmd := exec.Command("lrzip", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()

	default:
		if opts.Progress && hasTool("pv") {
			pipeFlags := strings.Fields(info.PipeFlags)
			decompCmd := exec.Command(info.Tool, pipeFlags...)
			decompCmd.Args = append(decompCmd.Args, "--", file)
			outputPath := filepath.Join(dir, stripCompressionExt(filepath.Base(file)))
			outFile, err := os.Create(outputPath)
			if err != nil {
				return fmt.Errorf("Error creando archivo de salida: %w", err)
			}
			defer outFile.Close()
			pvArgs := []string{"-B", "256k"}
			if size := EstimateUncompressedSize(file); size > 0 {
				pvArgs = append(pvArgs, "-s", fmt.Sprintf("%d", size))
			}
			pvCmd := exec.Command("pv", pvArgs...)
			if err := pipeline(outFile, os.Stderr, decompCmd, pvCmd); err != nil {
				return fmt.Errorf("Error descomprimiendo %s: %w", file, err)
			}
			return nil
		}
		directArgs := strings.Fields(info.DirectFlags)
		cmd := exec.Command(info.Tool, directArgs...)
		cmd.Args = append(cmd.Args, "--", file)
		cmd.Dir = dir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
}

var tarSuffixes = []string{".tar.gz", ".tgz", ".tar.xz", ".txz", ".tar.bz2", ".tbz2",
	".tar.bz3", ".tar.zst", ".tzst", ".tar.lz", ".tlz",
	".tar.lrz", ".tar.lz4", ".tar.br"}

func stripTarExt(file string) string {
	lower := strings.ToLower(file)
	for _, s := range tarSuffixes {
		if strings.HasSuffix(lower, s) {
			return file[:len(file)-len(s)]
		}
	}
	return file
}

var singleCompExts = []string{".gz", ".xz", ".bz2", ".bz3", ".zst", ".lz", ".lrz", ".lz4", ".br"}

func stripCompressionExt(base string) string {
	lower := strings.ToLower(base)
	for _, ext := range singleCompExts {
		if strings.HasSuffix(lower, ext) {
			return base[:len(base)-len(ext)]
		}
	}
	return base
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
	baseFile, ok := w.base.(*os.File)
	if !ok {
		return nil
	}
	if w.file != nil && w.file != baseFile {
		return w.file.Close()
	}
	return nil
}
