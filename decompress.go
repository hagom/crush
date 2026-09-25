package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	Password    string
	Filter      string
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

	seenFiles := make(map[string]bool)
	var uniqueFiles []string
	for _, f := range allFiles {
		base := resolveSplitBase(f)
		if !seenFiles[base] {
			seenFiles[base] = true
			uniqueFiles = append(uniqueFiles, base)
		}
	}
	allFiles = uniqueFiles

	for _, f := range allFiles {
		info, err := DetectFormat(f)
		if err == nil {
			if _, err := EnsureDecompressTool(info.Format); err != nil {
				return err
			}
		}
	}

	archiveSizes := SortByLPT(allFiles, TotalArchiveSize)

	neededByDir := make(map[string]int64)
	for _, f := range allFiles {
		targetDir := ResolveDecompressDir(f, opts.OutputDir)
		var sz int64
		if s := EstimateUncompressedSize(f); s > 0 {
			sz = s
		} else {
			sz = archiveSizes[f] * 3
		}
		neededByDir[targetDir] += sz
	}
	for targetDir, needed := range neededByDir {
		if needed > 0 {
			if err := CheckDiskSpace(needed*110/100, targetDir, "descomprimir"); err != nil {
				return err
			}
		}
	}

	var totalSize int64
	for _, f := range allFiles {
		totalSize += archiveSizes[f]
	}
	pt := opts.Progress
	if pt == nil {
		pt = NewProgressTracker(totalSize, len(allFiles))
		opts.Progress = pt
	} else {
		pt.total = totalSize
		pt.filesTotal = len(allFiles)
	}

	if opts.Parallel < 1 {
		opts.Parallel = NCPU()
	}

	fps := make([]*FileProgress, len(allFiles))
	for i, f := range allFiles {
		parts := findSplitParts(f)
		fps[i] = &FileProgress{Name: filepath.Base(f), Size: archiveSizes[f]}
		if len(parts) > 1 {
			fps[i].SetParts(len(parts), len(parts))
		}
		fps[i].SetStatus("waiting")
	}
	pt.SetFiles(fps)

	results := make(chan error, len(allFiles))
	var successes, errors int

	if len(allFiles) == 1 {
		pt.Start()
		defer pt.Stop()

		err := decompressFile(allFiles[0], opts, fps[0])
		if err != nil {
			fps[0].SetStatus("error")
			WriteLogf("%s✗ %s%s\n", Red, err, NC)
			errors++
		} else {
			fps[0].SetStatus("done")
			successes++
		}
	} else {
		totalCores := NCPU()
		maxWorkers := opts.Parallel
		if maxWorkers > 8 {
			maxWorkers = 8
		}
		if len(allFiles) < maxWorkers {
			maxWorkers = len(allFiles)
		}
		if maxWorkers < 1 {
			maxWorkers = 1
		}

		sizesSlice := make([]int64, len(allFiles))
		var totalDecompBytes int64
		for i, f := range allFiles {
			sz := archiveSizes[f]
			if sz > 0 {
				sizesSlice[i] = sz
				totalDecompBytes += sz
			}
		}

		pool := NewDynamicThreadPool(totalCores, 1, totalCores, len(allFiles), totalDecompBytes)
		var preAlloc []int
		if len(allFiles) <= maxWorkers {
			preAlloc = AllocateThreadsProportional(sizesSlice, totalCores, 1, totalCores)
		}

		WriteLogf("%sDescomprimiendo %d archivo(s) en paralelo...%s\n", Bold, len(allFiles), NC)
		WriteLogf("  Hilos: %d total (distribución adaptativa LPT)\n", totalCores)
		WriteLogf("\n")

		pt.Start()
		defer pt.Stop()

		sem := make(chan struct{}, maxWorkers)
		var wg sync.WaitGroup
		startTime := time.Now()

		for i, file := range allFiles {
			fp := fps[i]
			wg.Add(1)
			go func(f string, fp *FileProgress, idx int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				fileOpts := opts
				var th int
				if len(preAlloc) == len(allFiles) {
					th = preAlloc[idx]
				} else {
					th = pool.Acquire(archiveSizes[f])
					defer pool.Release(th)
				}
				fileOpts.ThreadLimit = th

				err := decompressFile(f, fileOpts, fp)
				if err != nil {
					fp.SetStatus("error")
				} else {
					fp.SetStatus("done")
				}
				results <- err
			}(file, fp, i)
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

func listArchiveOutputs(file string, dir string, info FormatInfo, passwords ...string) []string {
	password := ""
	if len(passwords) > 0 {
		password = passwords[0]
	}
	if info.Tool == "" {
		if fi, err := DetectFormat(file); err == nil {
			info = fi
		}
	}
	if info.IsTar || info.Format == Tar {
		if members, ok := listTarMembers(file); ok {
			return resolveOutputs(members, dir)
		}
		WriteLogf("  %s⚠ No se pudo listar %s para limpiar salidas parciales%s\n", Yellow, file, NC)
		return nil
	}
	if info.Format == SevenZ || info.Format == Zip {
		if members, ok := listSevenZipMembers(file, password); ok {
			return resolveOutputs(members, dir)
		}
		WriteLogf("  %s⚠ No se pudo listar %s para limpiar salidas parciales%s\n", Yellow, file, NC)
		return nil
	}
	if info.Format == Rar {
		if members, ok := listRarMembers(file, password); ok {
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
		args = []string{"-I", "brotli", "-tf"}
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

func listSevenZipMembers(file string, password string) ([]string, bool) {
	sevenz := sevenzBin()
	if !hasTool(sevenz) {
		return nil, false
	}
	args := []string{"l", "-ba", "-slt"}
	if password != "" {
		args = append(args, "-p"+password)
	}
	args = append(args, "--", file)
	cmd := exec.Command(sevenz, args...)
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

func listRarMembers(file string, password string) ([]string, bool) {
	tool := rarBin()
	if !hasTool(tool) {
		return nil, false
	}
	args := []string{"lb"}
	if password != "" {
		args = append(args, "-p"+password)
	}
	args = append(args, file)
	cmd := exec.Command(tool, args...)
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
	file = resolveSplitBase(file)
	parts := findSplitParts(file)

	if opts.Progress != nil {
		opts.Progress.SetCurrentFile(file)
	}
	if fp != nil {
		fp.SetStatus("active")
		fp.SetStart(time.Now())
	}

	info, err := DetectFormat(file)
	if err != nil {
		if strings.Contains(filepath.Base(file), ".part") {
			base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
			return fmt.Errorf("%s es un fragmento de división (split); concatena todas las partes antes de descomprimir (cat %s.part* > %s)",
				file, base, base)
		}
		return err
	}

	if opts.Password != "" && info.Format != SevenZ && info.Format != Zip && info.Format != Rar {
		WriteWarning("el descifrado con contraseña solo está soportado en formatos contenedor (7z, zip, rar); se ignora para %s", file)
		opts.Password = ""
	}

	if opts.DryRun {
		WriteLogf("%s[Simulacro] Descomprimiendo: %s%s\n", Blue, file, NC)
		return nil
	}

	startTime := time.Now()
	WriteLogf("%s%s%s\n", Bold, file, NC)

	dir := ResolveDecompressDir(file, opts.OutputDir)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("Error creando directorio de salida: %w", err)
	}

	compressedSize := PartsTotalSize(parts)
	if fp != nil && len(parts) > 1 {
		fp.Size = compressedSize
		fp.SetParts(len(parts), len(parts))
	}

	needed := EstimateUncompressedSize(file)
	if len(parts) > 1 && needed < compressedSize {
		needed = compressedSize * 4
	}
	if needed > 0 {
		if err := CheckDiskSpace(needed*110/100, dir, "descomprimir"); err != nil {
			return err
		}
	}

	outputs := listArchiveOutputs(file, dir, info, opts.Password)
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

	uncompressedSize := needed
	if uncompressedSize == 0 {
		uncompressedSize = compressedSize * 4
	}

	if !opts.KeepOrig {
		for _, p := range parts {
			if err := os.Remove(p); err != nil {
				WriteLogf("  %s⚠ No se pudo eliminar %s: %v%s\n", Yellow, p, err, NC)
			}
		}
		parentDir := filepath.Dir(file)
		if IsSplitPartsDir(parentDir) {
			_ = os.Remove(parentDir)
		}
	}
	var report strings.Builder
	fmt.Fprintf(&report, "\n%s=== Reporte de Descompresión ===%s\n", Green, NC)
	fmt.Fprintf(&report, "%sArchivo Origen:%s     %s%s%s\n", Blue, NC, Yellow, file, NC)
	fmt.Fprintf(&report, "%sTamaño Comprimido:%s  %s%s%s\n", Blue, NC, Red, FormatSize(compressedSize), NC)
	fmt.Fprintf(&report, "%sTamaño Descomprimido:%s %s%s%s\n", Blue, NC, Green, FormatSize(uncompressedSize), NC)
	fmt.Fprintf(&report, "%sTiempo:%s             %s%v%s\n", Blue, NC, Bold, elapsed.Round(time.Second), NC)
	fmt.Fprintf(&report, "%sHilos utilizados:%s   %s%d%s\n", Blue, NC, Bold, effectiveThreads(file, opts.ThreadLimit), NC)
	if len(parts) > 1 {
		fmt.Fprintf(&report, "%sPorciones:%s          %s%d / %d partes%s\n", Blue, NC, Bold, len(parts), len(parts), NC)
	}
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

	parts := findSplitParts(file)

	if len(parts) == 1 && opts.Progress == nil && hasTool("pv") {
		tarArgs := []string{"-xf", "-", "-C", dir}
		if opts.Filter != "" {
			tarArgs = append(tarArgs, "--wildcards", opts.Filter)
		}
		tarExtract := exec.Command("tar", tarArgs...)
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
			lz4Args := []string{"-dc"}
			if lz4SupportsThreads() && opts.ThreadLimit > 0 {
				lz4Args = append(lz4Args, "-T"+threadStr(opts.ThreadLimit))
			}
			lz4Args = append(lz4Args, "--", file)
			decompCmd = exec.Command("lz4", lz4Args...)
		case strings.HasSuffix(ext, ".tar.br"):
			decompCmd = exec.Command("brotli", "-dc", "--", file)
		case strings.HasSuffix(ext, ".tar"):
			tarArgs := []string{"-xf", file, "-C", dir}
			if opts.Filter != "" {
				tarArgs = append(tarArgs, "--wildcards", opts.Filter)
			}
			return exec.Command("tar", tarArgs...).Run()
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
		tarArgs := []string{"-xf", "-", "-C", dir}
		if opts.Filter != "" {
			tarArgs = append(tarArgs, "--wildcards", opts.Filter)
		}
		tarExtract := exec.Command("tar", tarArgs...)
		decompCmd, closer, err := pipeCmdForParts(info, parts, opts.Progress, fp, opts.ThreadLimit)
		if err != nil {
			return fmt.Errorf("Error preparando descompresión de %s: %w", file, err)
		}
		if closer != nil {
			defer closer.Close()
		}

		if err := pipeline(stdoutFor(opts.Progress), stderrFor(opts.Progress), decompCmd, tarExtract); err != nil {
			return fmt.Errorf("Error extrayendo %s: %w", file, err)
		}
	}

	return nil
}

// pipeCmdFor construye el comando de descompresión por pipe. lz4 detecta el
// formato por la extensión del nombre (case-sensitive), así que con .LZ4 debe
// leer por stdin (magic). El io.Closer devuelto cierra el archivo si se abrió.
func pipeCmdFor(info FormatInfo, file string, threads ...int) (*exec.Cmd, io.Closer) {
	th := NCPU()
	if len(threads) > 0 && threads[0] > 0 {
		th = threads[0]
	}
	flags := PipeFlagsForThreads(info, th)
	cmd := exec.Command(info.Tool, flags...)
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

type multiCloser struct {
	closers []io.Closer
}

func (mc *multiCloser) Close() error {
	var firstErr error
	for _, c := range mc.closers {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func pipeCmdForParts(info FormatInfo, parts []string, pt *ProgressTracker, fp *FileProgress, threads ...int) (*exec.Cmd, io.Closer, error) {
	th := NCPU()
	if len(threads) > 0 && threads[0] > 0 {
		th = threads[0]
	}
	flags := PipeFlagsForThreads(info, th)
	cmd := exec.Command(info.Tool, flags...)
	if len(parts) == 1 && info.Tool == "lrzip" {
		cmd.Args = append(cmd.Args, "--", parts[0])
		return cmd, nil, nil
	}

	readers := make([]io.Reader, len(parts))
	closers := make([]io.Closer, len(parts))
	for i, p := range parts {
		f, err := os.Open(p)
		if err != nil {
			for j := 0; j < i; j++ {
				_ = closers[j].Close()
			}
			return nil, nil, err
		}
		readers[i] = f
		closers[i] = f
	}

	mc := &multiCloser{closers: closers}
	multiReader := io.MultiReader(readers...)

	var inReader io.Reader = multiReader
	if pt != nil || fp != nil {
		inReader = &countingReader{r: multiReader, pt: pt, fp: fp}
	}
	cmd.Stdin = inReader
	return cmd, mc, nil
}

func pipeCmdForProgress(info FormatInfo, file string, pt *ProgressTracker, fp *FileProgress, threads ...int) (*exec.Cmd, io.Closer, error) {
	return pipeCmdForParts(info, findSplitParts(file), pt, fp, threads...)
}

func decompressSingle(file string, dir string, info FormatInfo, opts DecompressOptions, fp *FileProgress) error {
	ext := strings.ToLower(file)

	switch {
	case strings.HasSuffix(ext, ".zip"):
		sevenz := sevenzBin()
		if hasTool(sevenz) {
			args := []string{"x", "-tzip", "-bsp1", "-mmt=" + threadStr(opts.ThreadLimit)}
			if opts.Password != "" {
				args = append(args, "-p"+opts.Password)
			}
			if opts.Force {
				args = append(args, "-aoa")
			} else {
				args = append(args, "-aos")
			}
			args = append(args, file, fmt.Sprintf("-o%s", dir))
			if opts.Filter != "" {
				args = append(args, opts.Filter)
			}
			cmd := exec.Command(sevenz, args...)
			if fi, err := os.Stat(file); err == nil {
				return runWithProgress(cmd, opts.Progress, fi.Size(), fp)
			}
			cmd.Stderr = stderrFor(opts.Progress)
			return cmd.Run()
		}
		args := []string{}
		if opts.Password != "" {
			args = append(args, "-P", opts.Password)
		}
		if opts.Force {
			args = append(args, "-o")
		} else {
			args = append(args, "-n")
		}
		dirFlag := "-d"
		args = append(args, file, dirFlag, dir)
		if opts.Filter != "" {
			args = append(args, opts.Filter)
		}
		cmd := exec.Command("unzip", args...)
		cmd.Stdout = stdoutFor(opts.Progress)
		cmd.Stderr = stderrFor(opts.Progress)
		return cmd.Run()

	case strings.HasSuffix(ext, ".7z"):
		args := []string{"x", "-bsp1", "-y", "-mmt=" + threadStr(opts.ThreadLimit)}
		if opts.Password != "" {
			args = append(args, "-p"+opts.Password)
		}
		if opts.Force {
			args = append(args, "-aoa")
		} else {
			args = append(args, "-aos")
		}
		args = append(args, file, fmt.Sprintf("-o%s", dir))
		if opts.Filter != "" {
			args = append(args, opts.Filter)
		}
		cmd := exec.Command(info.Tool, args...)
		if fi, err := os.Stat(file); err == nil {
			return runWithProgress(cmd, opts.Progress, fi.Size(), fp)
		}
		cmd.Stderr = stderrFor(opts.Progress)
		return cmd.Run()

	case strings.HasSuffix(ext, ".rar"):
		args := []string{"x", "-y", "-mt" + threadStr(opts.ThreadLimit)}
		if opts.Password != "" {
			args = append(args, "-p"+opts.Password)
		}
		if opts.Force {
			args = append(args, "-o+")
		} else {
			args = append(args, "-o-")
		}
		args = append(args, file, fmt.Sprintf("%s/", dir))
		if opts.Filter != "" {
			args = append(args, opts.Filter)
		}
		cmd := exec.Command(info.Tool, args...)
		cmd.Stdout = stdoutFor(opts.Progress)
		cmd.Stderr = stderrFor(opts.Progress)
		return cmd.Run()

	case strings.HasSuffix(ext, ".tar"):
		args := []string{"-xf", file, "-C", dir}
		if opts.Filter != "" {
			args = append(args, "--wildcards", opts.Filter)
		}
		cmd := exec.Command("tar", args...)
		cmd.Stdout = stdoutFor(opts.Progress)
		cmd.Stderr = stderrFor(opts.Progress)
		return cmd.Run()

	case strings.HasSuffix(ext, ".lrz"):
		outputPath := filepath.Join(dir, strings.TrimSuffix(filepath.Base(file), ".lrz"))
		if !opts.Force {
			if _, err := os.Stat(outputPath); err == nil {
				return fmt.Errorf("el archivo de salida %s ya existe (use --force para sobrescribir)", outputPath)
			}
		}
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
			if err := pipeline(outFile, os.Stderr, decompCmd, exec.Command("pv", pvArgs...)); err != nil {
				return err
			}
			outFile.Close()
			checkAndApplyIsoExtension(outputPath)
			return nil
		}
		args := []string{"-d", "-p", threadStr(opts.ThreadLimit), "-o", outputPath, "--", file}
		cmd := exec.Command("lrzip", args...)
		cmd.Stdout = stdoutFor(opts.Progress)
		cmd.Stderr = stderrFor(opts.Progress)
		if err := cmd.Run(); err != nil {
			return err
		}
		checkAndApplyIsoExtension(outputPath)
		return nil

	default:
		outputPath := filepath.Join(dir, stripCompressionExt(filepath.Base(file)))
		if !opts.Force {
			if _, err := os.Stat(outputPath); err == nil {
				return fmt.Errorf("el archivo de salida %s ya existe (use --force para sobrescribir)", outputPath)
			}
		}
		parts := findSplitParts(file)
		if len(parts) == 1 && opts.Progress == nil && hasTool("pv") {
			decompCmd, closer := pipeCmdFor(info, file, opts.ThreadLimit)
			if closer != nil {
				defer closer.Close()
			}
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
			outFile.Close()
			checkAndApplyIsoExtension(outputPath)
			return nil
		}
		decompCmd, closer, err := pipeCmdForParts(info, parts, opts.Progress, fp, opts.ThreadLimit)
		if err != nil {
			return fmt.Errorf("Error preparando descompresión de %s: %w", file, err)
		}
		if closer != nil {
			defer closer.Close()
		}
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
		outFile.Close()
		checkAndApplyIsoExtension(outputPath)
		return nil
	}
}

func checkAndApplyIsoExtension(outputPath string) {
	if filepath.Ext(outputPath) != "" {
		return
	}
	f, err := os.Open(outputPath)
	if err != nil {
		return
	}
	buf := make([]byte, 5)
	n, err := f.ReadAt(buf, 32769)
	f.Close()
	if (err == nil || errors.Is(err, io.EOF)) && n == 5 && string(buf) == "CD001" {
		_ = os.Rename(outputPath, outputPath+".iso")
	}
}

func resolveTargetDir(outputDir, file string) string {
	return ResolveDecompressDir(file, outputDir)
}


func stripTarExt(file string) string {
	return StripTarSuffix(file)
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
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		if err != nil && !errors.Is(err, os.ErrClosed) {
			return err
		}
	}
	return nil
}

func FindDecompressibleFiles(dir string) ([]string, error) {
	if dir == "" {
		dir = "."
	}
	var files []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path != dir && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") {
			return nil
		}
		if strings.Contains(name, ".part") {
			return nil
		}
		if _, err := DetectFormat(name); err == nil {
			relPath := path
			if dir == "." {
				relPath = strings.TrimPrefix(path, "./")
			}
			files = append(files, relPath)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func PromptDecompressAll(r io.Reader, w io.Writer, files []string) (bool, error) {
	if len(files) == 0 {
		return false, nil
	}

	fmt.Fprintf(w, "%sArchivos comprimidos detectados en el directorio actual (%d):%s\n", Bold, len(files), NC)
	for _, f := range files {
		sizeStr := ""
		parts := findSplitParts(f)
		totalSz := PartsTotalSize(parts)
		if totalSz > 0 {
			sizeStr = fmt.Sprintf(" (%s)", FormatSize(totalSz))
		}
		fmt.Fprintf(w, "  • %s%s%s%s\n", Blue, f, NC, sizeStr)
	}
	fmt.Fprintf(w, "\n%s¿Desea descomprimir todos los archivos (%d)? [s/N]: %s", Yellow, len(files), NC)

	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, err
		}
		return false, nil
	}
	resp := strings.TrimSpace(scanner.Text())
	lower := strings.ToLower(resp)
	if lower == "s" || lower == "si" || lower == "sí" || lower == "y" || lower == "yes" {
		return true, nil
	}
	return false, nil
}
