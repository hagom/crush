package main

import (
	"compress/flate"
	"io"
	"math"
	"os"
	"path/filepath"
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
	if _, err := f.ReadAt(targetBuf[:sampleChunkSize], 0); err != nil && err != io.EOF {
		return nil, err
	}

	// 2. Mitad
	midOffset := (size - int64(sampleChunkSize)) / 2
	if _, err := f.ReadAt(targetBuf[sampleChunkSize:sampleChunkSize*2], midOffset); err != nil && err != io.EOF {
		return nil, err
	}

	// 3. Final
	endOffset := size - int64(sampleChunkSize)
	if _, err := f.ReadAt(targetBuf[sampleChunkSize*2:sampleBufferSize], endOffset); err != nil && err != io.EOF {
		return nil, err
	}

	return targetBuf[:sampleBufferSize], nil
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
