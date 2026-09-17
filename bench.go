package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var DefaultBenchFormats = []Format{
	Gz, Bz2, Xz, Zst, Lz4, Br, Zip, SevenZ, Lz, Bz3,
}

type BenchResult struct {
	Format      Format
	FormatName  string
	Compressor  string
	CompTime    time.Duration
	CompSpeed   float64 // MB/s
	OrigSize    int64
	CompSize    int64
	Ratio       float64 // (CompSize / OrigSize) * 100
	DecompTime  time.Duration
	DecompSpeed float64 // MB/s
	Status      string  // "OK", "OMITIDO", "ERROR"
	ErrorMsg    string
}

func GenerateBenchmarkDataset(sizeBytes int64, outPath string) error {
	if sizeBytes <= 0 {
		return fmt.Errorf("tamaño inválido para dataset: %d", sizeBytes)
	}

	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("no se pudo crear archivo de dataset %s: %w", outPath, err)
	}
	defer f.Close()

	w := bufio.NewWriterSize(f, 64*1024)
	defer w.Flush()

	r := rand.New(rand.NewSource(42))

	sampleText := []byte("Lorem ipsum dolor sit amet, consectetur adipiscing elit. " +
		"2026-09-17 [INFO] crush benchmark stream worker processing item status=OK. " +
		"func ProcessRecord(id int, data []byte) error { return nil }\n")

	var written int64
	randBuf := make([]byte, 256)

	for written < sizeBytes {
		remaining := sizeBytes - written

		textChunkSize := int64(len(sampleText))
		if textChunkSize > remaining {
			textChunkSize = remaining
		}
		if textChunkSize > 0 {
			toWrite := sampleText
			if int64(len(toWrite)) > textChunkSize {
				toWrite = toWrite[:textChunkSize]
			}
			if _, err := w.Write(toWrite); err != nil {
				return err
			}
			written += int64(len(toWrite))
		}

		if written >= sizeBytes {
			break
		}

		randChunkSize := int64(len(randBuf))
		if randChunkSize > (sizeBytes - written) {
			randChunkSize = sizeBytes - written
		}
		if randChunkSize > 0 {
			r.Read(randBuf[:randChunkSize])
			if _, err := w.Write(randBuf[:randChunkSize]); err != nil {
				return err
			}
			written += randChunkSize
		}
	}

	return nil
}

func getBenchCompressor(f Format) (tool string, available bool) {
	switch f {
	case Gz:
		if hasTool("pigz") {
			return "pigz", true
		}
		if hasTool("gzip") {
			return "gzip", true
		}
		return "pigz", false
	case Bz2:
		tool = bzip2Bin()
		return tool, hasTool(tool)
	case Xz:
		return "xz", hasTool("xz")
	case Zst:
		return "zstd", hasTool("zstd")
	case Lz4:
		return "lz4", hasTool("lz4")
	case Br:
		return "brotli", hasTool("brotli")
	case Zip:
		if hasTool(sevenzBin()) {
			return sevenzBin(), true
		}
		if hasTool("zip") && hasTool("unzip") {
			return "zip", true
		}
		return "zip", false
	case SevenZ:
		tool = sevenzBin()
		return tool, hasTool(tool)
	case Lz:
		if hasTool("plzip") {
			return "plzip", true
		}
		if hasTool("lzip") {
			return "lzip", true
		}
		return "plzip", false
	case Bz3:
		return "bzip3", hasTool("bzip3")
	default:
		return "", false
	}
}

func computeFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func BenchmarkFormat(datasetPath string, f Format, workDir string) BenchResult {
	tool, available := getBenchCompressor(f)
	res := BenchResult{
		Format:     f,
		FormatName: f.String(),
		Compressor: tool,
	}

	if !available {
		res.Status = "OMITIDO"
		res.ErrorMsg = "herramienta no disponible"
		return res
	}

	fi, err := os.Stat(datasetPath)
	if err != nil {
		res.Status = "ERROR"
		res.ErrorMsg = fmt.Sprintf("no se pudo acceder al dataset: %v", err)
		return res
	}
	res.OrigSize = fi.Size()

	origHash, err := computeFileSHA256(datasetPath)
	if err != nil {
		res.Status = "ERROR"
		res.ErrorMsg = fmt.Sprintf("error calculando hash del dataset: %v", err)
		return res
	}

	ext := f.String()
	compFile := filepath.Join(workDir, fmt.Sprintf("bench_%s_%d.%s", ext, time.Now().UnixNano(), ext))
	decompFile := filepath.Join(workDir, fmt.Sprintf("bench_%s_%d.decomp", ext, time.Now().UnixNano()))
	extractDir := filepath.Join(workDir, fmt.Sprintf("extract_%s_%d", ext, time.Now().UnixNano()))

	startComp := time.Now()
	compErr := runFormatCompression(f, tool, datasetPath, compFile)
	res.CompTime = time.Since(startComp)
	if res.CompTime <= 0 {
		res.CompTime = time.Microsecond
	}

	if compErr != nil {
		res.Status = "ERROR"
		res.ErrorMsg = fmt.Sprintf("error comprimiendo: %v", compErr)
		return res
	}

	compFi, err := os.Stat(compFile)
	if err != nil {
		res.Status = "ERROR"
		res.ErrorMsg = fmt.Sprintf("archivo comprimido no encontrado: %v", err)
		return res
	}
	res.CompSize = compFi.Size()

	if res.OrigSize > 0 {
		res.Ratio = float64(res.CompSize) / float64(res.OrigSize) * 100.0
		res.CompSpeed = (float64(res.OrigSize) / (1024 * 1024)) / res.CompTime.Seconds()
	}

	startDecomp := time.Now()
	decompErr := runFormatDecompression(f, tool, compFile, decompFile, extractDir)
	res.DecompTime = time.Since(startDecomp)
	if res.DecompTime <= 0 {
		res.DecompTime = time.Microsecond
	}

	if decompErr != nil {
		res.Status = "ERROR"
		res.ErrorMsg = fmt.Sprintf("error descomprimiendo: %v", decompErr)
		return res
	}

	if res.OrigSize > 0 {
		res.DecompSpeed = (float64(res.OrigSize) / (1024 * 1024)) / res.DecompTime.Seconds()
	}

	var targetDecompPath string
	if f == Zip || f == SevenZ {
		targetDecompPath = filepath.Join(extractDir, filepath.Base(datasetPath))
	} else {
		targetDecompPath = decompFile
	}

	decompHash, err := computeFileSHA256(targetDecompPath)
	if err != nil {
		res.Status = "ERROR"
		res.ErrorMsg = fmt.Sprintf("error verificando integridad: %v", err)
		return res
	}

	if decompHash != origHash {
		res.Status = "ERROR"
		res.ErrorMsg = "integridad fallida: sha256 no coincide con el original"
		return res
	}

	res.Status = "OK"
	return res
}

func runFormatCompression(f Format, tool, datasetPath, compFile string) error {
	datasetAbs, err := filepath.Abs(datasetPath)
	if err != nil {
		return err
	}
	compFileAbs, err := filepath.Abs(compFile)
	if err != nil {
		return err
	}

	if f == Zip {
		if hasTool(sevenzBin()) {
			cmd := exec.Command(sevenzBin(), "a", "-tzip", "-mx=9", "-mmt="+ncpuStr(), compFileAbs, filepath.Base(datasetAbs))
			cmd.Dir = filepath.Dir(datasetAbs)
			cmd.Stdout = io.Discard
			var stderrBuf bytes.Buffer
			cmd.Stderr = &stderrBuf
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("%w: %s", err, stderrBuf.String())
			}
			return nil
		}
		cmd := exec.Command("zip", "-q", "-r", "-9", compFileAbs, filepath.Base(datasetAbs))
		cmd.Dir = filepath.Dir(datasetAbs)
		var stderrBuf bytes.Buffer
		cmd.Stderr = &stderrBuf
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%w: %s", err, stderrBuf.String())
		}
		return nil
	}

	if f == SevenZ {
		cmd := exec.Command(sevenzBin(), "a", "-mx=9", "-md=128m", "-ms=on", "-mmt="+ncpuStr(), compFileAbs, filepath.Base(datasetAbs))
		cmd.Dir = filepath.Dir(datasetAbs)
		cmd.Stdout = io.Discard
		var stderrBuf bytes.Buffer
		cmd.Stderr = &stderrBuf
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%w: %s", err, stderrBuf.String())
		}
		return nil
	}

	inF, err := os.Open(datasetAbs)
	if err != nil {
		return err
	}
	defer inF.Close()

	outF, err := os.Create(compFileAbs)
	if err != nil {
		return err
	}
	defer outF.Close()

	var cmd *exec.Cmd
	switch f {
	case Gz:
		cmd = exec.Command(tool, "-c")
	case Bz2:
		cmd = exec.Command(tool, "-c")
	case Xz:
		cmd = exec.Command("xz", "-c", "-T0")
	case Zst:
		cmd = exec.Command("zstd", "-c", "-T0")
	case Lz4:
		cmd = exec.Command("lz4", "-c")
	case Br:
		cmd = exec.Command("brotli", "-c")
	case Lz:
		if tool == "plzip" {
			cmd = exec.Command("plzip", "-c", "--threads="+ncpuStr())
		} else {
			cmd = exec.Command("lzip", "-c")
		}
	case Bz3:
		cmd = exec.Command("bzip3", "-c", "-j"+ncpuStr())
	default:
		return fmt.Errorf("formato no soportado en bench: %s", f)
	}

	cmd.Stdin = inF
	cmd.Stdout = outF
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, stderrBuf.String())
	}
	return nil
}

func runFormatDecompression(f Format, tool, compFile, decompFile, extractDir string) error {
	compFileAbs, err := filepath.Abs(compFile)
	if err != nil {
		return err
	}
	decompFileAbs, err := filepath.Abs(decompFile)
	if err != nil {
		return err
	}
	extractDirAbs, err := filepath.Abs(extractDir)
	if err != nil {
		return err
	}

	if f == Zip {
		_ = os.MkdirAll(extractDirAbs, 0755)
		if hasTool(sevenzBin()) {
			cmd := exec.Command(sevenzBin(), "x", "-tzip", "-y", "-mmt="+ncpuStr(), "-o"+extractDirAbs, compFileAbs)
			cmd.Stdout = io.Discard
			var stderrBuf bytes.Buffer
			cmd.Stderr = &stderrBuf
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("%w: %s", err, stderrBuf.String())
			}
			return nil
		}
		cmd := exec.Command("unzip", "-q", "-o", compFileAbs, "-d", extractDirAbs)
		var stderrBuf bytes.Buffer
		cmd.Stderr = &stderrBuf
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%w: %s", err, stderrBuf.String())
		}
		return nil
	}

	if f == SevenZ {
		_ = os.MkdirAll(extractDirAbs, 0755)
		cmd := exec.Command(sevenzBin(), "x", "-y", "-mmt="+ncpuStr(), "-o"+extractDirAbs, compFileAbs)
		cmd.Stdout = io.Discard
		var stderrBuf bytes.Buffer
		cmd.Stderr = &stderrBuf
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%w: %s", err, stderrBuf.String())
		}
		return nil
	}

	inF, err := os.Open(compFileAbs)
	if err != nil {
		return err
	}
	defer inF.Close()

	outF, err := os.Create(decompFileAbs)
	if err != nil {
		return err
	}
	defer outF.Close()

	var cmd *exec.Cmd
	switch f {
	case Gz:
		cmd = exec.Command(tool, "-dc")
	case Bz2:
		cmd = exec.Command(tool, "-dc")
	case Xz:
		cmd = exec.Command("xz", "-dc", "-T0")
	case Zst:
		cmd = exec.Command("zstd", "-dc", "-T0")
	case Lz4:
		cmd = exec.Command("lz4", "-dc")
	case Br:
		cmd = exec.Command("brotli", "-dc")
	case Lz:
		if tool == "plzip" {
			cmd = exec.Command("plzip", "-dc", "--threads="+ncpuStr())
		} else {
			cmd = exec.Command("lzip", "-dc")
		}
	case Bz3:
		cmd = exec.Command("bzip3", "-dc", "-j"+ncpuStr())
	default:
		return fmt.Errorf("formato no soportado en bench: %s", f)
	}

	cmd.Stdin = inF
	cmd.Stdout = outF
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, stderrBuf.String())
	}
	return nil
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000.0)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}

func formatSpeed(speed float64) string {
	return fmt.Sprintf("%.1f MB/s", speed)
}

func formatRatio(ratio float64) string {
	return fmt.Sprintf("%.1f%%", ratio)
}

func FormatBenchTable(results []BenchResult) string {
	var sb strings.Builder

	sb.WriteString(Bold)
	sb.WriteString(fmt.Sprintf("%-7s | %-10s | %10s | %13s | %8s | %11s | %13s | %-10s\n",
		"FORMAT", "COMPRESSOR", "COMP TIME", "COMP SPEED", "RATIO", "DECOMP TIME", "DECOMP SPEED", "STATUS"))
	sb.WriteString(NC)
	sb.WriteString(fmt.Sprintf("%s-+-%s-+-%s-+-%s-+-%s-+-%s-+-%s-+-%s\n",
		strings.Repeat("-", 7), strings.Repeat("-", 10), strings.Repeat("-", 10),
		strings.Repeat("-", 13), strings.Repeat("-", 8), strings.Repeat("-", 11),
		strings.Repeat("-", 13), strings.Repeat("-", 10)))

	for _, r := range results {
		var compTimeStr, compSpeedStr, ratioStr, decompTimeStr, decompSpeedStr string
		var statusStr string

		switch r.Status {
		case "OK":
			compTimeStr = formatDuration(r.CompTime)
			compSpeedStr = formatSpeed(r.CompSpeed)
			ratioStr = formatRatio(r.Ratio)
			decompTimeStr = formatDuration(r.DecompTime)
			decompSpeedStr = formatSpeed(r.DecompSpeed)
			statusStr = Green + "OK" + NC
		case "OMITIDO":
			compTimeStr = "-"
			compSpeedStr = "-"
			ratioStr = "-"
			decompTimeStr = "-"
			decompSpeedStr = "-"
			statusStr = Yellow + "OMITIDO" + NC
		default:
			compTimeStr = "-"
			compSpeedStr = "-"
			ratioStr = "-"
			decompTimeStr = "-"
			decompSpeedStr = "-"
			statusStr = Red + "ERROR" + NC
		}

		sb.WriteString(fmt.Sprintf("%-7s | %-10s | %10s | %13s | %8s | %11s | %13s | %s\n",
			r.FormatName, r.Compressor, compTimeStr, compSpeedStr, ratioStr, decompTimeStr, decompSpeedStr, statusStr))
	}

	return sb.String()
}

func FormatBenchSummary(results []BenchResult, origSize int64) string {
	var sb strings.Builder

	var fastestComp *BenchResult
	var bestRatio *BenchResult
	var fastestDecomp *BenchResult

	for i := range results {
		r := &results[i]
		if r.Status != "OK" {
			continue
		}

		if fastestComp == nil || r.CompSpeed > fastestComp.CompSpeed {
			fastestComp = r
		}
		if bestRatio == nil || r.Ratio < bestRatio.Ratio {
			bestRatio = r
		}
		if fastestDecomp == nil || r.DecompSpeed > fastestDecomp.DecompSpeed {
			fastestDecomp = r
		}
	}

	sb.WriteString("\n")
	sb.WriteString(Bold + "================================== RESUMEN ==================================\n" + NC)

	if fastestComp == nil {
		sb.WriteString("  No se completó ningún formato con éxito.\n")
	} else {
		sb.WriteString(fmt.Sprintf("  %sCompresor más rápido:%s    %s%-5s (%-7s)%s - %s%s%s (%s)\n",
			Blue, NC, Bold, fastestComp.FormatName, fastestComp.Compressor, NC, Green, formatSpeed(fastestComp.CompSpeed), NC, formatDuration(fastestComp.CompTime)))
		sb.WriteString(fmt.Sprintf("  %sMejor ratio compresión:%s  %s%-5s (%-7s)%s - %s%s%s (%s de %s)\n",
			Blue, NC, Bold, bestRatio.FormatName, bestRatio.Compressor, NC, Green, formatRatio(bestRatio.Ratio), NC, FormatSize(bestRatio.CompSize), FormatSize(origSize)))
		sb.WriteString(fmt.Sprintf("  %sDescompresor más rápido:%s %s%-5s (%-7s)%s - %s%s%s (%s)\n",
			Blue, NC, Bold, fastestDecomp.FormatName, fastestDecomp.Compressor, NC, Green, formatSpeed(fastestDecomp.DecompSpeed), NC, formatDuration(fastestDecomp.DecompTime)))
	}

	sb.WriteString(Bold + "=============================================================================\n" + NC)
	return sb.String()
}

func DoBench(datasetPath string, sizeMB int) error {
	if sizeMB <= 0 {
		sizeMB = 10
	}

	var isTempDataset bool
	if datasetPath == "" {
		isTempDataset = true
		tempFile, err := os.CreateTemp("", "crush-bench-dataset-*.dat")
		if err != nil {
			return fmt.Errorf("no se pudo crear archivo temporal para dataset: %w", err)
		}
		datasetPath = tempFile.Name()
		_ = tempFile.Close()
		defer os.Remove(datasetPath)

		fmt.Printf("%sGenerando dataset determinista de %d MB...%s\n", Blue, sizeMB, NC)
		if err := GenerateBenchmarkDataset(int64(sizeMB)*1024*1024, datasetPath); err != nil {
			return fmt.Errorf("error generando dataset: %w", err)
		}
	} else {
		fi, err := os.Stat(datasetPath)
		if err != nil {
			return fmt.Errorf("archivo dataset no encontrado: %w", err)
		}
		if fi.IsDir() {
			return fmt.Errorf("%s es un directorio; el benchmark requiere un archivo regular", datasetPath)
		}
	}

	fi, err := os.Stat(datasetPath)
	if err != nil {
		return fmt.Errorf("error obteniendo información del dataset: %w", err)
	}
	origSize := fi.Size()

	workDir, err := os.MkdirTemp("", "crush-bench-run-*")
	if err != nil {
		return fmt.Errorf("no se pudo crear directorio de trabajo para benchmark: %w", err)
	}
	defer os.RemoveAll(workDir)

	fmt.Printf("\n%s=== CRUSH BENCHMARK ===%s\n", Bold, NC)
	if isTempDataset {
		fmt.Printf("Dataset:     %s (generado, %s)\n", datasetPath, FormatSize(origSize))
	} else {
		fmt.Printf("Dataset:     %s (%s)\n", datasetPath, FormatSize(origSize))
	}
	fmt.Printf("Núcleos CPU: %d\n\n", NCPU())

	results := make([]BenchResult, 0, len(DefaultBenchFormats))
	for _, f := range DefaultBenchFormats {
		tool, avail := getBenchCompressor(f)
		if !avail {
			results = append(results, BenchResult{
				Format:     f,
				FormatName: f.String(),
				Compressor: tool,
				Status:     "OMITIDO",
				ErrorMsg:   "herramienta no disponible",
			})
			continue
		}

		fmt.Printf("Probando %-5s (%-7s)... ", f.String(), tool)
		res := BenchmarkFormat(datasetPath, f, workDir)
		results = append(results, res)

		if res.Status == "OK" {
			fmt.Printf("%sOK%s (comp: %s, decomp: %s, ratio: %s)\n",
				Green, NC, formatSpeed(res.CompSpeed), formatSpeed(res.DecompSpeed), formatRatio(res.Ratio))
		} else if res.Status == "OMITIDO" {
			fmt.Printf("%sOMITIDO%s\n", Yellow, NC)
		} else {
			fmt.Printf("%sERROR%s: %s\n", Red, NC, res.ErrorMsg)
		}
	}

	fmt.Printf("\n")
	fmt.Print(FormatBenchTable(results))
	fmt.Print(FormatBenchSummary(results, origSize))

	return nil
}
