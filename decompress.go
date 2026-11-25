package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
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
	MemBudgetMB int
	Progress    *ProgressTracker
	Password    string
	Filter      string
	QuietReport bool
	Resume      bool
}

func effectiveDecompressMemBudget(memBudgetMB int) int {
	if memBudgetMB > 0 {
		return memBudgetMB
	}
	return getMemLimit()
}

func decompressStream(r io.Reader, w io.Writer, info FormatInfo) error {
	if info.Tool == "" {
		return fmt.Errorf("formato no soportado para pipe")
	}
	pipeFlags := PipeFlagsForThreadsAndMem(info, resolveThreadCap(info.Format, 0), getMemLimit())
	decompCmd := execCommand(info.Tool, pipeFlags...)
	decompCmd.Stdin = r
	decompCmd.Stdout = w
	decompCmd.Stderr = os.Stderr
	return decompCmd.Run()
}

func safeScaleInt64(v, factor int64) int64 {
	if v <= 0 || factor <= 0 {
		return 0
	}
	if v > math.MaxInt64/factor {
		return math.MaxInt64
	}
	return v * factor
}

func withDiskSafetyMargin(sz int64) int64 {
	if sz <= 0 {
		return 0
	}
	extra := sz / 10
	if sz > math.MaxInt64-extra {
		return math.MaxInt64
	}
	return sz + extra
}

func estimateFileNeeded(file string, archiveSize int64) int64 {
	var sz int64
	if s := EstimateUncompressedSize(file); s > 0 {
		sz = s
	} else {
		sz = safeScaleInt64(archiveSize, 3)
	}
	parts := findSplitParts(file)
	if len(parts) > 1 && sz < archiveSize {
		sz = safeScaleInt64(archiveSize, 4)
	}
	return withDiskSafetyMargin(sz)
}

// SelectFeasibleWave selecciona un subconjunto de archivos de pending cuyos requerimientos
// de espacio calculados mediante neededEstimator no superen availBytes.
// Devuelve los archivos seleccionados para la tanda (wave) y los archivos restantes (remaining).
func SelectFeasibleWave(pending []string, availBytes int64, neededEstimator func(string) int64) (wave []string, remaining []string) {
	if len(pending) == 0 {
		return nil, nil
	}
	var currentNeeded int64
	for _, f := range pending {
		req := neededEstimator(f)
		if req <= 0 {
			req = 1
		}
		if currentNeeded+req <= availBytes {
			wave = append(wave, f)
			currentNeeded += req
		} else {
			remaining = append(remaining, f)
		}
	}
	return wave, remaining
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

	if opts.Resume {
		var pendingFiles []string
		for _, f := range allFiles {
			targetDir := ResolveDecompressDir(f, opts.OutputDir)
			if isArchiveFullyExtracted(f, targetDir, opts.Password) {
				WriteLogf("%sℹ [reanudado] '%s' ya está completamente descomprimido; omitiendo.%s\n", Blue, f, NC)
			} else {
				pendingFiles = append(pendingFiles, f)
			}
		}
		if len(pendingFiles) == 0 {
			WriteLogf("%s✓ Todos los archivos ya estaban descomprimidos. Nada que reanudar.%s\n", Green, NC)
			return nil
		}
		allFiles = pendingFiles
	}

	WriteLogf("%sPreparando descompresión de %d archivo(s)...%s\n", Bold, len(allFiles), NC)

	for _, f := range allFiles {
		info, err := DetectFormat(f)
		if err == nil {
			if _, err := EnsureDecompressTool(info.Format); err != nil {
				return err
			}
		}
	}

	archiveSizes := SortByLPT(allFiles, TotalArchiveSize)

	filesByDir := make(map[string][]string)
	for _, f := range allFiles {
		targetDir := ResolveDecompressDir(f, opts.OutputDir)
		filesByDir[targetDir] = append(filesByDir[targetDir], f)
	}

	var totalSuccesses, totalErrors int
	var totalSkipped []string

	for targetDir, dirFiles := range filesByDir {
		neededEstimator := func(f string) int64 {
			return estimateFileNeeded(f, archiveSizes[f])
		}

		var totalNeeded int64
		var minNeeded int64 = -1
		for _, f := range dirFiles {
			req := neededEstimator(f)
			totalNeeded += req
			if minNeeded < 0 || req < minNeeded {
				minNeeded = req
			}
		}

		avail := getDiskAvail(targetDir)

		if avail < 0 || totalNeeded <= avail {
			// FAST PATH: Capacidad suficiente para todo el lote
			var readyFiles, deferredFiles []string
			for _, f := range dirFiles {
				if FileRequiresInteractivePassword(f, opts.Password) {
					deferredFiles = append(deferredFiles, f)
				} else {
					readyFiles = append(readyFiles, f)
				}
			}

			if len(readyFiles) > 0 {
				if len(deferredFiles) > 0 {
					WriteLogf("%s[Fase 1/2] Descomprimiendo %d archivo(s) listos en paralelo...%s\n", Bold, len(readyFiles), NC)
					WriteLogf("  (%d archivo(s) protegidos diferidos para atención posterior)\n\n", len(deferredFiles))
				}
				s, e := decompressBatch(readyFiles, archiveSizes, opts)
				totalSuccesses += s
				totalErrors += e
			}

			if len(deferredFiles) > 0 {
				if len(readyFiles) > 0 {
					WriteLogf("\n%s──────────────────────────────────────────────────%s\n", Blue, NC)
					WriteLogf("%s[Fase 2/2] Procesando %d archivo(s) protegidos con contraseña...%s\n\n", Bold, len(deferredFiles), NC)
				}
				for _, f := range deferredFiles {
					defOpts := opts
					defOpts.Progress = nil
					s, e := decompressBatch([]string{f}, archiveSizes, defOpts)
					totalSuccesses += s
					totalErrors += e
				}
			}
		} else {
			// Espacio disponible menor al total requerido: evaluar tandas adaptativas
			if minNeeded > avail {
				return CheckDiskSpace(minNeeded, targetDir, "descomprimir")
			}

			WriteLogf("%sEspacio en disco disponible (%s) menor al total requerido (%s).%s\n", Yellow, FormatSize(avail), FormatSize(totalNeeded), NC)
			WriteLogf("%sActivando descompresión adaptativa por tandas según capacidad...%s\n\n", Yellow, NC)

			remaining := append([]string(nil), dirFiles...)
			waveNum := 0
			for len(remaining) > 0 {
				curAvail := getDiskAvail(targetDir)
				wave, nextRemaining := SelectFeasibleWave(remaining, curAvail, neededEstimator)
				if len(wave) == 0 {
					totalSkipped = append(totalSkipped, remaining...)
					break
				}
				waveNum++
				var waveNeeded int64
				for _, wf := range wave {
					waveNeeded += neededEstimator(wf)
				}

				WriteLogf("%s[Tanda %d] Espacio libre: %s. Procesando %d archivo(s) (requiere %s)...%s\n",
					Bold, waveNum, FormatSize(curAvail), len(wave), FormatSize(waveNeeded), NC)

				var waveReady, waveDeferred []string
				for _, wf := range wave {
					if FileRequiresInteractivePassword(wf, opts.Password) {
						waveDeferred = append(waveDeferred, wf)
					} else {
						waveReady = append(waveReady, wf)
					}
				}

				if len(waveReady) > 0 {
					s, e := decompressBatch(waveReady, archiveSizes, opts)
					totalSuccesses += s
					totalErrors += e
				}

				if len(waveDeferred) > 0 {
					for _, wf := range waveDeferred {
						defOpts := opts
						defOpts.Progress = nil
						s, e := decompressBatch([]string{wf}, archiveSizes, defOpts)
						totalSuccesses += s
						totalErrors += e
					}
				}

				remaining = nextRemaining
			}
		}
	}

	if len(totalSkipped) > 0 {
		WriteLogf("\n%s⚠ %d archivo(s) no se pudieron descomprimir por espacio insuficiente en disco:%s\n",
			Yellow, len(totalSkipped), NC)
		for _, f := range totalSkipped {
			req := estimateFileNeeded(f, archiveSizes[f])
			WriteLogf("  • %s (requiere %s)\n", f, FormatSize(req))
		}
	}

	WriteLogf("\n")
	if totalErrors > 0 {
		WriteLogf("%sCompletado con %d errores, %d exitosos%s\n", Yellow, totalErrors, totalSuccesses, NC)
		return fmt.Errorf("%d error(es) durante la descompresión", totalErrors)
	}
	if len(totalSkipped) > 0 {
		if totalSuccesses > 0 {
			WriteLogf("%sCompletado: %d archivo(s) descomprimidos exitosamente, %d omitido(s) por falta de espacio%s\n", Yellow, totalSuccesses, len(totalSkipped), NC)
		}
		return fmt.Errorf("%d archivo(s) omitidos por falta de espacio en disco", len(totalSkipped))
	}
	WriteLogf("%s✓ Descompresión completada (%d archivo(s))%s\n", Green, totalSuccesses, NC)
	return nil
}

func decompressBatch(files []string, archiveSizes map[string]int64, opts DecompressOptions) (int, int) {
	if len(files) == 0 {
		return 0, 0
	}

	var totalSize int64
	for _, f := range files {
		totalSize += archiveSizes[f]
	}
	pt := opts.Progress
	if pt == nil {
		pt = NewProgressTracker(totalSize, len(files))
		opts.Progress = pt
	} else {
		pt.total = totalSize
		pt.filesTotal = len(files)
	}

	if opts.Parallel < 1 {
		opts.Parallel = NCPU()
	}

	fps := make([]*FileProgress, len(files))
	for i, f := range files {
		parts := findSplitParts(f)
		fps[i] = &FileProgress{Name: filepath.Base(f), Size: archiveSizes[f]}
		if len(parts) > 1 {
			fps[i].SetParts(len(parts), len(parts))
		}
		fps[i].SetStatus("waiting")
	}
	pt.SetFiles(fps)

	var successes, errors int

	if len(files) == 1 {
		pt.Start()
		defer pt.Stop()

		fileOpts := opts
		if fileOpts.MemBudgetMB <= 0 {
			fileOpts.MemBudgetMB = getMemLimit()
		}
		if fileOpts.ThreadLimit <= 0 {
			if info, err := DetectFormat(files[0]); err == nil {
				fileOpts.ThreadLimit = resolveThreadCapForBudget(info.Format, archiveSizes[files[0]], fileOpts.MemBudgetMB)
			}
		}

		err := decompressFile(files[0], fileOpts, fps[0])
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
		sizesSlice := make([]int64, len(files))
		formats := make([]Format, len(files))
		formatOK := make([]bool, len(files))
		var totalDecompBytes int64
		allMono := true
		for i, f := range files {
			sz := archiveSizes[f]
			if sz > 0 {
				sizesSlice[i] = sz
				totalDecompBytes += sz
			}
			info, err := DetectFormat(f)
			if err == nil {
				formats[i] = info.Format
				formatOK[i] = true
				if resolveThreadCap(info.Format, sz) > 1 {
					allMono = false
				}
			} else {
				allMono = false
			}
		}

		maxWorkers := opts.Parallel
		isRot := false
		if opts.OutputDir != "" {
			isRot = IsRotational(opts.OutputDir)
		} else if len(files) > 0 {
			isRot = IsRotational(files[0])
		}

		if isRot {
			if maxWorkers > 2 {
				maxWorkers = 2
			}
		} else if !allMono && maxWorkers > 8 {
			maxWorkers = 8
		}
		if len(files) < maxWorkers {
			maxWorkers = len(files)
		}
		if maxWorkers < 1 {
			maxWorkers = 1
		}

		memTotal := opts.MemBudgetMB
		if memTotal <= 0 {
			memTotal = getMemLimit()
		}
		workerBudget := 0
		if memTotal > 0 {
			workerBudget = max(128, memTotal/maxWorkers)
		}

		caps := make([]int, len(files))
		for i := range files {
			if formatOK[i] {
				caps[i] = resolveThreadCapForBudget(formats[i], sizesSlice[i], workerBudget)
			} else {
				caps[i] = totalCores
			}
		}

		pool := NewDynamicThreadPool(totalCores, 1, totalCores, len(files), totalDecompBytes, maxWorkers)
		var preAlloc []int
		if len(files) <= maxWorkers {
			preAlloc = AllocateThreadsProportionalWithCaps(sizesSlice, totalCores, 1, caps)
		}

		WriteLogf("%sDescomprimiendo %d archivo(s) en paralelo...%s\n", Bold, len(files), NC)
		WriteLogf("  Hilos: %d total (distribución adaptativa LPT)\n\n", totalCores)

		pt.Start()
		defer pt.Stop()

		sem := make(chan struct{}, maxWorkers)
		var wg sync.WaitGroup
		results := make(chan error, len(files))
		startTime := time.Now()

		for i, file := range files {
			fp := fps[i]
			wg.Add(1)
			go func(f string, fp *FileProgress, idx int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()

				fileOpts := opts
				fileOpts.QuietReport = true
				fileOpts.MemBudgetMB = workerBudget
				var th int
				if len(preAlloc) == len(files) {
					th = preAlloc[idx]
				} else {
					th = pool.Acquire(archiveSizes[f], caps[idx])
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

		var totalCompressed, totalUncompressed int64
		for i, fp := range fps {
			if fp.Status() != "error" {
				totalCompressed += archiveSizes[files[i]]
				totalUncompressed += fp.GetUncompressedSize()
			}
		}

		elapsed := time.Since(startTime)

		WriteLogf("\n")
		WriteLogf("%s=== Reporte de Descompresión Paralela ===%s\n", Green, NC)
		WriteLogf("%sArchivos:%s              %s%d%s\n", Blue, NC, Bold, len(files), NC)
		WriteLogf("%sTamaño Comprimido:%s     %s%s%s\n", Blue, NC, Red, FormatSize(totalCompressed), NC)
		WriteLogf("%sTamaño Descomprimido:%s  %s%s%s\n", Blue, NC, Green, FormatSize(totalUncompressed), NC)
		WriteLogf("%sTiempo total:%s          %s%v%s\n", Blue, NC, Bold, elapsed.Round(time.Second), NC)
		WriteLogf("%sHilos:%s                 %s%d total (distribución adaptativa LPT)%s\n", Blue, NC, Bold, totalCores, NC)
		if errors > 0 {
			WriteLogf("%sErrores:%s               %s%d%s\n", Blue, NC, Red, errors, NC)
		} else {
			WriteLogf("%s%s✓ Todos los archivos descomprimidos exitosamente%s\n", Green, Bold, NC)
		}
		WriteLogf("%s========================================%s\n", Green, NC)
	}

	return successes, errors
}

func listArchiveOutputs(file string, dir string, info FormatInfo, passwords ...string) []string {
	password := ""
	if len(passwords) > 0 {
		password = passwords[0]
	}
	members, err := ListArchiveMembers(file, password)
	if len(members) == 0 && err != nil {
		WriteLogf("  %s⚠ No se pudo listar %s para limpiar salidas parciales%s\n", Yellow, file, NC)
		return nil
	}
	var paths []string
	for _, m := range members {
		paths = append(paths, m.Path)
	}
	return resolveOutputs(paths, dir)
}

func sanitizeArchiveRelPath(member string) (string, bool) {
	norm := strings.ReplaceAll(member, "\\", "/")
	norm = strings.TrimRight(norm, "/")
	if norm == "" || strings.HasPrefix(norm, "/") {
		return "", false
	}
	clean := path.Clean(norm)
	if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	rel := filepath.FromSlash(clean)
	if !filepath.IsLocal(rel) {
		return "", false
	}
	return rel, true
}

func topLevelArchiveEntry(member string) (string, bool) {
	rel, ok := sanitizeArchiveRelPath(member)
	if !ok {
		return "", false
	}
	if idx := strings.IndexByte(rel, filepath.Separator); idx >= 0 {
		rel = rel[:idx]
	}
	if rel == "" || rel == "." || rel == ".." {
		return "", false
	}
	return rel, true
}

var errUnsafeArchiveMember = errors.New("ruta o enlace simbólico inseguro en archivo")

func isSymlinkTargetWithinRoot(relSlash, target string) bool {
	if target == "" || strings.ContainsRune(target, 0) {
		return false
	}
	if filepath.IsAbs(target) || strings.HasPrefix(target, "/") || strings.HasPrefix(target, `\`) {
		return false
	}
	normTarget := strings.ReplaceAll(target, "\\", "/")
	parent := path.Dir(relSlash)
	resolved := path.Clean(path.Join(parent, normTarget))
	return resolved != ".." && !strings.HasPrefix(resolved, "../")
}

func validateZipSymlinkTargets(file string) error {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return nil
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.Mode()&os.ModeSymlink == 0 {
			continue
		}
		rel, ok := sanitizeArchiveRelPath(f.Name)
		if !ok {
			return fmt.Errorf("%w: %q", errUnsafeArchiveMember, f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		targetBytes, _ := io.ReadAll(io.LimitReader(rc, 4096))
		_ = rc.Close()
		target := strings.TrimRight(string(targetBytes), "\x00")
		if !isSymlinkTargetWithinRoot(filepath.ToSlash(rel), target) {
			return fmt.Errorf("%w: symlink %s -> %s escapa del directorio destino", errUnsafeArchiveMember, rel, target)
		}
	}
	return nil
}

func prepareContainerOutputs(file, dir string, info FormatInfo, opts DecompressOptions) ([]string, []string, error) {
	if info.Format == Zip {
		if err := validateZipSymlinkTargets(file); err != nil {
			return nil, nil, err
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("Error abriendo directorio de salida: %w", err)
	}
	defer root.Close()

	seenTop := make(map[string]bool)
	checkedDirs := make(map[string]bool)
	archiveSymlinks := make(map[string]bool)
	parentPrefixes := make(map[string]bool)
	var outputs []string
	var symlinkMembers []string
	var secErr error

	iterErr := IterArchiveMembers(file, opts.Password, func(m ArchiveMember) error {
		rel, ok := sanitizeArchiveRelPath(m.Path)
		if !ok {
			secErr = fmt.Errorf("%w: ruta insegura %q", errUnsafeArchiveMember, m.Path)
			return errStopIter
		}
		relSlash := filepath.ToSlash(rel)
		parts := strings.Split(relSlash, "/")
		prefixLimit := len(parts) - 1
		if m.IsDir {
			prefixLimit = len(parts)
		}
		curPrefix := ""
		for i := 0; i < prefixLimit; i++ {
			if i == 0 {
				curPrefix = parts[0]
			} else {
				curPrefix += "/" + parts[i]
			}
			if archiveSymlinks[curPrefix] {
				secErr = fmt.Errorf("%w: ruta atraviesa symlink interno del archivo %s", errUnsafeArchiveMember, curPrefix)
				return errStopIter
			}
			if i < len(parts)-1 {
				parentPrefixes[curPrefix] = true
			}
			if checkedDirs[curPrefix] {
				continue
			}
			checkedDirs[curPrefix] = true
			if fi, lerr := root.Lstat(filepath.FromSlash(curPrefix)); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
				secErr = fmt.Errorf("%w: rechazando extracción a través de symlink existente %s", errUnsafeArchiveMember, filepath.Join(dir, filepath.FromSlash(curPrefix)))
				return errStopIter
			}
		}
		if !m.IsDir {
			if fi, lerr := root.Lstat(rel); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
				secErr = fmt.Errorf("%w: rechazando escritura sobre symlink existente %s", errUnsafeArchiveMember, filepath.Join(dir, rel))
				return errStopIter
			}
		}
		if m.Mode&os.ModeSymlink != 0 {
			if parentPrefixes[relSlash] {
				secErr = fmt.Errorf("%w: ruta atraviesa symlink interno del archivo %s", errUnsafeArchiveMember, relSlash)
				return errStopIter
			}
			archiveSymlinks[relSlash] = true
			symlinkMembers = append(symlinkMembers, rel)
		}
		top := parts[0]
		if top != "" && top != "." && top != ".." && !seenTop[top] {
			seenTop[top] = true
			outputs = append(outputs, filepath.Join(dir, top))
		}
		return nil
	})
	if secErr != nil {
		return nil, nil, secErr
	}
	if len(outputs) == 0 && iterErr != nil {
		WriteLogf("  %s⚠ No se pudo listar %s para limpiar salidas parciales%s\n", Yellow, file, NC)
		return nil, nil, nil
	}
	return outputs, symlinkMembers, nil
}

func verifyExtractedSymlinksInRoot(dir string, symlinkMembers []string) error {
	if len(symlinkMembers) == 0 {
		return nil
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, rel := range symlinkMembers {
		target, err := root.Readlink(rel)
		if err != nil {
			continue
		}
		if !isSymlinkTargetWithinRoot(filepath.ToSlash(rel), target) {
			_ = root.Remove(rel)
			return fmt.Errorf("%w: symlink %s -> %s escapa del directorio destino", errUnsafeArchiveMember, rel, target)
		}
	}
	return nil
}

func listTopLevelContainerOutputs(file string, dir string, password string) []string {
	seen := make(map[string]bool)
	var outputs []string
	err := IterArchiveMembers(file, password, func(m ArchiveMember) error {
		top, ok := topLevelArchiveEntry(m.Path)
		if !ok || seen[top] {
			return nil
		}
		seen[top] = true
		outputs = append(outputs, filepath.Join(dir, top))
		return nil
	})
	if len(outputs) == 0 && err != nil {
		WriteLogf("  %s⚠ No se pudo listar %s para limpiar salidas parciales%s\n", Yellow, file, NC)
		return nil
	}
	return outputs
}

const maxTarLineCarry = 64 * 1024

type tarTopLevelTracker struct {
	mu      sync.Mutex
	rem     []byte
	seen    map[string]bool
	topList []string
	out     io.Writer
}

func newTarTopLevelTracker(out io.Writer) *tarTopLevelTracker {
	return &tarTopLevelTracker{
		seen: make(map[string]bool),
		out:  out,
	}
}

func (t *tarTopLevelTracker) recordTopLevelLocked(top string) {
	if top == "" || top == "." || top == ".." || t.seen[top] {
		return
	}
	t.seen[top] = true
	t.topList = append(t.topList, top)
}

func (t *tarTopLevelTracker) recordLineLocked(raw string) {
	line := strings.TrimSpace(raw)
	if line == "" {
		return
	}
	top, ok := topLevelArchiveEntry(line)
	if !ok {
		return
	}
	t.recordTopLevelLocked(top)
}

func (t *tarTopLevelTracker) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.out != nil {
		_, _ = t.out.Write(p)
	}
	data := p
	if len(t.rem) > 0 {
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
			t.rem = append(t.rem, data[:idx]...)
			t.recordLineLocked(string(t.rem))
			t.rem = t.rem[:0]
			data = data[idx+1:]
		} else {
			if len(t.rem)+len(data) <= maxTarLineCarry {
				t.rem = append(t.rem, data...)
			}
			return len(p), nil
		}
	}
	for {
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			break
		}
		t.recordLineLocked(string(data[:idx]))
		data = data[idx+1:]
	}
	if len(data) > 0 && len(data) <= maxTarLineCarry {
		t.rem = append(t.rem[:0], data...)
	}
	return len(p), nil
}

func (t *tarTopLevelTracker) Flush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.rem) > 0 {
		t.recordLineLocked(string(t.rem))
		t.rem = t.rem[:0]
	}
}

func (t *tarTopLevelTracker) Outputs(dir string) []string {
	t.Flush()
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.topList))
	for _, top := range t.topList {
		out = append(out, filepath.Join(dir, top))
	}
	return out
}

func isArchiveFullyExtracted(file string, dir string, password string) bool {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	defer root.Close()

	memberCount := 0
	allExtracted := true
	err = IterArchiveMembers(file, password, func(m ArchiveMember) error {
		memberCount++
		if m.IsDir {
			return nil
		}
		rel, ok := sanitizeArchiveRelPath(m.Path)
		if !ok {
			allExtracted = false
			return errStopIter
		}
		fi, err := root.Lstat(rel)
		if err != nil || fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			allExtracted = false
			return errStopIter
		}
		if m.Size > 0 && fi.Size() != m.Size {
			allExtracted = false
			return errStopIter
		}
		return nil
	})
	return err == nil && memberCount > 0 && allExtracted
}

func collectCmdLines(cmd *exec.Cmd) ([]string, bool) {
	if cmd.Stdin == nil {
		cmd.Stdin = getNullFile()
	}
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false
	}
	if err := cmd.Start(); err != nil {
		return nil, false
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, listScannerInitBuf), listScannerMaxBuf)
	var members []string
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			members = append(members, line)
		}
	}
	_ = cmd.Wait()
	return members, len(members) > 0
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
	args = append(args, safeFlagArg(file), "--")
	return collectCmdLines(execCommand("tar", args...))
}

func listRarMembers(file string, password string) ([]string, bool) {
	tool := rarBin()
	if !hasTool(tool) {
		return nil, false
	}
	args := []string{"lb"}
	if password != "" {
		args = append(args, "-p"+password)
	} else {
		args = append(args, "-p-")
	}
	args = append(args, "--", safeFlagArg(file))
	return collectCmdLines(execCommand(tool, args...))
}

func resolveOutputs(members []string, dir string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, m := range members {
		rel, ok := sanitizeArchiveRelPath(m)
		if !ok {
			continue
		}
		full := filepath.Join(dir, rel)
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

	if info.Format.IsContainer() {
		resolvedPass, passErr := GetPasswordManager().ResolvePassword(file, info.Format, opts.Password)
		if passErr != nil {
			if errors.Is(passErr, ErrPasswordSkipped) {
				WriteLogf("  %s⚠ Omitido: %s requiere contraseña%s\n", Yellow, file, NC)
			}
			return passErr
		}
		opts.Password = resolvedPass
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
		needed = safeScaleInt64(compressedSize, 4)
	}
	if needed > 0 {
		if err := CheckDiskSpace(withDiskSafetyMargin(needed), dir, "descomprimir"); err != nil {
			return err
		}
	}

	isTar := info.IsTar || info.Format == Tar
	var outputs []string
	var symlinkMembers []string
	var preExisting map[string]bool
	var preExistingTop map[string]bool
	var tarTracker *tarTopLevelTracker

	if isTar {
		preExistingTop = make(map[string]bool)
		if entries, readErr := os.ReadDir(dir); readErr == nil {
			for _, e := range entries {
				preExistingTop[e.Name()] = true
			}
		}
		var verboseOut io.Writer
		if opts.Verbose && opts.Progress == nil {
			verboseOut = os.Stdout
		}
		tarTracker = newTarTopLevelTracker(verboseOut)
	} else if info.IsStream() {
		if outName, vErr := validateOutputName(stripCompressionExt(filepath.Base(file))); vErr == nil {
			outputs = []string{filepath.Join(dir, outName)}
		}
		if len(outputs) > 0 && fp != nil {
			fp.SetOutPath(outputs[0])
		}
		preExisting = make(map[string]bool, len(outputs))
		for _, o := range outputs {
			if _, lerr := os.Lstat(o); lerr == nil {
				preExisting[o] = true
			}
		}
	} else {
		var prepErr error
		outputs, symlinkMembers, prepErr = prepareContainerOutputs(file, dir, info, opts)
		if prepErr != nil {
			return prepErr
		}
		if len(outputs) > 0 && fp != nil {
			fp.SetOutPath(outputs[0])
		}
		preExisting = make(map[string]bool, len(outputs))
		for _, o := range outputs {
			if _, lerr := os.Lstat(o); lerr == nil {
				preExisting[o] = true
			}
		}
	}

	if isTar {
		err = decompressTar(file, dir, info, opts, fp, tarTracker)
		outputs = tarTracker.Outputs(dir)
		preExisting = make(map[string]bool, len(outputs))
		for _, o := range outputs {
			if preExistingTop[filepath.Base(o)] {
				preExisting[o] = true
			}
		}
	} else {
		err = decompressSingle(file, dir, info, opts, fp)
		if err == nil {
			err = verifyExtractedSymlinksInRoot(dir, symlinkMembers)
		}
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

	var realSize int64
	var foundDiskFiles bool
	for _, o := range outputs {
		targetPath := o
		fi, err := os.Lstat(targetPath)
		if err != nil && filepath.Ext(targetPath) == "" {
			if isoFi, isoErr := os.Lstat(targetPath + ".iso"); isoErr == nil {
				targetPath += ".iso"
				fi = isoFi
				err = nil
			}
		}
		if err == nil {
			foundDiskFiles = true
			if fi.IsDir() {
				dirSz, _ := GetDirSize(targetPath)
				realSize += dirSz
			} else {
				realSize += fi.Size()
			}
		}
	}

	uncompressedSize := realSize
	if !foundDiskFiles {
		if uncompressedSize == 0 {
			uncompressedSize = needed
		}
		if uncompressedSize == 0 {
			uncompressedSize = compressedSize * 4
		}
	}

	if fp != nil {
		fp.SetUncompressedSize(uncompressedSize)
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

	if !opts.QuietReport {
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
	}

	if opts.Progress != nil {
		opts.Progress.FileDone(file)
	}

	return nil
}

func buildTarExtractArgs(tarFile, dir string, opts DecompressOptions) []string {
	var args []string
	if tarFile == "-" {
		args = []string{"-xvf", "-"}
	} else {
		args = []string{"-xv", "-f", safeFlagArg(tarFile)}
	}
	args = append(args, "-C", safeFlagArg(dir))
	if !opts.Force && !opts.Resume {
		args = append(args, "--keep-old-files")
	}
	if opts.Filter != "" {
		args = append(args, "--wildcards", "--", opts.Filter)
	} else {
		args = append(args, "--")
	}
	return args
}

func build7zExtractArgs(file, dir string, opts DecompressOptions, baseFlags ...string) []string {
	args := append([]string(nil), baseFlags...)
	if opts.Password != "" {
		args = append(args, "-p"+opts.Password)
	} else {
		args = append(args, "-p-")
	}
	if opts.Force || opts.Resume {
		args = append(args, "-aoa")
	} else {
		args = append(args, "-aos")
	}
	args = append(args, fmt.Sprintf("-o%s", safeFlagArg(dir)), "--", safeFlagArg(file))
	if opts.Filter != "" {
		args = append(args, opts.Filter)
	}
	return args
}

func augmentPipelineErr(errBuf io.Writer, err error) error {
	if err == nil {
		return nil
	}
	if lb, ok := errBuf.(*limitedBuffer); ok {
		if s := strings.TrimSpace(lb.String()); s != "" {
			return fmt.Errorf("%w: %s", err, s)
		}
	}
	return err
}

func validateStageEntryAgainstRoot(stageDir, dir string, root *os.Root, opts DecompressOptions) ([]string, error) {
	topEntries, err := os.ReadDir(stageDir)
	if err != nil {
		return nil, err
	}
	if len(topEntries) == 0 {
		return nil, nil
	}

	var topNames []string
	for _, e := range topEntries {
		topNames = append(topNames, e.Name())
	}

	err = filepath.WalkDir(stageDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(stageDir, p)
		if err != nil || rel == "." {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if _, ok := sanitizeArchiveRelPath(relSlash); !ok {
			return fmt.Errorf("%w: ruta insegura %q", errUnsafeArchiveMember, rel)
		}
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(p)
			if err != nil || !isSymlinkTargetWithinRoot(relSlash, target) {
				return fmt.Errorf("%w: symlink %s -> %s escapa del directorio destino", errUnsafeArchiveMember, rel, target)
			}
		}
		destInfo, lerr := root.Lstat(rel)
		if lerr != nil {
			if !errors.Is(lerr, os.ErrNotExist) {
				return lerr
			}
			return nil
		}
		if destInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: rechazando escritura sobre symlink existente %s", errUnsafeArchiveMember, filepath.Join(dir, rel))
		}
		if d.IsDir() {
			if !destInfo.IsDir() && !opts.Force && !opts.Resume {
				return fmt.Errorf("el archivo de salida %s ya existe (use --force para sobrescribir)", filepath.Join(dir, rel))
			}
			return nil
		}
		if destInfo.IsDir() {
			return fmt.Errorf("no se puede sobrescribir directorio existente %s con archivo", filepath.Join(dir, rel))
		}
		if !opts.Force && !opts.Resume {
			return fmt.Errorf("el archivo de salida %s ya existe (use --force para sobrescribir)", filepath.Join(dir, rel))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return topNames, nil
}

func mergeStageNode(stageDir, dir, rel string, root *os.Root) error {
	srcPath := filepath.Join(stageDir, rel)
	dstPath := filepath.Join(dir, rel)

	destInfo, lerr := root.Lstat(rel)
	if errors.Is(lerr, os.ErrNotExist) {
		return os.Rename(srcPath, dstPath)
	}
	if lerr != nil {
		return lerr
	}
	srcInfo, err := os.Lstat(srcPath)
	if err != nil {
		return err
	}
	if srcInfo.IsDir() && destInfo.IsDir() {
		children, err := os.ReadDir(srcPath)
		if err != nil {
			return err
		}
		for _, child := range children {
			if err := mergeStageNode(stageDir, dir, filepath.Join(rel, child.Name()), root); err != nil {
				return err
			}
		}
		return nil
	}
	if err := root.RemoveAll(rel); err != nil {
		return err
	}
	return os.Rename(srcPath, dstPath)
}

func mergeTarStageIntoDir(stageDir, dir string, opts DecompressOptions, tracker *tarTopLevelTracker) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("Error abriendo directorio de salida: %w", err)
	}
	defer root.Close()

	topNames, err := validateStageEntryAgainstRoot(stageDir, dir, root, opts)
	if err != nil {
		return err
	}
	for _, top := range topNames {
		if err := mergeStageNode(stageDir, dir, top, root); err != nil {
			return err
		}
		if tracker != nil {
			tracker.mu.Lock()
			tracker.recordTopLevelLocked(top)
			tracker.mu.Unlock()
		}
	}
	return nil
}

func decompressTar(file string, dir string, info FormatInfo, opts DecompressOptions, fp *FileProgress, trackers ...*tarTopLevelTracker) error {
	if info.Tool == "" {
		return fmt.Errorf("No se detectó herramienta para: %s", file)
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("Error creando directorio de salida: %w", err)
	}
	stageDir, err := os.MkdirTemp(dir, ".crush-stage-*")
	if err != nil {
		return fmt.Errorf("Error creando directorio temporal de extracción: %w", err)
	}
	defer os.RemoveAll(stageDir)

	WriteLogf("  → %s/\n", dir)

	var tracker *tarTopLevelTracker
	if len(trackers) > 0 && trackers[0] != nil {
		tracker = trackers[0]
	} else {
		tracker = newTarTopLevelTracker(nil)
	}

	if strings.HasSuffix(strings.ToLower(file), ".tar") {
		tarExtract := execCommand("tar", buildTarExtractArgs(file, stageDir, opts)...)
		tarExtract.Stdout = tracker
		tarExtract.Stderr = stderrFor(opts.Progress)
		if err := augmentErr(tarExtract, tarExtract.Run()); err != nil {
			return fmt.Errorf("Error extrayendo %s: %w", file, err)
		}
		if err := mergeTarStageIntoDir(stageDir, dir, opts, tracker); err != nil {
			return fmt.Errorf("Error extrayendo %s: %w", file, err)
		}
		return nil
	}

	parts := findSplitParts(file)
	tarExtract := execCommand("tar", buildTarExtractArgs("-", stageDir, opts)...)
	budget := effectiveDecompressMemBudget(opts.MemBudgetMB)

	if len(parts) == 1 && opts.Progress == nil && hasTool("pv") {
		decompCmd, closer := pipeCmdForWithMem(info, file, opts.ThreadLimit, budget)
		if closer != nil {
			defer closer.Close()
		}

		pvArgs := []string{"-f", "-B", "256k"}
		if size := EstimateUncompressedSize(file); size > 0 {
			pvArgs = append(pvArgs, "-s", fmt.Sprintf("%d", size))
		}
		pvCmd := execCommand("pv", pvArgs...)

		if err := pipeline(tracker, os.Stderr, decompCmd, pvCmd, tarExtract); err != nil {
			return fmt.Errorf("Error extrayendo %s: %w", file, err)
		}
	} else {
		decompCmd, closer, err := pipeCmdForPartsWithMem(info, parts, opts.Progress, fp, opts.ThreadLimit, budget)
		if err != nil {
			return fmt.Errorf("Error preparando descompresión de %s: %w", file, err)
		}
		if closer != nil {
			defer closer.Close()
		}

		errBuf := stderrFor(opts.Progress)
		if err := pipeline(tracker, errBuf, decompCmd, tarExtract); err != nil {
			return fmt.Errorf("Error extrayendo %s: %w", file, augmentPipelineErr(errBuf, err))
		}
	}

	if err := mergeTarStageIntoDir(stageDir, dir, opts, tracker); err != nil {
		return fmt.Errorf("Error extrayendo %s: %w", file, err)
	}
	return nil
}

// pipeCmdFor construye el comando de descompresión por pipe. lz4 detecta el
// formato por la extensión del nombre (case-sensitive), así que con .LZ4 debe
// leer por stdin (magic). El io.Closer devuelto cierra el archivo si se abrió.
func pipeCmdFor(info FormatInfo, file string, threads ...int) (*exec.Cmd, io.Closer) {
	th := 0
	if len(threads) > 0 {
		th = threads[0]
	}
	return pipeCmdForWithMem(info, file, th, getMemLimit())
}

func pipeCmdForWithMem(info FormatInfo, file string, threads int, memBudgetMB int) (*exec.Cmd, io.Closer) {
	th := NCPU()
	if threads > 0 {
		th = threads
	}
	flags := PipeFlagsForThreadsAndMem(info, th, memBudgetMB)
	cmd := execCommand(info.Tool, flags...)
	if info.Tool == "lz4" {
		in, err := os.Open(file)
		if err != nil {
			cmd.Stdin = getNullFile()
			return cmd, nil
		}
		cmd.Stdin = in
		return cmd, in
	}
	cmd.Args = append(cmd.Args, "--", safeFlagArg(file))
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
	th := 0
	if len(threads) > 0 {
		th = threads[0]
	}
	return pipeCmdForPartsWithMem(info, parts, pt, fp, th, getMemLimit())
}

func pipeCmdForPartsWithMem(info FormatInfo, parts []string, pt *ProgressTracker, fp *FileProgress, threads int, memBudgetMB int) (*exec.Cmd, io.Closer, error) {
	if len(parts) == 0 {
		return nil, nil, fmt.Errorf("no se especificaron partes para descomprimir")
	}
	th := NCPU()
	if threads > 0 {
		th = threads
	}
	flags := PipeFlagsForThreadsAndMem(info, th, memBudgetMB)
	cmd := execCommand(info.Tool, flags...)
	if len(parts) == 1 && info.Tool == "lrzip" {
		cmd.Args = append(cmd.Args, "--", safeFlagArg(parts[0]))
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

func resolveDecompressSingleThreads(fileSize int64, info FormatInfo, opts DecompressOptions, budget int) int {
	if opts.ThreadLimit > 0 {
		return opts.ThreadLimit
	}
	return resolveThreadCapForBudget(info.Format, fileSize, budget)
}

func decompressSingle(file string, dir string, info FormatInfo, opts DecompressOptions, fp *FileProgress) error {
	ext := strings.ToLower(file)
	budget := effectiveDecompressMemBudget(opts.MemBudgetMB)
	var fileSize int64
	if fi, statErr := os.Stat(file); statErr == nil {
		fileSize = fi.Size()
	}
	threads := resolveDecompressSingleThreads(fileSize, info, opts, budget)

	switch {
	case strings.HasSuffix(ext, ".zip"):
		sevenz := sevenzBin()
		if hasTool(sevenz) {
			args := build7zExtractArgs(file, dir, opts, "x", "-tzip", "-bsp1", "-mmt="+threadStr(threads))
			cmd := execCommand(sevenz, args...)
			if fileSize > 0 {
				return runWithProgress(cmd, opts.Progress, fileSize, fp)
			}
			cmd.Stderr = stderrFor(opts.Progress)
			return augmentErr(cmd, cmd.Run())
		}
		args := []string{"-P", opts.Password}
		if opts.Force || opts.Resume {
			args = append(args, "-o")
		} else {
			args = append(args, "-n")
		}
		args = append(args, "-d", safeFlagArg(dir), "--", safeFlagArg(file))
		if opts.Filter != "" {
			args = append(args, opts.Filter)
		}
		cmd := execCommand("unzip", args...)
		cmd.Stdin = getNullFile()
		cmd.Stdout = stdoutFor(opts.Progress)
		cmd.Stderr = stderrFor(opts.Progress)
		return augmentErr(cmd, cmd.Run())

	case strings.HasSuffix(ext, ".7z"):
		args := build7zExtractArgs(file, dir, opts, "x", "-bsp1", "-y", "-mmt="+threadStr(threads))
		cmd := execCommand(info.Tool, args...)
		if fileSize > 0 {
			return runWithProgress(cmd, opts.Progress, fileSize, fp)
		}
		cmd.Stderr = stderrFor(opts.Progress)
		return augmentErr(cmd, cmd.Run())

	case strings.HasSuffix(ext, ".rar"):
		args := []string{"x", "-y", "-mt" + threadStr(threads)}
		if opts.Password != "" {
			args = append(args, "-p"+opts.Password)
		} else {
			args = append(args, "-p-")
		}
		if opts.Force || opts.Resume {
			args = append(args, "-o+")
		} else {
			args = append(args, "-o-")
		}
		args = append(args, "--", safeFlagArg(file))
		if opts.Filter != "" {
			args = append(args, opts.Filter)
		}
		args = append(args, fmt.Sprintf("%s/", safeFlagArg(dir)))
		cmd := execCommand(info.Tool, args...)
		if fileSize > 0 {
			return runWithProgress(cmd, opts.Progress, fileSize, fp)
		}
		cmd.Stderr = stderrFor(opts.Progress)
		return augmentErr(cmd, cmd.Run())

	case strings.HasSuffix(ext, ".tar"):
		args := buildTarExtractArgs(file, dir, opts)
		cmd := execCommand("tar", args...)
		cmd.Stdout = stdoutFor(opts.Progress)
		cmd.Stderr = stderrFor(opts.Progress)
		return augmentErr(cmd, cmd.Run())

	case strings.HasSuffix(ext, ".lrz"):
		outName, err := validateOutputName(stripCompressionExt(filepath.Base(file)))
		if err != nil {
			return err
		}
		outputPath := filepath.Join(dir, outName)
		root, err := os.OpenRoot(dir)
		if err != nil {
			return fmt.Errorf("Error abriendo directorio de salida: %w", err)
		}
		defer root.Close()

		if opts.Progress == nil && hasTool("pv") {
			outFile, err := openDecompressOutput(root, outName, outputPath, opts)
			if err != nil {
				return err
			}
			defer outFile.Close()
			pipeFlags := PipeFlagsForThreadsAndMem(info, threads, budget)
			decompCmd := execCommand(info.Tool, pipeFlags...)
			decompCmd.Args = append(decompCmd.Args, "--", safeFlagArg(file))
			pvArgs := []string{"-f", "-B", "256k"}
			if size := EstimateUncompressedSize(file); size > 0 {
				pvArgs = append(pvArgs, "-s", fmt.Sprintf("%d", size))
			}
			if err := pipeline(outFile, os.Stderr, decompCmd, execCommand("pv", pvArgs...)); err != nil {
				return err
			}
			outFile.Close()
			checkAndApplyIsoExtensionInRoot(root, outName, opts.Force || opts.Resume)
			return nil
		}
		if err := ensureLrzOutputSafe(root, outName, outputPath, opts); err != nil {
			return err
		}
		args := []string{"-d", "-p", threadStr(threads)}
		if budget > 0 {
			args = append(args, "-m", strconv.Itoa(max(1, budget/100)))
		}
		args = append(args, "-o", safeFlagArg(outputPath), "--", safeFlagArg(file))
		cmd := execCommand("lrzip", args...)
		cmd.Stdout = stdoutFor(opts.Progress)
		cmd.Stderr = stderrFor(opts.Progress)
		if err := augmentErr(cmd, cmd.Run()); err != nil {
			return err
		}
		checkAndApplyIsoExtensionInRoot(root, outName, opts.Force || opts.Resume)
		return nil

	default:
		outName, err := validateOutputName(stripCompressionExt(filepath.Base(file)))
		if err != nil {
			return err
		}
		outputPath := filepath.Join(dir, outName)
		root, err := os.OpenRoot(dir)
		if err != nil {
			return fmt.Errorf("Error abriendo directorio de salida: %w", err)
		}
		defer root.Close()

		outFile, err := openDecompressOutput(root, outName, outputPath, opts)
		if err != nil {
			return err
		}
		defer outFile.Close()

		parts := findSplitParts(file)
		if len(parts) == 1 && opts.Progress == nil && hasTool("pv") {
			decompCmd, closer := pipeCmdForWithMem(info, file, threads, budget)
			if closer != nil {
				defer closer.Close()
			}
			pvArgs := []string{"-f", "-B", "256k"}
			if size := EstimateUncompressedSize(file); size > 0 {
				pvArgs = append(pvArgs, "-s", fmt.Sprintf("%d", size))
			}
			pvCmd := execCommand("pv", pvArgs...)
			if err := pipeline(outFile, os.Stderr, decompCmd, pvCmd); err != nil {
				return fmt.Errorf("Error descomprimiendo %s: %w", file, err)
			}
			outFile.Close()
			checkAndApplyIsoExtensionInRoot(root, outName, opts.Force || opts.Resume)
			return nil
		}
		decompCmd, closer, err := pipeCmdForPartsWithMem(info, parts, opts.Progress, fp, threads, budget)
		if err != nil {
			return fmt.Errorf("Error preparando descompresión de %s: %w", file, err)
		}
		if closer != nil {
			defer closer.Close()
		}
		decompCmd.Stdout = outFile
		decompCmd.Stderr = stderrFor(opts.Progress)
		if err := augmentErr(decompCmd, decompCmd.Run()); err != nil {
			return fmt.Errorf("Error descomprimiendo %s: %w", file, err)
		}
		outFile.Close()
		checkAndApplyIsoExtensionInRoot(root, outName, opts.Force || opts.Resume)
		return nil
	}
}

func validateOutputName(name string) (string, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || !filepath.IsLocal(name) {
		return "", fmt.Errorf("nombre de archivo de salida inválido: %q", name)
	}
	return name, nil
}

func ensureLrzOutputSafe(root *os.Root, outName, displayPath string, opts DecompressOptions) error {
	fi, err := root.Lstat(outName)
	if err != nil {
		return nil
	}
	if !opts.Force && !opts.Resume {
		return fmt.Errorf("el archivo de salida %s ya existe (use --force para sobrescribir)", displayPath)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("rechazando escritura sobre symlink existente: %s", displayPath)
	}
	return nil
}

func openDecompressOutput(root *os.Root, outName, displayPath string, opts DecompressOptions) (*os.File, error) {
	flags := os.O_CREATE | os.O_WRONLY
	if !opts.Force && !opts.Resume {
		if _, err := root.Lstat(outName); err == nil {
			return nil, fmt.Errorf("el archivo de salida %s ya existe (use --force para sobrescribir)", displayPath)
		}
		flags |= os.O_EXCL
	} else {
		flags |= os.O_TRUNC
	}
	outFile, err := root.OpenFile(outName, flags, 0644)
	if err != nil {
		if os.IsExist(err) && !opts.Force && !opts.Resume {
			return nil, fmt.Errorf("el archivo de salida %s ya existe (use --force para sobrescribir)", displayPath)
		}
		return nil, fmt.Errorf("Error creando archivo de salida: %w", err)
	}
	return outFile, nil
}

func checkAndApplyIsoExtensionInRoot(root *os.Root, outName string, allowOverwrite bool) {
	if filepath.Ext(outName) != "" {
		return
	}
	f, err := root.Open(outName)
	if err != nil {
		return
	}
	buf := make([]byte, 5)
	n, err := f.ReadAt(buf, 32769)
	f.Close()
	if (err == nil || errors.Is(err, io.EOF)) && n == 5 && string(buf) == "CD001" {
		isoName := outName + ".iso"
		if fi, statErr := root.Lstat(isoName); statErr == nil {
			if !allowOverwrite || fi.Mode()&os.ModeSymlink != 0 {
				return
			}
		}
		_ = root.Rename(outName, isoName)
	}
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
	if w.file == nil {
		return 0, fmt.Errorf("splitWriter cerrado")
	}
	if w.split <= 0 {
		return 0, fmt.Errorf("tamaño de división inválido: %d", w.split)
	}
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
			closeErr := w.file.Close()
			w.file = nil
			if closeErr != nil {
				return totalWritten, closeErr
			}
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

func PromptDecompressAll(r io.Reader, w io.Writer, files []string, dir ...string) (bool, error) {
	if len(files) == 0 {
		return false, nil
	}

	dirMsg := "en el directorio actual"
	if len(dir) > 0 && dir[0] != "" && dir[0] != "." {
		dirMsg = fmt.Sprintf("en '%s'", dir[0])
	}
	fmt.Fprintf(w, "%sArchivos comprimidos detectados %s (%d):%s\n", Bold, dirMsg, len(files), NC)
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
