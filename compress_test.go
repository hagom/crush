package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompressDryRun(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{
		Format:    Gz,
		DryRun:    true,
		OutputDir: tmpDir,
	}

	_, err := DoCompress([]string{testFile}, opts)
	if err != nil {
		t.Errorf("DoCompress dry-run = %v", err)
	}
}

func TestCompressNoFiles(t *testing.T) {
	opts := CompressOptions{
		Format:    Gz,
		OutputDir: ".",
	}
	_, err := DoCompress(nil, opts)
	if err == nil {
		t.Error("DoCompress with nil files should error")
	}
}

func TestExpandGlobs(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "a.txt")
	f2 := filepath.Join(tmpDir, "b.txt")
	os.WriteFile(f1, []byte("a"), 0644)
	os.WriteFile(f2, []byte("b"), 0644)

	result := expandGlobs([]string{filepath.Join(tmpDir, "*.txt")})
	if len(result) != 2 {
		t.Errorf("expandGlobs got %d files, want 2", len(result))
	}
}

func TestCompressModeDesc(t *testing.T) {
	desc := compressModeDesc(Gz)
	if desc == "" {
		t.Error("compressModeDesc(Gz) = empty")
	}
}

func TestCompressUniqueNameWithOutputDir(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldWd)

	if err := os.Mkdir("src", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("src/a.txt", []byte("contenido a"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{Format: Gz, OutputDir: "out", KeepOrig: true}

	first, err := DoCompress([]string{"src/a.txt"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0] != "out/a.tar.gz" {
		t.Fatalf("primera compresión outPath = %v", first)
	}

	second, err := DoCompress([]string{"src/a.txt"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0] != "out/a_1.tar.gz" {
		t.Fatalf("segunda compresión outPath = %v (debería evitar colisión)", second)
	}

	for _, want := range []string{"out/a.tar.gz", "out/a_1.tar.gz"} {
		if _, statErr := os.Stat(want); statErr != nil {
			t.Errorf("Esperaba %s: %v", want, statErr)
		}
	}
}

func TestCompressParallelSplit(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte(strings.Repeat("aaa", 10000)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte(strings.Repeat("bbb", 10000)), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		SplitSize: 1,
		KeepOrig:  true,
		Parallel:  4,
	}

	out, err := DoCompress([]string{filepath.Join(tmpDir, "a.txt"), filepath.Join(tmpDir, "b.txt")}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("outPaths = %v, quiere 2", out)
	}
	for _, want := range []string{filepath.Join(tmpDir, "a.gz.part"), filepath.Join(tmpDir, "b.gz.part")} {
		if _, statErr := os.Stat(want); statErr != nil {
			t.Errorf("Con -s en paralelo esperaba %s: %v", want, statErr)
		}
	}
}

func TestCompressTarPipeSplit(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "file.txt"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		SplitSize: 1,
		KeepOrig:  true,
	}

	out, err := DoCompress([]string{subDir}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 output, got %d: %v", len(out), out)
	}
	if _, err := os.Stat(out[0]); err != nil {
		t.Errorf("output file returned %s does not exist on disk: %v", out[0], err)
	}
}

func TestSplitUnsupportedFormatsDisabled(t *testing.T) {
	if !hasTool(sevenzBin()) && !hasTool("zip") && !hasTool("lrzip") {
		t.Skip("sin herramientas zip/lrzip")
	}
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "a.txt")
	if err := os.WriteFile(src, []byte("contenido a"), 0644); err != nil {
		t.Fatal(err)
	}

	if hasTool("lrzip") {
		out, err := DoCompress([]string{src}, CompressOptions{Format: Lrz, OutputDir: tmpDir, SplitSize: 1, KeepOrig: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range out {
			if strings.HasSuffix(o, ".part") {
				t.Errorf("lrz con -s no debe producir .part: %s", o)
			}
			if _, statErr := os.Stat(o); statErr != nil {
				t.Errorf("salida %s no existe: %v", o, statErr)
			}
		}
	}

	if !hasTool(sevenzBin()) && !hasTool("zip") {
		return
	}
	src2 := filepath.Join(tmpDir, "b.txt")
	if err := os.WriteFile(src2, []byte("contenido b"), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := DoCompress([]string{src, src2}, CompressOptions{Format: Zip, OutputDir: tmpDir, SplitSize: 1, Parallel: 2, KeepOrig: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range out {
		if strings.HasSuffix(o, ".part") {
			t.Errorf("zip con -s no debe producir .part: %s", o)
		}
		if _, statErr := os.Stat(o); statErr != nil {
			t.Errorf("salida %s no existe: %v", o, statErr)
		}
	}
}
func TestCheckCompressTools(t *testing.T) {
	if err := CheckCompressTools(Gz); err != nil {
		t.Fatalf("gz no debería fallar: %v", err)
	}
	if hasTool("brotli") {
		t.Skip("brotli instalado; no se puede probar la ausencia de herramienta")
	}
	if err := CheckCompressTools(Br); err == nil {
		t.Fatal("esperaba error con brotli ausente")
	} else if !strings.Contains(err.Error(), "brotli") {
		t.Fatalf("error sin nombre de herramienta: %v", err)
	}
}

func TestCompressMissingToolNoOutput(t *testing.T) {
	if hasTool("brotli") {
		t.Skip("brotli instalado; no se puede probar la ausencia de herramienta")
	}
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "x.txt")
	if err := os.WriteFile(f, []byte("datos"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := DoCompress([]string{f}, CompressOptions{Format: Br, OutputDir: tmpDir, KeepOrig: true})
	if err == nil {
		t.Fatal("esperaba error de herramienta ausente")
	}
	if !strings.Contains(err.Error(), "herramienta") {
		t.Fatalf("error debería ser del pre-check de herramientas: %v", err)
	}
	if g, _ := filepath.Glob(filepath.Join(tmpDir, "*.br")); len(g) != 0 {
		t.Fatalf("no debió crear archivo de salida: %v", g)
	}
}

func TestCompressParallelNoBorraPreexistente(t *testing.T) {
	tmp := t.TempDir()
	preexist := filepath.Join(tmp, "missing.gz.part")
	if err := os.WriteFile(preexist, []byte("previo"), 0644); err != nil {
		t.Fatal(err)
	}
	opts := CompressOptions{
		Format:    Gz,
		OutputDir: tmp,
		SplitSize: 1,
		Parallel:  2,
		KeepOrig:  true,
		Progress:  NewProgressTracker(0, 1),
	}
	_, err := compressParallel([]string{filepath.Join(tmp, "missing.txt")}, opts)
	if err == nil {
		t.Fatal("esperaba error por archivo fuente inexistente")
	}
	if _, statErr := os.Stat(preexist); statErr != nil {
		t.Fatalf("archivo preexistente fue eliminado como 'salida parcial': %v", statErr)
	}
}

func TestFastOrSlow(t *testing.T) {
	opts := CompressOptions{CompressionOpts: ""}
	lvl := fastOrSlow(opts, 6)
	if lvl != 6 {
		t.Errorf("fastOrSlow with empty opts = %d, want 6", lvl)
	}

	opts.CompressionOpts = "-5"
	lvl = fastOrSlow(opts, 6)
	if lvl != 6 {
		t.Errorf("fastOrSlow with opts = %d, want 6", lvl)
	}
}

func TestCompressParallel(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "file1.txt")
	f2 := filepath.Join(tmpDir, "file2.txt")
	if err := os.WriteFile(f1, []byte("contenido uno"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("contenido dos"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		Parallel:  2,
		KeepOrig:  true, // no borrar originales en test
	}

	outPaths, err := DoCompress([]string{f1, f2}, opts)
	if err != nil {
		t.Fatalf("DoCompress en paralelo falló: %v", err)
	}

	if len(outPaths) != 2 {
		t.Errorf("Esperaba 2 archivos de salida, obtuvo %d: %v", len(outPaths), outPaths)
	}

	for _, path := range outPaths {
		if filepath.Ext(path) != ".gz" {
			t.Errorf("Extensión incorrecta para %s, esperaba .gz", path)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("El archivo de salida %s no existe: %v", path, err)
		}
	}
}

func TestCompressParallelCleanupOnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod 000 no bloquea la lectura como root")
	}
	tmpDir := t.TempDir()
	good := filepath.Join(tmpDir, "good.txt")
	if err := os.WriteFile(good, []byte("contenido bueno"), 0644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(tmpDir, "bad.txt")
	if err := os.WriteFile(bad, []byte("no legible"), 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(bad, 0644) })

	opts := CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		Parallel:  2,
		KeepOrig:  false,
	}

	_, err := DoCompress([]string{good, bad}, opts)
	if err == nil {
		t.Fatal("Esperaba error por la entrada ilegible, no hubo")
	}

	if _, statErr := os.Stat(good); !os.IsNotExist(statErr) {
		t.Errorf("El original exitoso %s debería eliminarse aunque otro falle", good)
	}
	if _, statErr := os.Stat(bad); statErr != nil {
		t.Errorf("El original fallido %s no debería eliminarse: %v", bad, statErr)
	}
	if _, statErr := os.Stat(filepath.Join(tmpDir, "good.gz")); statErr != nil {
		t.Errorf("El archivo comprimido de %s debería existir: %v", good, statErr)
	}
	if _, statErr := os.Stat(filepath.Join(tmpDir, "bad.gz")); !os.IsNotExist(statErr) {
		t.Errorf("La salida parcial del fallido %s debería haberse eliminado", bad)
	}
}

func TestCompressSequentialCleanupOnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod 000 no bloquea la lectura como root")
	}
	tmpDir := t.TempDir()
	bad := filepath.Join(tmpDir, "bad.txt")
	if err := os.WriteFile(bad, []byte("no legible"), 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(bad, 0644) })

	opts := CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		Parallel:  1,
		KeepOrig:  false,
	}

	_, err := DoCompress([]string{bad}, opts)
	if err == nil {
		t.Fatal("Esperaba error por entrada ilegible, no hubo")
	}

	if _, statErr := os.Stat(bad); statErr != nil {
		t.Errorf("El original %s no debería eliminarse tras fallo: %v", bad, statErr)
	}
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "bad.txt" {
			t.Errorf("Salida parcial residual en OutputDir: %s", e.Name())
		}
	}
}

func TestCompressMixedDirAndFile(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub_directory")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "inner.txt"), []byte("inner content"), 0644); err != nil {
		t.Fatal(err)
	}

	regFile := filepath.Join(tmpDir, "regular.txt")
	if err := os.WriteFile(regFile, []byte("regular content"), 0644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "out")
	opts := CompressOptions{
		Format:    Gz,
		OutputDir: outDir,
		Parallel:  4,
		KeepOrig:  true,
	}

	outPaths, err := DoCompress([]string{subDir, regFile}, opts)
	if err != nil {
		t.Fatalf("DoCompress con mezcla de carpeta y archivo falló: %v", err)
	}
	if len(outPaths) != 1 {
		t.Fatalf("esperaba 1 archivo combinado (tar.gz), obtuve %d: %v", len(outPaths), outPaths)
	}
	if !strings.HasSuffix(outPaths[0], ".tar.gz") {
		t.Errorf("el archivo de salida debería ser .tar.gz, obtuve: %s", outPaths[0])
	}
	status, err := TestFile(outPaths[0], TestOptions{})
	if err != nil || status != "OK" {
		t.Errorf("el archivo resultante .tar.gz no es válido: status=%s, err=%v", status, err)
	}
}

func TestCompressFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	file1 := filepath.Join(tmpDir, "f1.txt")
	file2 := filepath.Join(tmpDir, "f2.txt")
	if err := os.WriteFile(file1, []byte("contenido 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("contenido 2"), 0644); err != nil {
		t.Fatal(err)
	}

	listPath := filepath.Join(tmpDir, "files.list")
	listContent := "f1.txt\n" + file2 + "\n\n"
	if err := os.WriteFile(listPath, []byte(listContent), 0644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "out")
	opts := CompressOptions{
		Format:    Gz,
		OutputDir: outDir,
		FromFile:  listPath,
		KeepOrig:  true,
		Parallel:  2,
	}

	outPaths, err := DoCompress(nil, opts)
	if err != nil {
		t.Fatalf("DoCompress con FromFile falló: %v", err)
	}
	if len(outPaths) != 2 {
		t.Fatalf("esperaba 2 archivos comprimidos (paralelo), obtuve %d: %v", len(outPaths), outPaths)
	}
}


