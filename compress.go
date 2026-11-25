package main

import (
	"archive/tar"
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type CompressOptions struct {
	Format          Format
	Formats         []Format
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
	MemBudgetMB     int
	Progress        *ProgressTracker
	Hash            bool
	Password        string
	Sparse          bool
	NoSparse        bool
	Clean           bool
	Resume          bool
	CutoffTime      time.Time
}

func effectiveMemBudget(budget int) int {
	if budget > 0 {
		return budget
	}
	return getMemLimit()
}

func supportsPassword(f Format) bool {
	return f == SevenZ || f == Zip || f == Rar
}

func supportsSplit(f Format) bool {
	return f.IsStream() && f != Lrz && f != Auto
}

func archiveBaseNameForItem(f Format, item string, isDir bool) string {
	if isDir {
		return filepath.Base(item)
	}
	return f.ArchiveBaseName(item)
}

func isHeavyCompressFormat(f Format) bool {
	switch f {
	case Xz, SevenZ, Lrz, Zst, Bz3:
		return true
	default:
		return false
	}
}

var DefaultCleanExcludes = []string{
	"*.DS_Store",
	"Thumbs.db",
	"desktop.ini",
	"*.tmp",
	"*~",
	".git",
	".svn",
	".hg",
	".idea",
	".vscode",
	"node_modules",
	"__pycache__",
	"*.pyc",
	".pytest_cache",
	".build",
	"target",
}

type CompressionTask struct {
	ID              int
	File            string
	Format          Format
	Size            int64
	ThreadCap       int
	OutPath         string
	Skipped         bool
	RemoveBeforeRun bool
}

func removeArchiveAndParts(path string) bool {
	removed := os.Remove(path) == nil
	for _, p := range globSplitParts(path) {
		_ = os.Remove(p)
	}
	return removed
}

func FormatComplexityWeight(f Format) int64 {
	switch f {
	case Xz, SevenZ:
		return 4
	case Bz3, Lrz:
		return 3
	case Bz2, Br, Lz, Rar:
		return 2
	default:
		return 1
	}
}

var inspectAutoFormatFunc = InspectAutoFormat

func emitHighEntropyNotice(item string, fmtChoice Format, warned map[string]bool) {
	if warned != nil && warned[item] {
		return
	}
	if warned != nil {
		warned[item] = true
	}
	base := filepath.Base(item)
	if fmtChoice == Auto {
		WriteLogf("%sℹ '%s' ya parece estar comprimido; usando compresión rápida (lz4).%s\n", Blue, base, NC)
	} else if fmtChoice != Tar && fmtChoice != Lz4 {
		WriteLogf("%s⚠ '%s' ya parece estar comprimido o cifrado; la compresión apenas reducirá su tamaño.%s\n", Yellow, base, NC)
	}
}

func checkAndWarnHighEntropy(item string, fmtChoice Format, warned map[string]bool) {
	if warned != nil && warned[item] {
		return
	}
	if !IsHighEntropyFile(item) {
		return
	}
	emitHighEntropyNotice(item, fmtChoice, warned)
}

func resolveFormatAndWarnEntropy(item string, fmtChoice Format, warned map[string]bool) Format {
	if fmtChoice == Auto {
		detected, highEntropy := inspectAutoFormatFunc(item)
		if highEntropy {
			emitHighEntropyNotice(item, Auto, warned)
		}
		return detected
	}
	checkAndWarnHighEntropy(item, fmtChoice, warned)
	return fmtChoice
}

func isExcludedItem(name string, exclude []string) bool {
	base := filepath.Base(name)
	for _, pat := range exclude {
		if matched, _ := filepath.Match(pat, base); matched {
			return true
		}
		if matched, _ := filepath.Match(pat, name); matched {
			return true
		}
	}
	return false
}

func filterCompressItems(items []string, opts CompressOptions) []string {
	var filtered []string
	for _, f := range items {
		if isExcludedItem(f, opts.Exclude) {
			if opts.Verbose {
				WriteLogf("  %sExcluido: %s%s\n", Yellow, f, NC)
			}
			continue
		}
		if !opts.CutoffTime.IsZero() && !hasNewerFiles(f, opts.CutoffTime, opts.Exclude) {
			if opts.Verbose {
				WriteLogf("  %sℹ [omitido] '%s' no contiene cambios posteriores a %s%s\n", Blue, f, opts.CutoffTime.Format("2006-01-02 15:04:05"), NC)
			}
			continue
		}
		filtered = append(filtered, f)
	}
	return filtered
}

func collectAllNewerFiles(items []string, cutoff time.Time, exclude []string) []string {
	var collected []string
	for _, f := range items {
		if c, err := collectNewerFiles(f, cutoff, exclude); err == nil {
			collected = append(collected, c...)
		}
	}
	return collected
}

func append7zExcludes(args, exclude []string) []string {
	for _, excl := range exclude {
		args = append(args, "-xr!"+excl)
	}
	return args
}

func appendZipExcludes(args, exclude []string) []string {
	if len(exclude) == 0 {
		return args
	}
	args = append(args, "-x")
	for _, excl := range exclude {
		args = append(args, "*/"+excl+"/*", "*/"+excl, excl)
	}
	return args
}

func appendRarExcludes(args, exclude []string) []string {
	for _, excl := range exclude {
		args = append(args, "-x*/"+excl+"/*", "-x*/"+excl, "-x"+excl)
	}
	return args
}

func appendTarFilterFlags(args []string, opts CompressOptions) []string {
	for _, excl := range opts.Exclude {
		args = append(args, "--exclude="+excl)
	}
	if !opts.CutoffTime.IsZero() {
		args = append(args, "--newer-mtime="+opts.CutoffTime.Format(time.RFC3339))
	}
	return args
}

func hasNewerFiles(item string, cutoff time.Time, exclude []string) bool {
	if cutoff.IsZero() {
		return true
	}
	fi, err := os.Stat(item)
	if err != nil {
		return false
	}
	if !fi.IsDir() {
		return !isExcludedItem(item, exclude) && fi.ModTime().After(cutoff)
	}

	found := false
	_ = filepath.Walk(item, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		for _, pat := range exclude {
			if matched, _ := filepath.Match(pat, info.Name()); matched {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if !info.IsDir() && info.ModTime().After(cutoff) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func collectNewerFiles(item string, cutoff time.Time, exclude []string) ([]string, error) {
	fi, err := os.Stat(item)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		if !isExcludedItem(item, exclude) && (cutoff.IsZero() || fi.ModTime().After(cutoff)) {
			return []string{item}, nil
		}
		return nil, nil
	}

	var results []string
	err = filepath.Walk(item, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		for _, pat := range exclude {
			if matched, _ := filepath.Match(pat, info.Name()); matched {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if !info.IsDir() {
			if cutoff.IsZero() || info.ModTime().After(cutoff) {
				results = append(results, path)
			}
		}
		return nil
	})
	return results, err
}

func generateCompressionTasks(items []string, opts CompressOptions) ([]CompressionTask, error) {
	var tasks []CompressionTask
	id := 0
	warned := make(map[string]bool)

	for _, item := range items {
		if !opts.CutoffTime.IsZero() && !hasNewerFiles(item, opts.CutoffTime, opts.Exclude) {
			if opts.Verbose {
				WriteLogf("  %sℹ [omitido] '%s' no contiene cambios posteriores a %s%s\n", Blue, item, opts.CutoffTime.Format("2006-01-02 15:04:05"), NC)
			}
			continue
		}
		var formatsToUse []Format
		if len(opts.Formats) > 0 {
			seen := make(map[Format]bool)
			for _, f := range opts.Formats {
				targetFmt := resolveFormatAndWarnEntropy(item, f, warned)
				if !seen[targetFmt] {
					seen[targetFmt] = true
					formatsToUse = append(formatsToUse, targetFmt)
				}
			}
		} else {
			formatsToUse = []Format{resolveFormatAndWarnEntropy(item, opts.Format, warned)}
		}

		fi, statErr := os.Stat(item)
		isDir := (statErr == nil && fi.IsDir())
		var itemSize int64
		if statErr == nil {
			if isDir {
				itemSize, _ = GetDirSize(item)
			} else {
				itemSize = fi.Size()
			}
		}

		for _, fmtChoice := range formatsToUse {
			id++
			task := CompressionTask{
				ID:     id,
				File:   item,
				Format: fmtChoice,
				Size:   itemSize,
			}

			baseName := archiveBaseNameForItem(fmtChoice, item, isDir)
			targetDir := opts.OutputDir
			fileOpts := opts
			fileOpts.Format = fmtChoice
			if !supportsSplit(fmtChoice) {
				fileOpts.SplitSize = 0
			}
			if fileOpts.SplitSize > 0 {
				targetDir = filepath.Join(opts.OutputDir, baseName+"_parts")
			}
			ext := ExtForItem(fmtChoice, isDir)

			var outPath string
			if opts.Resume {
				canonicalPath := filepath.Join(targetDir, baseName+"."+ext)
				if info, err := os.Lstat(canonicalPath); err == nil && !info.IsDir() {
					status, testErr := testArchiveIntegrity(canonicalPath, TestOptions{Quick: true, Password: opts.Password})
					if testErr == nil && status == "OK" {
						task.OutPath = canonicalPath
						task.Skipped = true
						WriteLogf("%sℹ [reanudado] '%s' ya existe y es válido; omitiendo compresión.%s\n", Blue, canonicalPath, NC)
					} else {
						task.OutPath = canonicalPath
						task.RemoveBeforeRun = true
					}
				} else {
					task.OutPath = canonicalPath
				}
			} else {
				outPath = GetUniqueName(filepath.Join(targetDir, baseName), ext)
				task.OutPath = outPath
			}

			task.ThreadCap = resolveThreadCap(fmtChoice, itemSize)

			tasks = append(tasks, task)
		}
	}
	return tasks, nil
}

func (opts CompressOptions) UseSparse() bool {
	return !opts.NoSparse
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
	CompressCleanupFiles = nil
	var outPath string
	if len(items) == 0 && opts.FromFile == "" {
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

	if opts.Clean {
		opts.Exclude = append(opts.Exclude, DefaultCleanExcludes...)
	}

	filteredFiles := filterCompressItems(files, opts)

	if len(filteredFiles) == 0 {
		if !opts.CutoffTime.IsZero() {
			WriteLogf("%sℹ No se encontraron archivos modificados después de %s. Nada que comprimir.%s\n", Blue, opts.CutoffTime.Format("2006-01-02 15:04:05"), NC)
			return nil, nil
		}
		return nil, fmt.Errorf("Todos los archivos fueron excluidos")
	}
	files = filteredFiles

	if opts.Combine && len(opts.Formats) > 1 {
		var allOut []string
		origKeep := opts.KeepOrig
		for idx, fmtChoice := range opts.Formats {
			isLast := (idx == len(opts.Formats)-1)
			iterOpts := opts
			iterOpts.Format = fmtChoice
			iterOpts.Formats = nil
			iterOpts.FromFile = ""
			if !isLast {
				iterOpts.KeepOrig = true
			} else {
				iterOpts.KeepOrig = origKeep
			}
			out, err := DoCompress(files, iterOpts)
			if err != nil {
				return allOut, fmt.Errorf("fallo comprimiendo formato %s: %w", fmtChoice, err)
			}
			allOut = append(allOut, out...)
		}
		return allOut, nil
	}
	if len(opts.Formats) == 1 {
		opts.Format = opts.Formats[0]
		opts.Formats = nil
	}

	singleItem := len(files) == 1 && !opts.Combine && len(opts.Formats) <= 1
	singleIsDir := false
	if singleItem {
		opts.Format = resolveFormatAndWarnEntropy(files[0], opts.Format, nil)
		if fi, err := os.Stat(files[0]); err == nil && fi.IsDir() {
			singleIsDir = true
		}
	} else if opts.Combine && opts.Format == Auto {
		if hasTool(sevenzBin()) {
			opts.Format = SevenZ
		} else {
			opts.Format = Zst
		}
	}

	ext := ExtForFormat(opts.Format)
	if singleItem {
		ext = ExtForItem(opts.Format, singleIsDir)
	}

	canParallel := !singleItem && !opts.Combine

	if !canParallel {
		if opts.SplitSize > 0 && !supportsSplit(opts.Format) {
			WriteLogf("  %s⚠ split (-s) no soportado para %s (solo disponible para gz, xz, bz2, bz3, zst, lz, lz4, br y tar.*); se ignora%s\n", Yellow, opts.Format, NC)
			opts.SplitSize = 0
		}
		if opts.Password != "" && !supportsPassword(opts.Format) {
			WriteWarning("el cifrado con contraseña solo está soportado en formatos contenedor (7z, zip, rar); se ignora para %s", opts.Format)
			opts.Password = ""
		} else if opts.Password != "" {
			GetPasswordManager().AddSuccessful(opts.Password)
		}
	}

	calcOutPath := func() string {
		if singleItem {
			targetDir := opts.OutputDir
			baseName := archiveBaseNameForItem(opts.Format, files[0], singleIsDir)
			if opts.SplitSize > 0 {
				targetDir = filepath.Join(opts.OutputDir, baseName+"_parts")
			}
			if opts.Resume {
				return filepath.Join(targetDir, baseName+"."+ext)
			}
			return GetUniqueName(filepath.Join(targetDir, baseName), ext)
		}
		baseName := "crush_" + time.Now().Format("20060102_150405")
		targetDir := opts.OutputDir
		if opts.SplitSize > 0 {
			targetDir = filepath.Join(opts.OutputDir, baseName+"_parts")
		}
		if opts.Resume {
			return filepath.Join(targetDir, baseName+"."+ext)
		}
		return GetUniqueName(filepath.Join(targetDir, baseName), ext)
	}

	if opts.DryRun {
		WriteLogf("%s[Simulacro] Comprimiendo %d archivo(s)%s\n", Blue, len(files), NC)
		if canParallel {
			tasks, _ := generateCompressionTasks(files, opts)
			mode := "paralelo"
			WriteLogf("%s[Simulacro] Modo: %s (%d tareas × %d núcleos)%s\n", Blue, mode, len(tasks), NCPU(), NC)
			for _, t := range tasks {
				WriteLogf("%s[Simulacro]   %s → %s [%s]%s\n", Blue, t.File, t.OutPath, t.Format, NC)
				if !opts.Resume {
					ReleaseReservedName(t.OutPath)
				}
			}
		} else {
			simOut := calcOutPath()
			if !opts.Resume {
				ReleaseReservedName(simOut)
			}
			WriteLogf("%s[Simulacro] Salida: %s%s\n", Blue, simOut, NC)
			if opts.Format.IsStream() {
				if singleItem && !singleIsDir {
					WriteLogf("%s[Simulacro] Formato: %s (1 archivo → 1 archivo comprimido)%s\n", Blue, opts.Format, NC)
				} else {
					WriteLogf("%s[Simulacro] Formato: tar.%s (Multi-archivo → tar pipe)%s\n", Blue, opts.Format, NC)
				}
			} else {
				WriteLogf("%s[Simulacro] Formato: %s%s\n", Blue, ext, NC)
			}
		}
		return nil, nil
	}

	if opts.Format != Auto && len(opts.Formats) <= 1 {
		if err := CheckCompressTools(opts.Format); err != nil {
			return nil, err
		}
	}

	sizeCache := make(map[string]int64, len(files))
	getSize := func(path string) int64 {
		if sz, ok := sizeCache[path]; ok {
			return sz
		}
		fi, err := os.Stat(path)
		if err != nil {
			sizeCache[path] = 0
			return 0
		}
		if fi.IsDir() {
			sz, _ := GetDirSize(path)
			sizeCache[path] = sz
			return sz
		}
		sz := fi.Size()
		sizeCache[path] = sz
		return sz
	}

	totalSize := int64(0)
	for _, f := range files {
		totalSize += getSize(f)
	}

	estimated := EstimateCompressedSize(totalSize, opts.Format, files)
	if err := CheckDiskSpace(estimated, opts.OutputDir, "comprimir"); err != nil {
		return nil, err
	}

	if canParallel {
		tasks, err := generateCompressionTasks(files, opts)
		if err != nil {
			return nil, err
		}
		if len(tasks) == 0 {
			return nil, fmt.Errorf("no se generaron tareas de compresión válidas")
		}

		for _, t := range tasks {
			if err := CheckCompressTools(t.Format); err != nil {
				return nil, err
			}
		}

		warnedSplit := make(map[Format]bool)
		warnedPass := make(map[Format]bool)
		anyEncrypted := false
		for _, t := range tasks {
			if opts.SplitSize > 0 && !supportsSplit(t.Format) && !warnedSplit[t.Format] {
				warnedSplit[t.Format] = true
				WriteLogf("  %s⚠ split (-s) no soportado para %s (solo disponible para gz, xz, bz2, bz3, zst, lz, lz4, br y tar.*); se ignora%s\n", Yellow, t.Format, NC)
			}
			if opts.Password != "" {
				if supportsPassword(t.Format) {
					anyEncrypted = true
				} else if !warnedPass[t.Format] {
					warnedPass[t.Format] = true
					WriteWarning("el cifrado con contraseña solo está soportado en formatos contenedor (7z, zip, rar); se ignora para %s", t.Format)
				}
			}
		}
		if opts.Password != "" && anyEncrypted {
			GetPasswordManager().AddSuccessful(opts.Password)
		}

		totalSize = 0
		var totalEstimated int64
		for _, t := range tasks {
			totalSize += t.Size
			totalEstimated += EstimateCompressedSize(t.Size, t.Format, []string{t.File})
		}
		opts.TotalSize = totalSize

		if err := CheckDiskSpace(totalEstimated, opts.OutputDir, "comprimir"); err != nil {
			return nil, err
		}

		if opts.Progress == nil {
			opts.Progress = NewProgressTracker(totalSize, len(tasks))
		} else {
			opts.Progress.total = totalSize
			opts.Progress.filesTotal = len(tasks)
		}
		pt := opts.Progress

		hasMixedFormats := false
		if len(tasks) > 0 {
			firstFmt := tasks[0].Format
			for _, t := range tasks[1:] {
				if t.Format != firstFmt {
					hasMixedFormats = true
					break
				}
			}
		}

		if hasMixedFormats || len(opts.Formats) > 1 {
			fmtCounts := make(map[Format]int)
			for _, t := range tasks {
				fmtCounts[t.Format]++
			}
			var fmtSummary []string
			for f, count := range fmtCounts {
				fmtSummary = append(fmtSummary, fmt.Sprintf("%s (%d)", f, count))
			}
			WriteLogf("%sComprimiendo %d tarea(s) en paralelo...%s\n", Bold, len(tasks), NC)
			WriteLogf("  Formatos: %s\n", strings.Join(fmtSummary, ", "))
		} else {
			WriteLogf("%sComprimiendo %d archivo(s) en paralelo...%s\n", Bold, len(files), NC)
			WriteLogf("  Formato: %s\n", opts.Format.String())
			WriteLogf("  Modo: %s\n", compressModeDesc(opts.Format))
		}
		WriteLogf("\n")

		pt.Start()
		defer pt.Stop()

		outPaths, err = compressParallelTasks(tasks, opts, sizeCache)
		return outPaths, err
	}

	if opts.Progress == nil {
		opts.Progress = NewProgressTracker(totalSize, 1)
	}
	pt := opts.Progress

	outPath = calcOutPath()
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return nil, fmt.Errorf("no se pudo crear directorio de salida %s: %w", filepath.Dir(outPath), err)
	}

	opts.TotalSize = totalSize
	if opts.MemBudgetMB <= 0 {
		opts.MemBudgetMB = getMemLimit()
	}
	if cap := resolveThreadCapForBudget(opts.Format, totalSize, opts.MemBudgetMB); opts.ThreadLimit <= 0 || cap < opts.ThreadLimit {
		opts.ThreadLimit = cap
	}
	realOut := outPath
	if opts.Resume {
		if info, err := os.Lstat(realOut); err == nil && !info.IsDir() {
			status, testErr := testArchiveIntegrity(realOut, TestOptions{Quick: true, Password: opts.Password})
			if testErr == nil && status == "OK" {
				WriteLogf("%sℹ [reanudado] '%s' ya existe y es válido; omitiendo compresión.%s\n", Blue, realOut, NC)
				return []string{realOut}, nil
			}
			removeArchiveAndParts(realOut)
			WriteLogf("%s⚠ [reanudando] '%s' existe pero está incompleto o corrupto; recomprimiendo.%s\n", Yellow, realOut, NC)
		}
	}

	startTime := time.Now()

	WriteLogf("%sComprimiendo %d archivo(s)...%s\n", Bold, len(files), NC)
	WriteLogf("  Formato: %s\n", ext)
	WriteLogf("  Destino: %s\n", outPath)
	WriteLogf("  Modo: %s\n", compressModeDesc(opts.Format))
	if opts.TotalSize > 0 {
		WriteLogf("  Tamaño total: %s\n", FormatSize(totalSize))
	}
	WriteLogf("\n")

	pt.Start()
	defer pt.Stop()

	_, preExistErr := os.Lstat(realOut)
	if singleItem {
		err = compressSingleFile(files[0], outPath, opts, nil, totalSize)
	} else {
		err = compressItems(files, outPath, opts)
	}
	if err != nil {
		if preExistErr != nil {
			if removeArchiveAndParts(realOut) {
				WriteLogf("  %sSalida parcial eliminada: %s%s\n", Yellow, realOut, NC)
			}
			if opts.SplitSize > 0 {
				_ = os.Remove(filepath.Dir(outPath))
			}
			ReleaseReservedName(outPath)
		}
		return nil, err
	}

	elapsed := time.Since(startTime)

	allOutParts := append([]string{realOut}, globSplitParts(realOut)...)
	var primaryHash string
	if opts.Hash {
		for _, p := range allOutParts {
			h, err := WriteSHA256File(p)
			if err == nil && p == realOut {
				primaryHash = h
			}
		}
	}

	origSize := totalSize
	finalSize := PartsTotalSize(allOutParts)

	WriteLogf("\n")
	WriteLogf("%s=== Reporte de Compresión ===%s\n", Green, NC)
	WriteLogf("%sArchivo Salida:%s    %s%s%s\n", Blue, NC, Yellow, realOut, NC)
	WriteLogf("%sTamaño Original:%s   %s%s%s\n", Blue, NC, Red, FormatSize(origSize), NC)
	WriteLogf("%sTamaño Final:%s      %s%s%s\n", Blue, NC, Green, FormatSize(finalSize), NC)
	WriteLogf("%sAhorro de espacio:%s %s%s%%%s\n", Blue, NC, Green, CalcPct(origSize, finalSize), NC)
	WriteLogf("%sTiempo:%s            %s%v%s\n", Blue, NC, Bold, elapsed.Round(time.Second), NC)
	WriteLogf("%sHilos utilizados:%s  %s%d%s\n", Blue, NC, Bold, effectiveThreads(ext, opts.ThreadLimit), NC)
	if opts.SplitSize > 0 {
		totalParts := 1 + len(globSplitParts(realOut))
		WriteLogf("%sPorciones:%s          %s%d / %d partes%s\n", Blue, NC, Bold, totalParts, totalParts, NC)
	}
	if opts.Hash && primaryHash != "" {
		WriteLogf("%sSHA-256:%s           %s%s%s\n", Blue, NC, Bold, primaryHash, NC)
	}
	WriteLogf("%s=============================%s\n", Green, NC)

	CompressCleanupFiles = files
	if !opts.KeepOrig && !opts.SkipCleanup && opts.CutoffTime.IsZero() {
		removed := removeFiles(files, opts.Verbose)
		if removed > 0 {
			WriteLogf("  %sArchivos originales eliminados: %d%s\n", Yellow, removed, NC)
		}
	}
	return []string{realOut}, nil
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
		return "rar"
	default:
		return ""
	}
}

func CheckCompressTools(f Format) error {
	_, err := EnsureCompressTool(f)
	return err
}

func compressItems(files []string, outPath string, opts CompressOptions) error {
	// ponytail: no FileProgress in sequential path — single archive
	if opts.Format.IsStream() {
		return compressTarPipe(files, outPath, opts, nil)
	}

	targetFiles := files
	if !opts.CutoffTime.IsZero() {
		collected := collectAllNewerFiles(files, opts.CutoffTime, opts.Exclude)
		if len(collected) == 0 {
			return nil
		}
		targetFiles = collected
	}

	switch opts.Format {
	case Zip:
		return compressZip(targetFiles, outPath, opts, nil)
	case SevenZ:
		return compress7z(targetFiles, outPath, opts, nil)
	case Tar:
		return compressPlainTar(files, outPath, opts, nil)
	case Rar:
		return compressRar(targetFiles, outPath, opts, nil)
	default:
		return fmt.Errorf("formato no soportado para compresión: %s", opts.Format)
	}
}

func buildCompressCmd(opts CompressOptions) *exec.Cmd {
	ext := opts.Format.String()
	budget := effectiveMemBudget(opts.MemBudgetMB)
	switch ext {
	case "gz":
		level := fmt.Sprintf("-%d", fastOrSlow(opts, 9))
		if hasTool("pigz") {
			args := []string{"-c", "-p", threadStr(opts.ThreadLimit), level}
			args = append(args, strings.Fields(opts.CompressionOpts)...)
			return execCommand("pigz", args...)
		}
		args := []string{"-c", level}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return execCommand("gzip", args...)
	case "xz":
		args := []string{"-c", "-T" + threadStr(opts.ThreadLimit), fmt.Sprintf("-%d", fastOrSlow(opts, 9)), "-e"}
		if budget > 0 {
			args = append(args, "--memlimit-compress="+strconv.Itoa(max(128, budget))+"MiB")
		}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return execCommand("xz", args...)
	case "bz2":
		bin := bzip2Bin()
		args := []string{"-c"}
		if opts.ThreadLimit > 0 {
			if bin == "lbzip2" {
				args = append(args, "-n", threadStr(opts.ThreadLimit))
			} else if bin == "pbzip2" {
				args = append(args, "-p"+threadStr(opts.ThreadLimit))
			}
		}
		args = append(args, fmt.Sprintf("-%d", fastOrSlow(opts, 9)))
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return execCommand(bin, args...)
	case "bz3":
		blockSize := "64"
		if budget > 0 && budget < 512 {
			blockSize = "16"
		} else if budget > 0 && budget < 1024 {
			blockSize = "32"
		}
		args := []string{"-c", "-b", blockSize, "-j", threadStr(opts.ThreadLimit)}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return execCommand("bzip3", args...)
	case "zst":
		var args []string
		if budget > 0 && budget < 1024 && opts.CompressionOpts == "" {
			level := 15
			if budget >= 512 {
				level = 19
			}
			args = []string{"-c", "-T" + threadStr(opts.ThreadLimit), fmt.Sprintf("-%d", level)}
		} else {
			args = []string{"-c", "-T" + threadStr(opts.ThreadLimit), "--ultra", fmt.Sprintf("-%d", fastOrSlow(opts, 22))}
		}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return execCommand("zstd", args...)
	case "lz":
		tool := "plzip"
		if !hasTool("plzip") {
			tool = "lzip"
		}
		args := []string{"-c", fmt.Sprintf("-%d", fastOrSlow(opts, 9))}
		if tool == "plzip" {
			args = append(args, "--threads="+threadStr(opts.ThreadLimit))
		}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return execCommand(tool, args...)
	case "lz4":
		args := []string{"-c"}
		if lz4SupportsThreads() && opts.ThreadLimit > 0 {
			args = append(args, "-T"+threadStr(opts.ThreadLimit))
		}
		args = append(args, fmt.Sprintf("-%d", fastOrSlow(opts, 9)))
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return execCommand("lz4", args...)
	case "br":
		args := []string{"-c", "-q", fmt.Sprintf("%d", fastOrSlow(opts, 11))}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return execCommand("brotli", args...)
	case "lrz":
		args := []string{"-z", "-p", threadStr(opts.ThreadLimit), "-L", fmt.Sprintf("%d", fastOrSlow(opts, 9)), "-o", "-"}
		if budget > 0 {
			args = append(args, "-m", strconv.Itoa(max(1, budget/100)))
		}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		return execCommand("lrzip", args...)
	default:
		return execCommand("cat")
	}
}

func compressSingleFile(file, outPath string, opts CompressOptions, fp *FileProgress, precalcSize ...int64) error {
	var fileSize int64
	if len(precalcSize) > 0 && precalcSize[0] > 0 {
		fileSize = precalcSize[0]
	} else if fp != nil && fp.Size > 0 {
		fileSize = fp.Size
	}

	if opts.Progress != nil {
		opts.Progress.SetCurrentFile(file)
	}

	fi, statErr := os.Stat(file)
	if statErr == nil && fi.IsDir() {
		if opts.Format.IsStream() {
			return compressTarPipe([]string{file}, outPath, opts, fp)
		}
		targetFiles := []string{file}
		if !opts.CutoffTime.IsZero() {
			collected, err := collectNewerFiles(file, opts.CutoffTime, opts.Exclude)
			if err != nil {
				return err
			}
			if len(collected) == 0 {
				return nil
			}
			targetFiles = collected
		}
		switch opts.Format {
		case Zip:
			return compressZip(targetFiles, outPath, opts, fp)
		case SevenZ:
			return compress7z(targetFiles, outPath, opts, fp, fileSize)
		case Tar:
			return compressPlainTar([]string{file}, outPath, opts, fp)
		case Rar:
			return compressRar(targetFiles, outPath, opts, fp)
		default:
			return fmt.Errorf("formato no soportado para compresión de directorio: %s", opts.Format)
		}
	}

	if opts.Format.IsStream() {
		ext := opts.Format.String()
		if ext == "lrz" {
			args := []string{"-f", "-p", threadStr(opts.ThreadLimit), "-L", fmt.Sprintf("%d", fastOrSlow(opts, 9)), "-z"}
			if budget := effectiveMemBudget(opts.MemBudgetMB); budget > 0 {
				args = append(args, "-m", strconv.Itoa(max(1, budget/100)))
			}
			args = append(args, strings.Fields(opts.CompressionOpts)...)
			args = append(args, "-o", safeFlagArg(outPath), "--", file)
			cmd := execCommand("lrzip", args...)
			cmd.Stdout = stdoutFor(opts.Progress)
			cmd.Stderr = stderrFor(opts.Progress)
			if opts.Verbose {
				WriteLogf("  $ lrzip %s\n", strings.Join(args, " "))
			}
			err := augmentErr(cmd, cmd.Run())
			if err == nil {
				if opts.Progress != nil {
					opts.Progress.FileDone(outPath)
				}
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

		var writer io.WriteCloser = outFile
		if opts.SplitSize > 0 {
			writer = newSplitWriter(outFile, opts.SplitSize, outPath)
		}
		writerClosed := false
		defer func() {
			if !writerClosed {
				_ = writer.Close()
			}
		}()

		var inReader io.Reader = inFile
		if opts.Progress != nil {
			inReader = &countingReader{r: inFile, pt: opts.Progress, fp: fp}
		}

		compressCmd := buildCompressCmd(opts)
		compressCmd.Stderr = stderrFor(opts.Progress)

		if opts.Progress == nil && hasTool("pv") {
			pvArgs := []string{"-f", "-B", "256k"}
			if fi, statErr := os.Stat(file); statErr == nil {
				pvArgs = append(pvArgs, "-s", fmt.Sprintf("%d", fi.Size()))
			}
			pvCmd := execCommand("pv", pvArgs...)
			pvCmd.Stdin = inFile
			compressCmd.Stdout = writer

			if opts.Verbose {
				WriteLogf("  $ %s | %s %s > %s\n", file, compressCmd.Path, strings.Join(compressCmd.Args[1:], " "), outPath)
			}

			pvErr, compressErr := runAsyncPipePair(pvCmd, compressCmd, nil)
			writerClosed = true
			closeErr := writer.Close()
			if compressErr != nil {
				return compressErr
			}
			if pvErr != nil && pvCmd.Process == nil {
				return fmt.Errorf("Error iniciando pv: %w", pvErr)
			}
			if closeErr != nil {
				return fmt.Errorf("Error cerrando %s: %w", outPath, closeErr)
			}
			return nil
		}

		compressCmd.Stdin = inReader
		compressCmd.Stdout = writer

		if opts.Verbose {
			WriteLogf("  $ %s %s < %s > %s\n", compressCmd.Path, strings.Join(compressCmd.Args[1:], " "), file, outPath)
		}

		err = augmentErr(compressCmd, compressCmd.Run())
		writerClosed = true
		closeErr := writer.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return fmt.Errorf("Error cerrando %s: %w", outPath, closeErr)
		}
		if opts.Progress != nil {
			opts.Progress.FileDone(outPath)
		}
		return nil
	}

	switch opts.Format {
	case Zip:
		return compressZip([]string{file}, outPath, opts, fp)
	case SevenZ:
		return compress7z([]string{file}, outPath, opts, fp, fileSize)
	case Tar:
		return compressPlainTar([]string{file}, outPath, opts, fp)
	case Rar:
		return compressRar([]string{file}, outPath, opts, fp)
	default:
		return fmt.Errorf("formato no soportado para compresión: %s", opts.Format)
	}
}

func compressParallel(files []string, opts CompressOptions, precalcSizes ...map[string]int64) ([]string, error) {
	var sizeCache map[string]int64
	if len(precalcSizes) > 0 && precalcSizes[0] != nil {
		sizeCache = precalcSizes[0]
	}
	fileSizes := SortByLPT(files, func(f string) int64 {
		if sizeCache != nil {
			if sz, ok := sizeCache[f]; ok {
				return sz
			}
		}
		if fi, err := os.Stat(f); err == nil {
			if fi.IsDir() {
				sz, _ := GetDirSize(f)
				return sz
			}
			return fi.Size()
		}
		return -1
	})

	tasks, err := generateCompressionTasks(files, opts)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		if sz, ok := fileSizes[tasks[i].File]; ok && tasks[i].Size <= 0 {
			tasks[i].Size = sz
		}
	}
	return compressParallelTasks(tasks, opts, fileSizes)
}

func compressParallelTasks(tasks []CompressionTask, opts CompressOptions, precalcSizes ...map[string]int64) ([]string, error) {
	if len(tasks) == 0 {
		return nil, nil
	}

	allSkipped := true
	for _, t := range tasks {
		if !t.Skipped {
			allSkipped = false
			break
		}
	}
	if allSkipped {
		var outPaths []string
		for _, t := range tasks {
			outPaths = append(outPaths, t.OutPath)
		}
		WriteLogf("%s✓ Todos los archivos ya estaban completados. Nada que reanudar.%s\n", Green, NC)
		return outPaths, nil
	}

	var sizeCache map[string]int64
	if len(precalcSizes) > 0 && precalcSizes[0] != nil {
		sizeCache = precalcSizes[0]
	}
	for i := range tasks {
		if tasks[i].Size <= 0 && sizeCache != nil {
			if sz, ok := sizeCache[tasks[i].File]; ok {
				tasks[i].Size = sz
			}
		}
	}

	hasMixedFormats := false
	if len(tasks) > 0 {
		firstFmt := tasks[0].Format
		for _, t := range tasks[1:] {
			if t.Format != firstFmt {
				hasMixedFormats = true
				break
			}
		}
	}

	if hasMixedFormats || len(opts.Formats) > 1 {
		sort.SliceStable(tasks, func(i, j int) bool {
			weightI := tasks[i].Size * FormatComplexityWeight(tasks[i].Format)
			weightJ := tasks[j].Size * FormatComplexityWeight(tasks[j].Format)
			if weightI != weightJ {
				return weightI > weightJ
			}
			return tasks[i].Size > tasks[j].Size
		})
	} else {
		sort.SliceStable(tasks, func(i, j int) bool {
			return tasks[i].Size > tasks[j].Size
		})
	}

	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("no se pudo crear directorio de salida %s: %w", opts.OutputDir, err)
	}

	totalCores := NCPU()
	maxWorkers := opts.Parallel
	anyMultiThreaded := false
	anyHeavy := false
	for _, t := range tasks {
		if FormatMaxThreads(t.Format) > 1 {
			anyMultiThreaded = true
		}
		if isHeavyCompressFormat(t.Format) {
			anyHeavy = true
		}
	}
	isRot := false
	if len(tasks) > 0 {
		isRot = IsRotational(tasks[0].File)
	}
	if !isRot && opts.OutputDir != "" {
		isRot = IsRotational(opts.OutputDir)
	}

	if isRot {
		if maxWorkers > 2 {
			maxWorkers = 2
		}
	} else if anyMultiThreaded && maxWorkers > 8 {
		maxWorkers = 8
	}
	memTotal := effectiveMemBudget(opts.MemBudgetMB)
	if memTotal > 0 && anyHeavy {
		memMaxWorkers := max(1, memTotal/512)
		if maxWorkers > memMaxWorkers {
			maxWorkers = memMaxWorkers
		}
	}
	if len(tasks) < maxWorkers {
		maxWorkers = len(tasks)
	}
	if maxWorkers < 1 {
		maxWorkers = 1
	}

	var workerBudget int
	if memTotal > 0 {
		workerBudget = max(128, memTotal/maxWorkers)
	}

	sizesSlice := make([]int64, len(tasks))
	caps := make([]int, len(tasks))
	var totalBatchSize int64
	for i, t := range tasks {
		sz := t.Size
		if sz > 0 {
			sizesSlice[i] = sz
			totalBatchSize += sz
		}
		c := t.ThreadCap
		if workerBudget > 0 {
			budgetCap := resolveThreadCapForBudget(t.Format, t.Size, workerBudget)
			if c <= 0 || budgetCap < c {
				c = budgetCap
			}
		}
		if c < 1 {
			c = 1
		}
		caps[i] = c
	}

	pool := NewDynamicThreadPool(totalCores, 1, totalCores, len(tasks), totalBatchSize, maxWorkers)
	var preAlloc []int
	if len(tasks) <= maxWorkers {
		preAlloc = AllocateThreadsProportionalWithCaps(sizesSlice, totalCores, 1, caps)
	}

	sem := make(chan struct{}, maxWorkers)
	errCh := make(chan error, len(tasks))
	outFiles := make([]string, 0, len(tasks))
	var mu sync.Mutex
	var wg sync.WaitGroup

	pendingTasks := make(map[string]*atomic.Int32)
	for _, t := range tasks {
		if _, ok := pendingTasks[t.File]; !ok {
			pendingTasks[t.File] = &atomic.Int32{}
		}
		pendingTasks[t.File].Add(1)
	}
	var failedFiles sync.Map
	var removedMu sync.Mutex
	var removedCount int

	fps := make([]*FileProgress, len(tasks))
	for i, t := range tasks {
		label := filepath.Base(t.File)
		if len(opts.Formats) > 1 || hasMixedFormats {
			label = fmt.Sprintf("%s [%s]", filepath.Base(t.File), t.Format.String())
		}
		var sz int64
		if t.Size >= 0 {
			sz = t.Size
		}
		fps[i] = &FileProgress{Name: label, Size: sz}
		fps[i].SetStatus("waiting")
	}
	if opts.Progress != nil {
		opts.Progress.SetFiles(fps)
	}

	startTime := time.Now()
	var totalOrigBytes, totalFinalBytes atomic.Int64

	for i, task := range tasks {
		wg.Add(1)
		fp := fps[i]
		go func(t CompressionTask, fp *FileProgress, idx int) {
			defer wg.Done()
			if t.Skipped {
				fp.SetStatus("done")
				mu.Lock()
				outFiles = append(outFiles, t.OutPath)
				mu.Unlock()
				return
			}
			sem <- struct{}{}
			defer func() { <-sem }()
			fp.SetStatus("active")
			fp.SetStart(time.Now())

			fileOpts := opts
			fileOpts.Format = t.Format
			if !supportsSplit(t.Format) {
				fileOpts.SplitSize = 0
			}
			if !supportsPassword(t.Format) {
				fileOpts.Password = ""
			}
			if workerBudget > 0 {
				fileOpts.MemBudgetMB = workerBudget
			}
			var th int
			if len(preAlloc) == len(tasks) {
				th = preAlloc[idx]
			} else {
				th = pool.Acquire(t.Size, caps[idx])
				defer pool.Release(th)
			}
			fileOpts.ThreadLimit = th

			outPath := t.OutPath
			if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
				fp.SetStatus("error")
				failedFiles.Store(t.File, true)
				errCh <- fmt.Errorf("%s: %w", t.File, err)
				pendingTasks[t.File].Add(-1)
				return
			}
			if t.RemoveBeforeRun {
				removeArchiveAndParts(outPath)
				WriteLogf("%s⚠ [reanudando] '%s' existe pero está incompleto o corrupto; recomprimiendo.%s\n", Yellow, outPath, NC)
			}
			fp.SetOutPath(outPath)

			if opts.Verbose {
				WriteLogf("  %s → %s (formato: %s, hilos: %d)\n", t.File, outPath, t.Format, th)
			}

			_, preExistErr := os.Lstat(outPath)
			err := compressSingleFile(t.File, outPath, fileOpts, fp, t.Size)
			if err != nil {
				fp.SetStatus("error")
				failedFiles.Store(t.File, true)
				if preExistErr != nil {
					if removeArchiveAndParts(outPath) {
						WriteLogf("  %sSalida parcial eliminada: %s%s\n", Yellow, outPath, NC)
					}
					if fileOpts.SplitSize > 0 {
						_ = os.Remove(filepath.Dir(outPath))
					}
					ReleaseReservedName(outPath)
				}
				errCh <- fmt.Errorf("%s [%s]: %w", t.File, t.Format, err)
			} else {
				mu.Lock()
				outFiles = append(outFiles, outPath)
				mu.Unlock()
				allOutParts := append([]string{outPath}, globSplitParts(outPath)...)
				if opts.Hash {
					for _, p := range allOutParts {
						_, _ = WriteSHA256File(p)
					}
				}
				totalOrigBytes.Add(t.Size)
				totalFinalBytes.Add(PartsTotalSize(allOutParts))
				fp.SetStatus("done")
			}

			remaining := pendingTasks[t.File].Add(-1)
			if remaining == 0 {
				if _, failed := failedFiles.Load(t.File); !failed {
					if !opts.KeepOrig && !opts.SkipCleanup && opts.CutoffTime.IsZero() {
						if err := os.RemoveAll(t.File); err == nil {
							removedMu.Lock()
							removedCount++
							removedMu.Unlock()
						}
					}
				}
			}
		}(task, fp, i)
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
	if hasMixedFormats || len(opts.Formats) > 1 {
		fmtCounts := make(map[Format]int)
		for _, t := range tasks {
			fmtCounts[t.Format]++
		}
		var fmtSummary []string
		for f, count := range fmtCounts {
			fmtSummary = append(fmtSummary, fmt.Sprintf("%s (%d)", f, count))
		}
		WriteLogf("%sFormatos:%s          %s%s%s\n", Blue, NC, Yellow, strings.Join(fmtSummary, ", "), NC)
		WriteLogf("%sTareas:%s            %s%d%s\n", Blue, NC, Bold, len(tasks), NC)
	} else {
		WriteLogf("%sFormato:%s           %s%s%s\n", Blue, NC, Yellow, tasks[0].Format.String(), NC)
		WriteLogf("%sArchivos:%s          %s%d%s\n", Blue, NC, Bold, len(tasks), NC)
	}
	origSize := totalOrigBytes.Load()
	finalSize := totalFinalBytes.Load()
	if origSize > 0 {
		WriteLogf("%sTamaño Original:%s   %s%s%s\n", Blue, NC, Red, FormatSize(origSize), NC)
		WriteLogf("%sTamaño Final:%s      %s%s%s\n", Blue, NC, Green, FormatSize(finalSize), NC)
		WriteLogf("%sAhorro de espacio:%s %s%s%%%s\n", Blue, NC, Green, CalcPct(origSize, finalSize), NC)
	}
	WriteLogf("%sTiempo:%s            %s%v%s\n", Blue, NC, Bold, elapsed.Round(time.Second), NC)
	WriteLogf("%sHilos:%s             %s%d total (distribución adaptativa LPT)%s\n", Blue, NC, Bold, totalCores, NC)
	if opts.SplitSize > 0 {
		totalParts := 0
		for _, out := range outFiles {
			totalParts += 1 + len(globSplitParts(out))
		}
		WriteLogf("%sPorciones:%s          %s%d / %d partes%s\n", Blue, NC, Bold, totalParts, totalParts, NC)
	}
	if opts.Hash {
		WriteLogf("%sChecksums:%s         %s.sha256 generados%s\n", Blue, NC, Bold, NC)
	}
	if len(errors) > 0 {
		WriteLogf("%sErrores:%s           %s%d%s\n", Blue, NC, Red, len(errors), NC)
		for _, e := range errors {
			WriteLogf("  %s✗ %s%s\n", Red, e, NC)
		}
	} else {
		WriteLogf("%s%s✓ Todas las tareas completadas exitosamente%s\n", Green, Bold, NC)
	}
	WriteLogf("%s=============================%s\n", Green, NC)

	var successFiles []string
	for file := range pendingTasks {
		if _, failed := failedFiles.Load(file); !failed {
			successFiles = append(successFiles, file)
		}
	}
	CompressCleanupFiles = successFiles
	if removedCount > 0 {
		WriteLogf("  %sArchivos originales eliminados: %d%s\n", Yellow, removedCount, NC)
	}

	if len(errors) > 0 {
		return outFiles, fmt.Errorf("%d error(es) en compresión paralela", len(errors))
	}

	return outFiles, nil
}

func compressTarPipe(files []string, outPath string, opts CompressOptions, fp *FileProgress) error {
	var pvCmd *exec.Cmd
	ext := opts.Format.String()

	if opts.Progress == nil && hasTool("pv") {
		pvArgs := []string{"-f", "-B", "256k"}
		if opts.TotalSize > 0 {
			pvArgs = append(pvArgs, "-s", fmt.Sprintf("%d", opts.TotalSize))
		}
		pvCmd = execCommand("pv", pvArgs...)
	}

	tarArgs := []string{"-cf", "-"}
	if opts.UseSparse() {
		tarArgs = append(tarArgs, "--sparse")
	}
	tarArgs = appendTarFilterFlags(tarArgs, opts)
	if opts.ChunkSize > 0 {
		tarArgs = append(tarArgs, "--record-size="+fmt.Sprintf("%dK", opts.ChunkSize))
	}
	tarArgs = append(tarArgs, "--")
	tarArgs = append(tarArgs, files...)

	tarCmd := execCommand("tar", tarArgs...)

	// lrzip writes the archive directly to -o outPath while emitting progress on stdout
	if ext == "lrz" {
		args := []string{"-f", "-p", threadStr(opts.ThreadLimit), "-L", fmt.Sprintf("%d", fastOrSlow(opts, 9)), "-z"}
		if budget := effectiveMemBudget(opts.MemBudgetMB); budget > 0 {
			args = append(args, "-m", strconv.Itoa(max(1, budget/100)))
		}
		args = append(args, strings.Fields(opts.CompressionOpts)...)
		args = append(args, "-o", safeFlagArg(outPath))
		compressCmd := execCommand("lrzip", args...)
		pipeCmds := []*exec.Cmd{tarCmd}
		if pvCmd != nil {
			pipeCmds = append(pipeCmds, pvCmd)
		}
		pipeCmds = append(pipeCmds, compressCmd)
		if err := pipeline(stdoutFor(opts.Progress), stderrFor(opts.Progress), pipeCmds...); err != nil {
			return fmt.Errorf("Error en pipeline de compresión: %w", err)
		}
		if opts.Progress != nil {
			opts.Progress.FileDone(outPath)
		}
		return nil
	}

	compressCmd := buildCompressCmd(opts)

	outFile, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("Error creando archivo de salida: %w", err)
	}

	var writer io.WriteCloser = outFile
	if opts.SplitSize > 0 {
		writer = newSplitWriter(outFile, opts.SplitSize, outPath)
	}
	writerClosed := false
	defer func() {
		if !writerClosed {
			_ = writer.Close()
		}
	}()

	if opts.Progress != nil && pvCmd == nil {
		compressCmd.Stdout = writer
		compressCmd.Stderr = stderrFor(opts.Progress)
		tarCmd.Stderr = stderrFor(opts.Progress)

		if opts.Verbose {
			WriteLogf("  $ tar %s | %s > %s\n", strings.Join(tarArgs, " "), strings.Join(compressCmd.Args, " "), outPath)
		}

		tarErr, compErr := runAsyncPipePair(tarCmd, compressCmd, func(r io.Reader) io.Reader {
			return &countingReader{r: r, pt: opts.Progress, fp: fp}
		})
		writerClosed = true
		closeErr := writer.Close()

		if compErr != nil {
			return fmt.Errorf("Error en compresión: %w", compErr)
		}
		if tarErr != nil {
			return fmt.Errorf("Error en tar: %w", tarErr)
		}
		if closeErr != nil {
			return fmt.Errorf("Error cerrando archivo de salida: %w", closeErr)
		}

		opts.Progress.FileDone(outPath)
		return nil
	}

	pipeCmds := []*exec.Cmd{tarCmd}
	if pvCmd != nil {
		pipeCmds = append(pipeCmds, pvCmd)
	}
	pipeCmds = append(pipeCmds, compressCmd)
	if err := pipeline(writer, stderrFor(opts.Progress), pipeCmds...); err != nil {
		return fmt.Errorf("Error en pipeline de compresión: %w", err)
	}
	writerClosed = true
	if err := writer.Close(); err != nil {
		return fmt.Errorf("Error cerrando archivo de salida: %w", err)
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
		if opts.Password != "" {
			args = append(args, "-p"+opts.Password)
		}
		args = append7zExcludes(args, opts.Exclude)
		optFlags := strings.Fields(opts.CompressionOpts)
		args = append(args, optFlags...)
		args = append(args, safeFlagArg(outPath), "--")
		args = append(args, files...)

		cmd := execCommand(sevenz, args...)

		if opts.Verbose {
			WriteLogf("  $ %s %s\n", sevenz, strings.Join(redactArgs(args), " "))
		}

		fileSize := totalFileSize(files)
		err := runWithProgress(cmd, opts.Progress, fileSize, fp)
		if err == nil && opts.Progress != nil {
			opts.Progress.FileDone(outPath)
		}
		return err
	}

	args := []string{"-r", "-9"}
	if opts.Password != "" {
		args = append(args, "-P", opts.Password)
	}
	args = append(args, safeFlagArg(outPath))
	args = appendZipExcludes(args, opts.Exclude)
	args = append(args, "--")
	args = append(args, files...)

	cmd := execCommand("zip", args...)
	cmd.Stdout = stdoutFor(opts.Progress)
	cmd.Stderr = stderrFor(opts.Progress)

	if opts.Verbose {
		WriteLogf("  $ zip %s\n", strings.Join(redactArgs(args), " "))
	}

	err := augmentErr(cmd, cmd.Run())
	if err == nil && opts.Progress != nil {
		opts.Progress.FileDone(outPath)
	}
	return err
}

func adaptive7zDict(fileSize int64, threads int) string {
	return adaptive7zDictForBudget(fileSize, threads, getMemLimit())
}

func adaptive7zDictForBudget(fileSize int64, threads int, memBudgetMB int) string {
	memBudgetMB = effectiveMemBudget(memBudgetMB)
	dictMB := 256
	if memBudgetMB > 0 && memBudgetMB < 8192 {
		dictMB = 128
	}

	if fileSize > 0 {
		switch {
		case fileSize <= 16*1024*1024:
			dictMB = 16
		case fileSize <= 32*1024*1024:
			dictMB = 32
		case fileSize <= 64*1024*1024:
			dictMB = 64
		case fileSize <= 128*1024*1024:
			if dictMB > 128 {
				dictMB = 128
			}
		}
	}

	if memBudgetMB > 0 {
		blockPairs := max(1, threads/2)
		for _, candidate := range []int{256, 128, 64, 32, 16} {
			if candidate > dictMB {
				continue
			}
			dictMB = candidate
			if float64(blockPairs)*11.5*float64(candidate) <= float64(memBudgetMB) {
				break
			}
		}
	}

	return fmt.Sprintf("-md=%dm", dictMB)
}

func build7zArgs(files []string, outPath string, opts CompressOptions, precalcSize ...int64) []string {
	var totalSize int64
	if len(precalcSize) > 0 && precalcSize[0] > 0 {
		totalSize = precalcSize[0]
	} else {
		for _, f := range files {
			if fi, err := os.Stat(f); err == nil {
				if fi.IsDir() {
					sz, _ := GetDirSize(f)
					totalSize += sz
				} else {
					totalSize += fi.Size()
				}
			}
		}
	}
	md := adaptive7zDictForBudget(totalSize, opts.ThreadLimit, effectiveMemBudget(opts.MemBudgetMB))
	mmt := "-mmt=on"
	if opts.ThreadLimit > 0 {
		mmt = "-mmt=" + threadStr(opts.ThreadLimit)
	}
	args := []string{"a", "-mx=9", md, "-mfb=273", "-ms=on", mmt, "-bsp1"}
	if opts.Password != "" {
		args = append(args, "-p"+opts.Password, "-mhe=on")
	}
	args = append7zExcludes(args, opts.Exclude)
	optFlags := strings.Fields(opts.CompressionOpts)
	args = append(args, optFlags...)
	args = append(args, safeFlagArg(outPath), "--")
	args = append(args, files...)
	return args
}

func compress7z(files []string, outPath string, opts CompressOptions, fp *FileProgress, precalcSize ...int64) error {
	sevenz := sevenzBin()
	var fileSize int64
	if len(precalcSize) > 0 && precalcSize[0] > 0 {
		fileSize = precalcSize[0]
	} else if fp != nil && fp.Size > 0 {
		fileSize = fp.Size
	} else {
		fileSize = totalFileSize(files)
	}
	args := build7zArgs(files, outPath, opts, fileSize)

	cmd := execCommand(sevenz, args...)

	if opts.Verbose {
		WriteLogf("  $ %s %s\n", sevenz, strings.Join(redactArgs(args), " "))
	}

	err := runWithProgress(cmd, opts.Progress, fileSize, fp)
	if err == nil && opts.Progress != nil {
		opts.Progress.FileDone(outPath)
	}
	return err
}

func compressPlainTar(files []string, outPath string, opts CompressOptions, fp *FileProgress) error {
	args := []string{"-cf", safeTarArchiveArg(outPath)}
	if opts.UseSparse() {
		args = append(args, "--sparse")
	}
	args = appendTarFilterFlags(args, opts)
	args = append(args, "--")
	args = append(args, files...)

	cmd := execCommand("tar", args...)
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
	rar := "rar"
	args := []string{"a", "-m" + fmt.Sprintf("%d", fastOrSlow(opts, 5)), "-mt" + threadStr(opts.ThreadLimit)}
	if opts.Password != "" {
		args = append(args, "-p"+opts.Password)
	}
	args = appendRarExcludes(args, opts.Exclude)
	optFlags := strings.Fields(opts.CompressionOpts)
	args = append(args, optFlags...)
	if !opts.KeepOrig && !opts.SkipCleanup && opts.CutoffTime.IsZero() && len(opts.Formats) <= 1 {
		args = append(args, "-df")
	}
	args = append(args, safeFlagArg(outPath), "--")
	args = append(args, files...)

	cmd := execCommand(rar, args...)
	cmd.Stdout = stdoutFor(opts.Progress)
	cmd.Stderr = stderrFor(opts.Progress)

	if opts.Verbose {
		WriteLogf("  $ %s %s\n", rar, strings.Join(redactArgs(args), " "))
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

func FindCompressibleFiles(dir string) ([]string, error) {
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if strings.Contains(name, ".part") {
			continue
		}
		if strings.HasSuffix(name, ".sha256") {
			continue
		}
		if IsSplitPartsDir(name) {
			continue
		}
		if !entry.IsDir() {
			if _, err := DetectFormat(name); err == nil {
				continue
			}
		}
		relPath := name
		if dir != "." {
			relPath = filepath.Join(dir, name)
		}
		files = append(files, relPath)
	}
	sort.Strings(files)
	return files, nil
}

func PromptCompressAll(r io.Reader, w io.Writer, files []string, format string) (bool, error) {
	if len(files) == 0 {
		return false, nil
	}

	fmt.Fprintf(w, "%sElementos detectados para comprimir en el directorio actual (%d):%s\n", Bold, len(files), NC)
	for _, f := range files {
		sizeStr := ""
		if fi, err := os.Stat(f); err == nil {
			if fi.IsDir() {
				if sz, err := GetDirSize(f); err == nil && sz > 0 {
					sizeStr = fmt.Sprintf(" (%s)", FormatSize(sz))
				}
			} else {
				sizeStr = fmt.Sprintf(" (%s)", FormatSize(fi.Size()))
			}
		}
		fmt.Fprintf(w, "  • %s%s%s%s\n", Blue, f, NC, sizeStr)
	}
	fmt.Fprintf(w, "\n%s¿Desea comprimir todos los elementos (%d) [formato: %s]? [s/N]: %s", Yellow, len(files), format, NC)

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

func DoAppend(archive string, items []string, opts CompressOptions) error {
	fi, err := os.Stat(archive)
	if err != nil {
		return fmt.Errorf("el archivo comprimido objetivo no existe: %s", archive)
	}
	if fi.IsDir() {
		return fmt.Errorf("%s es un directorio, no un archivo comprimido", archive)
	}
	if len(items) == 0 {
		return fmt.Errorf("debe especificar al menos un archivo o carpeta para agregar")
	}
	for _, item := range items {
		if _, err := os.Stat(item); err != nil {
			return fmt.Errorf("el archivo o carpeta a agregar no existe: %s", item)
		}
	}

	if opts.Clean {
		opts.Exclude = append(opts.Exclude, DefaultCleanExcludes...)
	}

	info, err := DetectFormat(archive)
	if err != nil {
		return fmt.Errorf("no se pudo determinar el formato de %s: %w", archive, err)
	}

	filteredItems := filterCompressItems(items, opts)
	if len(filteredItems) == 0 {
		if !opts.CutoffTime.IsZero() {
			WriteLogf("%sℹ No se encontraron archivos modificados después de %s para agregar.%s\n", Blue, opts.CutoffTime.Format("2006-01-02 15:04:05"), NC)
			return nil
		}
		return fmt.Errorf("todos los archivos fueron excluidos")
	}
	items = filteredItems

	containerItems := items
	if !opts.CutoffTime.IsZero() && (info.Format == Zip || info.Format == SevenZ || info.Format == Rar) {
		containerItems = collectAllNewerFiles(items, opts.CutoffTime, opts.Exclude)
		if len(containerItems) == 0 {
			WriteLogf("%sℹ No se encontraron archivos modificados después de %s para agregar.%s\n", Blue, opts.CutoffTime.Format("2006-01-02 15:04:05"), NC)
			return nil
		}
	}

	if opts.Password != "" && supportsPassword(info.Format) {
		GetPasswordManager().AddSuccessful(opts.Password)
	}

	totalSize := totalFileSize(items)
	if info.IsTar && info.IsStream() {
		totalSize += fi.Size()
	}
	if opts.MemBudgetMB <= 0 {
		opts.MemBudgetMB = getMemLimit()
	}
	if cap := resolveThreadCapForBudget(info.Format, totalSize, opts.MemBudgetMB); opts.ThreadLimit <= 0 || cap < opts.ThreadLimit {
		opts.ThreadLimit = cap
	}

	switch {
	case info.Format == Zip:
		sevenz := sevenzBin()
		if hasTool(sevenz) {
			args := []string{"u", "-bsp1", "-mmt=" + threadStr(opts.ThreadLimit)}
			if opts.Password != "" {
				args = append(args, "-p"+opts.Password)
			}
			args = append7zExcludes(args, opts.Exclude)
			args = append(args, safeFlagArg(archive), "--")
			args = append(args, containerItems...)
			cmd := execCommand(sevenz, args...)
			cmd.Stdout = stdoutFor(opts.Progress)
			cmd.Stderr = stderrFor(opts.Progress)
			if err := augmentErr(cmd, cmd.Run()); err != nil {
				return fmt.Errorf("error agregando a zip: %w", err)
			}
		} else if hasTool("zip") {
			args := []string{"-u", "-r"}
			if opts.Password != "" {
				args = append(args, "-P", opts.Password)
			}
			args = append(args, safeFlagArg(archive))
			args = appendZipExcludes(args, opts.Exclude)
			args = append(args, "--")
			args = append(args, containerItems...)
			cmd := execCommand("zip", args...)
			cmd.Stdout = stdoutFor(opts.Progress)
			cmd.Stderr = stderrFor(opts.Progress)
			if err := augmentErr(cmd, cmd.Run()); err != nil {
				return fmt.Errorf("error agregando a zip: %w", err)
			}
		} else {
			return fmt.Errorf("no se encontró herramienta para actualizar zip (se requiere 7z o zip)")
		}

	case info.Format == SevenZ:
		sevenz := sevenzBin()
		if !hasTool(sevenz) {
			return fmt.Errorf("no se encontró %s para actualizar archivo 7z", sevenz)
		}
		args := []string{"u", "-bsp1", "-mmt=" + threadStr(opts.ThreadLimit)}
		if opts.Password != "" {
			args = append(args, "-p"+opts.Password)
			args = append(args, "-mhe=on")
		}
		args = append7zExcludes(args, opts.Exclude)
		args = append(args, safeFlagArg(archive), "--")
		args = append(args, containerItems...)
		cmd := execCommand(sevenz, args...)
		cmd.Stdout = stdoutFor(opts.Progress)
		cmd.Stderr = stderrFor(opts.Progress)
		if err := augmentErr(cmd, cmd.Run()); err != nil {
			return fmt.Errorf("error agregando a 7z: %w", err)
		}

	case info.Format == Rar:
		const tool = "rar"
		if !hasTool(tool) {
			return fmt.Errorf("no se encontró %s para actualizar archivo rar", tool)
		}
		args := []string{"u", "-y", "-mt" + threadStr(opts.ThreadLimit)}
		if opts.Password != "" {
			args = append(args, "-p"+opts.Password)
		}
		args = appendRarExcludes(args, opts.Exclude)
		args = append(args, safeFlagArg(archive), "--")
		args = append(args, containerItems...)
		cmd := execCommand(tool, args...)
		cmd.Stdout = stdoutFor(opts.Progress)
		cmd.Stderr = stderrFor(opts.Progress)
		if err := augmentErr(cmd, cmd.Run()); err != nil {
			return fmt.Errorf("error agregando a rar: %w", err)
		}

	case info.Format == Tar && !info.IsStream():
		args := []string{"-rf", safeTarArchiveArg(archive)}
		if opts.UseSparse() {
			args = append(args, "--sparse")
		}
		args = appendTarFilterFlags(args, opts)
		args = append(args, "--")
		args = append(args, items...)
		cmd := execCommand("tar", args...)
		cmd.Stdout = stdoutFor(opts.Progress)
		cmd.Stderr = stderrFor(opts.Progress)
		if err := augmentErr(cmd, cmd.Run()); err != nil {
			return fmt.Errorf("error agregando a tar: %w", err)
		}

	case info.IsTar && info.IsStream():
		if err := appendTarStream(archive, fi.Mode().Perm(), fi.Size(), totalSize, info, items, opts); err != nil {
			return err
		}

	default:
		return fmt.Errorf("el formato %s no soporta agregar múltiples archivos (es un flujo individual, use contenedores como .tar.%s o .zip)", info.Format.String(), info.Format.String())
	}

	shaPath := archive + ".sha256"
	if _, statErr := os.Stat(shaPath); statErr == nil || opts.Hash {
		newHash, err := WriteSHA256File(archive)
		if err == nil {
			WriteLogf("✓ Checksum SHA-256 actualizado para %s: %s\n", archive, newHash)
		}
	}

	return nil
}

func appendTarStream(archive string, archivePerm os.FileMode, archiveSize, totalSize int64, info FormatInfo, items []string, opts CompressOptions) error {
	stageBudget := 0
	if opts.MemBudgetMB > 0 {
		stageBudget = max(128, opts.MemBudgetMB/2)
	}
	decompThreads := cappedThreadsForBudget(info.Format, archiveSize, stageBudget, opts.ThreadLimit)
	compThreads := cappedThreadsForBudget(info.Format, totalSize, stageBudget, opts.ThreadLimit)

	outF, err := os.CreateTemp(filepath.Dir(archive), ".crush_append_out_*")
	if err != nil {
		return fmt.Errorf("creando archivo temporal re-comprimido: %w", err)
	}
	tmpOut := outF.Name()
	defer os.Remove(tmpOut)
	_ = outF.Chmod(archivePerm)

	outClosed := false
	defer func() {
		if !outClosed {
			_ = outF.Close()
		}
	}()

	decompR, decompW, err := newAsyncOSPipe()
	if err != nil {
		return fmt.Errorf("creando pipe asíncrono de lectura: %w", err)
	}
	compR, compW, err := newAsyncOSPipe()
	if err != nil {
		_ = decompR.Close()
		_ = decompW.Close()
		return fmt.Errorf("creando pipe asíncrono de escritura: %w", err)
	}

	decompCmd, decompCloser := pipeCmdForWithMem(info, archive, decompThreads, stageBudget)
	if decompCloser != nil {
		defer decompCloser.Close()
	}
	decompCmd.Stdout = decompW
	decompCmd.Stderr = stderrFor(opts.Progress)

	compOpts := opts
	compOpts.Format = info.Format
	compOpts.ThreadLimit = compThreads
	compOpts.MemBudgetMB = stageBudget
	compCmd := buildCompressCmd(compOpts)
	compCmd.Stdin = compR
	compCmd.Stdout = outF
	compCmd.Stderr = stderrFor(opts.Progress)

	if err := decompCmd.Start(); err != nil {
		_ = decompR.Close()
		_ = decompW.Close()
		_ = compR.Close()
		_ = compW.Close()
		return fmt.Errorf("error descomprimiendo tar existente: %w", augmentErr(decompCmd, err))
	}
	_ = decompW.Close()
	decompW = nil

	if err := compCmd.Start(); err != nil {
		_ = decompR.Close()
		_ = compR.Close()
		_ = compW.Close()
		if decompCmd.Process != nil {
			_ = decompCmd.Process.Kill()
			_ = decompCmd.Wait()
		}
		return fmt.Errorf("error re-comprimiendo archivo: %w", augmentErr(compCmd, err))
	}
	_ = compR.Close()
	compR = nil

	decompWaited := false
	compWaited := false
	defer func() {
		if decompR != nil {
			_ = decompR.Close()
		}
		if compW != nil {
			_ = compW.Close()
		}
		if decompCmd.Process != nil && !decompWaited {
			_ = decompCmd.Process.Kill()
			_ = decompCmd.Wait()
		}
		if compCmd.Process != nil && !compWaited {
			_ = compCmd.Process.Kill()
			_ = compCmd.Wait()
		}
	}()

	tr := tar.NewReader(bufio.NewReaderSize(decompR, streamBufSize))
	bw := bufio.NewWriterSize(compW, streamBufSize)
	tw := tar.NewWriter(bw)
	copyBuf := make([]byte, streamBufSize)

	reapOnCompWriteErr := func(writeErr error) error {
		_ = tw.Close()
		_ = bw.Flush()
		if compW != nil {
			_ = compW.Close()
			compW = nil
		}
		if !compWaited {
			compWaited = true
			if cErr := augmentErr(compCmd, compCmd.Wait()); cErr != nil {
				return fmt.Errorf("error re-comprimiendo archivo: %w", cErr)
			}
		}
		return writeErr
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = decompR.Close()
			decompR = nil
			if !decompWaited {
				decompWaited = true
				if dErr := augmentErr(decompCmd, decompCmd.Wait()); dErr != nil {
					return fmt.Errorf("error descomprimiendo tar existente: %w", dErr)
				}
			}
			return fmt.Errorf("error leyendo tar existente: %w", err)
		}
		if hdr.Typeflag == tar.TypeRegA {
			hdr.Typeflag = tar.TypeReg
		}
		if hdr.Typeflag == tar.TypeDir {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return reapOnCompWriteErr(fmt.Errorf("escribiendo cabecera tar: %w", err))
		}
		if hdr.Size > 0 && hdr.Typeflag != tar.TypeSymlink && hdr.Typeflag != tar.TypeLink {
			if _, err := io.CopyBuffer(tw, tr, copyBuf); err != nil {
				return reapOnCompWriteErr(fmt.Errorf("copiando entrada tar existente: %w", err))
			}
		}
	}

	_ = decompR.Close()
	decompR = nil
	decompWaited = true
	if err := augmentErr(decompCmd, decompCmd.Wait()); err != nil {
		return fmt.Errorf("error descomprimiendo tar existente: %w", err)
	}

	if err := writeAppendItemsToTar(tw, items, opts, copyBuf); err != nil {
		return reapOnCompWriteErr(fmt.Errorf("error agregando elementos al flujo tar: %w", err))
	}

	if err := tw.Close(); err != nil {
		return reapOnCompWriteErr(fmt.Errorf("cerrando flujo tar: %w", err))
	}
	if err := bw.Flush(); err != nil {
		return reapOnCompWriteErr(fmt.Errorf("vaciando búfer tar: %w", err))
	}
	if err := compW.Close(); err != nil {
		compW = nil
		return reapOnCompWriteErr(fmt.Errorf("cerrando pipe hacia compresor: %w", err))
	}
	compW = nil

	compWaited = true
	if err := augmentErr(compCmd, compCmd.Wait()); err != nil {
		return fmt.Errorf("error re-comprimiendo archivo: %w", err)
	}

	outClosed = true
	if err := outF.Close(); err != nil {
		return fmt.Errorf("cerrando archivo temporal re-comprimido: %w", err)
	}

	if err := os.Rename(tmpOut, archive); err != nil {
		return fmt.Errorf("error reemplazando archivo original: %w", err)
	}
	return nil
}

func writeAppendItemsToTar(tw *tar.Writer, items []string, opts CompressOptions, copyBuf []byte) error {
	for _, item := range items {
		cleanRoot := filepath.Clean(item)
		err := filepath.WalkDir(cleanRoot, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			cleanPath := filepath.Clean(path)
			slashPath := filepath.ToSlash(cleanPath)
			if isExcludedItem(cleanPath, opts.Exclude) || (slashPath != cleanPath && isExcludedItem(slashPath, opts.Exclude)) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			lfi, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if !opts.CutoffTime.IsZero() && !lfi.IsDir() && lfi.ModTime().Before(opts.CutoffTime) {
				return nil
			}

			tarName := strings.TrimLeft(slashPath, "/")
			if tarName == "" || tarName == "." {
				return nil
			}

			var linkTarget string
			if lfi.Mode()&os.ModeSymlink != 0 {
				linkTarget, err = os.Readlink(path)
				if err != nil {
					return err
				}
			}

			hdr, err := tar.FileInfoHeader(lfi, linkTarget)
			if err != nil {
				return err
			}
			if lfi.IsDir() {
				if !strings.HasSuffix(tarName, "/") {
					tarName += "/"
				}
				hdr.Size = 0
			}
			hdr.Name = tarName

			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if lfi.Mode().IsRegular() && hdr.Size > 0 {
				f, err := os.Open(path)
				if err != nil {
					return err
				}
				_, copyErr := io.CopyBuffer(tw, io.LimitReader(f, hdr.Size), copyBuf)
				closeErr := f.Close()
				if copyErr != nil {
					return copyErr
				}
				if closeErr != nil {
					return closeErr
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
