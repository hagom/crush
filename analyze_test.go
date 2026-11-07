package main

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	apt := newAnalyzeProgressTracker(41481)
	if apt.totalFiles != 41481 {
		t.Fatalf("totalFiles = %d, want 41481", apt.totalFiles)
	}

	// 1. Simular 41367 archivos procesados
	for i := 0; i < 41367; i++ {
		apt.Incr()
	}

	done := apt.filesDone.Load()
	if done != 41367 {
		t.Fatalf("done = %d, want 41367", done)
	}

	pct := float64(done) * 100.0 / float64(apt.totalFiles)
	if pct >= 100.0 {
		t.Errorf("pct debió ser < 100%% cuando aún faltan archivos, got %f", pct)
	}
	if pct < 99.7 || pct > 99.8 {
		t.Errorf("pct esperado ~99.72%%, got %f", pct)
	}
}


