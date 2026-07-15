package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type CompressOptions struct {
	Format          Format
	DryRun          bool
	Verbose         bool
	OutputDir       string
	SplitSize       int
	KeepOrig        bool
	Parallel        int
	CompressionOpts string
	ChunkSize       int
	Exclude         []string
	FromFile        string
	TarTool         string
	TotalSize       int64
	SkipCleanup     bool
}

func compressStream(r io.Reader, w io.Writer, opts CompressOptions) error {
	compressCmd := buildCompressCmd(opts)
	compressCmd.Stdin = r
	compressCmd.Stdout = w
	compressCmd.Stderr = os.Stderr
	return compressCmd.Run()
}

func DoCompress(items []string, opts CompressOptions) (outPaths []string, err error) {
	var outPath string
	if len(items) == 0 {
		return nil, fmt.Errorf("No se especificaron archivos. Use -i archivo o pase archivos como argumento")
	}

	var files []string
	if opts.FromFile != "" {
		lines, err := ReadFileLines(opts.FromFile)
		if err != nil {
			return nil, fmt.Errorf("Error leyendo archivo de lista: %w", err)
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
		return nil, fmt.Errorf("No se encontraron archivos válidos")
	}

	singleItem := false
	if len(files) == 1 {
		info, err := os.Stat(files[0])
		if err == nil && info.IsDir() {
			singleItem = true
		}
	}

	ext := ExtForFormat(opts.Format)

	if singleItem {
		base := filepath.Base(files[0])
		baseName := strings.TrimSuffix(base, filepath.Ext(base))
		outPath = filepath.Join(opts.OutputDir, GetUniqueName(baseName, ext))
	} else {
		baseName := "crush_" + time.Now().Format("20060102_150405")
		outPath = filepath.Join(opts.OutputDir, GetUniqueName(baseName, ext))
	}

	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("no se pudo crear directorio de salida %s: %w", opts.OutputDir, err)
	}

	if opts.DryRun {
		WriteLogf("%s[Simulacro] Comprimiendo %d archivo(s)%s\n", Blue, len(files), NC)
		if !singleItem && len(files) > 1 {
			WriteLogf("%s[Simulacro] Modo: paralelo (%d archivos × %d núcleos)%s\n", Blue, len(files), NCPU(), NC)
			if opts.Format == Gz || opts.Format == Xz || opts.Format == Bz2 ||
				opts.Format == Bz3 || opts.Format == Zst || opts.Format == Lz ||
				opts.Format == Lrz || opts.Format == Lz4 || opts.Format == Br {
				WriteLogf("%s[Simulacro] Formato: %s (1 archivo → 1 archivo comprimido)%s\n", Blue, opts.Format, NC)
			} else {
				WriteLogf("%s[Simulacro] Formato: %s%s\n", Blue, ext, NC)
			}
		} else {
			WriteLogf("%s[Simulacro] Salida: %s%s\n", Blue, outPath, NC)
			if opts.Format == Gz || opts.Format == Xz || opts.Format == Bz2 ||
				opts.Format == Bz3 || opts.Format == Zst || opts.Format == Lz ||
				opts.Format == Lrz || opts.Format == Lz4 || opts.Format == Br {
				WriteLogf("%s[Simulacro] Formato: tar.%s (Multi-archivo → tar pipe)%s\n", Blue, opts.Format, NC)
			} else {
				WriteLogf("%s[Simulacro] Formato: %s%s\n", Blue, ext, NC)
			}
		}
		return nil, nil
	}

	totalSize := int64(0)
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		if info.IsDir() {
			filepath.Walk(f, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				if !info.IsDir() {
					totalSize += info.Size()
				}
				return nil
			})
		} else {
			totalSize += info.Size()
		}
	}

	// Estimate compressed size per format using real-world compression ratios
	var estimated int64
	switch opts.Format {
	case SevenZ:
		// LZMA2: 5-15% typical. 20% is realistic.
		estimated = totalSize * 20 / 100
	case Rar:
		estimated = totalSize * 30 / 100
	case Zip:
		// Deflate: ~50-60% typical
		estimated = totalSize * 50 / 100
	case Tar:
		// No compression, add 10% tar overhead
		estimated = totalSize + totalSize/10
	default:
		// Tar-pipe formats (gz, xz, bz2, bz3, zst, lz, lrz, lz4, br):
		// ~10-25% typical for tar-pipe formats.
		estimated = totalSize * 20 / 100
	}
	if estimated < 1<<20 {
		estimated = 1 << 20 // at least 1MB
	}

	if err := CheckDiskSpace(estimated, opts.OutputDir); err != nil {
		return nil, err
	}

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
		return nil, fmt.Errorf("Todos los archivos fueron excluidos")
	}

	if opts.Parallel > 1 && !singleItem && len(filteredFiles) > 1 {
		totalSize = 0
		for _, f := range filteredFiles {
			info, err := os.Stat(f)
			if err == nil && !info.IsDir() {
				totalSize += info.Size()
			}
		}
		opts.TotalSize = totalSize

		parFormat := opts.Format.String()
		if opts.Format == SevenZ {
			parFormat = "7z"
		}
		WriteLogf("%sComprimiendo %d archivo(s) en paralelo...%s\n", Bold, len(filteredFiles), NC)
		WriteLogf("  Formato: %s\n", parFormat)
		WriteLogf("  Modo: %s\n", compressModeDesc(opts.Format))
		WriteLogf("\n")

		outPaths, err = compressParallel(filteredFiles, opts)
		return outPaths, err
	}

	startTime := time.Now()

	WriteLogf("%sComprimiendo %d archivo(s)...%s\n", Bold, len(filteredFiles), NC)
	WriteLogf("  Formato: %s\n", ext)
	WriteLogf("  Destino: %s\n", outPath)
	WriteLogf("  Modo: %s\n", compressModeDesc(opts.Format))
	if opts.TotalSize > 0 {
		WriteLogf("  Tamaño total: %s\n", FormatSize(totalSize))
	}
	WriteLogf("\n")

	opts.TotalSize = totalSize
	err = compressItems(filteredFiles, outPath, opts)
	if err != nil {
		return nil, err
	}

	elapsed := time.Since(startTime)

	origSize := totalSize
	finalSize := int64(0)
	if info, err := os.Stat(outPath); err == nil {
		finalSize = info.Size()
	}

	WriteLogf("\n")
	WriteLogf("%s=== Reporte de Compresión ===%s\n", Green, NC)
	WriteLogf("%sArchivo Salida:%s    %s%s%s\n", Blue, NC, Yellow, outPath, NC)
	WriteLogf("%sTamaño Original:%s   %s%s%s\n", Blue, NC, Red, FormatSize(origSize), NC)
	WriteLogf("%sTamaño Final:%s      %s%s%s\n", Blue, NC, Green, FormatSize(finalSize), NC)
	WriteLogf("%sAhorro de espacio:%s %s%s%%%s\n", Blue, NC, Green, CalcPct(origSize, finalSize), NC)
	WriteLogf("%sTiempo:%s            %s%v%s\n", Blue, NC, Bold, elapsed.Round(time.Second), NC)
	WriteLogf("%sHilos utilizados:%s  %s%d%s\n", Blue, NC, Bold, effectiveThreads(ext), NC)
	WriteLogf("%s=============================%s\n", Green, NC)

	CompressCleanupFiles = filteredFiles
	if !opts.KeepOrig && !opts.SkipCleanup {
		removed := removeFiles(filteredFiles, opts.Verbose)
		if removed > 0 {
			WriteLogf("  %sArchivos originales eliminados: %d%s\n", Yellow, removed, NC)
		}
	}
	return []string{outPath}, nil
}

var CompressCleanupFiles []string

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

func buildCompressCmd(opts CompressOptions) *exec.Cmd {
	ext := opts.Format.String()
	switch ext {
	case "gz":
		args := []string{"-c", fmt.Sprintf("-%d", fastOrSlow(opts, 9))}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		cmd := exec.Command("pigz", args...)
		if !hasTool("pigz") {
			cmd = exec.Command("gzip", args...)
		}
		return cmd
	case "xz":
		args := []string{"-c", "-T" + ncpuStr(), fmt.Sprintf("-%d", fastOrSlow(opts, 9)), "-e"}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return exec.Command("xz", args...)
	case "bz2":
		bin := bzip2Bin()
		args := []string{"-c", fmt.Sprintf("-%d", fastOrSlow(opts, 9))}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return exec.Command(bin, args...)
	case "bz3":
		args := []string{"-c", "-j" + ncpuStr()}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return exec.Command("bzip3", args...)
	case "zst":
		args := []string{"-c", "-T0", fmt.Sprintf("-%d", fastOrSlow(opts, 19))}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return exec.Command("zstd", args...)
	case "lz":
		tool := "plzip"
		if !hasTool("plzip") {
			tool = "lzip"
		}
		args := []string{"-c", fmt.Sprintf("-%d", fastOrSlow(opts, 9)), "--threads=" + ncpuStr()}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return exec.Command(tool, args...)
	case "lz4":
		args := []string{"-c", fmt.Sprintf("-%d", fastOrSlow(opts, 1))}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return exec.Command("lz4", args...)
	case "br":
		args := []string{"-c", fmt.Sprintf("-%d", fastOrSlow(opts, 11))}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return exec.Command("brotli", args...)
	case "lrz":
		args := []string{"-z", "-p", ncpuStr(), "-L", fmt.Sprintf("%d", fastOrSlow(opts, 9)), "-o", "-"}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return exec.Command("lrzip", args...)
	default:
		return exec.Command("cat")
	}
}

func compressSingleFile(file, outPath string, opts CompressOptions) error {
	isTarBased := opts.Format == Gz || opts.Format == Xz || opts.Format == Bz2 ||
		opts.Format == Bz3 || opts.Format == Zst || opts.Format == Lz ||
		opts.Format == Lrz || opts.Format == Lz4 || opts.Format == Br

	if isTarBased {
		ext := opts.Format.String()
		if ext == "lrz" {
			args := []string{"-f", "-p", ncpuStr(), "-L", fmt.Sprintf("%d", fastOrSlow(opts, 9)), "-z", "-o", outPath, file}
			args = append(args, strings.Fields(opts.CompressionOpts)...)
			cmd := exec.Command("lrzip", args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if opts.Verbose {
				WriteLogf("  $ lrzip %s\n", strings.Join(args, " "))
			}
			return cmd.Run()
		}

		inFile, err := os.Open(file)
		if err != nil {
			return fmt.Errorf("Error abriendo %s: %w", file, err)
		}
		defer inFile.Close()

		outFile, err := os.Create(outPath)
		if err != nil {
			return fmt.Errorf("Error creando %s: %w", outPath, err)
		}
		defer outFile.Close()

		compressCmd := buildCompressCmd(opts)
		compressCmd.Stderr = os.Stderr

		if hasTool("pv") {
			pvArgs := []string{"-f", "-B", "256k"}
			if fi, statErr := os.Stat(file); statErr == nil {
				pvArgs = append(pvArgs, "-s", fmt.Sprintf("%d", fi.Size()))
			}
			pvCmd := exec.Command("pv", pvArgs...)
			pvCmd.Stdin = inFile

			var err error
			compressCmd.Stdin, err = pvCmd.StdoutPipe()
			if err != nil {
				return fmt.Errorf("Error creando pipe para pv: %w", err)
			}
			compressCmd.Stdout = outFile

			if err := pvCmd.Start(); err != nil {
				return fmt.Errorf("Error iniciando pv: %w", err)
			}

			if opts.Verbose {
				WriteLogf("  $ %s | %s %s > %s\n", file, compressCmd.Path, strings.Join(compressCmd.Args[1:], " "), outPath)
			}

			compressErr := compressCmd.Run()
			pvCmd.Wait()
			return compressErr
		}

		compressCmd.Stdin = inFile
		compressCmd.Stdout = outFile

		if opts.Verbose {
			WriteLogf("  $ %s %s < %s > %s\n", compressCmd.Path, strings.Join(compressCmd.Args[1:], " "), file, outPath)
		}

		return compressCmd.Run()
	}

	switch opts.Format {
	case Zip:
		return compressZip([]string{file}, outPath, opts)
	case SevenZ:
		return compress7z([]string{file}, outPath, opts)
	case Tar:
		return compressPlainTar([]string{file}, outPath, opts)
	case Rar:
		return compressRar([]string{file}, outPath, opts)
	default:
		return fmt.Errorf("formato no soportado para compresión: %s", opts.Format)
	}
}

func compressParallel(files []string, opts CompressOptions) ([]string, error) {
	ext := opts.Format.String()
	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("no se pudo crear directorio de salida %s: %w", opts.OutputDir, err)
	}
	sem := make(chan struct{}, opts.Parallel)
	errCh := make(chan error, len(files))
	outFiles := make([]string, 0, len(files))
	var mu sync.Mutex
	var wg sync.WaitGroup

	startTime := time.Now()

	for _, f := range files {
		wg.Add(1)
		go func(file string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			base := filepath.Base(file)
			baseNoExt := strings.TrimSuffix(base, filepath.Ext(base))
			outPath := filepath.Join(opts.OutputDir, GetUniqueName(baseNoExt, ext))

			mu.Lock()
			outFiles = append(outFiles, outPath)
			mu.Unlock()

			if opts.Verbose {
				WriteLogf("  %s → %s\n", file, outPath)
			}

			err := compressSingleFile(file, outPath, opts)
			if err != nil {
				errCh <- fmt.Errorf("%s: %w", file, err)
			}
		}(f)
	}

	wg.Wait()
	close(errCh)
	close(sem)

	var errors []error
	for err := range errCh {
		errors = append(errors, err)
	}

	elapsed := time.Since(startTime)

	WriteLogf("\n")
	WriteLogf("%s=== Reporte de Compresión Paralela ===%s\n", Green, NC)
	WriteLogf("%sFormato:%s           %s%s%s\n", Blue, NC, Yellow, ext, NC)
	WriteLogf("%sArchivos:%s          %s%d%s\n", Blue, NC, Bold, len(files), NC)
	WriteLogf("%sTiempo:%s            %s%v%s\n", Blue, NC, Bold, elapsed.Round(time.Second), NC)
	WriteLogf("%sHilos:%s             %s%d (compresor) × %d archivos%s\n", Blue, NC, Bold, effectiveThreads(ext), len(files), NC)
	if len(errors) > 0 {
		WriteLogf("%sErrores:%s           %s%d%s\n", Blue, NC, Red, len(errors), NC)
		for _, e := range errors {
			WriteLogf("  %s✗ %s%s\n", Red, e, NC)
		}
	} else {
		WriteLogf("%s%s✓ Todos los archivos comprimidos exitosamente%s\n", Green, Bold, NC)
	}
	WriteLogf("%s=============================%s\n", Green, NC)

	if len(errors) > 0 {
		return outFiles, fmt.Errorf("%d error(es) en compresión paralela", len(errors))
	}

	CompressCleanupFiles = files
	if !opts.KeepOrig && !opts.SkipCleanup {
		removed := removeFiles(files, opts.Verbose)
		if removed > 0 {
			WriteLogf("  %sArchivos originales eliminados: %d%s\n", Yellow, removed, NC)
		}
	}

	return outFiles, nil
}

func compressTarPipe(files []string, outPath string, opts CompressOptions) error {
	var pvCmd *exec.Cmd
	ext := opts.Format.String()

	compressCmd := buildCompressCmd(opts)

	if hasTool("pv") {
		pvArgs := []string{"-f", "-B", "256k"}
		if opts.TotalSize > 0 {
			pvArgs = append(pvArgs, "-s", fmt.Sprintf("%d", opts.TotalSize))
		}
		pvCmd = exec.Command("pv", pvArgs...)
	}

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
		args := []string{"-f", "-p", ncpuStr(), "-L", fmt.Sprintf("%d", fastOrSlow(opts, 9)), "-z", "-o", outPath}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		compressCmd = exec.Command("lrzip", args...)
		pipeCmds := []*exec.Cmd{tarCmd}
		if hasTool("pv") {
			pvArgs := []string{"-f", "-B", "256k"}
			if opts.TotalSize > 0 {
				pvArgs = append(pvArgs, "-s", fmt.Sprintf("%d", opts.TotalSize))
			}
			pvCmd = exec.Command("pv", pvArgs...)
			pipeCmds = append(pipeCmds, pvCmd)
		}
		pipeCmds = append(pipeCmds, compressCmd)
		if err := pipeline(writer, os.Stderr, pipeCmds...); err != nil {
			return fmt.Errorf("Error en pipeline de compresión: %w", err)
		}
		return nil
	}

	pipeCmds := []*exec.Cmd{tarCmd}
	if pvCmd != nil {
		pipeCmds = append(pipeCmds, pvCmd)
	}
	pipeCmds = append(pipeCmds, compressCmd)
	if err := pipeline(writer, os.Stderr, pipeCmds...); err != nil {
		return fmt.Errorf("Error en pipeline de compresión: %w", err)
	}

	return nil
}

func compressZip(files []string, outPath string, opts CompressOptions) error {
	if hasTool(sevenzBin()) {
		sevenz := sevenzBin()
		args := []string{"a", "-tzip", "-mx=9", "-mmt=on"}
		optFlags := strings.Fields(opts.CompressionOpts)
		args = append(args, optFlags...)
		args = append(args, outPath)
		args = append(args, "--")
		args = append(args, files...)

		cmd := exec.Command(sevenz, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if opts.Verbose {
			WriteLogf("  $ %s %s\n", sevenz, strings.Join(args, " "))
		}

		return cmd.Run()
	}

	args := []string{"-r", "-9", outPath}
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
	args := []string{"a", "-mx=9", "-md=128m", "-ms=on"}
	optFlags := strings.Fields(opts.CompressionOpts)
	args = append(args, optFlags...)
	args = append(args, "-mmt=on")
	args = append(args, outPath)
	args = append(args, "--")
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
	args = append(args, "--")
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
	args := []string{"a", "-m" + fmt.Sprintf("%d", fastOrSlow(opts, 5)), "-mt" + ncpuStr()}
	optFlags := strings.Fields(opts.CompressionOpts)
	args = append(args, optFlags...)
	if !opts.KeepOrig {
		args = append(args, "-df")
	}
	args = append(args, outPath)
	args = append(args, "--")
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
	if strings.Contains(opts.CompressionOpts, "-fast") ||
		strings.Contains(opts.CompressionOpts, "--fast") {
		return 1
	}
	return defaultLevel
}

func removeFiles(files []string, verbose bool) int {
	removed := 0
	for _, f := range files {
		var err error
		if fi, statErr := os.Stat(f); statErr == nil && fi.IsDir() {
			err = os.RemoveAll(f)
		} else {
			err = os.Remove(f)
		}
		if err == nil {
			removed++
		} else if verbose {
			WriteLogf("  %sNo se pudo eliminar: %s (%v)%s\n", Yellow, f, err, NC)
		}
	}
	return removed
}
