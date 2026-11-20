package main

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Format int

const (
	Gz Format = iota
	Xz
	Bz2
	Bz3
	Zst
	Lz
	Lrz
	Zip
	SevenZ
	Tar
	Rar
	Lz4
	Br
	Auto
)

var formatNames = map[Format]string{
	Gz:     "gz",
	Xz:     "xz",
	Bz2:    "bz2",
	Bz3:    "bz3",
	Zst:    "zst",
	Lz:     "lz",
	Lrz:    "lrz",
	Zip:    "zip",
	SevenZ: "7z",
	Tar:    "tar",
	Rar:    "rar",
	Lz4:    "lz4",
	Br:     "br",
	Auto:   "auto",
}

var FormatsByCompression = []Format{
	Lrz, Bz3, Xz, SevenZ, Bz2, Br, Zst, Rar, Lz, Gz, Lz4, Zip, Tar,
}

func (f Format) String() string {
	if s, ok := formatNames[f]; ok {
		return s
	}
	return fmt.Sprintf("Format(%d)", f)
}

func (f Format) IsContainer() bool {
	switch f {
	case Zip, SevenZ, Tar, Rar:
		return true
	default:
		return false
	}
}

func (f Format) IsStream() bool {
	return !f.IsContainer()
}

func (f Format) ArchiveBaseName(inputPath string) string {
	base := filepath.Base(inputPath)
	if f.IsStream() {
		return base
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

type FormatInfo struct {
	Format      Format
	Tool        string
	PipeFlags   string
	DirectFlags string
	IsTar       bool
	TestFlag    string
}

func (fi FormatInfo) IsContainer() bool {
	if fi.Format != 0 {
		return fi.Format.IsContainer()
	}
	switch fi.Tool {
	case "unzip", "tar", "7z", "7za", "7zr", "rar", "unrar":
		return true
	}
	return fi.Format.IsContainer()
}

func (fi FormatInfo) IsStream() bool {
	return !fi.IsContainer()
}

func (fi FormatInfo) ArchiveBaseName(inputPath string) string {
	base := filepath.Base(inputPath)
	if fi.IsStream() {
		return base
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func ExtForFormat(f Format) string {
	if f == Auto {
		return "auto"
	}
	if f.IsContainer() {
		return f.String()
	}
	return "tar." + f.String()
}

func ExtForItem(f Format, isDir bool) string {
	if f == Auto {
		return "auto"
	}
	if f.IsContainer() {
		return f.String()
	}
	if isDir {
		return "tar." + f.String()
	}
	return f.String()
}

var mediaOrCompressedExts = map[string]bool{
	".mp4": true, ".mkv": true, ".avi": true, ".mov": true, ".wmv": true, ".flv": true, ".webm": true,
	".mp3": true, ".flac": true, ".aac": true, ".ogg": true, ".wav": true, ".m4a": true,
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true,
	".zip": true, ".gz": true, ".xz": true, ".bz2": true, ".bz3": true, ".zst": true, ".7z": true, ".rar": true,
	".lz": true, ".lrz": true, ".lz4": true, ".br": true, ".tgz": true, ".txz": true, ".tbz2": true, ".tzst": true,
	".iso": true, ".img": true, ".dmg": true,
	".pdf": true, ".docx": true, ".xlsx": true, ".pptx": true,
}

var textOrCodeExts = map[string]bool{
	".txt": true, ".log": true, ".csv": true, ".tsv": true, ".json": true, ".xml": true, ".html": true, ".htm": true,
	".css": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
	".c": true, ".cpp": true, ".h": true, ".hpp": true, ".go": true, ".rs": true, ".py": true, ".java": true,
	".kt": true, ".cs": true, ".sh": true, ".bash": true, ".zsh": true,
	".sql": true, ".md": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".conf": true, ".cfg": true,
	".bmp": true, ".svg": true, ".psd": true, ".ai": true,
	".tar": true,
}

const (
	headerSampleSize     = 65536
	minHighEntropySample = 4096
	highEntropyThreshold = 7.90
)

var headerSamplePool = sync.Pool{
	New: func() any {
		b := make([]byte, headerSampleSize)
		return &b
	},
}

func dirAutoFormat() Format {
	if hasTool(sevenzBin()) {
		return SevenZ
	}
	if hasTool("zstd") {
		return Zst
	}
	return Gz
}

func highEntropyAutoFormat() Format {
	if hasTool("lz4") {
		return Lz4
	}
	if hasTool("zstd") {
		return Zst
	}
	return Gz
}

func detectFormatByExt(path string) (Format, bool) {
	if HasTarSuffix(path) {
		return Zst, true
	}
	ext := strings.ToLower(filepath.Ext(path))
	if mediaOrCompressedExts[ext] {
		return Zst, true
	}
	if textOrCodeExts[ext] {
		return Xz, true
	}
	return 0, false
}

func classifyHeaderSample(sample []byte, highEntropy bool) Format {
	if len(sample) == 0 {
		return Zst
	}
	if highEntropy {
		return highEntropyAutoFormat()
	}

	nullCount := 0
	controlCount := 0
	checkLimit := len(sample)
	if checkLimit > 512 {
		checkLimit = 512
	}
	for _, b := range sample[:checkLimit] {
		if b == 0x00 {
			nullCount++
		} else if b < 0x20 && b != '\n' && b != '\r' && b != '\t' {
			controlCount++
		}
	}

	if nullCount > 0 || float64(controlCount)/float64(checkLimit) > 0.10 {
		return Zst
	}
	return Xz
}

func inspectFileHeader(path string, classify, checkEntropy bool) (Format, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Zst, false
	}
	defer f.Close()

	bufPtr := headerSamplePool.Get().(*[]byte)
	defer headerSamplePool.Put(bufPtr)

	n, err := io.ReadFull(f, *bufPtr)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return Zst, false
	}
	sample := (*bufPtr)[:n]
	highEntropy := false
	if checkEntropy && n >= minHighEntropySample {
		highEntropy = CalculateShannonEntropy(sample) >= highEntropyThreshold
	}
	if !classify {
		return Zst, highEntropy
	}
	return classifyHeaderSample(sample, highEntropy), highEntropy
}

func InspectAutoFormat(path string) (Format, bool) {
	fi, statErr := os.Stat(path)
	if statErr == nil && fi.IsDir() {
		return dirAutoFormat(), false
	}

	extFmt, hasExtFmt := detectFormatByExt(path)
	if statErr != nil {
		if hasExtFmt {
			return extFmt, false
		}
		return Zst, false
	}

	canBeHighEntropy := fi.Mode().IsRegular() && fi.Size() >= minHighEntropySample
	if hasExtFmt && !canBeHighEntropy {
		return extFmt, false
	}

	sampleFmt, highEntropy := inspectFileHeader(path, !hasExtFmt, canBeHighEntropy)
	if hasExtFmt {
		return extFmt, highEntropy
	}
	return sampleFmt, highEntropy
}

func DetectAutoFormat(path string) Format {
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return dirAutoFormat()
	}
	if extFmt, ok := detectFormatByExt(path); ok {
		return extFmt
	}
	sampleFmt, _ := inspectFileHeader(path, true, true)
	return sampleFmt
}

// CalculateShannonEntropy calcula la entropía de Shannon en bits por byte (0.00 a 8.00)
// sobre un slice de bytes. Devuelve 0.0 si la muestra está vacía.
func CalculateShannonEntropy(data []byte) float64 {
	if len(data) == 0 {
		return 0.0
	}
	var counts [256]int
	for _, b := range data {
		counts[b]++
	}
	total := float64(len(data))
	var entropy float64
	for _, count := range counts {
		if count > 0 {
			p := float64(count) / total
			entropy -= p * math.Log2(p)
		}
	}
	return entropy
}

// IsHighEntropyFile determina si un archivo regular tiene alta entropía
// (>= 7.90 bits/byte en muestras de al menos 4096 bytes), lo que indica datos ya comprimidos o cifrados.
func IsHighEntropyFile(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() < minHighEntropySample {
		return false
	}
	_, highEntropy := inspectFileHeader(path, false, true)
	return highEntropy
}

func ParseFormat(s string) (Format, error) {
	for f, name := range formatNames {
		if strings.EqualFold(s, name) {
			return f, nil
		}
	}
	return 0, fmt.Errorf("formato no soportado: %s", s)
}

func ParseFormatList(input string) ([]Format, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, fmt.Errorf("lista de formatos vacía")
	}
	rawParts := strings.Split(trimmed, ",")
	var result []Format
	seen := make(map[Format]bool)
	for _, part := range rawParts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		f, err := ParseFormat(part)
		if err != nil {
			return nil, fmt.Errorf("formato no reconocido en lista: %q", part)
		}
		if !seen[f] {
			seen[f] = true
			result = append(result, f)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no se especificó ningún formato válido en la lista")
	}
	return result, nil
}

func FormatInfoFromFormat(f Format) FormatInfo {
	switch f {
	case Gz:
		return FormatInfo{Format: Gz, Tool: "pigz", PipeFlags: "-dc -p " + ncpuStr(), DirectFlags: "-dk", IsTar: false, TestFlag: "-t"}
	case Xz:
		return FormatInfo{Format: Xz, Tool: "xz", PipeFlags: "-dc -T0", DirectFlags: "-d -T0 -k", IsTar: false, TestFlag: "-t"}
	case Bz2:
		tool := bzip2Bin()
		pipeFlags := "-dc"
		directFlags := "-dk"
		if tool == "lbzip2" {
			pipeFlags += " -n " + ncpuStr()
			directFlags += " -n " + ncpuStr()
		}
		return FormatInfo{Format: Bz2, Tool: tool, PipeFlags: pipeFlags, DirectFlags: directFlags, IsTar: false, TestFlag: "-t"}
	case Bz3:
		return FormatInfo{Format: Bz3, Tool: "bzip3", PipeFlags: "-dc -j " + ncpuStr(), DirectFlags: "-d -kj " + ncpuStr(), IsTar: false, TestFlag: "-t"}
	case Zst:
		return FormatInfo{Format: Zst, Tool: "zstd", PipeFlags: "-dc -T0", DirectFlags: "-d -T0 -k", IsTar: false, TestFlag: "-t"}
	case Lz:
		return FormatInfo{Format: Lz, Tool: "plzip", PipeFlags: "-dc --threads=" + ncpuStr(), DirectFlags: "-dk --threads=" + ncpuStr(), IsTar: false, TestFlag: "-t"}
	case Lrz:
		return FormatInfo{Format: Lrz, Tool: "lrzip", PipeFlags: "-d -p " + ncpuStr() + " -o -", DirectFlags: "-d -p " + ncpuStr(), IsTar: false, TestFlag: "-t"}
	case Lz4:
		pipeFlags := "-dc"
		directFlags := "-dk"
		if lz4SupportsThreads() {
			pipeFlags += " -T" + ncpuStr()
			directFlags += " -T" + ncpuStr()
		}
		return FormatInfo{Format: Lz4, Tool: "lz4", PipeFlags: pipeFlags, DirectFlags: directFlags, IsTar: false, TestFlag: "-t"}
	case Br:
		return FormatInfo{Format: Br, Tool: "brotli", PipeFlags: "-dc", DirectFlags: "-dk", IsTar: false, TestFlag: "-t"}
	case Zip:
		return FormatInfo{Format: Zip, Tool: "unzip", PipeFlags: "-o", DirectFlags: "-o", TestFlag: "-t"}
	case SevenZ:
		return FormatInfo{Format: SevenZ, Tool: sevenzBin(), PipeFlags: "x -mmt=on", DirectFlags: "x -mmt=on", TestFlag: "t"}
	case Rar:
		return FormatInfo{Format: Rar, Tool: rarBin(), PipeFlags: "x -mt" + ncpuStr(), DirectFlags: "x -mt" + ncpuStr(), TestFlag: "t"}
	case Tar:
		return FormatInfo{Format: Tar, Tool: "tar", PipeFlags: "-xf", DirectFlags: "-xf", TestFlag: "-tf"}
	default:
		return FormatInfo{}
	}
}

var knownTarSuffixes = []struct {
	suffix string
	format Format
}{
	{".tar.gz", Gz}, {".tgz", Gz},
	{".tar.xz", Xz}, {".txz", Xz},
	{".tar.bz2", Bz2}, {".tbz2", Bz2},
	{".tar.bz3", Bz3},
	{".tar.zst", Zst}, {".tzst", Zst},
	{".tar.lz", Lz}, {".tlz", Lz},
	{".tar.lrz", Lrz}, {".tar.lz4", Lz4}, {".tar.br", Br},
}

func HasTarSuffix(filename string) bool {
	lower := strings.ToLower(filename)
	for _, entry := range knownTarSuffixes {
		if strings.HasSuffix(lower, entry.suffix) {
			return true
		}
	}
	return false
}

func StripTarSuffix(filename string) string {
	lower := strings.ToLower(filename)
	for _, entry := range knownTarSuffixes {
		if strings.HasSuffix(lower, entry.suffix) {
			return filename[:len(filename)-len(entry.suffix)]
		}
	}
	return filename
}

func ParseFormatFromExt(filename string) (Format, bool) {
	lower := strings.ToLower(filename)
	for _, entry := range knownTarSuffixes {
		if strings.HasSuffix(lower, entry.suffix) {
			return entry.format, true
		}
	}
	base := filepath.Base(lower)
	dot := strings.LastIndexByte(base, '.')
	if dot < 0 || dot == len(base)-1 {
		return 0, false
	}
	ext := base[dot+1:]
	f, err := ParseFormat(ext)
	if err != nil {
		return 0, false
	}
	return f, true
}

func DetectFormat(filename string) (FormatInfo, error) {
	ext, ok := ParseFormatFromExt(filename)
	if !ok {
		return FormatInfo{}, fmt.Errorf("formato no reconocido: %s", filename)
	}
	fi := FormatInfoFromFormat(ext)
	fi.IsTar = HasTarSuffix(filename)
	return fi, nil
}

// FormatMaxThreads returns the maximum practical number of threads the tool for format f can utilize.
// Formats whose CLI tools are strictly single-threaded return 1, preventing wasted thread allocation.
func FormatMaxThreads(f Format) int {
	switch f {
	case Tar, Br:
		return 1
	case Lz4:
		if lz4SupportsThreads() {
			return NCPU()
		}
		return 1
	case Bz2:
		if bzip2Bin() == "bzip2" {
			return 1
		}
		return NCPU()
	case Gz:
		if !hasTool("pigz") {
			return 1
		}
		return NCPU()
	case Lz:
		if !hasTool("plzip") {
			return 1
		}
		return NCPU()
	default:
		return NCPU()
	}
}

// MaxEffectiveThreads returns the maximum number of CPU threads that the compressor
// can physically utilize for an input of fileSize bytes.
// Multi-threaded compressors split data into blocks (e.g. 24MB for xz, 16MB for 7z, 64MB for bzip3).
// If a file is smaller than the block size, assigning dozens of threads produces idle workers.
func MaxEffectiveThreads(f Format, fileSize int64) int {
	fmtMax := FormatMaxThreads(f)
	if fmtMax <= 1 {
		return 1
	}
	if fileSize <= 0 {
		return fmtMax
	}

	var maxUseful int
	switch f {
	case Xz:
		maxUseful = int(fileSize / (24 * 1024 * 1024))
	case SevenZ:
		maxUseful = int(fileSize / (16 * 1024 * 1024))
	case Bz3:
		maxUseful = int(fileSize / (64 * 1024 * 1024))
	case Zst:
		maxUseful = int(fileSize / (4 * 1024 * 1024))
	case Gz:
		maxUseful = int(fileSize / (128 * 1024))
	case Bz2:
		maxUseful = int(fileSize / (900 * 1024))
	case Lz4:
		maxUseful = int(fileSize / (4 * 1024 * 1024))
	case Lz:
		maxUseful = int(fileSize / (2 * 1024 * 1024))
	default:
		maxUseful = fmtMax
	}

	if maxUseful < 1 {
		maxUseful = 1
	}
	if maxUseful > fmtMax {
		maxUseful = fmtMax
	}
	return maxUseful
}

