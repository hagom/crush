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

type DecompressOptions struct {
	DryRun      bool
	Verbose     bool
	OutputDir   string
	KeepOrig    bool
	Force       bool
	Parallel    int
	ThreadLimit int
	Progress    *ProgressTracker
}

func decompressStream(r io.Reader, w io.Writer, info FormatInfo) error {
	if info.Tool == "" {
		return fmt.Errorf("formato no soportado para pipe")
	}
	pipeFlags := strings.Fields(info.PipeFlags)
	decompCmd := exec.Command(info.Tool, pipeFlags...)
	decompCmd.Stdin = r
	decompCmd.Stdout = w
	decompCmd.Stderr = os.Stderr
	return decompCmd.Run()
}

func DoDecompress(files []string, opts DecompressOptions) error {
	if len(files) == 0 {
		return fmt.Errorf("No se especificaron archivos. Use -i archivo o pase archivos como argumento")
	}

	if opts.DryRun {
		for _, file := range files {
			WriteLogf("%s[Simulacro] Descomprimiendo: %s%s\n", Blue, file, NC)
		}
		return nil
	}

	var allFiles []string
	for _, file := range files {
		matches, err := filepath.Glob(file)
		if err != nil || len(matches) == 0 {
			if _, err := os.Stat(file); err == nil {
				allFiles = append(allFiles, file)
			} else {
				WriteLogf("%s✗ No encontrado: %s%s\n", Red, file, NC)
			}
			continue
		}
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil {
				WriteLogf("%s✗ Error: %s%s\n", Red, err, NC)
				continue
			}
			if info.IsDir() {
				WriteLogf("%s✗ Es un directorio: %s%s\n", Red, match, NC)
				continue
			}
			allFiles = append(allFiles, match)
		}
	}

	if len(allFiles) == 0 {
		return fmt.Errorf("No se encontraron archivos válidos")
	}

	var totalSize int64
	for _, f := range allFiles {
		if fi, err := os.Stat(f); err == nil {
			totalSize += fi.Size()
		}
	}
	opts.Progress = NewProgressTracker(totalSize, len(allFiles))
	pt := opts.Progress

	if opts.Parallel < 1 {
		opts.Parallel = NCPU()
	}

	fps := make([]*FileProgress, len(allFiles))
	for i, f := range allFiles {
		fi, err := os.Stat(f)
		var sz int64
		if err == nil {
			sz = fi.Size()
		}
		fps[i] = &FileProgress{Name: filepath.Base(f), Size: sz, Status: "waiting"}
	}
	opts.Progress.SetFiles(fps)

	results := make(chan error, len(allFiles))
	var successes, errors int

	if len(allFiles) == 1 {
		pt.Start()
		defer pt.Stop()

		err := decompressFile(allFiles[0], opts, fps[0])
		if err != nil {
			fps[0].Status = "error"
			WriteLogf("%s✗ %s%s\n", Red, err, NC)
			errors++
		} else {
			fps[0].Status = "done"
			successes++
		}
	} else {
		numWorkers := len(allFiles)
		if numWorkers > opts.Parallel {
			numWorkers = opts.Parallel
		}
		opts.ThreadLimit = max(1, NCPU()/numWorkers)

		WriteLogf("%sDescomprimiendo %d archivo(s) en paralelo...%s\n", Bold, len(allFiles), NC)
		WriteLogf("  Hilos: %d × %d concurrentes\n", opts.ThreadLimit, numWorkers)
		WriteLogf("\n")

		pt.Start()
		defer pt.Stop()

		sem := make(chan struct{}, numWorkers)
		var wg sync.WaitGroup
		startTime := time.Now()

		for i, file := range allFiles {
			fp := fps[i]
			wg.Add(1)
			go func(f string, fp *FileProgress) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				err := decompressFile(f, opts, fp)
				if err != nil {
					fp.Status = "error"
				} else {
					fp.Status = "done"
				}
				results <- err
			}(file, fp)
		}

		wg.Wait()
		close(results)

		for err := range results {
			if err != nil {
				WriteLogf("%s✗ %s%s\n", Red, err, NC)
				errors++
			} else {
				successes++
			}
		}

		elapsed := time.Since(startTime)
		WriteLogf("\n%sTiempo total: %v%s\n", Bold, elapsed.Round(time.Second), NC)
	}

	WriteLogf("\n")
	if errors > 0 {
		WriteLogf("%sCompletado con %d errores, %d exitosos%s\n", Yellow, errors, successes, NC)
		return fmt.Errorf("%d error(es) durante la descompresión", errors)
	}
	WriteLogf("%s✓ Descompresión completada (%d archivo(s))%s\n", Green, successes, NC)
	return nil
}

func listArchiveOutputs(file string, dir string, info FormatInfo) []string {
	lower := strings.ToLower(file)
	if strings.HasSuffix(lower, ".tar") ||
		strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") ||
		strings.HasSuffix(lower, ".tar.xz") || strings.HasSuffix(lower, ".txz") ||
		strings.HasSuffix(lower, ".tar.bz2") || strings.HasSuffix(lower, ".tbz2") ||
		strings.HasSuffix(lower, ".tar.bz3") || strings.HasSuffix(lower, ".tar.br") ||
		strings.HasSuffix(lower, ".tar.lrz") ||
		strings.HasSuffix(lower, ".tar.zst") || strings.HasSuffix(lower, ".tzst") ||
		strings.HasSuffix(lower, ".tar.lz") || strings.HasSuffix(lower, ".tlz") ||
		strings.HasSuffix(lower, ".tar.lz4") {
		if members, ok := listTarMembers(file); ok {
			return resolveOutputs(members, dir)
		}
		WriteLogf("  %s⚠ No se pudo listar %s para limpiar salidas parciales%s\n", Yellow, file, NC)
		return nil
	}
	if strings.HasSuffix(lower, ".7z") || strings.HasSuffix(lower, ".zip") {
		if members, ok := listSevenZipMembers(file); ok {
			return resolveOutputs(members, dir)
		}
		WriteLogf("  %s⚠ No se pudo listar %s para limpiar salidas parciales%s\n", Yellow, file, NC)
		return nil
	}
	if strings.HasSuffix(lower, ".rar") {
		if members, ok := listRarMembers(file); ok {
			return resolveOutputs(members, dir)
		}
		WriteLogf("  %s⚠ No se pudo listar %s para limpiar salidas parciales%s\n", Yellow, file, NC)
		return nil
	}
	return []string{filepath.Join(dir, stripCompressionExt(filepath.Base(file)))}
}

func listTarMembers(file string) ([]string, bool) {
	lower := strings.ToLower(file)
	args := []string{"-tf"}
	switch {
	case strings.HasSuffix(lower, ".tar.bz3"):
		args = []string{"-I", "bzip3 -dc", "-tf"}
	case strings.HasSuffix(lower, ".tar.br"):
		args = []string{"-I", "brotli -dc", "-tf"}
	case strings.HasSuffix(lower, ".tar.lrz"):
		args = []string{"-I", "lrzip -d -p 1 -o -", "-tf"}
	case strings.HasSuffix(lower, ".tar.lz4"):
		args = []string{"-I", "lz4 -dc", "-tf"}
	}
	args = append(args, file)
	cmd := exec.Command("tar", args...)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	_ = cmd.Run()
	var members []string
	for _, line := range strings.Split(out.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			members = append(members, line)
		}
	}
	return members, len(members) > 0
}

func listSevenZipMembers(file string) ([]string, bool) {
	sevenz := sevenzBin()
	if !hasTool(sevenz) {
		return nil, false
	}
	cmd := exec.Command(sevenz, "l", "-ba", "-slt", "--", file)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	_ = cmd.Run()
	var members []string
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Path = ") {
			members = append(members, strings.TrimPrefix(line, "Path = "))
		}
	}
	return members, len(members) > 0
}

func listRarMembers(file string) ([]string, bool) {
	tool := rarBin()
	if !hasTool(tool) {
		return nil, false
	}
	cmd := exec.Command(tool, "lb", file)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	_ = cmd.Run()
	var members []string
	for _, line := range strings.Split(out.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			members = append(members, line)
		}
	}
	return members, len(members) > 0
}

func resolveOutputs(members []string, dir string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, m := range members {
		p := strings.TrimSuffix(m, "/")
		if p == "" || strings.Contains(p, "..") || filepath.IsAbs(p) {
			continue
		}
		full := filepath.Join(dir, p)
		if seen[full] {
			continue
		}
		seen[full] = true
		out = append(out, full)
	}
	return out
}

func decompressFile(file string, opts DecompressOptions, fp *FileProgress) error {
	if opts.Progress != nil {
		opts.Progress.SetCurrentFile(file)
	}
	if fp != nil {
		fp.Status = "active"
		fp.Start = time.Now()
	}

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

	needed := EstimateUncompressedSize(file)
	if needed > 0 {
		if err := CheckDiskSpace(needed*110/100, dir, "descomprimir"); err != nil {
			return err
		}
	}

	outputs := listArchiveOutputs(file, dir, info)
	preExisting := make(map[string]bool, len(outputs))
	for _, o := range outputs {
		if _, err := os.Stat(o); err == nil {
			preExisting[o] = true
		}
	}

	if info.IsTar {
		err = decompressTar(file, dir, info, opts, fp)
	} else {
		err = decompressSingle(file, dir, info, opts, fp)
	}

	if err != nil {
		for _, o := range outputs {
			if preExisting[o] {
				continue
			}
			if rmErr := os.RemoveAll(o); rmErr == nil {
				WriteLogf("  %sSalida parcial eliminada: %s%s\n", Yellow, o, NC)
			}
		}
		return err
	}

	elapsed := time.Since(startTime)

	compressedSize := int64(0)
	if fi, err := os.Stat(file); err == nil {
		compressedSize = fi.Size()
	}

	uncompressedSize := needed
	if uncompressedSize == 0 {
		if fi, err := os.Stat(file); err == nil {
			uncompressedSize = fi.Size() * 4
		}
	}

	if !opts.KeepOrig {
		if err := os.Remove(file); err != nil {
			WriteLogf("  %s⚠ No se pudo eliminar %s: %v%s\n", Yellow, file, err, NC)
		}
	}
	var report strings.Builder
	fmt.Fprintf(&report, "\n%s=== Reporte de Descompresión ===%s\n", Green, NC)
	fmt.Fprintf(&report, "%sArchivo Origen:%s     %s%s%s\n", Blue, NC, Yellow, file, NC)
	fmt.Fprintf(&report, "%sTamaño Comprimido:%s  %s%s%s\n", Blue, NC, Red, FormatSize(compressedSize), NC)
	fmt.Fprintf(&report, "%sTamaño Descomprimido:%s %s%s%s\n", Blue, NC, Green, FormatSize(uncompressedSize), NC)
	fmt.Fprintf(&report, "%sTiempo:%s             %s%v%s\n", Blue, NC, Bold, elapsed.Round(time.Second), NC)
	fmt.Fprintf(&report, "%sHilos utilizados:%s   %s%d%s\n", Blue, NC, Bold, effectiveThreads(file), NC)
	fmt.Fprintf(&report, "%s=============================%s\n", Green, NC)
	WriteLog(report.String())

	if opts.Progress != nil {
		opts.Progress.FileDone(file)
	}

	return nil
}

func decompressTar(file string, dir string, info FormatInfo, opts DecompressOptions, fp *FileProgress) error {
	if info.Tool == "" {
			return fmt.Errorf("No se detectó herramienta para: %s", file)
		}

		WriteLogf("  → %s/\n", dir)

	if opts.Progress == nil && hasTool("pv") {
		tarExtract := exec.Command("tar", "-xf", "-", "-C", dir)
		// Build decompressor pipe: decompress -> pv -> tar -xf -
		var decompCmd *exec.Cmd
		ext := strings.ToLower(file)
		switch {
		case strings.HasSuffix(ext, ".tar.gz") || strings.HasSuffix(ext, ".tgz"):
			if hasTool("pigz") {
				decompCmd = exec.Command("pigz", "-dc", "-p", threadStr(opts.ThreadLimit), "--", file)
			} else {
				decompCmd = exec.Command("gzip", "-dc", "--", file)
			}
		case strings.HasSuffix(ext, ".tar.xz") || strings.HasSuffix(ext, ".txz"):
			decompCmd = exec.Command("xz", "-dc", "-T"+threadStr(opts.ThreadLimit), "--", file)
		case strings.HasSuffix(ext, ".tar.bz2") || strings.HasSuffix(ext, ".tbz2"):
			decompCmd = exec.Command(bzip2Bin(), "-dc", "--", file)
		case strings.HasSuffix(ext, ".tar.bz3"):
			decompCmd = exec.Command("bzip3", "-dc", "-j", threadStr(opts.ThreadLimit), "--", file)
		case strings.HasSuffix(ext, ".tar.zst") || strings.HasSuffix(ext, ".tzst"):
			decompCmd = exec.Command("zstd", "-dc", "-T"+threadStr(opts.ThreadLimit), "--", file)
		case strings.HasSuffix(ext, ".tar.lz") || strings.HasSuffix(ext, ".tlz"):
			if hasTool("plzip") {
				decompCmd = exec.Command("plzip", "-dc", "--threads="+threadStr(opts.ThreadLimit), "--", file)
			} else {
				decompCmd = exec.Command("lzip", "-dc", "--", file)
			}
		case strings.HasSuffix(ext, ".tar.lrz"):
			decompCmd = exec.Command("lrzip", "-d", "-p", threadStr(opts.ThreadLimit), "-o", "-", "--", file)
		case strings.HasSuffix(ext, ".tar.lz4"):
			decompCmd = exec.Command("lz4", "-dc", "--", file)
		case strings.HasSuffix(ext, ".tar.br"):
			decompCmd = exec.Command("brotli", "-dc", "--", file)
		case strings.HasSuffix(ext, ".tar"):
			return exec.Command("tar", "-xf", file, "-C", dir).Run()
		}

		pvArgs := []string{"-f", "-B", "256k"}
		if size := EstimateUncompressedSize(file); size > 0 {
			pvArgs = append(pvArgs, "-s", fmt.Sprintf("%d", size))
		}
		pvCmd := exec.Command("pv", pvArgs...)

		if err := pipeline(os.Stdout, os.Stderr, decompCmd, pvCmd, tarExtract); err != nil {
			return fmt.Errorf("Error extrayendo %s: %w", file, err)
		}
	} else {
		// Decompress the compression layer, writing the tar into dir
		tarName := filepath.Join(dir, filepath.Base(stripTarExt(file))+".tar")
		decompCmd, closer := pipeCmdFor(info, file)
		if closer != nil {
			defer closer.Close()
		}
		tarFile, err := os.Create(tarName)
		if err != nil {
			return fmt.Errorf("Error creando tar temporal: %w", err)
		}
		defer func() {
			tarFile.Close()
			if !opts.KeepOrig {
				os.Remove(tarName)
			}
		}()
		decompCmd.Stdout = tarFile
		decompCmd.Stderr = stderrFor(opts.Progress)
		if err := decompCmd.Run(); err != nil {
			return fmt.Errorf("Error descomprimiendo %s: %w", file, err)
		}

		if info.IsTar {
			extractCmd := exec.Command("tar", "-xf", tarName, "-C", dir)
			extractCmd.Stdout = stdoutFor(opts.Progress)
			extractCmd.Stderr = stderrFor(opts.Progress)
			if err := extractCmd.Run(); err != nil {
				return fmt.Errorf("Error extrayendo tar de %s: %w", tarName, err)
			}
		}
	}

	return nil
}

// pipeCmdFor construye el comando de descompresión por pipe. lz4 detecta el
// formato por la extensión del nombre (case-sensitive), así que con .LZ4 debe
// leer por stdin (magic). El io.Closer devuelto cierra el archivo si se abrió.
func pipeCmdFor(info FormatInfo, file string) (*exec.Cmd, io.Closer) {
	cmd := exec.Command(info.Tool, strings.Fields(info.PipeFlags)...)
	if info.Tool == "lz4" {
		in, err := os.Open(file)
		if err != nil {
			return cmd, nil
		}
		cmd.Stdin = in
		return cmd, in
	}
	cmd.Args = append(cmd.Args, "--", file)
	return cmd, nil
}

func decompressSingle(file string, dir string, info FormatInfo, opts DecompressOptions, fp *FileProgress) error {
	ext := strings.ToLower(file)

	switch {
	case strings.HasSuffix(ext, ".zip"):
		sevenz := sevenzBin()
		if hasTool(sevenz) {
			args := []string{"x", "-tzip", "-bsp1", "-mmt=" + threadStr(opts.ThreadLimit), file}
			if opts.Force {
				args = append(args, "-aoa")
			} else {
				args = append(args, "-aos")
			}
			args = append(args, fmt.Sprintf("-o%s", dir))
			cmd := exec.Command(sevenz, args...)
			if fi, err := os.Stat(file); err == nil {
				return runWithProgress(cmd, opts.Progress, fi.Size(), fp)
			}
			cmd.Stderr = stderrFor(opts.Progress)
			return cmd.Run()
		}
		args := []string{}
		if opts.Force {
			args = append(args, "-o")
		} else {
			args = append(args, "-n")
		}
		dirFlag := "-d"
		args = append(args, file, dirFlag, dir)
		cmd := exec.Command("unzip", args...)
		cmd.Stdout = stdoutFor(opts.Progress)
		cmd.Stderr = stderrFor(opts.Progress)
		return cmd.Run()

	case strings.HasSuffix(ext, ".7z"):
		args := []string{"x", "-bsp1", "-y", "-mmt=" + threadStr(opts.ThreadLimit), file, fmt.Sprintf("-o%s", dir)}
		if opts.Force {
			args = append(args, "-aoa")
		} else {
			args = append(args, "-aos")
		}
		cmd := exec.Command(info.Tool, args...)
		if fi, err := os.Stat(file); err == nil {
			return runWithProgress(cmd, opts.Progress, fi.Size(), fp)
		}
		cmd.Stderr = stderrFor(opts.Progress)
		return cmd.Run()

	case strings.HasSuffix(ext, ".rar"):
		args := []string{"x", "-y", "-mt" + threadStr(opts.ThreadLimit), file, fmt.Sprintf("%s/", dir)}
		if opts.Force {
			args = append(args, "-o+")
		} else {
			args = append(args, "-o-")
		}
		cmd := exec.Command(info.Tool, args...)
		cmd.Stdout = stdoutFor(opts.Progress)
		cmd.Stderr = stderrFor(opts.Progress)
		return cmd.Run()

	case strings.HasSuffix(ext, ".tar"):
		cmd := exec.Command("tar", "-xf", file, "-C", dir)
		cmd.Stdout = stdoutFor(opts.Progress)
		cmd.Stderr = stderrFor(opts.Progress)
		return cmd.Run()

	case strings.HasSuffix(ext, ".lrz"):
		outputPath := filepath.Join(dir, strings.TrimSuffix(filepath.Base(file), ".lrz"))
		if opts.Progress == nil && hasTool("pv") {
			pipeFlags := strings.Fields(info.PipeFlags)
			decompCmd := exec.Command(info.Tool, pipeFlags...)
			decompCmd.Args = append(decompCmd.Args, "--", file)
			outFile, err := os.Create(outputPath)
			if err != nil {
				return fmt.Errorf("Error creando archivo de salida: %w", err)
			}
			defer outFile.Close()
			pvArgs := []string{"-f", "-B", "256k"}
			if size := EstimateUncompressedSize(file); size > 0 {
				pvArgs = append(pvArgs, "-s", fmt.Sprintf("%d", size))
			}
			return pipeline(outFile, os.Stderr, decompCmd, exec.Command("pv", pvArgs...))
		}
		args := []string{"-d", "-p", threadStr(opts.ThreadLimit), "-o", outputPath, "--", file}
		cmd := exec.Command("lrzip", args...)
		cmd.Stdout = stdoutFor(opts.Progress)
		cmd.Stderr = stderrFor(opts.Progress)
		return cmd.Run()

	default:
		if opts.Progress == nil && hasTool("pv") {
			decompCmd, closer := pipeCmdFor(info, file)
			if closer != nil {
				defer closer.Close()
			}
			outputPath := filepath.Join(dir, stripCompressionExt(filepath.Base(file)))
			outFile, err := os.Create(outputPath)
			if err != nil {
				return fmt.Errorf("Error creando archivo de salida: %w", err)
			}
			defer outFile.Close()
			pvArgs := []string{"-f", "-B", "256k"}
			if size := EstimateUncompressedSize(file); size > 0 {
				pvArgs = append(pvArgs, "-s", fmt.Sprintf("%d", size))
			}
			pvCmd := exec.Command("pv", pvArgs...)
			if err := pipeline(outFile, os.Stderr, decompCmd, pvCmd); err != nil {
				return fmt.Errorf("Error descomprimiendo %s: %w", file, err)
			}
			return nil
		}
		decompCmd, closer := pipeCmdFor(info, file)
		if closer != nil {
			defer closer.Close()
		}
		outputPath := filepath.Join(dir, stripCompressionExt(filepath.Base(file)))
		outFile, err := os.Create(outputPath)
		if err != nil {
			return fmt.Errorf("Error creando archivo de salida: %w", err)
		}
		defer outFile.Close()
		decompCmd.Stdout = outFile
		decompCmd.Stderr = stderrFor(opts.Progress)
		if err := decompCmd.Run(); err != nil {
			return fmt.Errorf("Error descomprimiendo %s: %w", file, err)
		}
		return nil
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
