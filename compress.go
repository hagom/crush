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

type CompressOptions struct {
	Format          Format
	DryRun          bool
	Verbose         bool
	OutputDir       string
	SplitSize       int
	Progress        bool
	KeepOrig        bool
	Threads         int
	CompressionOpts string
	ChunkSize       int
	Exclude         []string
	FromFile        string
	TarTool         string
}

func DoCompress(items []string, opts CompressOptions) error {
	if len(items) == 0 {
		return fmt.Errorf("No se especificaron archivos. Use -i archivo o pase archivos como argumento")
	}

	// Resolve input items
	var files []string
	if opts.FromFile != "" {
		lines, err := ReadFileLines(opts.FromFile)
		if err != nil {
			return fmt.Errorf("Error leyendo archivo de lista: %w", err)
		}
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				if strings.HasPrefix(line, "/") {
					files = append(files, line)
				} else {
					files = append(files, filepath.Join(filepath.Dir(opts.FromFile), line))
				}
			}
		}
	} else {
		files = expandGlobs(items)
	}

	if len(files) == 0 {
		return fmt.Errorf("No se encontraron archivos válidos")
	}

	// Handle directory as single item (one .tar.* or .zip etc)
	singleItem := false
	if len(files) == 1 {
		info, err := os.Stat(files[0])
		if err == nil && info.IsDir() {
			singleItem = true
		}
	}

	// Build output path
	ext := ExtForFormat(opts.Format)
	var outPath string

	if singleItem {
		base := filepath.Base(files[0])
		baseName := strings.TrimSuffix(base, filepath.Ext(base))
		outPath = filepath.Join(opts.OutputDir, GetUniqueName(baseName, ext))
	} else {
		baseName := "compresor_" + time.Now().Format("20060102_150405")
		outPath = filepath.Join(opts.OutputDir, GetUniqueName(baseName, ext))
	}

	if opts.DryRun {
		WriteLogf("%s[Simulacro] Comprimiendo %d archivo(s)%s\n", Blue, len(files), NC)
		WriteLogf("%s[Simulacro] Salida: %s%s\n", Blue, outPath, NC)
		if opts.Format == Gz || opts.Format == Xz || opts.Format == Bz2 ||
			opts.Format == Bz3 || opts.Format == Zst || opts.Format == Lz ||
			opts.Format == Lrz || opts.Format == Lz4 || opts.Format == Br {
			WriteLogf("%s[Simulacro] Formato: tar.%s (Multi-archivo → tar pipe)%s\n", Blue, opts.Format, NC)
		} else {
			WriteLogf("%s[Simulacro] Formato: %s%s\n", Blue, ext, NC)
		}
		return nil
	}

	// Check disk space
	totalSize := int64(0)
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		totalSize += info.Size()
	}

	// Estimate compressed size (very rough: 30% of original for most, 50% for zip/rar/7z/tar)
	var estimated int64
	switch opts.Format {
	case Zip, SevenZ, Rar:
		estimated = totalSize * 80 / 100
	case Tar:
		estimated = totalSize * 100 / 100
	default:
		estimated = totalSize * 30 / 100
	}
	// Add 10% margin
	estimated = estimated * 110 / 100

	if err := CheckDiskSpace(estimated, opts.OutputDir); err != nil {
		return err
	}

	// Check exclude patterns
	excludeFunc := func(name string) bool {
		for _, pat := range opts.Exclude {
			if matched, _ := filepath.Match(pat, filepath.Base(name)); matched {
				return true
			}
			if matched, _ := filepath.Match(pat, name); matched {
				return true
			}
		}
		return false
	}

	var filteredFiles []string
	for _, f := range files {
		if excludeFunc(f) {
			if opts.Verbose {
				WriteLogf("  %sExcluido: %s%s\n", Yellow, f, NC)
			}
			continue
		}
		filteredFiles = append(filteredFiles, f)
	}

	if len(filteredFiles) == 0 {
		return fmt.Errorf("Todos los archivos fueron excluidos")
	}

	startTime := time.Now()

	WriteLogf("%sComprimiendo %d archivo(s)...%s\n", Bold, len(filteredFiles), NC)
	WriteLogf("  Formato: %s\n", ext)
	WriteLogf("  Destino: %s\n", outPath)
	WriteLogf("  Modo: %s\n", compressModeDesc(opts.Format))
	WriteLogf("\n")

	err := compressItems(filteredFiles, outPath, opts)
	if err != nil {
		return err
	}

	elapsed := time.Since(startTime)

	// Show results
	origSize := totalSize
	finalSize := int64(0)
	if info, err := os.Stat(outPath); err == nil {
		finalSize = info.Size()
	}

	WriteLogf("\n")
	WriteLogf("%s✓ Compresión completada%s\n", Green, NC)
	WriteLogf("  Original: %s\n", FormatSize(origSize))
	WriteLogf("  Comprimido: %s\n", FormatSize(finalSize))
	WriteLogf("  Reducción: %s%%\n", CalcPct(origSize, finalSize))
	WriteLogf("  Tiempo: %v\n", elapsed.Round(time.Second))
	WriteLogf("  Archivo: %s\n", outPath)

	if !opts.KeepOrig {
		removed := 0
		for _, f := range filteredFiles {
			if !strings.HasPrefix(f, "/") {
				continue
			}
			if err := os.Remove(f); err == nil {
				removed++
			}
		}
		if removed > 0 {
			WriteLogf("  %sArchivos originales eliminados: %d%s\n", Yellow, removed, NC)
		}
	}

	return nil
}

func compressModeDesc(f Format) string {
	switch f {
	case Gz:
		return "Compresión rápida (gzip)"
	case Xz:
		return "Máxima compresión (xz)"
	case Bz2:
		return "Compresión balanceada (bzip2)"
	case Zst:
		return "Compresión moderna multihilo (zstd)"
	case Lz:
		return "Compresión LZMA (lzip)"
	case Lrz:
		return "Compresión LRZIP (grandes archivos)"
	case Bz3:
		return "Compresión bzip3 (alta tasa)"
	case Zip:
		return "Compresión ZIP"
	case SevenZ:
		return "Compresión 7z (LZMA2)"
	case Tar:
		return "Solo empaquetado tar"
	case Rar:
		return "Compresión RAR"
	case Lz4:
		return "Compresión ultrarrápida (lz4)"
	case Br:
		return "Compresión Brotli (alta tasa)"
	default:
		return "Desconocido"
	}
}

func expandGlobs(items []string) []string {
	var result []string
	for _, item := range items {
		matches, err := filepath.Glob(item)
		if err != nil || len(matches) == 0 {
			if err != nil {
				WriteLogf("warning: patrón glob inválido %q: %v\n", item, err)
			}
			// Try as literal file
			if _, err := os.Stat(item); err == nil {
				result = append(result, item)
			}
			continue
		}
		result = append(result, matches...)
	}
	return result
}

func compressItems(files []string, outPath string, opts CompressOptions) error {
	isTarBased := opts.Format == Gz || opts.Format == Xz || opts.Format == Bz2 ||
		opts.Format == Bz3 || opts.Format == Zst || opts.Format == Lz ||
		opts.Format == Lrz || opts.Format == Lz4 || opts.Format == Br

	if isTarBased {
		return compressTarPipe(files, outPath, opts)
	}

	switch opts.Format {
	case Zip:
		return compressZip(files, outPath, opts)
	case SevenZ:
		return compress7z(files, outPath, opts)
	case Tar:
		return compressPlainTar(files, outPath, opts)
	case Rar:
		return compressRar(files, outPath, opts)
	default:
		return fmt.Errorf("formato no soportado para compresión: %s", opts.Format)
	}
}

func compressTarPipe(files []string, outPath string, opts CompressOptions) error {
	var compressCmd *exec.Cmd
	ext := opts.Format.String()

	switch ext {
	case "gz":
		if opts.Progress && hasTool("pv") {
			compressCmd = exec.Command("pv", "-B", "256k")
		} else {
			optFlags := strings.Fields(opts.CompressionOpts)
			args := []string{"-c", fmt.Sprintf("-%d", fastOrSlow(opts, 1))}
			args = append(args, optFlags...)
			compressCmd = exec.Command("pigz", args...)
			if !hasTool("pigz") {
				compressCmd = exec.Command("gzip", args...)
			}
		}
	case "xz":
		optFlags := strings.Fields(opts.CompressionOpts)
		args := []string{"-c", "-T" + ncpuStr(), fmt.Sprintf("-%d", fastOrSlow(opts, 6))}
		args = append(args, optFlags...)
		compressCmd = exec.Command("xz", args...)
	case "bz2":
		bin := bzip2Bin()
		optFlags := strings.Fields(opts.CompressionOpts)
		args := []string{"-c", fmt.Sprintf("-%d", fastOrSlow(opts, 6))}
		args = append(args, optFlags...)
		compressCmd = exec.Command(bin, args...)
	case "bz3":
		args := []string{"-c", "-j" + ncpuStr(), fmt.Sprintf("-%d", fastOrSlow(opts, 6))}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		if opts.Progress && hasTool("pv") {
			compressCmd = exec.Command("pv", "-B", "256k")
		} else {
			compressCmd = exec.Command("bzip3", args...)
		}
	case "zst":
		optFlags := strings.Fields(opts.CompressionOpts)
		args := []string{"-c", "-T0", fmt.Sprintf("-%d", fastOrSlow(opts, 3))}
		args = append(args, optFlags...)
		if opts.Progress && hasTool("pv") {
			compressCmd = exec.Command("pv", "-B", "256k")
		} else {
			compressCmd = exec.Command("zstd", args...)
		}
	case "lz":
		optFlags := strings.Fields(opts.CompressionOpts)
		args := []string{"-c", fmt.Sprintf("-%d", fastOrSlow(opts, 6)), "--threads=" + ncpuStr()}
		args = append(args, optFlags...)
		compressCmd = exec.Command("plzip", args...)
	case "lz4":
		optFlags := strings.Fields(opts.CompressionOpts)
		args := []string{"-c", fmt.Sprintf("-%d", fastOrSlow(opts, 1))}
		args = append(args, optFlags...)
		compressCmd = exec.Command("lz4", args...)
	case "br":
		optFlags := strings.Fields(opts.CompressionOpts)
		args := []string{"-c", fmt.Sprintf("-%d", fastOrSlow(opts, 6))}
		args = append(args, optFlags...)
		compressCmd = exec.Command("brotli", args...)
	default:
		compressCmd = exec.Command("cat")
	}

	// Split support
	if opts.SplitSize > 0 {
		outPath = outPath + ".part"
	}

	outFile, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("Error creando archivo de salida: %w", err)
	}
	defer outFile.Close()

	var writer io.Writer = outFile

	if opts.SplitSize > 0 {
		writer = newSplitWriter(outFile, opts.SplitSize, outPath)
	}

	// Build tar pipe
	tarArgs := []string{"-cf", "-"}
	for _, excl := range opts.Exclude {
		tarArgs = append(tarArgs, "--exclude="+excl)
	}
	if opts.ChunkSize > 0 {
		tarArgs = append(tarArgs, "--record-size="+fmt.Sprintf("%dK", opts.ChunkSize))
	}
	tarArgs = append(tarArgs, "--", "")
	tarArgs = tarArgs[:len(tarArgs)-1] // remove trailing --
	tarArgs = append(tarArgs, files...)

	tarCmd := exec.Command("tar", tarArgs...)

	// For lrzip which writes directly to file instead of stdout
	if ext == "lrz" {
		args := []string{"-f", "-p", ncpuStr(), "-L", fmt.Sprintf("%d", fastOrSlow(opts, 6)), "-o", outPath}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		compressCmd = exec.Command("lrzip", args...)
		compressCmd.Stdin = nil
		if err := pipeline(writer, os.Stderr, tarCmd, compressCmd); err != nil {
			return fmt.Errorf("Error en pipeline de compresión: %w", err)
		}
		return nil
	}

	if err := pipeline(writer, os.Stderr, tarCmd, compressCmd); err != nil {
		return fmt.Errorf("Error en pipeline de compresión: %w", err)
	}

	return nil
}

func compressZip(files []string, outPath string, opts CompressOptions) error {
	args := []string{"-r"}
	if !opts.KeepOrig {
		args = append(args, "-m")
	}
	optFlags := strings.Fields(opts.CompressionOpts)
	args = append(args, optFlags...)
	args = append(args, outPath)
	args = append(args, files...)

	cmd := exec.Command("zip", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if opts.Verbose {
		WriteLogf("  $ zip %s\n", strings.Join(args, " "))
	}

	return cmd.Run()
}

func compress7z(files []string, outPath string, opts CompressOptions) error {
	sevenz := sevenzBin()
	args := []string{"a", "-mx" + fmt.Sprintf("%d", fastOrSlow(opts, 5))}
	optFlags := strings.Fields(opts.CompressionOpts)
	args = append(args, optFlags...)
	if opts.Threads > 0 {
		args = append(args, "-mmt"+fmt.Sprintf("%d", opts.Threads))
	}
	if !opts.KeepOrig {
		args = append(args, "-sdel")
	}
	args = append(args, outPath)
	args = append(args, files...)

	cmd := exec.Command(sevenz, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if opts.Verbose {
		WriteLogf("  $ %s %s\n", sevenz, strings.Join(args, " "))
	}

	return cmd.Run()
}

func compressPlainTar(files []string, outPath string, opts CompressOptions) error {
	args := []string{"-cf"}
	for _, excl := range opts.Exclude {
		args = append(args, "--exclude="+excl)
	}
	args = append(args, outPath)
	args = append(args, files...)

	cmd := exec.Command("tar", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if opts.Verbose {
		WriteLogf("  $ tar %s\n", strings.Join(args, " "))
	}

	return cmd.Run()
}

func compressRar(files []string, outPath string, opts CompressOptions) error {
	rar := rarBin()
	args := []string{"a", "-m" + fmt.Sprintf("%d", fastOrSlow(opts, 5))}
	optFlags := strings.Fields(opts.CompressionOpts)
	args = append(args, optFlags...)
	if !opts.KeepOrig {
		args = append(args, "-df")
	}
	args = append(args, outPath)
	args = append(args, files...)

	cmd := exec.Command(rar, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if opts.Verbose {
		WriteLogf("  $ %s %s\n", rar, strings.Join(args, " "))
	}

	return cmd.Run()
}

func fastOrSlow(opts CompressOptions, defaultLevel int) int {
	if opts.CompressionOpts != "" {
		return defaultLevel
	}
	return defaultLevel
}
