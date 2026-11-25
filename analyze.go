package main

import (
	"compress/flate"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	analyzeWalkDir              = filepath.WalkDir
	analyzeFileFunc             = analyzeFileWithSize
	analyzeIsTerminal           = isTerminal
	analyzeStderr     io.Writer = os.Stderr
	analyzeIsRotational         = IsRotational
)

// FileAnalysis almacena las métricas individuales de análisis de un archivo regular.
type FileAnalysis struct {
	Path           string  // Ruta del archivo
	Size           int64   // Tamaño real en bytes
	SampleBytes    int64   // Bytes reales leídos de la unidad de almacenamiento durante el muestreo
	Entropy        float64 // Entropía de Shannon (0.00 a 8.00 b/B)
	ProbeRatio     float64 // Ratio de la micro-sonda DEFLATE
	Compressible   bool    // ¿Es razonablemente compresible?
	CompLevel      string  // "Alta", "Media", "Nula"
	RecommendedFmt string  // Formato recomendado ("xz", "zst", "7z", "omitir")
	EstimatedSize  int64   // Tamaño estimado resultante tras compresión
	SavingsBytes   int64   // Bytes potenciales de ahorro (Size - EstimatedSize)
	SavingsPct     float64 // Porcentaje de ahorro
}

// AnalyzeFile analiza un archivo individual y devuelve su reporte de compresibilidad.
func AnalyzeFile(path string) (FileAnalysis, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return FileAnalysis{}, err
	}
	return analyzeFileWithSize(path, fi.Size())
}

// analyzeFileWithSize analiza un archivo reutilizando un tamaño ya conocido para evitar llamadas os.Stat adicionales.
func analyzeFileWithSize(path string, size int64) (FileAnalysis, error) {
	if size < 0 {
		size = 0
	}
	res := FileAnalysis{
		Path:          path,
		Size:          size,
		EstimatedSize: size,
	}

	if size == 0 {
		res.Compressible = false
		res.CompLevel = "Nula"
		res.RecommendedFmt = "omitir"
		res.EstimatedSize = 0
		res.SampleBytes = 0
		return res, nil
	}

	if size < 512 {
		res.Compressible = false
		res.CompLevel = "Nula"
		res.RecommendedFmt = "omitir"
		res.SampleBytes = size
		return res, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return FileAnalysis{}, err
	}
	defer f.Close()

	bufPtr := sampleBufferPool.Get().(*[]byte)
	defer sampleBufferPool.Put(bufPtr)

	targetBuf := *bufPtr
	if len(targetBuf) < sampleBufferSize {
		targetBuf = make([]byte, sampleBufferSize)
	}

	ext := strings.ToLower(filepath.Ext(path))

	var sample []byte
	var entropy float64
	var probeRatio float64

	if size <= int64(sampleBufferSize) {
		// Archivo pequeño (<= 96 KiB): lectura secuencial completa en una sola llamada
		n, err := io.ReadFull(f, targetBuf[:size])
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return FileAnalysis{}, err
		}
		sample = targetBuf[:n]
		res.SampleBytes = int64(n)
		entropy = CalculateShannonEntropy(sample)
		res.Entropy = entropy

		if (len(sample) >= 4096 && entropy >= 7.90) || (mediaOrCompressedExts[ext] && entropy >= 7.20) {
			res.Compressible = false
			res.CompLevel = "Nula"
			res.RecommendedFmt = "omitir"
			res.ProbeRatio = 1.0
			return res, nil
		}
	} else {
		// Archivo mayor a 96 KiB: lectura inicial de cabecera (32 KiB)
		n1, err := f.ReadAt(targetBuf[:sampleChunkSize], 0)
		if err != nil && err != io.EOF {
			return FileAnalysis{}, err
		}
		if n1 == 0 {
			res.Compressible = false
			res.CompLevel = "Nula"
			res.RecommendedFmt = "omitir"
			return res, nil
		}

		h1 := CalculateShannonEntropy(targetBuf[:n1])

		// Fast-path: Si la cabecera ya es incompresible (entropía máxima o archivo pre-comprimido/multimedia),
		// evitamos los seeks a la mitad y al final del disco y la micro-sonda DEFLATE.
		if (n1 >= 4096 && h1 >= 7.85) || (mediaOrCompressedExts[ext] && h1 >= 7.20) {
			res.SampleBytes = int64(n1)
			res.Entropy = h1
			res.Compressible = false
			res.CompLevel = "Nula"
			res.RecommendedFmt = "omitir"
			res.ProbeRatio = 1.0
			return res, nil
		}

		// Cabecera compresible: completar el muestreo estratificado con fragmentos de mitad y final
		fullSample, n2, n3, err := readRemainingChunks(f, size, targetBuf, n1)
		if err != nil {
			return FileAnalysis{}, err
		}
		sample = fullSample

		chunkHead := fullSample[:n1]
		var chunkMid, chunkTail []byte
		if n2 > 0 && len(fullSample) >= n1+n2 {
			chunkMid = fullSample[n1 : n1+n2]
		}
		if n3 > 0 && len(fullSample) >= n1+n2+n3 {
			chunkTail = fullSample[n1+n2 : n1+n2+n3]
		}

		hMid := h1
		if len(chunkMid) > 0 {
			hMid = CalculateShannonEntropy(chunkMid)
		}

		// Puerta del cuerpo dominante: en archivos grandes (>= 192 KiB), si el cuerpo central
		// es de alta entropía (ruido/video/cifrado), el archivo es incompresible con independencia
		// de cuántos metadatos o ceros contenga la cabecera.
		if len(chunkMid) >= 4096 && size >= int64(sampleBufferSize*2) {
			if hMid >= 7.85 || (mediaOrCompressedExts[ext] && hMid >= 7.20) {
				res.SampleBytes = int64(len(sample))
				res.Entropy = hMid
				res.Compressible = false
				res.CompLevel = "Nula"
				res.RecommendedFmt = "omitir"
				res.ProbeRatio = 1.0
				return res, nil
			}
		}

		// Extrapolación ponderada por volumen real
		ratioHead := probeDeflateRatio(chunkHead)
		ratioMid := ratioHead
		if len(chunkMid) > 0 {
			ratioMid = probeDeflateRatio(chunkMid)
		}
		ratioTail := ratioMid
		if len(chunkTail) > 0 {
			ratioTail = probeDeflateRatio(chunkTail)
		}

		hHead := h1
		hTail := hMid
		if len(chunkTail) > 0 {
			hTail = CalculateShannonEntropy(chunkTail)
		}

		wHead := int64(len(chunkHead) * 2)
		wTail := int64(len(chunkTail) * 2)
		if wHead+wTail > size {
			wHead = size / 2
			wTail = size - wHead
		}
		wBody := size - wHead - wTail
		if wBody < 0 {
			wBody = 0
		}

		probeRatio = (float64(wHead)*ratioHead + float64(wBody)*ratioMid + float64(wTail)*ratioTail) / float64(size)
		entropy = (float64(wHead)*hHead + float64(wBody)*hMid + float64(wTail)*hTail) / float64(size)
		res.SampleBytes = int64(len(sample))
		res.Entropy = entropy
		res.ProbeRatio = probeRatio

		if (size >= 4096 && entropy >= 7.90) || (mediaOrCompressedExts[ext] && entropy >= 7.20) {
			res.Compressible = false
			res.CompLevel = "Nula"
			res.RecommendedFmt = "omitir"
			res.ProbeRatio = 1.0
			return res, nil
		}
	}

	if size <= int64(sampleBufferSize) {
		probeRatio = probeDeflateRatio(sample)
		res.ProbeRatio = probeRatio
	}

	if probeRatio >= 0.96 {
		res.Compressible = false
		res.CompLevel = "Nula"
		res.RecommendedFmt = "omitir"
		return res, nil
	}

	// Archivo compresible: determinar grado y formato
	res.Compressible = true

	isHigh := probeRatio <= 0.45 || (entropy < 5.50 && probeRatio <= 0.60) || (textOrCodeExts[ext] && probeRatio < 0.70)

	var estRatio float64
	if isHigh {
		res.CompLevel = "Alta"
		if hasTool("xz") {
			res.RecommendedFmt = "xz"
		} else if hasTool(sevenzBin()) {
			res.RecommendedFmt = "7z"
		} else {
			res.RecommendedFmt = "zst"
		}
		estRatio = probeRatio * 0.78
		if estRatio < 0.01 {
			estRatio = 0.01
		}
		if estRatio > 0.94 {
			estRatio = 0.94
		}
	} else {
		res.CompLevel = "Media"
		if hasTool("zstd") {
			res.RecommendedFmt = "zst"
		} else if hasTool(sevenzBin()) {
			res.RecommendedFmt = "7z"
		} else {
			res.RecommendedFmt = "gz"
		}
		estRatio = probeRatio * 0.86
		if estRatio < 0.02 {
			estRatio = 0.02
		}
		if estRatio > 0.95 {
			estRatio = 0.95
		}
	}

	estSize := int64(math.Round(float64(size) * estRatio))
	if estSize > size {
		estSize = size
	}
	if estSize < 0 {
		estSize = 0
	}
	res.EstimatedSize = estSize
	res.SavingsBytes = size - estSize
	res.SavingsPct = (float64(res.SavingsBytes) / float64(size)) * 100.0

	return res, nil
}

// AnalyzeOptions configura la ejecución de crush -analyze.
type AnalyzeOptions struct {
	Verbose bool     // Muestra el desglose detallado de archivos internos
	Clean   bool     // Aplica DefaultCleanExcludes
	Exclude []string // Patrones de exclusión adicionales
}

// EntryReport representa una entrada del informe (subdirectorio o archivo de primer nivel).
type EntryReport struct {
	Name               string  // Nombre para mostrar (ej: "src/" o "backup.sql")
	RelPath            string  // Ruta relativa respecto a la raíz analizada
	IsDir              bool    // Indica si la entrada es un directorio
	TotalSize          int64   // Tamaño total acumulado en bytes
	CompressibleSize   int64   // Bytes correspondientes a datos compresibles
	IncompressibleSize int64   // Bytes correspondientes a datos incompresibles
	EstimatedSize      int64   // Tamaño estimado comprimido
	SavingsBytes       int64   // Ahorro estimado en bytes
	SavingsPct         float64 // Porcentaje de ahorro estimado
	WeightedEntropy    float64 // Entropía promedio ponderada por tamaño
	CompLevel          string  // "Alta", "Media", "Nula", "Parcial"
	RecommendedFmt     string  // Formato sugerido ("7z", "xz", "tar.zst", "omitir", "selectivo*")
	IsMixed            bool    // Indica si mezcla compresibles e incompresibles voluminosos
	MixedCompSavings   int64   // Ahorro dentro de la porción compresible
	FileCount          int     // Número de archivos contenidos
	Files              []FileAnalysis // Desglose si Verbose está activo
}

// AnalyzeSummary sintetiza las métricas globales del estudio.
type AnalyzeSummary struct {
	TotalFiles          int
	TotalDirs           int
	TotalBytes          int64
	CompressibleBytes   int64
	IncompressibleBytes int64
	EstimatedBytes      int64
	SavingsBytes        int64
	SavingsPct          float64
	UnreadableCount     int
	MixedNotes          []string
}

// DoAnalyze ejecuta el análisis de compresibilidad de una lista de rutas y emite el informe a out.
func DoAnalyze(targets []string, opts AnalyzeOptions, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	if len(targets) == 0 {
		targets = []string{"."}
	}

	allExcludes := append([]string(nil), opts.Exclude...)
	if opts.Clean {
		allExcludes = append(allExcludes, DefaultCleanExcludes...)
	}

	if len(targets) == 1 {
		return analyzeTarget(targets[0], opts, allExcludes, out)
	}

	return analyzeTargetsConcurrent(targets, opts, allExcludes, out)
}

func isPathExcluded(path string, isDir bool, excludes []string) bool {
	if len(excludes) == 0 {
		return false
	}
	base := filepath.Base(path)
	for _, pat := range excludes {
		if matched, _ := filepath.Match(pat, base); matched {
			return true
		}
		if matched, _ := filepath.Match(pat, path); matched {
			return true
		}
		// Si es directorio, chequear patrón con barra
		if isDir {
			cleanPat := strings.TrimSuffix(pat, "/")
			if matched, _ := filepath.Match(cleanPat, base); matched {
				return true
			}
			if matched, _ := filepath.Match(cleanPat, path); matched {
				return true
			}
		}
	}
	return false
}

type analyzeGroupData struct {
	isDir              bool
	name               string
	totalSize          int64
	compressibleSize   int64
	incompressibleSize int64
	estimatedSize      int64
	entropySum         float64
	fileCount          int
	topAnalysis        FileAnalysis
	files              []FileAnalysis
}

func resolveAnalyzeTarget(raw string) (cleanTarget, displayTarget string) {
	cleanTarget = filepath.Clean(raw)
	displayTarget = cleanTarget
	if cleanTarget == "." || cleanTarget == "" {
		if abs, err := filepath.Abs(cleanTarget); err == nil {
			displayTarget = abs
		}
	}
	return cleanTarget, displayTarget
}

func addAnalysisToGroups(groups map[string]*analyzeGroupData, relPath string, an FileAnalysis, verbose bool) {
	topKey, _, isSubDir := strings.Cut(filepath.ToSlash(relPath), "/")

	g, ok := groups[topKey]
	if !ok {
		gName := topKey
		if isSubDir {
			gName += "/"
		}
		g = &analyzeGroupData{
			isDir: isSubDir,
			name:  gName,
		}
		groups[topKey] = g
	}

	if !isSubDir {
		g.topAnalysis = an
	}

	g.totalSize += an.Size
	g.estimatedSize += an.EstimatedSize
	if an.Compressible {
		g.compressibleSize += an.Size
	} else {
		g.incompressibleSize += an.Size
	}
	g.entropySum += an.Entropy * float64(an.Size)
	g.fileCount++
	if verbose {
		g.files = append(g.files, an)
	}
}

func buildSingleFileReport(target string, analysis FileAnalysis) ([]EntryReport, AnalyzeSummary) {
	entry := EntryReport{
		Name:            filepath.Base(target),
		RelPath:         target,
		IsDir:           false,
		TotalSize:       analysis.Size,
		EstimatedSize:   analysis.EstimatedSize,
		SavingsBytes:    analysis.SavingsBytes,
		SavingsPct:      analysis.SavingsPct,
		WeightedEntropy: analysis.Entropy,
		CompLevel:       analysis.CompLevel,
		RecommendedFmt:  analysis.RecommendedFmt,
		FileCount:       1,
	}
	summary := AnalyzeSummary{
		TotalFiles:     1,
		TotalDirs:      0,
		TotalBytes:     analysis.Size,
		EstimatedBytes: analysis.EstimatedSize,
		SavingsBytes:   analysis.SavingsBytes,
		SavingsPct:     analysis.SavingsPct,
	}
	if analysis.Compressible {
		entry.CompressibleSize = analysis.Size
		summary.CompressibleBytes = analysis.Size
	} else {
		entry.IncompressibleSize = analysis.Size
		summary.IncompressibleBytes = analysis.Size
	}
	return []EntryReport{entry}, summary
}

func buildDirReport(target string, groups map[string]*analyzeGroupData, dirCount, unreadableCount int, opts AnalyzeOptions) ([]EntryReport, AnalyzeSummary) {
	var entries []EntryReport
	summary := AnalyzeSummary{
		TotalDirs:       dirCount,
		UnreadableCount: unreadableCount,
	}

	for _, g := range groups {
		ent := EntryReport{
			Name:               g.name,
			RelPath:            filepath.Join(target, strings.TrimSuffix(g.name, "/")),
			IsDir:              g.isDir,
			TotalSize:          g.totalSize,
			CompressibleSize:   g.compressibleSize,
			IncompressibleSize: g.incompressibleSize,
			FileCount:          g.fileCount,
			Files:              g.files,
		}

		if g.totalSize > 0 {
			ent.WeightedEntropy = g.entropySum / float64(g.totalSize)
		}

		if !g.isDir {
			// Archivo de primer nivel
			an := g.topAnalysis
			if an.Size == 0 && len(g.files) > 0 {
				an = g.files[0]
			}
			ent.EstimatedSize = an.EstimatedSize
			ent.SavingsBytes = an.SavingsBytes
			ent.SavingsPct = an.SavingsPct
			ent.CompLevel = an.CompLevel
			ent.RecommendedFmt = an.RecommendedFmt
		} else {
			// Subdirectorio
			ent.EstimatedSize = g.estimatedSize
			ent.SavingsBytes = g.totalSize - g.estimatedSize
			if ent.SavingsBytes < 0 {
				ent.SavingsBytes = 0
				ent.EstimatedSize = g.totalSize
			}
			if g.totalSize > 0 {
				ent.SavingsPct = (float64(ent.SavingsBytes) / float64(g.totalSize)) * 100.0
			}

			// Evaluación de carpeta mixta
			// Mixta: tiene tanto datos compresibles como incompresibles de peso significativo
			isMixed := g.incompressibleSize >= int64(float64(g.totalSize)*0.25) &&
				g.compressibleSize >= (256*1024) &&
				g.incompressibleSize >= (256*1024)

			if isMixed && ent.SavingsBytes >= (256*1024) {
				ent.IsMixed = true
				ent.CompLevel = "Parcial"
				ent.MixedCompSavings = ent.SavingsBytes
				if ent.SavingsPct >= 40.0 {
					if hasTool(sevenzBin()) {
						ent.RecommendedFmt = "7z"
					} else if hasTool("xz") {
						ent.RecommendedFmt = "tar.xz"
					} else {
						ent.RecommendedFmt = "tar.zst"
					}
				} else {
					if hasTool("zstd") {
						ent.RecommendedFmt = "tar.zst"
					} else if hasTool(sevenzBin()) {
						ent.RecommendedFmt = "7z"
					} else {
						ent.RecommendedFmt = "tar.gz"
					}
				}
				note := fmt.Sprintf("%s: %s compresibles (ahorro estimado %s con %s selectivo*) y %s ya comprimidos que conviene omitir.",
					g.name, FormatSize(g.compressibleSize), FormatSize(ent.SavingsBytes), ent.RecommendedFmt, FormatSize(g.incompressibleSize))
				summary.MixedNotes = append(summary.MixedNotes, note)
			} else if ent.SavingsPct < 5.0 || g.compressibleSize == 0 {
				ent.CompLevel = "Nula"
				ent.RecommendedFmt = "omitir"
				ent.EstimatedSize = g.totalSize
				ent.SavingsBytes = 0
				ent.SavingsPct = 0
			} else {
				if ent.SavingsPct >= 55.0 {
					ent.CompLevel = "Alta"
					if hasTool(sevenzBin()) {
						ent.RecommendedFmt = "7z"
					} else if hasTool("xz") {
						ent.RecommendedFmt = "tar.xz"
					} else {
						ent.RecommendedFmt = "tar.zst"
					}
				} else {
					ent.CompLevel = "Media"
					if hasTool("zstd") {
						ent.RecommendedFmt = "tar.zst"
					} else if hasTool(sevenzBin()) {
						ent.RecommendedFmt = "7z"
					} else {
						ent.RecommendedFmt = "tar.gz"
					}
				}
			}
		}

		summary.TotalFiles += ent.FileCount
		summary.TotalBytes += ent.TotalSize
		summary.CompressibleBytes += ent.CompressibleSize
		summary.IncompressibleBytes += ent.IncompressibleSize
		summary.EstimatedBytes += ent.EstimatedSize

		entries = append(entries, ent)
	}

	// Ordenar entradas: mayor compresibilidad (porcentaje de ahorro) primero,
	// desempate por mayor volumen de ahorro en bytes, luego mayor tamaño total.
	sort.Slice(entries, func(i, j int) bool {
		if math.Abs(entries[i].SavingsPct-entries[j].SavingsPct) >= 0.05 {
			return entries[i].SavingsPct > entries[j].SavingsPct
		}
		if entries[i].SavingsBytes != entries[j].SavingsBytes {
			return entries[i].SavingsBytes > entries[j].SavingsBytes
		}
		return entries[i].TotalSize > entries[j].TotalSize
	})

	if opts.Verbose {
		for i := range entries {
			sort.Slice(entries[i].Files, func(a, b int) bool {
				if math.Abs(entries[i].Files[a].SavingsPct-entries[i].Files[b].SavingsPct) >= 0.05 {
					return entries[i].Files[a].SavingsPct > entries[i].Files[b].SavingsPct
				}
				if entries[i].Files[a].SavingsBytes != entries[i].Files[b].SavingsBytes {
					return entries[i].Files[a].SavingsBytes > entries[i].Files[b].SavingsBytes
				}
				return entries[i].Files[a].Size > entries[i].Files[b].Size
			})
		}
	}

	summary.SavingsBytes = summary.TotalBytes - summary.EstimatedBytes
	if summary.SavingsBytes < 0 {
		summary.SavingsBytes = 0
		summary.EstimatedBytes = summary.TotalBytes
	}
	if summary.TotalBytes > 0 {
		summary.SavingsPct = (float64(summary.SavingsBytes) / float64(summary.TotalBytes)) * 100.0
	}

	return entries, summary
}

func walkDirToItems(target string, excludes []string, dirCount, unreadableCount *atomic.Int64, apt *analyzeProgressTracker, emit func(absPath, relPath string, size int64)) error {
	return analyzeWalkDir(target, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			unreadableCount.Add(1)
			return nil
		}

		if path == target {
			return nil
		}

		isDir := d.IsDir()
		rel, relErr := filepath.Rel(target, path)
		if relErr != nil {
			rel = path
		}

		if isPathExcluded(rel, isDir, excludes) || isPathExcluded(path, isDir, excludes) {
			if isDir {
				return filepath.SkipDir
			}
			return nil
		}

		// Ignorar symlinks para evitar bucles o falsos duplicados
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		if isDir {
			dirCount.Add(1)
			return nil
		}

		// Solo archivos regulares
		if !d.Type().IsRegular() {
			return nil
		}

		info, statErr := d.Info()
		if statErr != nil {
			unreadableCount.Add(1)
			return nil
		}

		sz := info.Size()
		if apt != nil {
			apt.AddDiscovered(1)
			apt.AddDiscoveredBytes(sz)
		}

		emit(path, rel, sz)
		return nil
	})
}

func analyzeTargetsConcurrent(targets []string, opts AnalyzeOptions, excludes []string, out io.Writer) error {
	type targetState struct {
		target          string
		displayTarget   string
		isDir           bool
		isRot           bool
		fileSize        int64
		err             error
		walkErr         error
		dirCount        atomic.Int64
		unreadableCount atomic.Int64
		groups          map[string]*analyzeGroupData
		singleAnalysis  FileAnalysis
		hasSingle       bool
	}

	type multiWalkItem struct {
		targetIdx    int
		absPath      string
		relPath      string
		size         int64
		isSingleFile bool
	}

	type multiFileResult struct {
		targetIdx    int
		relPath      string
		isSingleFile bool
		analysis     FileAnalysis
		err          error
	}

	states := make([]*targetState, len(targets))
	validCount := 0
	for i, raw := range targets {
		clean, display := resolveAnalyzeTarget(raw)
		st := &targetState{
			target:        clean,
			displayTarget: display,
		}
		fi, err := os.Stat(clean)
		if err != nil {
			st.err = fmt.Errorf("accediendo a %s: %w", display, err)
		} else {
			st.isDir = fi.IsDir()
			st.fileSize = fi.Size()
			if st.isDir {
				st.isRot = analyzeIsRotational(clean)
				st.groups = make(map[string]*analyzeGroupData)
			}
			validCount++
		}
		states[i] = st
	}

	if validCount > 0 {
		workers := NCPU()
		if workers < 1 {
			workers = 1
		}
		if workers > 32 {
			workers = 32
		}

		workChan := make(chan multiWalkItem, workers*4)
		resChan := make(chan multiFileResult, workers*4)

		var apt *analyzeProgressTracker
		stopProgress := func() {}
		if analyzeIsTerminal() {
			fmt.Fprintf(analyzeStderr, "%s🔍 Analizando compresibilidad en %d elementos...%s\n", Yellow, validCount, NC)
			apt = newAnalyzeProgressTracker()
			apt.Start()
			var cleanupOnce sync.Once
			stopProgress = func() {
				cleanupOnce.Do(func() {
					apt.Stop()
					fmt.Fprintf(analyzeStderr, "\033[1A\r\033[K")
				})
			}
			defer stopProgress()
		}

		var workerWG sync.WaitGroup
		for w := 0; w < workers; w++ {
			workerWG.Add(1)
			go func() {
				defer workerWG.Done()
				for item := range workChan {
					an, aErr := analyzeFileFunc(item.absPath, item.size)
					if apt != nil {
						apt.Incr()
						apt.AddBytes(an.SampleBytes)
					}
					resChan <- multiFileResult{
						targetIdx:    item.targetIdx,
						relPath:      item.relPath,
						isSingleFile: item.isSingleFile,
						analysis:     an,
						err:          aErr,
					}
				}
			}()
		}

		go func() {
			workerWG.Wait()
			close(resChan)
		}()

		rotSem := make(chan struct{}, 2)
		ssdWalkCap := workers
		if ssdWalkCap > 8 {
			ssdWalkCap = 8
		}
		ssdSem := make(chan struct{}, ssdWalkCap)

		var walkWG sync.WaitGroup
		for i, st := range states {
			if st.err != nil {
				continue
			}
			walkWG.Add(1)
			go func(idx int, s *targetState) {
				defer walkWG.Done()
				if !s.isDir {
					if apt != nil {
						apt.AddDiscovered(1)
						apt.AddDiscoveredBytes(s.fileSize)
					}
					workChan <- multiWalkItem{
						targetIdx:    idx,
						absPath:      s.target,
						relPath:      s.target,
						size:         s.fileSize,
						isSingleFile: true,
					}
					return
				}

				walkSem := ssdSem
				if s.isRot {
					walkSem = rotSem
				}
				walkSem <- struct{}{}
				defer func() { <-walkSem }()

				s.walkErr = walkDirToItems(s.target, excludes, &s.dirCount, &s.unreadableCount, apt, func(absPath, relPath string, size int64) {
					workChan <- multiWalkItem{
						targetIdx: idx,
						absPath:   absPath,
						relPath:   relPath,
						size:      size,
					}
				})
			}(i, st)
		}

		go func() {
			walkWG.Wait()
			if apt != nil {
				apt.MarkWalkDone()
			}
			close(workChan)
		}()

		for res := range resChan {
			st := states[res.targetIdx]
			if res.isSingleFile {
				if res.err != nil {
					st.err = fmt.Errorf("analizando %s: %w", st.displayTarget, res.err)
				} else {
					st.singleAnalysis = res.analysis
					st.hasSingle = true
				}
				continue
			}
			if res.err != nil {
				st.unreadableCount.Add(1)
				continue
			}
			addAnalysisToGroups(st.groups, res.relPath, res.analysis, opts.Verbose)
		}

		stopProgress()

		for _, st := range states {
			if st.walkErr != nil && st.err == nil {
				st.err = fmt.Errorf("recorriendo %s: %w", st.target, st.walkErr)
			}
		}
	}

	var globalSummary AnalyzeSummary
	var errs []error
	rendered := 0
	succeeded := 0

	for _, st := range states {
		if rendered > 0 {
			fmt.Fprintln(out)
		}
		if st.err != nil {
			fmt.Fprintf(out, "\n%s✗ Error analizando %s: %v%s\n", Red, st.displayTarget, st.err, NC)
			errs = append(errs, st.err)
			rendered++
			continue
		}

		var entries []EntryReport
		var summary AnalyzeSummary
		if !st.isDir {
			if st.hasSingle {
				entries, summary = buildSingleFileReport(st.target, st.singleAnalysis)
			}
		} else {
			entries, summary = buildDirReport(st.target, st.groups, int(st.dirCount.Load()), int(st.unreadableCount.Load()), opts)
		}

		renderAnalyzeReport(st.displayTarget, entries, summary, opts, out)

		globalSummary.TotalFiles += summary.TotalFiles
		globalSummary.TotalDirs += summary.TotalDirs
		globalSummary.TotalBytes += summary.TotalBytes
		globalSummary.CompressibleBytes += summary.CompressibleBytes
		globalSummary.IncompressibleBytes += summary.IncompressibleBytes
		globalSummary.EstimatedBytes += summary.EstimatedBytes
		globalSummary.UnreadableCount += summary.UnreadableCount

		succeeded++
		rendered++
	}

	if succeeded >= 2 {
		globalSummary.SavingsBytes = globalSummary.TotalBytes - globalSummary.EstimatedBytes
		if globalSummary.SavingsBytes < 0 {
			globalSummary.SavingsBytes = 0
			globalSummary.EstimatedBytes = globalSummary.TotalBytes
		}
		if globalSummary.TotalBytes > 0 {
			globalSummary.SavingsPct = (float64(globalSummary.SavingsBytes) / float64(globalSummary.TotalBytes)) * 100.0
		}
		renderAnalyzeSummary(fmt.Sprintf("=== Resumen Global (%d elementos) ===", succeeded), globalSummary, out)
	}

	return errors.Join(errs...)
}

func analyzeTarget(target string, opts AnalyzeOptions, excludes []string, out io.Writer) error {
	target, displayTarget := resolveAnalyzeTarget(target)
	fi, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("accediendo a %s: %w", displayTarget, err)
	}

	if !fi.IsDir() {
		// Análisis de un solo archivo
		if analyzeIsTerminal() {
			fmt.Fprintf(analyzeStderr, "%s🔍 Analizando '%s'...%s\r", Yellow, displayTarget, NC)
		}
		analysis, err := analyzeFileFunc(target, fi.Size())
		if analyzeIsTerminal() {
			fmt.Fprintf(analyzeStderr, "\r\033[K")
		}
		if err != nil {
			return fmt.Errorf("analizando %s: %w", displayTarget, err)
		}
		entries, summary := buildSingleFileReport(target, analysis)
		renderAnalyzeReport(displayTarget, entries, summary, opts, out)
		return nil
	}

	type walkItem struct {
		absPath string
		relPath string
		size    int64
	}

	type fileResult struct {
		relPath  string
		analysis FileAnalysis
		err      error
	}

	// Análisis concurrente en streaming mediante pool acotado de workers (1..min(NCPU(), 32)).
	// En --analyze se procesan micro-muestras de 32 KiB; mantener hasta 32 workers permite
	// que la cola NCQ del almacenamiento (tanto SSD/NVMe como HDD SATA con queue_depth=32)
	// despache decenas de miles de archivos sin acumular las rutas en RAM.
	workers := NCPU()
	if workers < 1 {
		workers = 1
	}
	if workers > 32 {
		workers = 32
	}

	workChan := make(chan walkItem, workers*4)
	resChan := make(chan fileResult, workers*4)

	var apt *analyzeProgressTracker
	stopProgress := func() {}
	if analyzeIsTerminal() {
		fmt.Fprintf(analyzeStderr, "%s🔍 Analizando compresibilidad en '%s'...%s\n", Yellow, displayTarget, NC)
		apt = newAnalyzeProgressTracker()
		apt.Start()
		var cleanupOnce sync.Once
		stopProgress = func() {
			cleanupOnce.Do(func() {
				apt.Stop()
				fmt.Fprintf(analyzeStderr, "\033[1A\r\033[K")
			})
		}
		defer stopProgress()
	}

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range workChan {
				an, aErr := analyzeFileFunc(item.absPath, item.size)
				if apt != nil {
					apt.Incr()
					apt.AddBytes(an.SampleBytes)
				}
				resChan <- fileResult{
					relPath:  item.relPath,
					analysis: an,
					err:      aErr,
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(resChan)
	}()

	var dirCount atomic.Int64
	var unreadableCount atomic.Int64
	walkErrCh := make(chan error, 1)

	go func() {
		defer func() {
			if apt != nil {
				apt.MarkWalkDone()
			}
			close(workChan)
		}()
		walkErrCh <- walkDirToItems(target, excludes, &dirCount, &unreadableCount, apt, func(absPath, relPath string, size int64) {
			workChan <- walkItem{
				absPath: absPath,
				relPath: relPath,
				size:    size,
			}
		})
	}()

	groups := make(map[string]*analyzeGroupData)

	for res := range resChan {
		if res.err != nil {
			unreadableCount.Add(1)
			continue
		}
		addAnalysisToGroups(groups, res.relPath, res.analysis, opts.Verbose)
	}

	stopProgress()

	if walkErr := <-walkErrCh; walkErr != nil {
		return fmt.Errorf("recorriendo %s: %w", target, walkErr)
	}

	entries, summary := buildDirReport(target, groups, int(dirCount.Load()), int(unreadableCount.Load()), opts)
	renderAnalyzeReport(displayTarget, entries, summary, opts, out)
	return nil
}

func truncateAnalyzeLabel(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

func renderAnalyzeReport(target string, entries []EntryReport, summary AnalyzeSummary, opts AnalyzeOptions, out io.Writer) {
	fmt.Fprintf(out, "\n%s=== Informe de Compresibilidad: %s ===%s\n\n", Bold, target, NC)

	if len(entries) == 0 {
		fmt.Fprintf(out, "  No se encontraron archivos para analizar.\n\n")
		return
	}

	fmt.Fprintf(out, "  %-30s %-5s %14s %-12s %-13s %14s %18s\n",
		"Elemento", "Tipo", "Tamaño Actual", "Compresible", "Formato Rec.", "Tamaño Est.", "Ahorro Est.")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 108))

	for _, e := range entries {
		typeStr := "FILE"
		if e.IsDir {
			typeStr = "DIR"
		}

		nameDisplay := truncateAnalyzeLabel(e.Name, 28)

		var compSymbol string
		switch e.CompLevel {
		case "Alta":
			compSymbol = fmt.Sprintf("%s✓ Alta%s   ", Green, NC)
		case "Media":
			compSymbol = fmt.Sprintf("%s✓ Media%s  ", Blue, NC)
		case "Parcial":
			if e.IsMixed {
				compSymbol = fmt.Sprintf("%s◐ Parcial*%s", Yellow, NC)
			} else {
				compSymbol = fmt.Sprintf("%s◐ Parcial%s ", Yellow, NC)
			}
		default:
			compSymbol = fmt.Sprintf("%s✗ Nula%s   ", Red, NC)
		}

		savingsStr := "-"
		if e.SavingsBytes > 0 {
			savingsStr = fmt.Sprintf("-%s (%4.1f%%)", FormatSize(e.SavingsBytes), e.SavingsPct)
		} else {
			savingsStr = fmt.Sprintf(" 0 B ( 0.0%%)")
		}

		fmt.Fprintf(out, "  %-30s %-5s %14s %-21s %-13s %14s %18s\n",
			nameDisplay,
			typeStr,
			FormatSize(e.TotalSize),
			compSymbol,
			e.RecommendedFmt,
			FormatSize(e.EstimatedSize),
			savingsStr,
		)

		if opts.Verbose && len(e.Files) > 0 {
			for _, f := range e.Files {
				fName := truncateAnalyzeLabel("  └─ "+filepath.Base(f.Path), 28)
				fSavings := "-"
				if f.SavingsBytes > 0 {
					fSavings = fmt.Sprintf("-%s (%4.1f%%)", FormatSize(f.SavingsBytes), f.SavingsPct)
				} else {
					fSavings = " 0 B ( 0.0%)"
				}
				fmt.Fprintf(out, "    %-28s %-5s %14s %-12s %-13s %14s %18s\n",
					fName,
					"file",
					FormatSize(f.Size),
					f.CompLevel,
					f.RecommendedFmt,
					FormatSize(f.EstimatedSize),
					fSavings,
				)
			}
		}
	}

	if len(summary.MixedNotes) > 0 {
		fmt.Fprintf(out, "\n  %sNotas sobre carpetas mixtas (*):%s\n", Yellow, NC)
		for _, note := range summary.MixedNotes {
			fmt.Fprintf(out, "    • %s\n", note)
		}
	}

	renderAnalyzeSummary("=== Resumen del Estudio ===", summary, out)
}

func renderAnalyzeSummary(title string, summary AnalyzeSummary, out io.Writer) {
	fmt.Fprintf(out, "\n%s%s%s\n", Bold, title, NC)
	dirWord := "directorios"
	if summary.TotalDirs == 1 {
		dirWord = "directorio"
	}
	fileWord := "archivos"
	if summary.TotalFiles == 1 {
		fileWord = "archivo"
	}
	fmt.Fprintf(out, "  Archivos analizados:           %s %s en %d %s\n",
		FormatNumber(summary.TotalFiles), fileWord, summary.TotalDirs, dirWord)
	fmt.Fprintf(out, "  Tamaño total actual:           %s\n", FormatSize(summary.TotalBytes))

	incompPct := 0.0
	if summary.TotalBytes > 0 {
		incompPct = (float64(summary.IncompressibleBytes) / float64(summary.TotalBytes)) * 100.0
	}
	fmt.Fprintf(out, "  Ya comprimidos (omitir):       %s (%4.1f%% no requiere compresión)\n",
		FormatSize(summary.IncompressibleBytes), incompPct)

	compTargetEst := "-"
	if summary.CompressibleBytes > 0 {
		compSavings := summary.CompressibleBytes - (summary.EstimatedBytes - summary.IncompressibleBytes)
		if compSavings < 0 {
			compSavings = 0
		}
		compTargetEst = FormatSize(summary.CompressibleBytes - compSavings)
		compSavingsPct := (float64(compSavings) / float64(summary.CompressibleBytes)) * 100.0
		fmt.Fprintf(out, "  Datos compresibles útiles:     %s  ──►  %s estimados (%4.1f%% de ahorro)\n",
			FormatSize(summary.CompressibleBytes), compTargetEst, compSavingsPct)
	}

	fmt.Fprintf(out, "  %sAhorro total estimado:         %s%s (%4.1f%% sobre el total)\n",
		Green, FormatSize(summary.SavingsBytes), NC, summary.SavingsPct)

	if summary.UnreadableCount > 0 {
		fmt.Fprintf(out, "  %s⚠ %d elemento(s) no se pudieron leer por permisos o errores de E/S%s\n",
			Yellow, summary.UnreadableCount, NC)
	}
	fmt.Fprintf(out, "%s===========================%s\n\n", Bold, NC)
}

// FormatNumber formatea un número entero con separadores de miles para mayor legibilidad.
func FormatNumber(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var res []byte
	rem := len(s) % 3
	if rem > 0 {
		res = append(res, s[:rem]...)
		if len(s) > rem {
			res = append(res, ',')
		}
	}
	for i := rem; i < len(s); i += 3 {
		res = append(res, s[i:i+3]...)
		if i+3 < len(s) {
			res = append(res, ',')
		}
	}
	return string(res)
}



const (
	sampleChunkSize  = 32 * 1024
	sampleBufferSize = 3 * sampleChunkSize // 96 KiB
)

var sampleBufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, sampleBufferSize)
		return &b
	},
}

// countingSink implementa io.Writer contando exclusivamente los bytes recibidos.
type countingSink struct {
	count int64
}

func (s *countingSink) Write(p []byte) (int, error) {
	s.count += int64(len(p))
	return len(p), nil
}

var flateWriterPool = sync.Pool{
	New: func() any {
		w, _ := flate.NewWriter(io.Discard, flate.DefaultCompression)
		return w
	},
}

// readRemainingChunks lee los fragmentos de la mitad y final para completar los 96 KiB
// cuando el primer fragmento ya fue leído en targetBuf[:n1].
func readRemainingChunks(f *os.File, size int64, targetBuf []byte, n1 int) ([]byte, int, int, error) {
	if n1 < sampleChunkSize {
		return targetBuf[:n1], 0, 0, nil
	}

	// 2. Mitad
	midOffset := (size - int64(sampleChunkSize)) / 2
	n2, err := f.ReadAt(targetBuf[sampleChunkSize:sampleChunkSize*2], midOffset)
	if err != nil && err != io.EOF {
		return nil, 0, 0, err
	}

	// 3. Final
	endOffset := size - int64(sampleChunkSize)
	n3, err := f.ReadAt(targetBuf[sampleChunkSize*2:sampleBufferSize], endOffset)
	if err != nil && err != io.EOF {
		return nil, 0, 0, err
	}

	total := sampleChunkSize
	if n2 > 0 {
		if n2 < sampleChunkSize {
			copy(targetBuf[total:], targetBuf[sampleChunkSize:sampleChunkSize+n2])
		}
		total += n2
	}
	if n3 > 0 {
		if n3 < sampleChunkSize || total < sampleChunkSize*2 {
			copy(targetBuf[total:], targetBuf[sampleChunkSize*2:sampleChunkSize*2+n3])
		}
		total += n3
	}

	return targetBuf[:total], n2, n3, nil
}

// readSampleChunks lee los 3 fragmentos de 32 KiB (inicio, mitad y fin) sobre un archivo abierto.
func readSampleChunks(f *os.File, size int64, targetBuf []byte) ([]byte, error) {
	n1, err := f.ReadAt(targetBuf[:sampleChunkSize], 0)
	if err != nil && err != io.EOF {
		return nil, err
	}
	sample, _, _, err := readRemainingChunks(f, size, targetBuf, n1)
	return sample, err
}

// sampleFile toma una muestra estratificada de hasta 96 KiB de un archivo.
// Si el tamaño es <= 96 KiB, lee el archivo completo.
// Si el tamaño es > 96 KiB, lee 3 fragmentos de 32 KiB: inicio, mitad y fin.
func sampleFile(path string, size int64, buf []byte) ([]byte, error) {
	if size <= 0 {
		return nil, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if size <= int64(sampleBufferSize) {
		targetBuf := buf
		if len(targetBuf) < int(size) {
			targetBuf = make([]byte, size)
		}
		n, err := io.ReadFull(f, targetBuf[:size])
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, err
		}
		return targetBuf[:n], nil
	}

	targetBuf := buf
	if len(targetBuf) < sampleBufferSize {
		targetBuf = make([]byte, sampleBufferSize)
	}
	return readSampleChunks(f, size, targetBuf)
}

// probeDeflateRatio pasa una muestra por DEFLATE en memoria y devuelve el ratio
// (bytes comprimidos / bytes originales). Si los datos son nulos o vacíos, devuelve 1.0.
func probeDeflateRatio(sample []byte) float64 {
	if len(sample) == 0 {
		return 1.0
	}

	fw := flateWriterPool.Get().(*flate.Writer)
	defer flateWriterPool.Put(fw)

	var sink countingSink
	fw.Reset(&sink)

	if _, err := fw.Write(sample); err != nil {
		return 1.0
	}
	if err := fw.Close(); err != nil {
		return 1.0
	}

	ratio := float64(sink.count) / float64(len(sample))
	return ratio
}

type analyzeProgressTracker struct {
	discovered      atomic.Int64
	discoveredBytes atomic.Int64
	walkDone        atomic.Bool
	filesDone       atomic.Int64
	bytesRead       atomic.Int64
	startTime       time.Time
	tickInterval    time.Duration
	done            chan struct{}
	stopped         chan struct{}
	started         atomic.Bool
	stopOnce        sync.Once
	isTTY           bool
	out             io.Writer
}

func newAnalyzeProgressTracker() *analyzeProgressTracker {
	return &analyzeProgressTracker{
		startTime:    time.Now(),
		tickInterval: 100 * time.Millisecond,
		done:         make(chan struct{}),
		stopped:      make(chan struct{}),
		isTTY:        analyzeIsTerminal(),
		out:          analyzeStderr,
	}
}

func (apt *analyzeProgressTracker) writer() io.Writer {
	if apt.out != nil {
		return apt.out
	}
	return os.Stderr
}

func (apt *analyzeProgressTracker) Start() {
	if !apt.isTTY || !apt.started.CompareAndSwap(false, true) {
		return
	}
	io.WriteString(apt.writer(), "\033[?25l")
	interval := apt.tickInterval
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer close(apt.stopped)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				apt.render()
			case <-apt.done:
				return
			}
		}
	}()
}

func (apt *analyzeProgressTracker) AddDiscovered(n int64) {
	apt.discovered.Add(n)
}

func (apt *analyzeProgressTracker) AddDiscoveredBytes(n int64) {
	apt.discoveredBytes.Add(n)
}

func (apt *analyzeProgressTracker) MarkWalkDone() {
	apt.walkDone.Store(true)
}

func (apt *analyzeProgressTracker) Incr() {
	apt.filesDone.Add(1)
}

func (apt *analyzeProgressTracker) AddBytes(n int64) {
	apt.bytesRead.Add(n)
}

func (apt *analyzeProgressTracker) formatProgress() string {
	done := apt.filesDone.Load()
	discovered := apt.discovered.Load()
	if discovered < done {
		discovered = done
	}
	bytesRead := apt.bytesRead.Load()
	elapsed := time.Since(apt.startTime)

	var speedStr string
	if elapsed.Seconds() > 0.1 && bytesRead > 0 {
		speed := float64(bytesRead) / elapsed.Seconds()
		speedStr = fmt.Sprintf("%s/s", FormatSize(int64(speed)))
	} else {
		speedStr = "iniciando..."
	}

	if !apt.walkDone.Load() {
		if discBytes := apt.discoveredBytes.Load(); discBytes > 0 {
			return fmt.Sprintf("\rEscaneando... %s/%s (%s)  %s leídos  %s\033[K",
				FormatNumber(int(done)), FormatNumber(int(discovered)), FormatSize(discBytes), FormatSize(bytesRead), speedStr)
		}
		return fmt.Sprintf("\rEscaneando... %s/%s  %s leídos  %s\033[K",
			FormatNumber(int(done)), FormatNumber(int(discovered)), FormatSize(bytesRead), speedStr)
	}

	pct := 0.0
	if discovered > 0 {
		pct = float64(done) * 100.0 / float64(discovered)
		if pct > 100.0 {
			pct = 100.0
		}
	}

	var etaStr string
	if done < discovered {
		if eta := formatETA(elapsed, done, discovered); eta != "" && eta != "--:--" {
			if eta == "0s" {
				eta = "<1s"
			}
			etaStr = fmt.Sprintf(" %s restantes", eta)
		}
	}

	bar := makeBar(pct, 20)
	return fmt.Sprintf("\r[%s] %5.1f%%  %s/%s  %s%s\033[K",
		bar, pct, FormatNumber(int(done)), FormatNumber(int(discovered)), speedStr, etaStr)
}

func (apt *analyzeProgressTracker) render() {
	if !apt.isTTY {
		return
	}
	io.WriteString(apt.writer(), apt.formatProgress())
}

func (apt *analyzeProgressTracker) Stop() {
	apt.stopOnce.Do(func() {
		if !apt.started.Load() {
			return
		}
		close(apt.done)
		<-apt.stopped
		io.WriteString(apt.writer(), "\r\033[K\033[?25h")
	})
}
