package main

import (
	"bytes"
	"errors"
	"io/fs"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSampleFile_SmallMediumLarge(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Archivo pequeño (10 KiB) <= 96 KiB
	smallPath := filepath.Join(tmpDir, "small.bin")
	smallData := bytes.Repeat([]byte("ABCDEFGHIJ"), 1024) // 10,240 bytes
	if err := os.WriteFile(smallPath, smallData, 0644); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, sampleBufferSize)
	s1, err := sampleFile(smallPath, int64(len(smallData)), buf)
	if err != nil {
		t.Fatalf("sampleFile(small) error: %v", err)
	}
	if len(s1) != len(smallData) {
		t.Errorf("sampleFile(small) len = %d, want %d", len(s1), len(smallData))
	}
	if !bytes.Equal(s1, smallData) {
		t.Errorf("sampleFile(small) data mismatch")
	}

	// 2. Archivo exacto o mediano cercano a 96 KiB (ej: 80 KiB)
	medPath := filepath.Join(tmpDir, "med.bin")
	medData := bytes.Repeat([]byte("12345678"), 10240) // 81,920 bytes
	if err := os.WriteFile(medPath, medData, 0644); err != nil {
		t.Fatal(err)
	}
	s2, err := sampleFile(medPath, int64(len(medData)), buf)
	if err != nil {
		t.Fatalf("sampleFile(med) error: %v", err)
	}
	if len(s2) != len(medData) {
		t.Errorf("sampleFile(med) len = %d, want %d", len(s2), len(medData))
	}

	// 3. Archivo grande (500 KiB) > 96 KiB (debe tomar 3 fragmentos de 32 KiB)
	largePath := filepath.Join(tmpDir, "large.bin")
	largeData := make([]byte, 500*1024)
	for i := range largeData {
		largeData[i] = byte(i % 251)
	}
	if err := os.WriteFile(largePath, largeData, 0644); err != nil {
		t.Fatal(err)
	}

	s3, err := sampleFile(largePath, int64(len(largeData)), buf)
	if err != nil {
		t.Fatalf("sampleFile(large) error: %v", err)
	}
	if len(s3) != sampleBufferSize {
		t.Fatalf("sampleFile(large) len = %d, want %d", len(s3), sampleBufferSize)
	}

	// Verificar los 3 fragmentos de 32 KiB
	chunkSize := sampleChunkSize
	// Fragmento 1: inicio (0..32768)
	if !bytes.Equal(s3[:chunkSize], largeData[:chunkSize]) {
		t.Errorf("fragmento inicial no coincide")
	}
	// Fragmento 2: centro
	midOffset := (int64(len(largeData)) - int64(chunkSize)) / 2
	if !bytes.Equal(s3[chunkSize:chunkSize*2], largeData[midOffset:midOffset+int64(chunkSize)]) {
		t.Errorf("fragmento central no coincide")
	}
	// Fragmento 3: final
	endOffset := int64(len(largeData)) - int64(chunkSize)
	if !bytes.Equal(s3[chunkSize*2:chunkSize*3], largeData[endOffset:]) {
		t.Errorf("fragmento final no coincide")
	}
}

func TestProbeDeflateRatio(t *testing.T) {
	// 1. Datos vacíos -> 1.0 (incompresible por definición)
	if r := probeDeflateRatio(nil); r != 1.0 {
		t.Errorf("probeDeflateRatio(nil) = %f, want 1.0", r)
	}

	// 2. Todos ceros repetidos (altamente compresible)
	zeros := make([]byte, 65536)
	if r := probeDeflateRatio(zeros); r > 0.05 {
		t.Errorf("probeDeflateRatio(zeros) = %f, want <= 0.05", r)
	}

	// 3. Texto plano legible
	text := bytes.Repeat([]byte("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hola mundo\")\n}\n"), 300)
	if r := probeDeflateRatio(text); r > 0.40 {
		t.Errorf("probeDeflateRatio(text) = %f, want <= 0.40", r)
	}

	// 4. Datos pseudo-aleatorios uniformes (debe dar >= 0.98)
	rnd := rand.New(rand.NewSource(12345))
	randomData := make([]byte, 65536)
	rnd.Read(randomData)
	if r := probeDeflateRatio(randomData); r < 0.98 {
		t.Errorf("probeDeflateRatio(randomData) = %f, want >= 0.98", r)
	}
}

func TestAnalyzeFile_Classification(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Archivo de texto plano (100 KiB)
	txtPath := filepath.Join(tmpDir, "sample.txt")
	txtData := bytes.Repeat([]byte("Lorem ipsum dolor sit amet, consectetur adipiscing elit. Aliquam id massa sed nulla.\n"), 1200)
	if err := os.WriteFile(txtPath, txtData, 0644); err != nil {
		t.Fatal(err)
	}
	resTxt, err := AnalyzeFile(txtPath)
	if err != nil {
		t.Fatalf("AnalyzeFile(txt) error: %v", err)
	}
	if !resTxt.Compressible {
		t.Errorf("sample.txt debió clasificarse como compresible")
	}
	if resTxt.RecommendedFmt != "xz" && resTxt.RecommendedFmt != "7z" {
		t.Errorf("sample.txt formato recomendado: got %s, want xz o 7z", resTxt.RecommendedFmt)
	}
	if resTxt.SavingsBytes <= 0 || resTxt.SavingsPct < 60.0 {
		t.Errorf("sample.txt ahorro esperado > 60%%, got %.2f%% (%d bytes)", resTxt.SavingsPct, resTxt.SavingsBytes)
	}

	// 2. Archivo incompresible / pseudo-aleatorio (64 KiB)
	rndPath := filepath.Join(tmpDir, "sample.bin")
	rnd := rand.New(rand.NewSource(9999))
	rndData := make([]byte, 65536)
	rnd.Read(rndData)
	if err := os.WriteFile(rndPath, rndData, 0644); err != nil {
		t.Fatal(err)
	}
	resRnd, err := AnalyzeFile(rndPath)
	if err != nil {
		t.Fatalf("AnalyzeFile(rnd) error: %v", err)
	}
	if resRnd.Compressible {
		t.Errorf("sample.bin no debe ser compresible (entropía alta)")
	}
	if resRnd.RecommendedFmt != "omitir" {
		t.Errorf("sample.bin formato recomendado: got %s, want omitir", resRnd.RecommendedFmt)
	}
	if resRnd.SavingsBytes != 0 || resRnd.SavingsPct != 0.0 {
		t.Errorf("sample.bin ahorro debe ser 0, got %d (%.2f%%)", resRnd.SavingsBytes, resRnd.SavingsPct)
	}

	// 3. Archivo pre-comprimido por extensión (.mp4 con datos aleatorios)
	mp4Path := filepath.Join(tmpDir, "video.mp4")
	if err := os.WriteFile(mp4Path, rndData, 0644); err != nil {
		t.Fatal(err)
	}
	resMp4, err := AnalyzeFile(mp4Path)
	if err != nil {
		t.Fatalf("AnalyzeFile(mp4) error: %v", err)
	}
	if resMp4.Compressible || resMp4.RecommendedFmt != "omitir" {
		t.Errorf("video.mp4 debe ser incompresible con recomendación omitir, got %v (%s)", resMp4.Compressible, resMp4.RecommendedFmt)
	}

	// 4. Archivo vacío (0 bytes)
	emptyPath := filepath.Join(tmpDir, "empty.txt")
	if err := os.WriteFile(emptyPath, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	resEmpty, err := AnalyzeFile(emptyPath)
	if err != nil {
		t.Fatalf("AnalyzeFile(empty) error: %v", err)
	}
	if resEmpty.Compressible || resEmpty.RecommendedFmt != "omitir" {
		t.Errorf("empty.txt debe ser omitir, got %v (%s)", resEmpty.Compressible, resEmpty.RecommendedFmt)
	}

	// 5. Archivo diminuto (< 512 bytes)
	tinyPath := filepath.Join(tmpDir, "tiny.txt")
	if err := os.WriteFile(tinyPath, []byte("hola mundo corto"), 0644); err != nil {
		t.Fatal(err)
	}
	resTiny, err := AnalyzeFile(tinyPath)
	if err != nil {
		t.Fatalf("AnalyzeFile(tiny) error: %v", err)
	}
	if resTiny.Compressible || resTiny.RecommendedFmt != "omitir" {
		t.Errorf("tiny.txt debe ser omitir, got %v (%s)", resTiny.Compressible, resTiny.RecommendedFmt)
	}
}

func TestDoAnalyze_DirectoryAggregation(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. subcarpeta compresible (código / texto)
	srcDir := filepath.Join(tmpDir, "src")
	os.MkdirAll(srcDir, 0755)
	txtData := bytes.Repeat([]byte("SELECT * FROM users WHERE active = 1 AND role = 'admin';\n"), 800) // ~48 KiB
	os.WriteFile(filepath.Join(srcDir, "query.sql"), txtData, 0644)
	os.WriteFile(filepath.Join(srcDir, "app.go"), bytes.Repeat([]byte("package main\nfunc Run() {}\n"), 1000), 0644)

	// 2. subcarpeta incompresible (videos / binarios aleatorios)
	mediaDir := filepath.Join(tmpDir, "media")
	os.MkdirAll(mediaDir, 0755)
	rnd := rand.New(rand.NewSource(4321))
	rndData := make([]byte, 65536)
	rnd.Read(rndData)
	os.WriteFile(filepath.Join(mediaDir, "clip.mp4"), rndData, 0644)

	// 3. subcarpeta mixta (mezcla archivos compresibles e incompresibles >= 1 MiB)
	mixedDir := filepath.Join(tmpDir, "mixed")
	os.MkdirAll(mixedDir, 0755)
	// archivo incompresible de 1.1 MiB
	bigRnd := make([]byte, 1153434)
	rnd.Read(bigRnd)
	os.WriteFile(filepath.Join(mixedDir, "backup.zip"), bigRnd, 0644)
	// archivo compresible de 1.2 MiB
	bigTxt := bytes.Repeat([]byte("LOG LINE [INFO] user authenticated successfully from 192.168.1.1\n"), 19000)
	os.WriteFile(filepath.Join(mixedDir, "server.log"), bigTxt, 0644)

	// 4. archivos sueltos en raíz
	os.WriteFile(filepath.Join(tmpDir, "top.txt"), txtData, 0644)
	os.WriteFile(filepath.Join(tmpDir, "top.mp4"), rndData, 0644)

	var buf bytes.Buffer
	opts := AnalyzeOptions{}
	err := DoAnalyze([]string{tmpDir}, opts, &buf)
	if err != nil {
		t.Fatalf("DoAnalyze error: %v", err)
	}

	out := buf.String()

	// Verificar encabezado y secciones
	if !strings.Contains(out, "=== Informe de Compresibilidad:") {
		t.Errorf("salida no contiene título principal:\n%s", out)
	}
	if !strings.Contains(out, "=== Resumen del Estudio ===") {
		t.Errorf("salida no contiene resumen final:\n%s", out)
	}

	// Verificar presencia de elementos de primer nivel
	for _, expected := range []string{"src/", "media/", "mixed/", "top.txt", "top.mp4"} {
		if !strings.Contains(out, expected) {
			t.Errorf("salida no contiene elemento esperado %q:\n%s", expected, out)
		}
	}

	// Verificar que mixed/ haya sido detectada como carpeta mixta
	if !strings.Contains(out, "Notas sobre carpetas mixtas") {
		t.Errorf("debió incluirse sección de notas de carpetas mixtas:\n%s", out)
	}
	if strings.Contains(out, "Entropía") {
		t.Errorf("la tabla no debe contener la columna Entropía:\n%s", out)
	}
}

func TestDoAnalyze_FiltersAndClean(t *testing.T) {
	tmpDir := t.TempDir()

	gitDir := filepath.Join(tmpDir, ".git")
	os.MkdirAll(gitDir, 0755)
	os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0644)

	nodeDir := filepath.Join(tmpDir, "node_modules")
	os.MkdirAll(nodeDir, 0755)
	os.WriteFile(filepath.Join(nodeDir, "index.js"), []byte("console.log(1)\n"), 0644)

	docDir := filepath.Join(tmpDir, "docs")
	os.MkdirAll(docDir, 0755)
	os.WriteFile(filepath.Join(docDir, "readme.txt"), bytes.Repeat([]byte("documentacion general\n"), 100), 0644)
	os.WriteFile(filepath.Join(docDir, "debug.log"), bytes.Repeat([]byte("log error line\n"), 100), 0644)

	var buf bytes.Buffer
	opts := AnalyzeOptions{
		Clean:   true,
		Exclude: []string{"*.log"},
	}

	err := DoAnalyze([]string{tmpDir}, opts, &buf)
	if err != nil {
		t.Fatalf("DoAnalyze error: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, ".git") {
		t.Errorf("-clean no excluyó .git:\n%s", out)
	}
	if strings.Contains(out, "node_modules") {
		t.Errorf("-clean no excluyó node_modules:\n%s", out)
	}
	if strings.Contains(out, "debug.log") {
		t.Errorf("-exclude no excluyó *.log:\n%s", out)
	}
	if !strings.Contains(out, "docs/") {
		t.Errorf("docs/ debió aparecer en el reporte:\n%s", out)
	}
}

func TestDoAnalyze_SingleFileTarget(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "single_dump.sql")
	sqlData := bytes.Repeat([]byte("CREATE TABLE example (id INT PRIMARY KEY, name VARCHAR(255));\n"), 500)
	os.WriteFile(filePath, sqlData, 0644)

	var buf bytes.Buffer
	opts := AnalyzeOptions{}
	err := DoAnalyze([]string{filePath}, opts, &buf)
	if err != nil {
		t.Fatalf("DoAnalyze(singleFile) error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "single_dump.sql") {
		t.Errorf("informe de archivo individual no contiene el nombre del archivo:\n%s", out)
	}
	if !strings.Contains(out, "=== Resumen del Estudio ===") {
		t.Errorf("informe de archivo individual no contiene resumen:\n%s", out)
	}
}

func TestDoAnalyze_MixedFolderDominantIncompressible(t *testing.T) {
	tmpDir := t.TempDir()
	mixedDir := filepath.Join(tmpDir, "heavy_media")
	os.MkdirAll(mixedDir, 0755)

	// Incompresible dominante: 3 MiB pseudo-aleatorio
	rnd := rand.New(rand.NewSource(777))
	rndData := make([]byte, 3*1024*1024)
	rnd.Read(rndData)
	if err := os.WriteFile(filepath.Join(mixedDir, "video.mp4"), rndData, 0644); err != nil {
		t.Fatal(err)
	}

	// Compresible útil: 350 KiB de texto (ahorro ~280 KiB, que es < 10% del total pero > 256 KiB)
	txtData := bytes.Repeat([]byte("CRUSH REPORT ENTROPY LOG TEST SAMPLE LINE 12345\n"), 7500)
	if err := os.WriteFile(filepath.Join(mixedDir, "raw.log"), txtData, 0644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	opts := AnalyzeOptions{}
	if err := DoAnalyze([]string{tmpDir}, opts, &buf); err != nil {
		t.Fatalf("DoAnalyze error: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "heavy_media/") {
		t.Errorf("salida no contiene heavy_media/:\n%s", out)
	}
	if !strings.Contains(out, "selectivo*") {
		t.Errorf("heavy_media/ debió clasificarse como selectivo* pese a ratio total bajo:\n%s", out)
	}
	if !strings.Contains(out, "Notas sobre carpetas mixtas") {
		t.Errorf("debió generar nota sobre carpeta mixta:\n%s", out)
	}
}

func TestFormatNumber(t *testing.T) {
	cases := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{9, "9"},
		{999, "999"},
		{1000, "1,000"},
		{12345, "12,345"},
		{123456, "123,456"},
		{1234567, "1,234,567"},
		{10000000, "10,000,000"},
	}

	for _, tc := range cases {
		got := FormatNumber(tc.input)
		if got != tc.want {
			t.Errorf("FormatNumber(%d) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestAnalyzeProgressTracker_AccuratePercentageAndETA(t *testing.T) {
	apt := newAnalyzeProgressTracker()
	apt.AddDiscovered(41481)
	apt.MarkWalkDone()
	if got := apt.discovered.Load(); got != 41481 {
		t.Fatalf("discovered = %d, want 41481", got)
	}

	// 1. Simular 41367 archivos procesados
	for i := 0; i < 41367; i++ {
		apt.Incr()
	}

	done := apt.filesDone.Load()
	if done != 41367 {
		t.Fatalf("done = %d, want 41367", done)
	}

	pct := float64(done) * 100.0 / float64(apt.discovered.Load())
	if pct >= 100.0 {
		t.Errorf("pct debió ser < 100%% cuando aún faltan archivos, got %f", pct)
	}
	if pct < 99.7 || pct > 99.8 {
		t.Errorf("pct esperado ~99.72%%, got %f", pct)
	}
	line := apt.formatProgress()
	if !strings.Contains(line, "99.7%") || !strings.Contains(line, "41,367/41,481") {
		t.Errorf("formatProgress() = %q, esperaba 99.7%% y 41,367/41,481", line)
	}
}

func TestAnalyzeFile_FastPathShortCircuit(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Archivo multimedia grande (1 MiB) con datos de alta entropía
	mp4Path := filepath.Join(tmpDir, "movie.mp4")
	rnd := rand.New(rand.NewSource(1234))
	rndData := make([]byte, 1024*1024)
	rnd.Read(rndData)
	if err := os.WriteFile(mp4Path, rndData, 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Archivo compresible grande (1 MiB) de texto repetitivo
	txtPath := filepath.Join(tmpDir, "dump.sql")
	txtData := bytes.Repeat([]byte("INSERT INTO table VALUES (1, 'test', 'sample string data');\n"), 17000)
	if err := os.WriteFile(txtPath, txtData, 0644); err != nil {
		t.Fatal(err)
	}

	// Probar AnalyzeFile en mp4: debe disparar fast-path (solo leer sampleChunkSize = 32 KiB)
	resMp4, err := AnalyzeFile(mp4Path)
	if err != nil {
		t.Fatalf("AnalyzeFile(mp4) error: %v", err)
	}
	if resMp4.Compressible {
		t.Errorf("movie.mp4 debe ser incompresible")
	}
	if resMp4.SampleBytes != int64(sampleChunkSize) {
		t.Errorf("movie.mp4 SampleBytes = %d, want fast-path %d (solo cabecera)", resMp4.SampleBytes, sampleChunkSize)
	}

	// Probar analyzeFileWithSize directamente
	resMp4Direct, err := analyzeFileWithSize(mp4Path, int64(len(rndData)))
	if err != nil {
		t.Fatalf("analyzeFileWithSize(mp4) error: %v", err)
	}
	if resMp4Direct.SampleBytes != int64(sampleChunkSize) {
		t.Errorf("analyzeFileWithSize(mp4) SampleBytes = %d, want %d", resMp4Direct.SampleBytes, sampleChunkSize)
	}

	// Probar dump.sql: al ser compresible, debe leer la muestra completa de 3 fragmentos (96 KiB)
	resTxt, err := analyzeFileWithSize(txtPath, int64(len(txtData)))
	if err != nil {
		t.Fatalf("analyzeFileWithSize(txt) error: %v", err)
	}
	if !resTxt.Compressible {
		t.Errorf("dump.sql debe ser compresible")
	}
	if resTxt.SampleBytes != int64(sampleBufferSize) {
		t.Errorf("dump.sql SampleBytes = %d, want %d (muestra estratificada completa)", resTxt.SampleBytes, sampleBufferSize)
	}
}

func TestDoAnalyze_SortByCompressibility(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Carpeta con alta compresibilidad (> 80%) pero tamaño modesto (50 KiB, ahorro ~45 KiB)
	highDir := filepath.Join(tmpDir, "high_comp")
	os.MkdirAll(highDir, 0755)
	highData := bytes.Repeat([]byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\n"), 900)
	os.WriteFile(filepath.Join(highDir, "data.txt"), highData, 0644)

	// 2. Carpeta con compresibilidad media (~40-50%) pero tamaño grande (1 MiB, ahorro ~450 KiB)
	// (Con ordenamiento por bytes de ahorro, 450 KiB > 50 KiB pondría med_comp primero.
	// Con ordenamiento por compresibilidad, 99% > 45% pone high_comp primero).
	medDir := filepath.Join(tmpDir, "med_comp")
	os.MkdirAll(medDir, 0755)
	rnd := rand.New(rand.NewSource(42))
	var medData []byte
	chunkText := []byte("Lorem ipsum dolor sit amet, consectetur adipiscing elit. ")
	for i := 0; i < 15000; i++ {
		medData = append(medData, chunkText...)
		rndBlock := make([]byte, 50)
		rnd.Read(rndBlock)
		medData = append(medData, rndBlock...)
	}
	os.WriteFile(filepath.Join(medDir, "data.bin"), medData, 0644)

	// 3. Carpeta incompresible (0% ahorro)
	nullDir := filepath.Join(tmpDir, "null_comp")
	os.MkdirAll(nullDir, 0755)
	nullData := make([]byte, 100*1024)
	rnd.Read(nullData)
	os.WriteFile(filepath.Join(nullDir, "random.bin"), nullData, 0644)

	var buf bytes.Buffer
	err := DoAnalyze([]string{tmpDir}, AnalyzeOptions{}, &buf)
	if err != nil {
		t.Fatalf("DoAnalyze error: %v", err)
	}

	out := buf.String()
	idxHigh := strings.Index(out, "high_comp/")
	idxMed := strings.Index(out, "med_comp/")
	idxNull := strings.Index(out, "null_comp/")

	if idxHigh == -1 || idxMed == -1 || idxNull == -1 {
		t.Fatalf("salida no contiene las 3 carpetas:\n%s", out)
	}

	if !(idxHigh < idxMed && idxMed < idxNull) {
		t.Errorf("orden incorrecto: high_comp (%d) debe ir antes de med_comp (%d) y este antes de null_comp (%d):\n%s",
			idxHigh, idxMed, idxNull, out)
	}
}

func TestAnalyzeFile_HeaderContamination_IncompressibleBody(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "video_container.mkv")

	// 1. Cabecera (32 KiB): texto repetitivo y ceros con baja entropía
	head := bytes.Repeat([]byte("EBML\x00\x00\x00\x01TrackEntry\x00\x00\x00VideoCodec\x00\x00\x00\x00"), 1024)

	// 2. Cuerpo (2 MiB): datos pseudoaleatorios incompresibles con alta entropía (H > 7.95)
	rnd := rand.New(rand.NewSource(12345))
	body := make([]byte, 2*1024*1024)
	rnd.Read(body)

	// 3. Pie (32 KiB): índices / cues con ceros
	tail := bytes.Repeat([]byte("CuesEntry\x00\x00\x00\x00CueTime\x00\x00\x00\x00CueTrack\x00\x00"), 1024)

	var data []byte
	data = append(data, head...)
	data = append(data, body...)
	data = append(data, tail...)

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		t.Fatal(err)
	}

	analysis, err := AnalyzeFile(filePath)
	if err != nil {
		t.Fatalf("AnalyzeFile error: %v", err)
	}

	if analysis.CompLevel != "Nula" {
		t.Errorf("CompLevel = %q, want %q", analysis.CompLevel, "Nula")
	}
	if analysis.RecommendedFmt != "omitir" {
		t.Errorf("RecommendedFmt = %q, want %q", analysis.RecommendedFmt, "omitir")
	}
	if analysis.SavingsPct > 5.0 {
		t.Errorf("SavingsPct = %.2f%%, want <= 5.0%%", analysis.SavingsPct)
	}
}

func TestAnalyzeFile_LargeAndMediumBoundaryFiles(t *testing.T) {
	tmpDir := t.TempDir()

	rnd := rand.New(rand.NewSource(2026))
	randMid := make([]byte, 140*1024)
	rnd.Read(randMid)

	cases := []struct {
		name             string
		filename         string
		data             []byte
		wantCompressible bool
		wantCompLevel    string
		wantSampleBytes  int64
	}{
		{
			name:             "large_compressible_ge_192KiB",
			filename:         "large_text.log",
			data:             bytes.Repeat([]byte("2026-11-20 INFO worker processed batch item successfully\n"), 5000),
			wantCompressible: true,
			wantCompLevel:    "Alta",
			wantSampleBytes:  int64(sampleBufferSize),
		},
		{
			name:             "medium_compressible_between_96KiB_and_192KiB",
			filename:         "medium_text.txt",
			data:             bytes.Repeat([]byte("medium compressible record payload 0123456789\n"), 3200),
			wantCompressible: true,
			wantCompLevel:    "Alta",
			wantSampleBytes:  int64(sampleBufferSize),
		},
		{
			name:             "medium_incompressible_between_96KiB_and_192KiB",
			filename:         "medium_rand.bin",
			data:             randMid,
			wantCompressible: false,
			wantCompLevel:    "Nula",
			wantSampleBytes:  int64(sampleChunkSize),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(tmpDir, tc.filename)
			if err := os.WriteFile(p, tc.data, 0644); err != nil {
				t.Fatal(err)
			}
			got, err := AnalyzeFile(p)
			if err != nil {
				t.Fatalf("AnalyzeFile(%s) error: %v", tc.filename, err)
			}
			if got.Compressible != tc.wantCompressible {
				t.Errorf("Compressible = %v, want %v", got.Compressible, tc.wantCompressible)
			}
			if got.CompLevel != tc.wantCompLevel {
				t.Errorf("CompLevel = %q, want %q", got.CompLevel, tc.wantCompLevel)
			}
			if got.SampleBytes != tc.wantSampleBytes {
				t.Errorf("SampleBytes = %d, want %d", got.SampleBytes, tc.wantSampleBytes)
			}
			if tc.wantCompressible && (got.Entropy <= 0 || got.Entropy >= 7.85) {
				t.Errorf("Entropy = %.2f, expected in (0, 7.85)", got.Entropy)
			}
		})
	}
}

func TestDoAnalyze_StreamingCases(t *testing.T) {
	txtPayload := bytes.Repeat([]byte("streaming test compressible text payload line\n"), 120)

	cases := []struct {
		name           string
		setup          func(t *testing.T, root string) string
		opts           AnalyzeOptions
		wantErr        bool
		wantErrSubstr  string
		wantContains   []string
		wantNotContain []string
	}{
		{
			name: "nested_dirs",
			setup: func(t *testing.T, root string) string {
				deep := filepath.Join(root, "alpha", "beta", "gamma")
				if err := os.MkdirAll(deep, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(deep, "deep.txt"), txtPayload, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "alpha", "mid.txt"), txtPayload, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "root.txt"), txtPayload, 0644); err != nil {
					t.Fatal(err)
				}
				return root
			},
			wantContains: []string{"alpha/", "root.txt", "3 archivos en 3 directorios"},
		},
		{
			name: "empty_dir",
			setup: func(t *testing.T, root string) string {
				return root
			},
			wantContains: []string{"No se encontraron archivos para analizar."},
		},
		{
			name: "only_empty_subdirs",
			setup: func(t *testing.T, root string) string {
				if err := os.MkdirAll(filepath.Join(root, "empty1", "empty2"), 0755); err != nil {
					t.Fatal(err)
				}
				return root
			},
			wantContains: []string{"No se encontraron archivos para analizar."},
		},
		{
			name: "excluded_dirs_and_files",
			setup: func(t *testing.T, root string) string {
				for _, d := range []string{"keep", "skip_dir", ".git"} {
					if err := os.MkdirAll(filepath.Join(root, d), 0755); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(root, "keep", "ok.txt"), txtPayload, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "keep", "drop.tmp"), txtPayload, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "skip_dir", "hidden.txt"), txtPayload, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, ".git", "config"), txtPayload, 0644); err != nil {
					t.Fatal(err)
				}
				return root
			},
			opts: AnalyzeOptions{
				Clean:   true,
				Exclude: []string{"skip_dir", "*.tmp"},
				Verbose: true,
			},
			wantContains:   []string{"keep/", "ok.txt", "1 archivo en 1 directorio"},
			wantNotContain: []string{"skip_dir", "hidden.txt", "drop.tmp", ".git"},
		},
		{
			name: "symlinks_ignored_including_cycle",
			setup: func(t *testing.T, root string) string {
				sub := filepath.Join(root, "real")
				if err := os.MkdirAll(sub, 0755); err != nil {
					t.Fatal(err)
				}
				realFile := filepath.Join(sub, "actual.txt")
				if err := os.WriteFile(realFile, txtPayload, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(realFile, filepath.Join(root, "link_file.txt")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(root, filepath.Join(sub, "loop_link")); err != nil {
					t.Fatal(err)
				}
				return root
			},
			opts:           AnalyzeOptions{Verbose: true},
			wantContains:   []string{"real/", "actual.txt", "1 archivo en 1 directorio"},
			wantNotContain: []string{"link_file.txt", "loop_link"},
		},
		{
			name: "verbose_mode_breakdown",
			setup: func(t *testing.T, root string) string {
				sub := filepath.Join(root, "docs")
				if err := os.MkdirAll(sub, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sub, "guide.md"), txtPayload, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sub, "notes.txt"), bytes.Repeat(txtPayload, 2), 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "top.sql"), txtPayload, 0644); err != nil {
					t.Fatal(err)
				}
				return root
			},
			opts:         AnalyzeOptions{Verbose: true},
			wantContains: []string{"docs/", "└─ guide.md", "└─ notes.txt", "top.sql", "└─ top.sql"},
		},
		{
			name: "path_with_dotdot",
			setup: func(t *testing.T, root string) string {
				if err := os.MkdirAll(filepath.Join(root, "sub"), 0755); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(root, "real_target")
				if err := os.MkdirAll(target, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "clean.txt"), txtPayload, 0644); err != nil {
					t.Fatal(err)
				}
				return filepath.Join(root, "sub", "..", "real_target")
			},
			wantContains: []string{"clean.txt", "1 archivo en 0 directorios"},
		},
		{
			name: "nonexistent_target",
			setup: func(t *testing.T, root string) string {
				return filepath.Join(root, "does-not-exist")
			},
			wantErr:       true,
			wantErrSubstr: "accediendo a",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			target := tc.setup(t, root)
			var buf bytes.Buffer
			err := DoAnalyze([]string{target}, tc.opts, &buf)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("esperaba error, obtuve nil")
				}
				if tc.wantErrSubstr != "" && !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Errorf("error = %q, esperaba contener %q", err.Error(), tc.wantErrSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DoAnalyze error inesperado: %v", err)
			}
			out := buf.String()
			for _, want := range tc.wantContains {
				if !strings.Contains(out, want) {
					t.Errorf("salida no contiene %q:\n%s", want, out)
				}
			}
			for _, forbid := range tc.wantNotContain {
				if strings.Contains(out, forbid) {
					t.Errorf("salida no debía contener %q:\n%s", forbid, out)
				}
			}
		})
	}
}

func TestDoAnalyze_UnreadableEntries(t *testing.T) {
	t.Run("walk_and_worker_errors_counted_without_race", func(t *testing.T) {
		root := t.TempDir()
		goodPath := filepath.Join(root, "good.txt")
		badPath := filepath.Join(root, "bad.txt")
		payload := bytes.Repeat([]byte("unreadable test compressible line\n"), 100)
		if err := os.WriteFile(goodPath, payload, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(badPath, payload, 0644); err != nil {
			t.Fatal(err)
		}

		origWalk := analyzeWalkDir
		origAnalyze := analyzeFileFunc
		t.Cleanup(func() {
			analyzeWalkDir = origWalk
			analyzeFileFunc = origAnalyze
		})

		analyzeWalkDir = func(r string, fn fs.WalkDirFunc) error {
			// Simular una entrada ilegible reportada por WalkDir
			if err := fn(filepath.Join(r, "denied_dir"), nil, os.ErrPermission); err != nil {
				return err
			}
			return origWalk(r, fn)
		}
		analyzeFileFunc = func(path string, size int64) (FileAnalysis, error) {
			if path == badPath {
				return FileAnalysis{}, os.ErrPermission
			}
			return origAnalyze(path, size)
		}

		var buf bytes.Buffer
		if err := DoAnalyze([]string{root}, AnalyzeOptions{}, &buf); err != nil {
			t.Fatalf("DoAnalyze error: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "good.txt") {
			t.Errorf("esperaba good.txt en salida:\n%s", out)
		}
		if strings.Contains(out, "bad.txt") {
			t.Errorf("no debía incluir bad.txt fallido en salida:\n%s", out)
		}
		if !strings.Contains(out, "2 elemento(s) no se pudieron leer") {
			t.Errorf("esperaba conteo de 2 elementos ilegibles:\n%s", out)
		}
	})

	t.Run("real_fs_chmod_unreadable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("chmod 0000 es legible como root")
		}
		root := t.TempDir()
		payload := bytes.Repeat([]byte("permission test payload line\n"), 100)

		okFile := filepath.Join(root, "readable.txt")
		if err := os.WriteFile(okFile, payload, 0644); err != nil {
			t.Fatal(err)
		}
		noReadFile := filepath.Join(root, "secret.txt")
		if err := os.WriteFile(noReadFile, payload, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(noReadFile, 0000); err != nil {
			t.Fatal(err)
		}
		noReadDir := filepath.Join(root, "locked_dir")
		if err := os.MkdirAll(noReadDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(noReadDir, 0000); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = os.Chmod(noReadFile, 0644)
			_ = os.Chmod(noReadDir, 0755)
		})

		var buf bytes.Buffer
		if err := DoAnalyze([]string{root}, AnalyzeOptions{}, &buf); err != nil {
			t.Fatalf("DoAnalyze error: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "readable.txt") {
			t.Errorf("esperaba readable.txt:\n%s", out)
		}
		if !strings.Contains(out, "2 elemento(s) no se pudieron leer") {
			t.Errorf("esperaba 2 elementos ilegibles:\n%s", out)
		}
	})
}

func TestDoAnalyze_StreamsBeforeWalkCompletes(t *testing.T) {
	root := t.TempDir()
	payload := bytes.Repeat([]byte("streaming proof payload\n"), 100)
	firstFile := filepath.Join(root, "first.txt")
	secondFile := filepath.Join(root, "second.txt")
	if err := os.WriteFile(firstFile, payload, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondFile, payload, 0644); err != nil {
		t.Fatal(err)
	}

	origWalk := analyzeWalkDir
	origAnalyze := analyzeFileFunc
	t.Cleanup(func() {
		analyzeWalkDir = origWalk
		analyzeFileFunc = origAnalyze
	})

	firstAnalyzed := make(chan struct{})
	var signalOnce sync.Once

	analyzeFileFunc = func(path string, size int64) (FileAnalysis, error) {
		an, err := origAnalyze(path, size)
		if path == firstFile {
			signalOnce.Do(func() { close(firstAnalyzed) })
		}
		return an, err
	}

	var walkedAfterWorker atomic.Bool
	analyzeWalkDir = func(r string, fn fs.WalkDirFunc) error {
		entries, err := os.ReadDir(r)
		if err != nil {
			return err
		}
		for _, e := range entries {
			p := filepath.Join(r, e.Name())
			if err := fn(p, e, nil); err != nil {
				return err
			}
			if p == firstFile {
				select {
				case <-firstAnalyzed:
					walkedAfterWorker.Store(true)
				case <-time.After(2 * time.Second):
					return errors.New("worker no inició antes de terminar WalkDir (buffering detectado)")
				}
			}
		}
		return nil
	}

	var buf bytes.Buffer
	if err := DoAnalyze([]string{root}, AnalyzeOptions{}, &buf); err != nil {
		t.Fatalf("DoAnalyze falló: %v", err)
	}
	if !walkedAfterWorker.Load() {
		t.Fatalf("WalkDir debió continuar después de que un worker analizó first.txt")
	}
	if !strings.Contains(buf.String(), "first.txt") || !strings.Contains(buf.String(), "second.txt") {
		t.Errorf("reporte incompleto:\n%s", buf.String())
	}
}

func TestDoAnalyze_WalkErrorPropagationAndCleanTeardown(t *testing.T) {
	errBoom := errors.New("boom walk failure")

	cases := []struct {
		name         string
		filesToEmit  int
		returnErr    error
		simulateTTY  bool
		wantErr      bool
		wantReportOk bool
	}{
		{
			name:        "immediate_walk_error_non_tty",
			filesToEmit: 0,
			returnErr:   errBoom,
			simulateTTY: false,
			wantErr:     true,
		},
		{
			name:        "immediate_walk_error_tty",
			filesToEmit: 0,
			returnErr:   errBoom,
			simulateTTY: true,
			wantErr:     true,
		},
		{
			name:        "mid_stream_walk_error_exceeds_channel_cap",
			filesToEmit: 180,
			returnErr:   errBoom,
			simulateTTY: true,
			wantErr:     true,
		},
		{
			name:         "zero_files_tty_clean_stop",
			filesToEmit:  0,
			returnErr:    nil,
			simulateTTY:  true,
			wantErr:      false,
			wantReportOk: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			payload := bytes.Repeat([]byte("teardown line\n"), 50)
			for i := 0; i < tc.filesToEmit; i++ {
				name := filepath.Join(root, "f_"+FormatNumber(i)+".txt")
				if err := os.WriteFile(name, payload, 0644); err != nil {
					t.Fatal(err)
				}
			}

			origWalk := analyzeWalkDir
			origTerm := analyzeIsTerminal
			origStderr := analyzeStderr
			t.Cleanup(func() {
				analyzeWalkDir = origWalk
				analyzeIsTerminal = origTerm
				analyzeStderr = origStderr
			})

			var stderrBuf bytes.Buffer
			analyzeIsTerminal = func() bool { return tc.simulateTTY }
			analyzeStderr = &stderrBuf

			analyzeWalkDir = func(r string, fn fs.WalkDirFunc) error {
				if tc.filesToEmit > 0 {
					if err := origWalk(r, fn); err != nil {
						return err
					}
				}
				return tc.returnErr
			}

			runtime.GC()
			baseGoroutines := runtime.NumGoroutine()

			var out bytes.Buffer
			err := DoAnalyze([]string{root}, AnalyzeOptions{}, &out)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("esperaba error, obtuve nil")
				}
				if !errors.Is(err, errBoom) {
					t.Errorf("error = %v, esperaba envolver %v", err, errBoom)
				}
				if out.Len() != 0 {
					t.Errorf("no debía imprimir reporte en error, obtuvo: %q", out.String())
				}
			} else {
				if err != nil {
					t.Fatalf("error inesperado: %v", err)
				}
				if tc.wantReportOk && !strings.Contains(out.String(), "No se encontraron archivos para analizar.") {
					t.Errorf("salida esperada de directorio vacío, obtenida:\n%s", out.String())
				}
			}

			if tc.simulateTTY {
				s := stderrBuf.String()
				if !strings.Contains(s, "\033[?25l") {
					t.Errorf("TTY debió ocultar cursor al iniciar tracker")
				}
				if !strings.Contains(s, "\033[?25h") {
					t.Errorf("TTY debió restaurar cursor al detener tracker")
				}
			}

			deadline := time.Now().Add(500 * time.Millisecond)
			for runtime.NumGoroutine() > baseGoroutines && time.Now().Before(deadline) {
				runtime.Gosched()
				time.Sleep(5 * time.Millisecond)
			}
			if got := runtime.NumGoroutine(); got > baseGoroutines {
				t.Errorf("posible fuga de goroutines: antes=%d después=%d", baseGoroutines, got)
			}
		})
	}
}

func TestAnalyzeProgressTracker_StreamingDiscoveryAndRace(t *testing.T) {
	t.Run("format_states_table", func(t *testing.T) {
		cases := []struct {
			name           string
			discovered     int64
			discoveredSize int64
			done           int64
			bytesRead      int64
			walkDone       bool
			elapsed        time.Duration
			wantContains   []string
			wantNotContain []string
		}{
			{
				name:           "scanning_initial",
				discovered:     0,
				done:           0,
				bytesRead:      0,
				walkDone:       false,
				elapsed:        10 * time.Millisecond,
				wantContains:   []string{"Escaneando...", "0/0", "iniciando..."},
				wantNotContain: []string{"%", "restantes"},
			},
			{
				name:           "scanning_live_progress",
				discovered:     1200,
				discoveredSize: 10 * 1024 * 1024,
				done:           400,
				bytesRead:      8 * 1024 * 1024,
				walkDone:       false,
				elapsed:        2 * time.Second,
				wantContains:   []string{"Escaneando...", "400/1,200", "MiB/s"},
				wantNotContain: []string{"%", "restantes"},
			},
			{
				name:         "walk_done_with_pct_and_eta",
				discovered:   1000,
				done:         250,
				bytesRead:    10 * 1024 * 1024,
				walkDone:     true,
				elapsed:      5 * time.Second,
				wantContains: []string{"25.0%", "250/1,000", "restantes", "MiB/s"},
			},
			{
				name:           "walk_done_complete_no_eta",
				discovered:     500,
				done:           500,
				bytesRead:      5 * 1024 * 1024,
				walkDone:       true,
				elapsed:        5 * time.Second,
				wantContains:   []string{"100.0%", "500/500"},
				wantNotContain: []string{"restantes"},
			},
			{
				name:         "done_exceeds_discovered_clamped",
				discovered:   5,
				done:         8,
				bytesRead:    1024,
				walkDone:     true,
				elapsed:      2 * time.Second,
				wantContains: []string{"100.0%"},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				apt := newAnalyzeProgressTracker()
				apt.startTime = time.Now().Add(-tc.elapsed)
				if tc.discovered > 0 {
					apt.AddDiscovered(tc.discovered)
				}
				if tc.discoveredSize > 0 {
					apt.AddDiscoveredBytes(tc.discoveredSize)
				}
				if tc.done > 0 {
					apt.filesDone.Store(tc.done)
				}
				if tc.bytesRead > 0 {
					apt.AddBytes(tc.bytesRead)
				}
				if tc.walkDone {
					apt.MarkWalkDone()
				}

				line := apt.formatProgress()
				for _, want := range tc.wantContains {
					if !strings.Contains(line, want) {
						t.Errorf("formatProgress() = %q, esperaba contener %q", line, want)
					}
				}
				for _, forbid := range tc.wantNotContain {
					if strings.Contains(line, forbid) {
						t.Errorf("formatProgress() = %q, no debía contener %q", line, forbid)
					}
				}
			})
		}
	})

	t.Run("concurrent_updates_and_lifecycle_under_race", func(t *testing.T) {
		var buf bytes.Buffer
		apt := newAnalyzeProgressTracker()
		apt.isTTY = true
		apt.out = &buf
		apt.tickInterval = 5 * time.Millisecond

		apt.Start()
		apt.Start() // idempotente

		const total = 400
		const workers = 4
		ch := make(chan int, 16)

		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range ch {
					apt.Incr()
					apt.AddBytes(512)
				}
			}()
		}

		go func() {
			for i := 0; i < total; i++ {
				apt.AddDiscovered(1)
				apt.AddDiscoveredBytes(1024)
				ch <- i
				if i%100 == 0 {
					time.Sleep(6 * time.Millisecond)
				}
			}
			apt.MarkWalkDone()
			close(ch)
		}()

		wg.Wait()
		time.Sleep(15 * time.Millisecond)
		apt.Stop()
		apt.Stop() // idempotente

		if got := apt.discovered.Load(); got != total {
			t.Errorf("discovered = %d, want %d", got, total)
		}
		if got := apt.filesDone.Load(); got != total {
			t.Errorf("filesDone = %d, want %d", got, total)
		}
		if got := apt.bytesRead.Load(); got != total*512 {
			t.Errorf("bytesRead = %d, want %d", got, total*512)
		}
		if !apt.walkDone.Load() {
			t.Errorf("walkDone = false, want true")
		}
		rendered := buf.String()
		if !strings.Contains(rendered, "\033[?25l") || !strings.Contains(rendered, "\033[?25h") {
			t.Errorf("salida TTY incompleta: %q", rendered)
		}
	})

	t.Run("stop_without_start_and_non_tty_noop", func(t *testing.T) {
		var buf bytes.Buffer
		apt := newAnalyzeProgressTracker()
		apt.isTTY = false
		apt.out = &buf
		apt.Start()
		apt.AddDiscovered(10)
		apt.MarkWalkDone()
		apt.Stop()
		if buf.Len() != 0 {
			t.Errorf("non-TTY no debía escribir salida, obtuvo: %q", buf.String())
		}

		apt2 := newAnalyzeProgressTracker()
		apt2.isTTY = true
		apt2.out = &buf
		apt2.Stop() // sin Start() no debe bloquear ni entrar en pánico
	})
}

func TestDoAnalyze_MultiTargetConcurrent(t *testing.T) {
	tmpDir := t.TempDir()
	dirA := filepath.Join(tmpDir, "dirA")
	dirB := filepath.Join(tmpDir, "dirB")
	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "data.txt"), bytes.Repeat([]byte("hola "), 2048), 0644); err != nil {
			t.Fatal(err)
		}
	}

	origWalk := analyzeWalkDir
	t.Cleanup(func() { analyzeWalkDir = origWalk })

	var inFlight atomic.Int32
	bothInside := make(chan struct{})
	var closeOnce sync.Once

	analyzeWalkDir = func(root string, fn fs.WalkDirFunc) error {
		if n := inFlight.Add(1); n >= 2 {
			closeOnce.Do(func() { close(bothInside) })
		}
		defer inFlight.Add(-1)

		select {
		case <-bothInside:
		case <-time.After(1500 * time.Millisecond):
			return errors.New("timeout esperando ejecución concurrente de WalkDir")
		}
		return origWalk(root, fn)
	}

	var buf bytes.Buffer
	if err := DoAnalyze([]string{dirA, dirB}, AnalyzeOptions{}, &buf); err != nil {
		t.Fatalf("DoAnalyze concurrente falló: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, dirA) || !strings.Contains(out, dirB) {
		t.Errorf("esperaba ambos informes en la salida, obtuve:\n%s", out)
	}
}

func TestDoAnalyze_MultiTargetOptionB_OrderAndGlobalSummary(t *testing.T) {
	tmpDir := t.TempDir()
	dirA := filepath.Join(tmpDir, "alpha")
	dirB := filepath.Join(tmpDir, "zeta")
	singleFile := filepath.Join(tmpDir, "middle.log")

	for _, d := range []string{dirA, dirB} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}

	textPayload := bytes.Repeat([]byte("linea repetitiva de log para comprimir bien\n"), 1500)
	rnd := rand.New(rand.NewSource(99))
	incompPayload := make([]byte, 64*1024)
	rnd.Read(incompPayload)

	fileA := filepath.Join(dirA, "app.txt")
	fileB := filepath.Join(dirB, "blob.bin")
	if err := os.WriteFile(fileA, textPayload, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(singleFile, textPayload, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileB, incompPayload, 0644); err != nil {
		t.Fatal(err)
	}

	var bufSingle bytes.Buffer
	if err := DoAnalyze([]string{dirA}, AnalyzeOptions{}, &bufSingle); err != nil {
		t.Fatalf("DoAnalyze single error: %v", err)
	}
	if strings.Contains(bufSingle.String(), "Resumen Global") {
		t.Errorf("un solo parámetro no debe mostrar Resumen Global")
	}

	var buf bytes.Buffer
	if err := DoAnalyze([]string{dirA, singleFile, dirB}, AnalyzeOptions{Verbose: true}, &buf); err != nil {
		t.Fatalf("DoAnalyze multi error: %v", err)
	}
	out := buf.String()

	hA := "=== Informe de Compresibilidad: " + dirA + " ==="
	hMid := "=== Informe de Compresibilidad: " + singleFile + " ==="
	hB := "=== Informe de Compresibilidad: " + dirB + " ==="

	idxA := strings.Index(out, hA)
	idxMid := strings.Index(out, hMid)
	idxB := strings.Index(out, hB)
	if idxA < 0 || idxMid < 0 || idxB < 0 {
		t.Fatalf("faltan encabezados de informes separados en salida:\n%s", out)
	}
	if !(idxA < idxMid && idxMid < idxB) {
		t.Errorf("orden incorrecto de informes: idxA=%d idxMid=%d idxB=%d", idxA, idxMid, idxB)
	}

	if got := strings.Count(out, "=== Resumen del Estudio ==="); got != 3 {
		t.Errorf("esperaba 3 resúmenes individuales, obtuve %d", got)
	}

	globalHeader := "=== Resumen Global (3 elementos) ==="
	idxGlobal := strings.Index(out, globalHeader)
	if idxGlobal < 0 {
		t.Fatalf("falta %q en salida:\n%s", globalHeader, out)
	}
	if idxGlobal <= idxB {
		t.Errorf("Resumen Global (%d) debe aparecer después del último informe (%d)", idxGlobal, idxB)
	}

	totalBytes := int64(len(textPayload)*2 + len(incompPayload))
	globalTail := out[idxGlobal:]
	if !strings.Contains(globalTail, "3 archivos") {
		t.Errorf("Resumen Global no muestra total de 3 archivos:\n%s", globalTail)
	}
	if !strings.Contains(globalTail, FormatSize(totalBytes)) {
		t.Errorf("Resumen Global no muestra tamaño total %s:\n%s", FormatSize(totalBytes), globalTail)
	}
}

func TestDoAnalyze_MultiTargetPartialAndAllErrors(t *testing.T) {
	t.Run("partial_error_continues_valid_targets", func(t *testing.T) {
		tmpDir := t.TempDir()
		dir1 := filepath.Join(tmpDir, "valid1")
		dir2 := filepath.Join(tmpDir, "valid2")
		missing := filepath.Join(tmpDir, "missing_target")
		for _, d := range []string{dir1, dir2} {
			if err := os.MkdirAll(d, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(d, "a.txt"), bytes.Repeat([]byte("abcd"), 512), 0644); err != nil {
				t.Fatal(err)
			}
		}

		var buf bytes.Buffer
		err := DoAnalyze([]string{dir1, missing, dir2}, AnalyzeOptions{}, &buf)
		if err == nil {
			t.Fatalf("esperaba error por %s, obtuve nil", missing)
		}
		out := buf.String()
		if !strings.Contains(out, "=== Informe de Compresibilidad: "+dir1+" ===") {
			t.Errorf("faltó informe de %s tras error parcial:\n%s", dir1, out)
		}
		if !strings.Contains(out, "=== Informe de Compresibilidad: "+dir2+" ===") {
			t.Errorf("faltó informe de %s tras error parcial:\n%s", dir2, out)
		}
		if !strings.Contains(out, "✗ Error analizando "+missing) {
			t.Errorf("faltó aviso de error en salida para %s:\n%s", missing, out)
		}
		if !strings.Contains(out, "=== Resumen Global (2 elementos) ===") {
			t.Errorf("faltó Resumen Global de 2 elementos válidos:\n%s", out)
		}
	})

	t.Run("single_valid_plus_invalid_omits_global_summary", func(t *testing.T) {
		tmpDir := t.TempDir()
		dir1 := filepath.Join(tmpDir, "valid1")
		missing := filepath.Join(tmpDir, "missing")
		if err := os.MkdirAll(dir1, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir1, "a.txt"), bytes.Repeat([]byte("abcd"), 512), 0644); err != nil {
			t.Fatal(err)
		}

		var buf bytes.Buffer
		err := DoAnalyze([]string{dir1, missing}, AnalyzeOptions{}, &buf)
		if err == nil {
			t.Fatalf("esperaba error por %s", missing)
		}
		out := buf.String()
		if !strings.Contains(out, "=== Informe de Compresibilidad: "+dir1+" ===") {
			t.Errorf("faltó informe de %s:\n%s", dir1, out)
		}
		if strings.Contains(out, "Resumen Global") {
			t.Errorf("no debe haber Resumen Global cuando solo 1 objetivo tuvo éxito:\n%s", out)
		}
	})

	t.Run("all_targets_invalid_under_tty", func(t *testing.T) {
		tmpDir := t.TempDir()
		m1 := filepath.Join(tmpDir, "no1")
		m2 := filepath.Join(tmpDir, "no2")

		origTerm := analyzeIsTerminal
		origStderr := analyzeStderr
		t.Cleanup(func() {
			analyzeIsTerminal = origTerm
			analyzeStderr = origStderr
		})
		var stderrBuf bytes.Buffer
		analyzeIsTerminal = func() bool { return true }
		analyzeStderr = &lockedWriter{w: &stderrBuf}

		var buf bytes.Buffer
		err := DoAnalyze([]string{m1, m2}, AnalyzeOptions{}, &buf)
		if err == nil {
			t.Fatalf("esperaba error cuando todos los objetivos faltan")
		}
		out := buf.String()
		if !strings.Contains(out, "✗ Error analizando "+m1) || !strings.Contains(out, "✗ Error analizando "+m2) {
			t.Errorf("se esperaban ambos errores en salida:\n%s", out)
		}
		if strings.Contains(out, "Resumen Global") {
			t.Errorf("no debe imprimir Resumen Global si todos fallan:\n%s", out)
		}
	})

	t.Run("walk_error_on_one_dir_preserves_other_dir", func(t *testing.T) {
		tmpDir := t.TempDir()
		dirA := filepath.Join(tmpDir, "dirA")
		dirB := filepath.Join(tmpDir, "dirB")
		for _, d := range []string{dirA, dirB} {
			if err := os.MkdirAll(d, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(d, "f.txt"), bytes.Repeat([]byte("x"), 2048), 0644); err != nil {
				t.Fatal(err)
			}
		}

		origWalk := analyzeWalkDir
		t.Cleanup(func() { analyzeWalkDir = origWalk })
		errWalkFail := errors.New("fallo walk simulado")
		analyzeWalkDir = func(root string, fn fs.WalkDirFunc) error {
			if root == dirA {
				return errWalkFail
			}
			return origWalk(root, fn)
		}

		var buf bytes.Buffer
		err := DoAnalyze([]string{dirA, dirB}, AnalyzeOptions{}, &buf)
		if !errors.Is(err, errWalkFail) {
			t.Fatalf("esperaba errWalkFail en err, obtuve: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "✗ Error analizando "+dirA) {
			t.Errorf("faltó error de %s:\n%s", dirA, out)
		}
		if !strings.Contains(out, "=== Informe de Compresibilidad: "+dirB+" ===") {
			t.Errorf("faltó informe de %s:\n%s", dirB, out)
		}
	})
}

func TestDoAnalyze_MultiTargetTTYProgressSingleBar(t *testing.T) {
	tmpDir := t.TempDir()
	dir1 := filepath.Join(tmpDir, "d1")
	dir2 := filepath.Join(tmpDir, "d2")
	for _, d := range []string{dir1, dir2} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 5; i++ {
			name := filepath.Join(d, "f_"+FormatNumber(i)+".txt")
			if err := os.WriteFile(name, bytes.Repeat([]byte("tty line\n"), 200), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}

	origTerm := analyzeIsTerminal
	origStderr := analyzeStderr
	t.Cleanup(func() {
		analyzeIsTerminal = origTerm
		analyzeStderr = origStderr
	})

	var stderrBuf bytes.Buffer
	analyzeIsTerminal = func() bool { return true }
	analyzeStderr = &lockedWriter{w: &stderrBuf}

	var out bytes.Buffer
	if err := DoAnalyze([]string{dir1, dir2}, AnalyzeOptions{}, &out); err != nil {
		t.Fatalf("DoAnalyze error: %v", err)
	}

	errOut := stderrBuf.String()
	if !strings.Contains(errOut, "Analizando compresibilidad en 2 elementos") {
		t.Errorf("faltó encabezado único multi-target en TTY stderr, obtuve: %q", errOut)
	}
	if !strings.Contains(errOut, "\033[?25l") || !strings.Contains(errOut, "\033[?25h") {
		t.Errorf("TTY debe ocultar y restaurar cursor una sola vez: %q", errOut)
	}
	if got := strings.Count(errOut, "🔍 Analizando compresibilidad en"); got != 1 {
		t.Errorf("esperaba 1 solo encabezado de progreso global, obtuve %d: %q", got, errOut)
	}
}

func TestDoAnalyze_MultiTargetRotationalLimit(t *testing.T) {
	dirs := make([]string, 4)
	tmpDir := t.TempDir()
	for i := range dirs {
		d := filepath.Join(tmpDir, "dir_"+FormatNumber(i))
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "a.txt"), bytes.Repeat([]byte("rot "), 1024), 0644); err != nil {
			t.Fatal(err)
		}
		dirs[i] = d
	}

	t.Run("rotational_hdd_caps_concurrent_walks_to_2", func(t *testing.T) {
		origWalk := analyzeWalkDir
		origRot := analyzeIsRotational
		t.Cleanup(func() {
			analyzeWalkDir = origWalk
			analyzeIsRotational = origRot
		})

		analyzeIsRotational = func(string) bool { return true }

		var cur, maxObserved atomic.Int32
		analyzeWalkDir = func(root string, fn fs.WalkDirFunc) error {
			n := cur.Add(1)
			for {
				prev := maxObserved.Load()
				if n <= prev || maxObserved.CompareAndSwap(prev, n) {
					break
				}
			}
			time.Sleep(40 * time.Millisecond)
			cur.Add(-1)
			return origWalk(root, fn)
		}

		var out bytes.Buffer
		if err := DoAnalyze(dirs, AnalyzeOptions{}, &out); err != nil {
			t.Fatalf("DoAnalyze rotational error: %v", err)
		}
		if got := maxObserved.Load(); got > 2 {
			t.Errorf("concurrencia rotacional = %d, debía ser <= 2", got)
		}
	})

	t.Run("ssd_allows_more_than_2_concurrent_walks", func(t *testing.T) {
		if NCPU() < 3 {
			t.Skipf("requiere >= 3 CPUs (NCPU=%d)", NCPU())
		}
		origWalk := analyzeWalkDir
		origRot := analyzeIsRotational
		t.Cleanup(func() {
			analyzeWalkDir = origWalk
			analyzeIsRotational = origRot
		})

		analyzeIsRotational = func(string) bool { return false }

		var cur, maxObserved atomic.Int32
		threeInside := make(chan struct{})
		var closeOnce sync.Once
		analyzeWalkDir = func(root string, fn fs.WalkDirFunc) error {
			n := cur.Add(1)
			for {
				prev := maxObserved.Load()
				if n <= prev || maxObserved.CompareAndSwap(prev, n) {
					break
				}
			}
			if n >= 3 {
				closeOnce.Do(func() { close(threeInside) })
			}
			select {
			case <-threeInside:
			case <-time.After(1500 * time.Millisecond):
			}
			cur.Add(-1)
			return origWalk(root, fn)
		}

		var out bytes.Buffer
		if err := DoAnalyze(dirs, AnalyzeOptions{}, &out); err != nil {
			t.Fatalf("DoAnalyze SSD error: %v", err)
		}
		if got := maxObserved.Load(); got < 3 {
			t.Errorf("concurrencia SSD = %d, esperaba >= 3", got)
		}
	})
}

func TestAnalyze_UTF8TruncationAndEdgeCases(t *testing.T) {
	t.Run("renderAnalyzeReport preserves valid UTF-8 and truncates by rune count", func(t *testing.T) {
		// Multi-byte UTF-8 names where byte index 25 lands mid-rune
		entries := []EntryReport{
			{
				Name:           "documentación_histórica_español_2026.sql",
				RelPath:        "documentación_histórica_español_2026.sql",
				IsDir:          true,
				TotalSize:      102400,
				EstimatedSize:  20480,
				SavingsBytes:   81920,
				SavingsPct:     80.0,
				CompLevel:      "Alta",
				RecommendedFmt: "xz",
				FileCount:      2,
				Files: []FileAnalysis{
					{
						// 22 ASCII chars -> "  └─ " (5 runes, 9 bytes) + 22 runes = 27 runes (<= 28 runes, but 31 bytes > 28 bytes!)
						Path:           "123456789012345678.txt",
						Size:           51200,
						EstimatedSize:  10240,
						SavingsBytes:   40960,
						SavingsPct:     80.0,
						CompLevel:      "Alta",
						RecommendedFmt: "xz",
					},
					{
						// Multi-byte runes where byte 25 of "  └─ " + base cuts a 2-byte UTF-8 character in half
						Path:           "configuración_producción_final.json",
						Size:           51200,
						EstimatedSize:  10240,
						SavingsBytes:   40960,
						SavingsPct:     80.0,
						CompLevel:      "Alta",
						RecommendedFmt: "xz",
					},
				},
			},
		}
		summary := AnalyzeSummary{
			TotalFiles:        2,
			TotalDirs:         1,
			TotalBytes:        102400,
			CompressibleBytes: 102400,
			EstimatedBytes:    20480,
			SavingsBytes:      81920,
			SavingsPct:        80.0,
		}

		var buf bytes.Buffer
		renderAnalyzeReport("test_utf8", entries, summary, AnalyzeOptions{Verbose: true}, &buf)
		out := buf.String()

		if strings.ContainsRune(out, '\ufffd') {
			t.Errorf("renderAnalyzeReport produced invalid UTF-8 replacement character:\\n%s", out)
		}
		if !strings.Contains(out, "  └─ 123456789012345678.txt") {
			t.Errorf("27-rune inner file label should not be truncated at 28 runes, got:\\n%s", out)
		}
	})

	t.Run("analyzeFileWithSize clamps negative size to zero", func(t *testing.T) {
		res, err := analyzeFileWithSize("nonexistent_negative.bin", -128)
		if err != nil {
			t.Fatalf("unexpected error for negative size: %v", err)
		}
		if res.Size != 0 || res.EstimatedSize != 0 || res.SampleBytes != 0 {
			t.Errorf("negative size leaked into result: Size=%d EstimatedSize=%d SampleBytes=%d",
				res.Size, res.EstimatedSize, res.SampleBytes)
		}
	})
}
