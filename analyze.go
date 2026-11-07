package main

import (
	"compress/flate"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FileAnalysis almacena las métricas individuales de análisis de un archivo regular.
type FileAnalysis struct {
	Path           string  // Ruta del archivo
	Size           int64   // Tamaño real en bytes
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
	size := fi.Size()

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
		return res, nil
	}

	if size < 512 {
		res.Compressible = false
		res.CompLevel = "Nula"
		res.RecommendedFmt = "omitir"
		return res, nil
	}

	bufPtr := sampleBufferPool.Get().(*[]byte)
	defer sampleBufferPool.Put(bufPtr)

	sample, err := sampleFile(path, size, *bufPtr)
	if err != nil {
		return FileAnalysis{}, err
	}

	entropy := CalculateShannonEntropy(sample)
	res.Entropy = entropy

	ext := strings.ToLower(filepath.Ext(path))

	// Detección de incompresibles por entropía o contenedor ya comprimido
	if (len(sample) >= 4096 && entropy >= 7.90) || (mediaOrCompressedExts[ext] && entropy >= 7.20) {
		res.Compressible = false
		res.CompLevel = "Nula"
		res.RecommendedFmt = "omitir"
		res.ProbeRatio = 1.0
		return res, nil
	}

	probeRatio := probeDeflateRatio(sample)
	res.ProbeRatio = probeRatio

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

	for i, target := range targets {
		if i > 0 {
			fmt.Fprintln(out)
		}
		if err := analyzeTarget(target, opts, allExcludes, out); err != nil {
			return err
		}
	}

	return nil
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
		}
	}
	return false
}

func analyzeTarget(target string, opts AnalyzeOptions, excludes []string, out io.Writer) error {
	target = filepath.Clean(target)
	fi, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("accediendo a %s: %w", target, err)
	}

	if !fi.IsDir() {
		// Análisis de un solo archivo
		if isTerminal() {
			fmt.Fprintf(os.Stderr, "%s🔍 Analizando '%s'...%s\r", Yellow, target, NC)
		}
		analysis, err := AnalyzeFile(target)
		if isTerminal() {
			fmt.Fprintf(os.Stderr, "\r\033[K")
		}
		if err != nil {
			return fmt.Errorf("analizando %s: %w", target, err)
		}
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
			TotalFiles:          1,
			TotalDirs:           0,
			TotalBytes:          analysis.Size,
			EstimatedBytes:      analysis.EstimatedSize,
			SavingsBytes:        analysis.SavingsBytes,
			SavingsPct:          analysis.SavingsPct,
			CompressibleBytes:   0,
			IncompressibleBytes: 0,
		}
		if analysis.Compressible {
			summary.CompressibleBytes = analysis.Size
		} else {
			summary.IncompressibleBytes = analysis.Size
		}
		renderAnalyzeReport(target, []EntryReport{entry}, summary, opts, out)
		return nil
	}

	// Análisis de directorio recursivo
	type walkItem struct {
		absPath string
		relPath string
		size    int64
	}

	var items []walkItem
	var totalWalkBytes int64
	dirCount := 0
	unreadableCount := 0

	if isTerminal() {
		fmt.Fprintf(os.Stderr, "%s🔍 Escaneando archivos en '%s'...%s\r", Yellow, target, NC)
	}

	err = filepath.WalkDir(target, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			unreadableCount++
			return nil
		}

		isDir := d.IsDir()
		rel, relErr := filepath.Rel(target, path)
		if relErr != nil {
			rel = path
		}

		if path == target {
			return nil
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
			dirCount++
			return nil
		}

		// Solo archivos regulares
		if !d.Type().IsRegular() {
			return nil
		}

		info, statErr := d.Info()
		if statErr != nil {
			unreadableCount++
			return nil
		}

		items = append(items, walkItem{
			absPath: path,
			relPath: rel,
			size:    info.Size(),
		})
		totalWalkBytes += info.Size()
		return nil
	})

	if isTerminal() {
		fmt.Fprintf(os.Stderr, "\r\033[K")
	}

	if err != nil {
		return fmt.Errorf("recorriendo %s: %w", target, err)
	}

	// Análisis concurrente de los archivos recolectados mediante pool acotado de workers
	workers := NCPU()
	if workers < 1 {
		workers = 1
	}
	if workers > 32 {
		workers = 32
	}
	if workers > len(items) && len(items) > 0 {
		workers = len(items)
	}

	type fileResult struct {
		relPath  string
		analysis FileAnalysis
		err      error
	}

	workChan := make(chan walkItem, workers*2)
	resChan := make(chan fileResult, workers*2)
	var wg sync.WaitGroup

	var pt *ProgressTracker
	if isTerminal() && len(items) > 0 {
		fmt.Fprintf(os.Stderr, "%s🔍 Analizando compresibilidad en '%s' (%s en %s)...%s\n",
			Yellow, target, FormatSize(totalWalkBytes), FormatNumber(len(items))+" archivos", NC)
		pt = NewProgressTracker(totalWalkBytes, len(items))
		pt.Start()
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range workChan {
				an, aErr := AnalyzeFile(item.absPath)
				if pt != nil {
					pt.Add(item.size)
					pt.FileDone(item.relPath)
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

	go func() {
		for _, it := range items {
			workChan <- it
		}
		close(workChan)
	}()

	// Agrupación por entrada de primer nivel bajo target
	type groupData struct {
		isDir              bool
		name               string
		totalSize          int64
		compressibleSize   int64
		incompressibleSize int64
		estimatedSize      int64
		entropySum         float64
		fileCount          int
		files              []FileAnalysis
	}

	groups := make(map[string]*groupData)

	for res := range resChan {
		if res.err != nil {
			unreadableCount++
			continue
		}

		an := res.analysis
		parts := strings.Split(filepath.ToSlash(res.relPath), "/")
		topKey := parts[0]
		isSubDir := len(parts) > 1

		g, ok := groups[topKey]
		if !ok {
			gName := topKey
			if isSubDir {
				gName += "/"
			}
			g = &groupData{
				isDir: isSubDir,
				name:  gName,
			}
			groups[topKey] = g
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
		if opts.Verbose {
			g.files = append(g.files, an)
		}
	}

	if pt != nil {
		pt.Stop()
		fmt.Fprintf(os.Stderr, "\033[1A\r\033[K")
	}

	// Construcción de entradas y resumen
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
			if len(g.files) > 0 {
				an := g.files[0]
				ent.EstimatedSize = an.EstimatedSize
				ent.SavingsBytes = an.SavingsBytes
				ent.SavingsPct = an.SavingsPct
				ent.CompLevel = an.CompLevel
				ent.RecommendedFmt = an.RecommendedFmt
			} else {
				an, _ := AnalyzeFile(ent.RelPath)
				ent.EstimatedSize = an.EstimatedSize
				ent.SavingsBytes = an.SavingsBytes
				ent.SavingsPct = an.SavingsPct
				ent.CompLevel = an.CompLevel
				ent.RecommendedFmt = an.RecommendedFmt
			}
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
				ent.RecommendedFmt = "selectivo*"
				ent.MixedCompSavings = ent.SavingsBytes
				note := fmt.Sprintf("%s: %s compresibles (ahorro estimado %s) y %s ya comprimidos que conviene omitir.",
					g.name, FormatSize(g.compressibleSize), FormatSize(ent.SavingsBytes), FormatSize(g.incompressibleSize))
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

	// Ordenar entradas: mayor ahorro potencial primero, luego mayor tamaño
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].SavingsBytes != entries[j].SavingsBytes {
			return entries[i].SavingsBytes > entries[j].SavingsBytes
		}
		return entries[i].TotalSize > entries[j].TotalSize
	})

	summary.SavingsBytes = summary.TotalBytes - summary.EstimatedBytes
	if summary.SavingsBytes < 0 {
		summary.SavingsBytes = 0
		summary.EstimatedBytes = summary.TotalBytes
	}
	if summary.TotalBytes > 0 {
		summary.SavingsPct = (float64(summary.SavingsBytes) / float64(summary.TotalBytes)) * 100.0
	}

	renderAnalyzeReport(target, entries, summary, opts, out)
	return nil
}

func renderAnalyzeReport(target string, entries []EntryReport, summary AnalyzeSummary, opts AnalyzeOptions, out io.Writer) {
	fmt.Fprintf(out, "\n%s=== Informe de Compresibilidad: %s ===%s\n\n", Bold, target, NC)

	if len(entries) == 0 {
		fmt.Fprintf(out, "  No se encontraron archivos para analizar.\n\n")
		return
	}

	fmt.Fprintf(out, "  %-30s %-5s %14s %10s %-12s %-13s %14s %18s\n",
		"Elemento", "Tipo", "Tamaño Actual", "Entropía", "Compresible", "Formato Rec.", "Tamaño Est.", "Ahorro Est.")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 116))

	for _, e := range entries {
		typeStr := "FILE"
		if e.IsDir {
			typeStr = "DIR"
		}

		nameDisplay := e.Name
		if len(nameDisplay) > 28 {
			nameDisplay = nameDisplay[:25] + "..."
		}

		entropyStr := fmt.Sprintf("%.2f b/B", e.WeightedEntropy)
		if e.TotalSize == 0 {
			entropyStr = "-     "
		}

		var compSymbol string
		switch e.CompLevel {
		case "Alta":
			compSymbol = fmt.Sprintf("%s✓ Alta%s   ", Green, NC)
		case "Media":
			compSymbol = fmt.Sprintf("%s✓ Media%s  ", Blue, NC)
		case "Parcial":
			compSymbol = fmt.Sprintf("%s◐ Parcial%s", Yellow, NC)
		default:
			compSymbol = fmt.Sprintf("%s✗ Nula%s   ", Red, NC)
		}

		savingsStr := "-"
		if e.SavingsBytes > 0 {
			savingsStr = fmt.Sprintf("-%s (%4.1f%%)", FormatSize(e.SavingsBytes), e.SavingsPct)
		} else {
			savingsStr = fmt.Sprintf(" 0 B ( 0.0%%)")
		}

		fmt.Fprintf(out, "  %-30s %-5s %14s %10s %-21s %-13s %14s %18s\n",
			nameDisplay,
			typeStr,
			FormatSize(e.TotalSize),
			entropyStr,
			compSymbol,
			e.RecommendedFmt,
			FormatSize(e.EstimatedSize),
			savingsStr,
		)

		if opts.Verbose && len(e.Files) > 0 {
			for _, f := range e.Files {
				fName := "  └─ " + filepath.Base(f.Path)
				if len(fName) > 28 {
					fName = fName[:25] + "..."
				}
				fSavings := "-"
				if f.SavingsBytes > 0 {
					fSavings = fmt.Sprintf("-%s (%4.1f%%)", FormatSize(f.SavingsBytes), f.SavingsPct)
				} else {
					fSavings = " 0 B ( 0.0%)"
				}
				fmt.Fprintf(out, "    %-28s %-5s %14s %10s %-12s %-13s %14s %18s\n",
					fName,
					"file",
					FormatSize(f.Size),
					fmt.Sprintf("%.2f b/B", f.Entropy),
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

	fmt.Fprintf(out, "\n%s=== Resumen del Estudio ===%s\n", Bold, NC)
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

	// Archivo mayor a 96 KiB: 3 fragmentos de 32 KiB
	targetBuf := buf
	if len(targetBuf) < sampleBufferSize {
		targetBuf = make([]byte, sampleBufferSize)
	}

	// 1. Inicio: offset 0
	n1, err := f.ReadAt(targetBuf[:sampleChunkSize], 0)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if n1 < sampleChunkSize {
		return targetBuf[:n1], nil
	}

	// 2. Mitad
	midOffset := (size - int64(sampleChunkSize)) / 2
	n2, err := f.ReadAt(targetBuf[sampleChunkSize:sampleChunkSize*2], midOffset)
	if err != nil && err != io.EOF {
		return nil, err
	}

	// 3. Final
	endOffset := size - int64(sampleChunkSize)
	n3, err := f.ReadAt(targetBuf[sampleChunkSize*2:sampleBufferSize], endOffset)
	if err != nil && err != io.EOF {
		return nil, err
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

	return targetBuf[:total], nil
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
