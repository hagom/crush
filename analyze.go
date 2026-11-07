package main

import (
	"compress/flate"
	"io"
	"os"
	"sync"
)

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
