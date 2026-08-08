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
	Combine         bool
	ThreadLimit     int
	Progress        *ProgressTracker
}

func compressStream(r io.Reader, w io.Writer, opts CompressOptions) error {
	// ponytail: no FileProgress needed — pipe mode has no per-file tracking
	compressCmd := buildCompressCmd(opts)
	compressCmd.Stdin = r
	compressCmd.Stdout = w
	compressCmd.Stderr = stderrFor(opts.Progress)
	return augmentErr(compressCmd, compressCmd.Run())
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

	singleItem := len(files) == 1

	ext := ExtForFormat(opts.Format)

	if singleItem {
		base := filepath.Base(files[0])
		baseName := strings.TrimSuffix(base, filepath.Ext(base))
		outPath = GetUniqueName(filepath.Join(opts.OutputDir, baseName), ext)
	} else {
		baseName := "crush_" + time.Now().Format("20060102_150405")
		outPath = GetUniqueName(filepath.Join(opts.OutputDir, baseName), ext)
	}

	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("no se pudo crear directorio de salida %s: %w", opts.OutputDir, err)
	}

	if opts.DryRun {
		WriteLogf("%s[Simulacro] Comprimiendo %d archivo(s)%s\n", Blue, len(files), NC)
		if !singleItem && len(files) > 1 {
			mode := "paralelo"
			if opts.Combine {
				mode = "combinado"
			}
			WriteLogf("%s[Simulacro] Modo: %s (%d archivos × %d núcleos)%s\n", Blue, mode, len(files), NCPU(), NC)
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

	if err := CheckCompressTools(opts.Format); err != nil {
		return nil, err
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

	if err := CheckDiskSpace(estimated, opts.OutputDir, "comprimir"); err != nil {
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

	filesTotal := 1
	if !opts.Combine && len(filteredFiles) > 1 {
		filesTotal = len(filteredFiles)
	}
	opts.Progress = NewProgressTracker(totalSize, filesTotal)
	pt := opts.Progress

	if opts.Parallel > 1 && !singleItem && len(filteredFiles) > 1 && !opts.Combine {
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

		pt.Start()
		defer pt.Stop()

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

	pt.Start()
	defer pt.Stop()

	opts.TotalSize = totalSize
	_, preExistErr := os.Stat(outPath)
	err = compressItems(filteredFiles, outPath, opts)
	if err != nil {
		if preExistErr != nil {
			if rmErr := os.Remove(outPath); rmErr == nil {
				WriteLogf("  %sSalida parcial eliminada: %s%s\n", Yellow, outPath, NC)
			}
		}
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

func splitOutPath(outPath string, opts CompressOptions) string {
	if opts.SplitSize > 0 {
		return outPath + ".part"
	}
	return outPath
}

func isTarBased(f Format) bool {
	return f == Gz || f == Xz || f == Bz2 || f == Bz3 || f == Zst ||
		f == Lz || f == Lrz || f == Lz4 || f == Br
}

func compressToolName(f Format) string {
	switch f {
	case Gz:
		if hasTool("pigz") {
			return "pigz"
		}
		return "gzip"
	case Xz:
		return "xz"
	case Bz2:
		return bzip2Bin()
	case Bz3:
		return "bzip3"
	case Zst:
		return "zstd"
	case Lz:
		if hasTool("plzip") {
			return "plzip"
		}
		return "lzip"
	case Lz4:
		return "lz4"
	case Br:
		return "brotli"
	case Lrz:
		return "lrzip"
	case Zip:
		return "zip"
	case SevenZ:
		return sevenzBin()
	case Tar:
		return "tar"
	case Rar:
		return rarBin()
	default:
		return ""
	}
}

func CheckCompressTools(f Format) error {
	tool := compressToolName(f)
	if tool == "" {
		return fmt.Errorf("formato no soportado para compresión: %s", f)
	}
	if !hasTool(tool) {
		return fmt.Errorf("herramienta no instalada: %s (ejecuta crush --install-deps)", tool)
	}
	return nil
}

func compressItems(files []string, outPath string, opts CompressOptions) error {
	// ponytail: no FileProgress in sequential path — single archive
	if isTarBased(opts.Format) {
		return compressTarPipe(files, outPath, opts, nil)
	}

	switch opts.Format {
	case Zip:
		return compressZip(files, outPath, opts, nil)
	case SevenZ:
		return compress7z(files, outPath, opts, nil)
	case Tar:
		return compressPlainTar(files, outPath, opts, nil)
	case Rar:
		return compressRar(files, outPath, opts, nil)
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
		args := []string{"-c", "-T" + threadStr(opts.ThreadLimit), fmt.Sprintf("-%d", fastOrSlow(opts, 9)), "-e"}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return exec.Command("xz", args...)
	case "bz2":
		bin := bzip2Bin()
		args := []string{"-c", fmt.Sprintf("-%d", fastOrSlow(opts, 9))}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return exec.Command(bin, args...)
	case "bz3":
		args := []string{"-c", "-j" + threadStr(opts.ThreadLimit)}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return exec.Command("bzip3", args...)
	case "zst":
		args := []string{"-c", "-T" + threadStr(opts.ThreadLimit), fmt.Sprintf("-%d", fastOrSlow(opts, 19))}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return exec.Command("zstd", args...)
	case "lz":
		tool := "plzip"
		if !hasTool("plzip") {
			tool = "lzip"
		}
		args := []string{"-c", fmt.Sprintf("-%d", fastOrSlow(opts, 9)), "--threads=" + threadStr(opts.ThreadLimit)}
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
		args := []string{"-z", "-p", threadStr(opts.ThreadLimit), "-L", fmt.Sprintf("%d", fastOrSlow(opts, 9)), "-o", "-"}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return exec.Command("lrzip", args...)
	default:
		return exec.Command("cat")
	}
}

func compressSingleFile(file, outPath string, opts CompressOptions, fp *FileProgress) error {
	if opts.Progress != nil {
		opts.Progress.SetCurrentFile(file)
	}
	if isTarBased(opts.Format) {
		ext := opts.Format.String()
		if ext == "lrz" {
			args := []string{"-f", "-p", threadStr(opts.ThreadLimit), "-L", fmt.Sprintf("%d", fastOrSlow(opts, 9)), "-z", "-o", outPath, file}
			args = append(args, strings.Fields(opts.CompressionOpts)...)
			cmd := exec.Command("lrzip", args...)
			cmd.Stdout = stdoutFor(opts.Progress)
			cmd.Stderr = stderrFor(opts.Progress)
			if opts.Verbose {
				WriteLogf("  $ lrzip %s\n", strings.Join(args, " "))
			}
			err := augmentErr(cmd, cmd.Run())
			if err == nil && opts.Progress != nil {
				opts.Progress.FileDone(outPath)
				if fp != nil {
					fp.SetStatus("done")
				}
			} else if fp != nil {
				fp.SetStatus("error")
			}
			return err
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

		var writer io.Writer = outFile
		if opts.SplitSize > 0 {
			writer = newSplitWriter(outFile, opts.SplitSize, outPath)
		}
		if opts.Progress != nil {
			writer = &countingWriter{w: writer, pt: opts.Progress, fp: fp}
		}

		compressCmd := buildCompressCmd(opts)
		compressCmd.Stderr = stderrFor(opts.Progress)

		if opts.Progress == nil && hasTool("pv") {
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
			compressCmd.Stdout = writer

			if err := pvCmd.Start(); err != nil {
				return fmt.Errorf("Error iniciando pv: %w", err)
			}

			if opts.Verbose {
				WriteLogf("  $ %s | %s %s > %s\n", file, compressCmd.Path, strings.Join(compressCmd.Args[1:], " "), outPath)
			}

			compressErr := augmentErr(compressCmd, compressCmd.Run())
			pvCmd.Wait()
			if compressErr == nil && opts.Progress != nil {
				opts.Progress.FileDone(outPath)
			}
			return compressErr
		}

		compressCmd.Stdin = inFile
		compressCmd.Stdout = writer

		if opts.Verbose {
			WriteLogf("  $ %s %s < %s > %s\n", compressCmd.Path, strings.Join(compressCmd.Args[1:], " "), file, outPath)
		}

		err = augmentErr(compressCmd, compressCmd.Run())
		if err == nil && opts.Progress != nil {
			opts.Progress.FileDone(outPath)
		}
		return err
	}

	switch opts.Format {
	case Zip:
		return compressZip([]string{file}, outPath, opts, fp)
	case SevenZ:
		return compress7z([]string{file}, outPath, opts, fp)
	case Tar:
		return compressPlainTar([]string{file}, outPath, opts, fp)
	case Rar:
		return compressRar([]string{file}, outPath, opts, fp)
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

	numWorkers := len(files)
	if numWorkers > opts.Parallel {
		numWorkers = opts.Parallel
	}
	opts.ThreadLimit = max(1, NCPU()/numWorkers)

	fps := make([]*FileProgress, len(files))
	for i, f := range files {
		fi, err := os.Stat(f)
		var sz int64
		if err == nil {
			sz = fi.Size()
		}
		fps[i] = &FileProgress{Name: filepath.Base(f), Size: sz}
		fps[i].SetStatus("waiting")
	}
	opts.Progress.SetFiles(fps)

	startTime := time.Now()

	for i, f := range files {
		wg.Add(1)
		fp := fps[i]
		go func(file string, fp *FileProgress) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fp.SetStatus("active")
			fp.SetStart(time.Now())

			base := filepath.Base(file)
			baseNoExt := strings.TrimSuffix(base, filepath.Ext(base))
			outPath := GetUniqueName(filepath.Join(opts.OutputDir, baseNoExt), ext)
			outPath = splitOutPath(outPath, opts)

			fp.SetOutPath(outPath)

			mu.Lock()
			outFiles = append(outFiles, outPath)
			mu.Unlock()

			if opts.Verbose {
				WriteLogf("  %s → %s\n", file, outPath)
			}

			_, preExistErr := os.Stat(outPath)
			err := compressSingleFile(file, outPath, opts, fp)
			if err != nil {
				fp.SetStatus("error")
				if preExistErr != nil {
					if rmErr := os.Remove(outPath); rmErr == nil {
						WriteLogf("  %sSalida parcial eliminada: %s%s\n", Yellow, outPath, NC)
					}
				}
				errCh <- fmt.Errorf("%s: %w", file, err)
			} else {
				fp.SetStatus("done")
			}
		}(f, fp)
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
	WriteLogf("%sHilos:%s             %s%d × %d concurrentes%s\n", Blue, NC, Bold, opts.ThreadLimit, numWorkers, NC)
	if len(errors) > 0 {
		WriteLogf("%sErrores:%s           %s%d%s\n", Blue, NC, Red, len(errors), NC)
		for _, e := range errors {
			WriteLogf("  %s✗ %s%s\n", Red, e, NC)
		}
	} else {
		WriteLogf("%s%s✓ Todos los archivos comprimidos exitosamente%s\n", Green, Bold, NC)
	}
	WriteLogf("%s=============================%s\n", Green, NC)

	successFiles := make([]string, 0, len(files))
	for i, fp := range fps {
		if fp.Status() == "done" {
			successFiles = append(successFiles, files[i])
		}
	}

	CompressCleanupFiles = successFiles
	if !opts.KeepOrig && !opts.SkipCleanup {
		removed := removeFiles(successFiles, opts.Verbose)
		if removed > 0 {
			WriteLogf("  %sArchivos originales eliminados: %d%s\n", Yellow, removed, NC)
		}
	}

	if len(errors) > 0 {
		return outFiles, fmt.Errorf("%d error(es) en compresión paralela", len(errors))
	}

	return outFiles, nil
}

func compressTarPipe(files []string, outPath string, opts CompressOptions, fp *FileProgress) error {
	var pvCmd *exec.Cmd
	ext := opts.Format.String()

	compressCmd := buildCompressCmd(opts)

	if opts.Progress == nil && hasTool("pv") {
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

	if opts.Progress != nil {
		writer = &countingWriter{w: writer, pt: opts.Progress, fp: fp}
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
		args := []string{"-f", "-p", threadStr(opts.ThreadLimit), "-L", fmt.Sprintf("%d", fastOrSlow(opts, 9)), "-z", "-o", outPath}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		compressCmd = exec.Command("lrzip", args...)
		pipeCmds := []*exec.Cmd{tarCmd}
		if opts.Progress == nil && hasTool("pv") {
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
		if opts.Progress != nil {
			opts.Progress.FileDone(outPath)
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

	if opts.Progress != nil {
		opts.Progress.FileDone(outPath)
	}

	return nil
}

func compressZip(files []string, outPath string, opts CompressOptions, fp *FileProgress) error {
	if hasTool(sevenzBin()) {
		sevenz := sevenzBin()
		args := []string{"a", "-tzip", "-mx=9", "-bsp1", "-mmt=" + threadStr(opts.ThreadLimit)}
		optFlags := strings.Fields(opts.CompressionOpts)
		args = append(args, optFlags...)
		args = append(args, outPath)
		args = append(args, "--")
		args = append(args, files...)

		cmd := exec.Command(sevenz, args...)

		if opts.Verbose {
			WriteLogf("  $ %s %s\n", sevenz, strings.Join(args, " "))
		}

		fileSize := totalFileSize(files)
		err := runWithProgress(cmd, opts.Progress, fileSize, fp)
		if err == nil && opts.Progress != nil {
			opts.Progress.FileDone(outPath)
		}
		return err
	}

	args := []string{"-r", "-9", outPath}
	args = append(args, files...)

	cmd := exec.Command("zip", args...)
	cmd.Stdout = stdoutFor(opts.Progress)
	cmd.Stderr = stderrFor(opts.Progress)

	if opts.Verbose {
		WriteLogf("  $ zip %s\n", strings.Join(args, " "))
	}

	err := augmentErr(cmd, cmd.Run())
	if err == nil && opts.Progress != nil {
		opts.Progress.FileDone(outPath)
	}
	return err
}

func compress7z(files []string, outPath string, opts CompressOptions, fp *FileProgress) error {
	sevenz := sevenzBin()
	args := []string{"a", "-mx=9", "-md=128m", "-ms=on", "-bsp1"}
	optFlags := strings.Fields(opts.CompressionOpts)
	args = append(args, optFlags...)
	args = append(args, "-mmt="+threadStr(opts.ThreadLimit))
	args = append(args, outPath)
	args = append(args, "--")
	args = append(args, files...)

	cmd := exec.Command(sevenz, args...)

	if opts.Verbose {
		WriteLogf("  $ %s %s\n", sevenz, strings.Join(args, " "))
	}

	fileSize := totalFileSize(files)
	err := runWithProgress(cmd, opts.Progress, fileSize, fp)
	if err == nil && opts.Progress != nil {
		opts.Progress.FileDone(outPath)
	}
	return err
}

func compressPlainTar(files []string, outPath string, opts CompressOptions, fp *FileProgress) error {
	args := []string{"-cf"}
	for _, excl := range opts.Exclude {
		args = append(args, "--exclude="+excl)
	}
	args = append(args, outPath)
	args = append(args, "--")
	args = append(args, files...)

	cmd := exec.Command("tar", args...)
	cmd.Stdout = stdoutFor(opts.Progress)
	cmd.Stderr = stderrFor(opts.Progress)

	if opts.Verbose {
		WriteLogf("  $ tar %s\n", strings.Join(args, " "))
	}

	err := augmentErr(cmd, cmd.Run())
	if err == nil && opts.Progress != nil {
		opts.Progress.FileDone(outPath)
	}
	return err
}

func compressRar(files []string, outPath string, opts CompressOptions, fp *FileProgress) error {
	rar := rarBin()
	args := []string{"a", "-m" + fmt.Sprintf("%d", fastOrSlow(opts, 5)), "-mt" + threadStr(opts.ThreadLimit)}
	optFlags := strings.Fields(opts.CompressionOpts)
	args = append(args, optFlags...)
	if !opts.KeepOrig {
		args = append(args, "-df")
	}
	args = append(args, outPath)
	args = append(args, "--")
	args = append(args, files...)

	cmd := exec.Command(rar, args...)
	cmd.Stdout = stdoutFor(opts.Progress)
	cmd.Stderr = stderrFor(opts.Progress)

	if opts.Verbose {
		WriteLogf("  $ %s %s\n", rar, strings.Join(args, " "))
	}

	err := augmentErr(cmd, cmd.Run())
	if err == nil && opts.Progress != nil {
		opts.Progress.FileDone(outPath)
	}
	return err
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
