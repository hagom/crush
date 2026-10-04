package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
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

func TestCompressMultiFormat(t *testing.T) {
	if !hasTool("tar") {
		t.Skip("tar no disponible")
	}
	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "data.txt")
	if err := os.WriteFile(srcFile, []byte("contenido para multiples formatos"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{
		Formats:   []Format{Gz, Tar},
		OutputDir: tmpDir,
		KeepOrig:  false,
	}

	outPaths, err := DoCompress([]string{srcFile}, opts)
	if err != nil {
		t.Fatalf("DoCompress multi-format error: %v", err)
	}
	if len(outPaths) != 2 {
		t.Fatalf("DoCompress multi-format devolvió %d archivos, esperados 2: %v", len(outPaths), outPaths)
	}
	for _, p := range outPaths {
		if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
			t.Errorf("archivo generado no existe o está vacío: %s", p)
		}
	}
	// With KeepOrig: false, the original should have been kept for Gz, and deleted only after Tar
	if _, err := os.Stat(srcFile); !os.IsNotExist(err) {
		t.Errorf("archivo original debería haber sido eliminado tras el último formato, pero aún existe")
	}

	// Test with KeepOrig: true
	srcFile2 := filepath.Join(tmpDir, "data2.txt")
	if err := os.WriteFile(srcFile2, []byte("otro archivo para prueba keepOrig"), 0644); err != nil {
		t.Fatal(err)
	}
	optsKeep := CompressOptions{
		Formats:   []Format{Gz, Tar},
		OutputDir: tmpDir,
		KeepOrig:  true,
	}
	outPaths2, err := DoCompress([]string{srcFile2}, optsKeep)
	if err != nil {
		t.Fatalf("DoCompress multi-format keep: %v", err)
	}
	if len(outPaths2) != 2 {
		t.Fatalf("DoCompress multi-format keep devolvió %d archivos, esperados 2", len(outPaths2))
	}
	if _, err := os.Stat(srcFile2); err != nil {
		t.Errorf("archivo original debería existir con KeepOrig: true, pero no se encontró: %v", err)
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
	if len(first) != 1 || first[0] != "out/a.txt.gz" {
		t.Fatalf("primera compresión outPath = %v", first)
	}

	second, err := DoCompress([]string{"src/a.txt"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0] != "out/a.txt_1.gz" {
		t.Fatalf("segunda compresión outPath = %v (debería evitar colisión)", second)
	}

	for _, want := range []string{"out/a.txt.gz", "out/a.txt_1.gz"} {
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
	for _, want := range []string{filepath.Join(tmpDir, "a.txt_parts", "a.txt.gz"), filepath.Join(tmpDir, "b.txt_parts", "b.txt.gz")} {
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
	if _, statErr := os.Stat(filepath.Join(tmpDir, "good.txt.gz")); statErr != nil {
		t.Errorf("El archivo comprimido de %s debería existir: %v", good, statErr)
	}
	if _, statErr := os.Stat(filepath.Join(tmpDir, "bad.txt.gz")); !os.IsNotExist(statErr) {
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
		Combine:   true,
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

func TestSplitUnsupportedFormatsWarning(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("contenido de prueba"), 0644); err != nil {
		t.Fatal(err)
	}

	unsupported := []struct {
		format Format
		name   string
	}{
		{Lrz, "lrz"},
		{Zip, "zip"},
		{SevenZ, "7z"},
		{Tar, "tar"},
		{Rar, "rar"},
	}

	for _, tc := range unsupported {
		t.Run(tc.name, func(t *testing.T) {
			oldStderr := os.Stderr
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("os.Pipe failed: %v", err)
			}
			os.Stderr = w

			opts := CompressOptions{
				Format:    tc.format,
				SplitSize: 10,
				DryRun:    true,
				OutputDir: tmpDir,
			}
			_, _ = DoCompress([]string{testFile}, opts)

			w.Close()
			os.Stderr = oldStderr

			var buf bytes.Buffer
			io.Copy(&buf, r)
			got := buf.String()

			expected := fmt.Sprintf("⚠ split (-s) no soportado para %s (solo disponible para gz, xz, bz2, bz3, zst, lz, lz4, br y tar.*); se ignora", tc.format)
			if !strings.Contains(got, expected) {
				t.Errorf("DoCompress con formato no soportado %s no emitió el warning esperado.\nEsperado contener: %q\nObtenido: %q", tc.format, expected, got)
			}
		})
	}

	// Verificar que formatos soportados no emiten el warning
	supported := []Format{Gz, Xz, Bz2, Bz3, Zst, Lz, Lz4, Br}
	for _, fmtVal := range supported {
		t.Run("supported_"+fmtVal.String(), func(t *testing.T) {
			oldStderr := os.Stderr
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("os.Pipe failed: %v", err)
			}
			os.Stderr = w

			opts := CompressOptions{
				Format:    fmtVal,
				SplitSize: 10,
				DryRun:    true,
				OutputDir: tmpDir,
			}
			_, _ = DoCompress([]string{testFile}, opts)

			w.Close()
			os.Stderr = oldStderr

			var buf bytes.Buffer
			io.Copy(&buf, r)
			got := buf.String()

			if strings.Contains(got, "split (-s) no soportado") {
				t.Errorf("DoCompress con formato soportado %s emitió warning de split no soportado: %q", fmtVal, got)
			}
		})
	}
}

func TestCompressSplitDedicatedDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "myfile.txt")
	if err := os.WriteFile(src, []byte("contenido de prueba para split"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		SplitSize: 1,
		KeepOrig:  true,
	}

	out, err := DoCompress([]string{src}, opts)
	if err != nil {
		t.Fatalf("DoCompress falló: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("esperado 1 archivo de salida, obtenido %d: %v", len(out), out)
	}

	expectedDir := filepath.Join(tmpDir, "myfile.txt_parts")
	fi, err := os.Stat(expectedDir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("se esperaba la creación del directorio dedicado %s", expectedDir)
	}

	// El archivo base debe encontrarse dentro del directorio dedicado
	if !strings.HasPrefix(out[0], expectedDir) {
		t.Errorf("el archivo de salida %s no está dentro del directorio dedicado %s", out[0], expectedDir)
	}
	if _, err := os.Stat(out[0]); err != nil {
		t.Errorf("el archivo base %s no existe en disco: %v", out[0], err)
	}
}

func TestCompressSplitReportPortions(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "report_test.txt")
	if err := os.WriteFile(src, []byte("contenido de prueba para reporte de porciones"), 0644); err != nil {
		t.Fatal(err)
	}

	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w

	opts := CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		SplitSize: 1,
		KeepOrig:  true,
	}

	_, err = DoCompress([]string{src}, opts)

	w.Close()
	os.Stderr = oldStderr

	if err != nil {
		t.Fatalf("DoCompress falló: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	output := buf.String()

	if !strings.Contains(output, "Porciones:") || !strings.Contains(output, "partes") {
		t.Errorf("reporte de compresión no incluye campo de porciones (X / X partes): %q", output)
	}
}

func TestCompressParallelNoGhostDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "fileA.txt")
	f2 := filepath.Join(tmpDir, "fileB.txt")
	if err := os.WriteFile(f1, []byte("data1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("data2"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		SplitSize: 10,
		KeepOrig:  true,
		Parallel:  2,
	}

	_, err := DoCompress([]string{f1, f2}, opts)
	if err != nil {
		t.Fatalf("DoCompress falló: %v", err)
	}

	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "crush_") {
			t.Errorf("se detectó directorio fantasma/residual no utilizado: %s", e.Name())
		}
	}
}

func TestCompressParallelPreservesExtension(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "game1.iso")
	f2 := filepath.Join(tmpDir, "game2.iso")
	if err := os.WriteFile(f1, []byte("isodata1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("isodata2"), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Formato de flujo (single-stream): gz -> debe preservar .iso (game1.iso.gz, game2.iso.gz)
	outDirGz := filepath.Join(tmpDir, "out_gz")
	optsGz := CompressOptions{
		Format:    Gz,
		OutputDir: outDirGz,
		KeepOrig:  true,
		Parallel:  2,
	}
	outGz, err := DoCompress([]string{f1, f2}, optsGz)
	if err != nil {
		t.Fatalf("DoCompress paralelo con gz falló: %v", err)
	}
	if len(outGz) != 2 {
		t.Fatalf("se esperaban 2 archivos, obtenido %d", len(outGz))
	}
	for _, p := range outGz {
		base := filepath.Base(p)
		if !strings.HasSuffix(base, ".iso.gz") {
			t.Errorf("formato de flujo (gz) debería preservar extensión completa .iso.gz, obtenido: %s", base)
		}
	}

	// 2. Formato contenedor: zip -> debe reemplazar la extensión a .zip (game1.zip, game2.zip)
	outDirZip := filepath.Join(tmpDir, "out_zip")
	optsZip := CompressOptions{
		Format:    Zip,
		OutputDir: outDirZip,
		KeepOrig:  true,
		Parallel:  2,
	}
	outZip, err := DoCompress([]string{f1, f2}, optsZip)
	if err != nil {
		t.Fatalf("DoCompress paralelo con zip falló: %v", err)
	}
	if len(outZip) != 2 {
		t.Fatalf("se esperaban 2 archivos, obtenido %d", len(outZip))
	}
	for _, p := range outZip {
		base := filepath.Base(p)
		if strings.Contains(base, ".iso") || !strings.HasSuffix(base, ".zip") {
			t.Errorf("formato contenedor (zip) debería reemplazar extensión a .zip, obtenido: %s", base)
		}
	}
}

func TestCompressionRatioAndMultithreadFlags(t *testing.T) {
	// 1. zstd: --ultra -22
	t.Run("zstd flags", func(t *testing.T) {
		cmd := buildCompressCmd(CompressOptions{Format: Zst})
		args := strings.Join(cmd.Args, " ")
		if !strings.Contains(args, "--ultra") || !strings.Contains(args, "-22") {
			t.Errorf("zstd args want --ultra -22, got %v", cmd.Args)
		}
		cmdFast := buildCompressCmd(CompressOptions{Format: Zst, CompressionOpts: "-fast"})
		argsFast := strings.Join(cmdFast.Args, " ")
		if !strings.Contains(argsFast, "--ultra") || !strings.Contains(argsFast, "-1") {
			t.Errorf("zstd fast args want --ultra -1, got %v", cmdFast.Args)
		}
	})

	// 2. 7z: -mx=9 -md=256m -mfb=273 -ms=on -mmt=on (fallback to -md=128m if RAM < 8GB)
	t.Run("7z flags high memory", func(t *testing.T) {
		origMem := getMemLimit
		defer func() { getMemLimit = origMem }()
		getMemLimit = func() int { return 16384 } // 16GB RAM

		args := build7zArgs([]string{"test.txt"}, "test.7z", CompressOptions{Format: SevenZ})
		joined := strings.Join(args, " ")
		expected := []string{"-mx=9", "-md=256m", "-mfb=273", "-ms=on", "-mmt=on"}
		for _, exp := range expected {
			if !strings.Contains(joined, exp) {
				t.Errorf("7z args missing %q, got %v", exp, args)
			}
		}
	})

	t.Run("7z flags low memory fallback", func(t *testing.T) {
		origMem := getMemLimit
		defer func() { getMemLimit = origMem }()
		getMemLimit = func() int { return 4096 } // 4GB RAM (< 8GB)

		args := build7zArgs([]string{"test.txt"}, "test.7z", CompressOptions{Format: SevenZ})
		joined := strings.Join(args, " ")
		expected := []string{"-mx=9", "-md=128m", "-mfb=273", "-ms=on", "-mmt=on"}
		for _, exp := range expected {
			if !strings.Contains(joined, exp) {
				t.Errorf("7z args missing %q, got %v", exp, args)
			}
		}
	})

	// 3. bzip3: -b 64 -j + threadStr(opts.ThreadLimit)
	t.Run("bzip3 flags", func(t *testing.T) {
		opts := CompressOptions{Format: Bz3, ThreadLimit: 4}
		cmd := buildCompressCmd(opts)
		args := strings.Join(cmd.Args, " ")
		if !strings.Contains(args, "-b 64") && (!containsArg(cmd.Args, "-b") || !containsArg(cmd.Args, "64")) {
			t.Errorf("bzip3 args missing -b 64, got %v", cmd.Args)
		}
		if !strings.Contains(args, "-j 4") && (!containsArg(cmd.Args, "-j") || !containsArg(cmd.Args, "4")) {
			t.Errorf("bzip3 args missing -j 4, got %v", cmd.Args)
		}
	})

	// 4. lz4: change default from 1 to 9 (fastOrSlow(opts, 9)), using LZ4HC high compression
	t.Run("lz4 flags", func(t *testing.T) {
		cmd := buildCompressCmd(CompressOptions{Format: Lz4})
		if !containsArg(cmd.Args, "-9") {
			t.Errorf("lz4 default args want -9 (LZ4HC), got %v", cmd.Args)
		}

		cmdFast := buildCompressCmd(CompressOptions{Format: Lz4, CompressionOpts: "-fast"})
		if !containsArg(cmdFast.Args, "-1") {
			t.Errorf("lz4 fast args want -1, got %v", cmdFast.Args)
		}
	})

	// 5. pigz: ensure -p + threadStr(opts.ThreadLimit) is always passed
	t.Run("pigz flags", func(t *testing.T) {
		opts := CompressOptions{Format: Gz, ThreadLimit: 6}
		cmd := buildCompressCmd(opts)
		if cmd.Path == "pigz" || strings.HasSuffix(cmd.Path, "/pigz") {
			args := strings.Join(cmd.Args, " ")
			if !strings.Contains(args, "-p 6") && (!containsArg(cmd.Args, "-p") || !containsArg(cmd.Args, "6")) {
				t.Errorf("pigz args missing -p 6, got %v", cmd.Args)
			}
		}
	})
}

func containsArg(args []string, target string) bool {
	for _, a := range args {
		if a == target {
			return true
		}
	}
	return false
}

func TestCompressParallelLPTScheduling(t *testing.T) {
	tmpDir := t.TempDir()

	f500b := filepath.Join(tmpDir, "f500b.txt")
	f50kb := filepath.Join(tmpDir, "f50kb.txt")
	f5kb := filepath.Join(tmpDir, "f5kb.txt")
	f200kb := filepath.Join(tmpDir, "f200kb.txt")

	if err := os.WriteFile(f500b, bytes.Repeat([]byte("a"), 500), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f50kb, bytes.Repeat([]byte("b"), 50*1024), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f5kb, bytes.Repeat([]byte("c"), 5*1024), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f200kb, bytes.Repeat([]byte("d"), 200*1024), 0644); err != nil {
		t.Fatal(err)
	}

	files := []string{f500b, f50kb, f5kb, f200kb}
	outDir := filepath.Join(tmpDir, "out")

	totalSize := int64(500 + 50*1024 + 5*1024 + 200*1024)
	pt := NewProgressTracker(totalSize, len(files))

	opts := CompressOptions{
		Format:    Gz,
		OutputDir: outDir,
		KeepOrig:  true,
		Parallel:  2,
		Progress:  pt,
	}

	outPaths, err := compressParallel(files, opts)
	if err != nil {
		t.Fatalf("compressParallel falló: %v", err)
	}
	if len(outPaths) != 4 {
		t.Fatalf("se esperaban 4 archivos de salida, obtenido %d", len(outPaths))
	}

	wantOrder := []string{"f200kb.txt", "f50kb.txt", "f5kb.txt", "f500b.txt"}
	wantSizes := []int64{200 * 1024, 50 * 1024, 5 * 1024, 500}

	if len(pt.files) != len(wantOrder) {
		t.Fatalf("pt.files tiene %d elementos, se esperaban %d", len(pt.files), len(wantOrder))
	}

	for i, wantName := range wantOrder {
		if pt.files[i].Name != wantName {
			t.Errorf("pos %d: progreso archivo %s, se esperaba %s", i, pt.files[i].Name, wantName)
		}
		if pt.files[i].Size != wantSizes[i] {
			t.Errorf("pos %d: tamaño %d, se esperaba %d", i, pt.files[i].Size, wantSizes[i])
		}
		if filepath.Base(files[i]) != wantName {
			t.Errorf("pos %d: slice files ordenado %s, se esperaba %s", i, filepath.Base(files[i]), wantName)
		}
	}

	fEq1 := filepath.Join(tmpDir, "eq1.txt")
	fEq2 := filepath.Join(tmpDir, "eq2.txt")
	if err := os.WriteFile(fEq1, bytes.Repeat([]byte("1"), 1024), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fEq2, bytes.Repeat([]byte("2"), 1024), 0644); err != nil {
		t.Fatal(err)
	}
	filesEq := []string{fEq1, fEq2}
	ptEq := NewProgressTracker(2048, 2)
	optsEq := CompressOptions{
		Format:    Gz,
		OutputDir: filepath.Join(tmpDir, "out_eq"),
		KeepOrig:  true,
		Parallel:  2,
		Progress:  ptEq,
	}
	_, err = compressParallel(filesEq, optsEq)
	if err != nil {
		t.Fatalf("compressParallel estabilidad falló: %v", err)
	}
	if ptEq.files[0].Name != "eq1.txt" || ptEq.files[1].Name != "eq2.txt" {
		t.Errorf("estabilidad no preservada en archivos de igual tamaño: got [%s, %s]", ptEq.files[0].Name, ptEq.files[1].Name)
	}

	fNon1 := filepath.Join(tmpDir, "nonexistent1.txt")
	fNon2 := filepath.Join(tmpDir, "nonexistent2.txt")
	fReal := filepath.Join(tmpDir, "real.txt")
	if err := os.WriteFile(fReal, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	filesNon := []string{fNon1, fReal, fNon2}
	ptNon := NewProgressTracker(4, 3)
	optsNon := CompressOptions{
		Format:    Gz,
		OutputDir: filepath.Join(tmpDir, "out_non"),
		KeepOrig:  true,
		Parallel:  2,
		Progress:  ptNon,
	}
	_, _ = compressParallel(filesNon, optsNon)
	if ptNon.files[0].Name != "real.txt" || ptNon.files[1].Name != "nonexistent1.txt" || ptNon.files[2].Name != "nonexistent2.txt" {
		t.Errorf("estabilidad de fallos de stat no preservada: got [%s, %s, %s]", ptNon.files[0].Name, ptNon.files[1].Name, ptNon.files[2].Name)
	}
}

func TestCompressHashOption(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "sample.txt")
	content := []byte("Hello world for sha256 checksum test!")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		KeepOrig:  true,
		Hash:      true,
	}

	outPaths, err := DoCompress([]string{src}, opts)
	if err != nil {
		t.Fatalf("DoCompress falló: %v", err)
	}
	if len(outPaths) != 1 {
		t.Fatalf("esperado 1 archivo, obtenido %d", len(outPaths))
	}

	archivePath := outPaths[0]
	shaPath := archivePath + ".sha256"
	data, err := os.ReadFile(shaPath)
	if err != nil {
		t.Fatalf("no se creó el archivo de checksum %s: %v", shaPath, err)
	}

	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	expectedHash := fmt.Sprintf("%x", sha256.Sum256(archiveBytes))
	expectedLine := fmt.Sprintf("%s  %s\n", expectedHash, filepath.Base(archivePath))

	if string(data) != expectedLine {
		t.Errorf("contenido de .sha256 incorrecto.\nEsperado: %q\nObtenido: %q", expectedLine, string(data))
	}
}

func TestCompressHashParallel(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "p1.txt")
	f2 := filepath.Join(tmpDir, "p2.txt")
	if err := os.WriteFile(f1, []byte("parallel 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("parallel 2"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{
		Format:    Gz,
		OutputDir: filepath.Join(tmpDir, "out_par"),
		KeepOrig:  true,
		Parallel:  2,
		Hash:      true,
	}

	outPaths, err := DoCompress([]string{f1, f2}, opts)
	if err != nil {
		t.Fatalf("DoCompress paralelo falló: %v", err)
	}
	if len(outPaths) != 2 {
		t.Fatalf("esperado 2 archivos, obtenido %d", len(outPaths))
	}

	for _, p := range outPaths {
		shaPath := p + ".sha256"
		data, err := os.ReadFile(shaPath)
		if err != nil {
			t.Fatalf("no se creó el archivo de checksum %s: %v", shaPath, err)
		}
		archiveBytes, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		expectedHash := fmt.Sprintf("%x", sha256.Sum256(archiveBytes))
		expectedLine := fmt.Sprintf("%s  %s\n", expectedHash, filepath.Base(p))
		if string(data) != expectedLine {
			t.Errorf("contenido de .sha256 incorrecto para %s.\nEsperado: %q\nObtenido: %q", p, expectedLine, string(data))
		}
	}
}

func TestCompressSparseTarFlag(t *testing.T) {
	origExec := execCommand
	defer func() { execCommand = origExec }()

	var capturedPlainArgs []string
	var capturedPipeArgs []string

	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "sparse.bin")
	f, err := os.Create(f1)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Truncate(10 * 1024 * 1024)
	f.Close()

	// 1. Plain tar with Sparse: true
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "tar" {
			capturedPlainArgs = append([]string(nil), args...)
		}
		return origExec(name, args...)
	}

	optsPlain := CompressOptions{
		Format:    Tar,
		Sparse:    true,
		OutputDir: tmpDir,
		KeepOrig:  true,
	}
	_, err = DoCompress([]string{f1}, optsPlain)
	if err != nil {
		t.Fatalf("DoCompress Plain Tar failed: %v", err)
	}

	hasSparse := false
	for _, a := range capturedPlainArgs {
		if a == "--sparse" {
			hasSparse = true
			break
		}
	}
	if !hasSparse {
		t.Errorf("compressPlainTar did not include --sparse in tar args: %v", capturedPlainArgs)
	}

	// 2. Tar pipe with Sparse: true
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "tar" {
			capturedPipeArgs = append([]string(nil), args...)
		}
		return origExec(name, args...)
	}

	optsPipe := CompressOptions{
		Format:    Gz,
		Sparse:    true,
		OutputDir: tmpDir,
		KeepOrig:  true,
		Combine:   true,
	}
	_, err = DoCompress([]string{f1}, optsPipe)
	if err != nil {
		t.Fatalf("DoCompress Tar Pipe failed: %v", err)
	}

	hasSparsePipe := false
	for _, a := range capturedPipeArgs {
		if a == "--sparse" {
			hasSparsePipe = true
			break
		}
	}
	if !hasSparsePipe {
		t.Errorf("compressTarPipe did not include --sparse in tar args: %v", capturedPipeArgs)
	}

	// 3. Plain tar with Sparse: false (verify --sparse is NOT present)
	capturedPlainArgs = nil
	optsNoSparse := CompressOptions{
		Format:    Tar,
		Sparse:    false,
		OutputDir: tmpDir,
		KeepOrig:  true,
	}
	_, err = DoCompress([]string{f1}, optsNoSparse)
	if err != nil {
		t.Fatalf("DoCompress Plain Tar without sparse failed: %v", err)
	}
	for _, a := range capturedPlainArgs {
		if a == "--sparse" {
			t.Errorf("compressPlainTar should not include --sparse when Sparse is false: %v", capturedPlainArgs)
		}
	}
}

func TestCompressPasswordWarningOnStream(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "stream_warn.txt")
	if err := os.WriteFile(src, []byte("stream content"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		KeepOrig:  true,
		Password:  "supersecret",
	}

	outPaths, err := DoCompress([]string{src}, opts)
	if err != nil {
		t.Fatalf("DoCompress con contraseña en stream no debería fallar: %v", err)
	}
	if len(outPaths) != 1 {
		t.Fatalf("esperado 1 archivo, obtenido %d", len(outPaths))
	}
}

func TestCompressPassword7zAndZip(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "secret_data.txt")
	content := []byte("top secret confidential data")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatal(err)
	}

	// 7z
	opts7z := CompressOptions{
		Format:    SevenZ,
		OutputDir: tmpDir,
		KeepOrig:  true,
		Password:  "passwd7z",
	}
	out7z, err := DoCompress([]string{src}, opts7z)
	if err != nil {
		t.Fatalf("DoCompress 7z con password falló: %v", err)
	}
	if len(out7z) != 1 {
		t.Fatalf("esperado 1 archivo 7z")
	}

	// Zip
	optsZip := CompressOptions{
		Format:    Zip,
		OutputDir: tmpDir,
		KeepOrig:  true,
		Password:  "passwdzip",
	}
	outZip, err := DoCompress([]string{src}, optsZip)
	if err != nil {
		t.Fatalf("DoCompress zip con password falló: %v", err)
	}
	if len(outZip) != 1 {
		t.Fatalf("esperado 1 archivo zip")
	}
}

func TestFindCompressibleFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Archivos normales
	if err := os.WriteFile(filepath.Join(tmpDir, "documento.txt"), []byte("hola"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "datos.csv"), []byte("a,b,c"), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Carpeta normal
	subDir := filepath.Join(tmpDir, "fotos")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "foto1.jpg"), []byte("img"), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Archivos y directorios ocultos (deben ser ignorados)
	if err := os.WriteFile(filepath.Join(tmpDir, ".oculto.txt"), []byte("secreto"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	// 4. Archivos ya comprimidos (deben ser ignorados)
	if err := os.WriteFile(filepath.Join(tmpDir, "respaldo.tar.gz"), []byte("tar-gz-mock"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "archivo.zip"), []byte("zip-mock"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "archivo.7z"), []byte("7z-mock"), 0644); err != nil {
		t.Fatal(err)
	}

	// 5. Archivos de checksum y partes split (deben ser ignorados)
	if err := os.WriteFile(filepath.Join(tmpDir, "respaldo.tar.gz.sha256"), []byte("hash"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "dividido.tar.gz.part01"), []byte("p1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmpDir, "dividido_parts"), 0755); err != nil {
		t.Fatal(err)
	}

	files, err := FindCompressibleFiles(tmpDir)
	if err != nil {
		t.Fatalf("FindCompressibleFiles failed: %v", err)
	}

	want := []string{"datos.csv", "documento.txt", "fotos"}
	if len(files) != len(want) {
		t.Fatalf("got %d files %v, want %d %v", len(files), files, len(want), want)
	}
	for i, f := range files {
		if filepath.Base(f) != want[i] {
			t.Errorf("at index %d: got %s, want %s", i, f, want[i])
		}
	}
}

func TestPromptCompressAll(t *testing.T) {
	files := []string{"file1.txt", "file2.txt"}

	// Caso 's'
	r := strings.NewReader("s\n")
	var w bytes.Buffer
	ok, err := PromptCompressAll(r, &w, files, "gz")
	if err != nil {
		t.Fatalf("PromptCompressAll error: %v", err)
	}
	if !ok {
		t.Errorf("PromptCompressAll con 's' debería confirmar")
	}

	// Caso 'no'
	rNo := strings.NewReader("n\n")
	var wNo bytes.Buffer
	okNo, err := PromptCompressAll(rNo, &wNo, files, "7z")
	if err != nil {
		t.Fatalf("PromptCompressAll error: %v", err)
	}
	if okNo {
		t.Errorf("PromptCompressAll con 'n' no debería confirmar")
	}

	// Caso lista vacía
	okEmpty, _ := PromptCompressAll(strings.NewReader(""), &bytes.Buffer{}, nil, "gz")
	if okEmpty {
		t.Errorf("con lista vacía debería retornar false")
	}
}

func TestDoAppendZip(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "f1.txt")
	f2 := filepath.Join(tmpDir, "f2.txt")
	subDir := filepath.Join(tmpDir, "carpeta")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	f3 := filepath.Join(subDir, "f3.txt")

	if err := os.WriteFile(f1, []byte("content 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("content 2"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f3, []byte("content 3"), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Crear archivo inicial .zip con f1.txt
	opts := CompressOptions{
		Format:    Zip,
		OutputDir: tmpDir,
		KeepOrig:  true,
	}
	out, err := DoCompress([]string{f1}, opts)
	if err != nil || len(out) == 0 {
		t.Fatalf("DoCompress zip inicial falló: %v", err)
	}
	zipPath := out[0]

	// 2. Agregar f2.txt y carpeta/ a zipPath existente
	if err := DoAppend(zipPath, []string{f2, subDir}, CompressOptions{}); err != nil {
		t.Fatalf("DoAppend zip falló: %v", err)
	}

	// 3. Verificar que los archivos están dentro de zipPath
	members, ok := listSevenZipMembers(zipPath, "")
	if !ok {
		t.Fatalf("no se pudo listar miembros de zip actualizado")
	}
	joined := strings.Join(members, " ")
	if !strings.Contains(joined, "f1.txt") || !strings.Contains(joined, "f2.txt") {
		t.Errorf("zip no contiene f1 y f2: %v", members)
	}
}

func TestDoAppendTarGz(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "archivo1.txt")
	f2 := filepath.Join(tmpDir, "archivo2.txt")
	if err := os.WriteFile(f1, []byte("texto 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("texto 2"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		KeepOrig:  true,
		Combine:   true,
	}
	out, err := DoCompress([]string{f1}, opts)
	if err != nil || len(out) == 0 {
		t.Fatalf("DoCompress tar.gz inicial falló: %v", err)
	}
	tarGzPath := out[0]

	if err := DoAppend(tarGzPath, []string{f2}, CompressOptions{}); err != nil {
		t.Fatalf("DoAppend tar.gz falló: %v", err)
	}

	members, ok := listTarMembers(tarGzPath)
	if !ok {
		t.Fatalf("no se pudo listar tar.gz actualizado")
	}
	joined := strings.Join(members, " ")
	if !strings.Contains(joined, "archivo1.txt") || !strings.Contains(joined, "archivo2.txt") {
		t.Errorf("tar.gz no contiene ambos archivos: %v", members)
	}
}

func TestDoAppendStreamError(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "simple.txt")
	if err := os.WriteFile(f1, []byte("simple file"), 0644); err != nil {
		t.Fatal(err)
	}
	// Comprimir como stream directo (no tar)
	opts := CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		KeepOrig:  true,
	}
	outPaths, err := DoCompress([]string{f1}, opts)
	if err != nil || len(outPaths) == 0 {
		t.Fatalf("creando archivo de prueba: %v", err)
	}
	// Renombrar o usar archivo de flujo
	gzPath := filepath.Join(tmpDir, "raw.gz")
	cmd := exec.Command("gzip", "-c", f1)
	rawOut, err := os.Create(gzPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = rawOut
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	rawOut.Close()

	f2 := filepath.Join(tmpDir, "extra.txt")
	if err := os.WriteFile(f2, []byte("extra"), 0644); err != nil {
		t.Fatal(err)
	}

	err = DoAppend(gzPath, []string{f2}, CompressOptions{})
	if err == nil {
		t.Errorf("DoAppend a flujo simple (.gz) debería fallar con error explicativo")
	}
}

func TestDoAppend7z(t *testing.T) {
	if !hasTool(sevenzBin()) {
		t.Skip("7z no disponible")
	}
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "file1.txt")
	f2 := filepath.Join(tmpDir, "file2.txt")
	if err := os.WriteFile(f1, []byte("contenido 7z 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("contenido 7z 2"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{
		Format:    SevenZ,
		OutputDir: tmpDir,
		KeepOrig:  true,
	}
	out, err := DoCompress([]string{f1}, opts)
	if err != nil || len(out) == 0 {
		t.Fatalf("DoCompress 7z inicial falló: %v", err)
	}
	archive7z := out[0]

	if err := DoAppend(archive7z, []string{f2}, CompressOptions{}); err != nil {
		t.Fatalf("DoAppend 7z falló: %v", err)
	}

	members, ok := listSevenZipMembers(archive7z, "")
	if !ok {
		t.Fatalf("no se pudo listar miembros de 7z actualizado")
	}
	joined := strings.Join(members, " ")
	if !strings.Contains(joined, "file1.txt") || !strings.Contains(joined, "file2.txt") {
		t.Errorf("7z actualizado no contiene ambos archivos: %v", members)
	}
}

func TestDoAppendTar(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "doc1.txt")
	f2 := filepath.Join(tmpDir, "doc2.txt")
	if err := os.WriteFile(f1, []byte("plain tar 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("plain tar 2"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{
		Format:    Tar,
		OutputDir: tmpDir,
		KeepOrig:  true,
	}
	out, err := DoCompress([]string{f1}, opts)
	if err != nil || len(out) == 0 {
		t.Fatalf("DoCompress tar inicial falló: %v", err)
	}
	tarPath := out[0]

	if err := DoAppend(tarPath, []string{f2}, CompressOptions{}); err != nil {
		t.Fatalf("DoAppend plain tar falló: %v", err)
	}

	members, ok := listTarMembers(tarPath)
	if !ok {
		t.Fatalf("no se pudo listar plain tar actualizado")
	}
	joined := strings.Join(members, " ")
	if !strings.Contains(joined, "doc1.txt") || !strings.Contains(joined, "doc2.txt") {
		t.Errorf("plain tar actualizado no contiene ambos archivos: %v", members)
	}
}

func TestDoAppendWithSha256(t *testing.T) {
	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "hash1.txt")
	f2 := filepath.Join(tmpDir, "hash2.txt")
	if err := os.WriteFile(f1, []byte("hash content 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f2, []byte("hash content 2"), 0644); err != nil {
		t.Fatal(err)
	}

	opts := CompressOptions{
		Format:    Zip,
		OutputDir: tmpDir,
		KeepOrig:  true,
		Hash:      true,
	}
	out, err := DoCompress([]string{f1}, opts)
	if err != nil || len(out) == 0 {
		t.Fatalf("creación inicial con hash falló: %v", err)
	}
	zipPath := out[0]
	shaFile := zipPath + ".sha256"
	if _, err := os.Stat(shaFile); err != nil {
		t.Fatalf("no se generó el archivo .sha256 inicial: %v", err)
	}

	origHash, err := os.ReadFile(shaFile)
	if err != nil {
		t.Fatal(err)
	}

	if err := DoAppend(zipPath, []string{f2}, CompressOptions{Hash: true}); err != nil {
		t.Fatalf("DoAppend con hash falló: %v", err)
	}

	newHash, err := os.ReadFile(shaFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(origHash) == string(newHash) {
		t.Errorf("el hash no cambió tras modificar el archivo comprimido")
	}

	parsedHash, err := ParseSHA256File(shaFile, zipPath)
	if err != nil {
		t.Fatalf("ParseSHA256File falló: %v", err)
	}
	actualHash, err := ComputeSHA256(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if parsedHash != actualHash {
		t.Errorf("el hash en .sha256 (%s) no coincide con el hash real del archivo (%s)", parsedHash, actualHash)
	}
}

func TestDoCompressMultipleDirectoriesIndependent(t *testing.T) {
	tmpDir := t.TempDir()
	d1 := filepath.Join(tmpDir, "Juego1")
	d2 := filepath.Join(tmpDir, "Juego2")
	if err := os.MkdirAll(d1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(d2, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d1, "game.bin"), []byte("data1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d2, "game.bin"), []byte("data2"), 0644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "out")
	opts := CompressOptions{
		Format:    SevenZ,
		OutputDir: outDir,
		KeepOrig:  true,
		Parallel:  2,
		Combine:   false,
	}

	outPaths, err := DoCompress([]string{d1, d2}, opts)
	if err != nil {
		t.Fatalf("DoCompress failed: %v", err)
	}

	if len(outPaths) != 2 {
		t.Fatalf("esperado 2 archivos independientes para 2 carpetas, pero se obtuvieron %d: %v", len(outPaths), outPaths)
	}
	for _, p := range outPaths {
		base := filepath.Base(p)
		if strings.HasPrefix(base, "crush_") {
			t.Errorf("archivo de salida %q tiene prefijo crush_ (fue combinado indebidamente)", p)
		}
	}
}

func TestDoCompressMultipleDirectoriesTarGz(t *testing.T) {
	tmpDir := t.TempDir()
	d1 := filepath.Join(tmpDir, "CarpetaA")
	d2 := filepath.Join(tmpDir, "CarpetaB")
	if err := os.MkdirAll(d1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(d2, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d1, "file1.txt"), []byte("dataA"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d2, "file2.txt"), []byte("dataB"), 0644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "out")
	opts := CompressOptions{
		Format:    Gz,
		OutputDir: outDir,
		KeepOrig:  true,
		Parallel:  2,
		Combine:   false,
	}

	outPaths, err := DoCompress([]string{d1, d2}, opts)
	if err != nil {
		t.Fatalf("DoCompress failed: %v", err)
	}

	if len(outPaths) != 2 {
		t.Fatalf("esperado 2 archivos independientes tar.gz, pero se obtuvieron %d: %v", len(outPaths), outPaths)
	}
	for _, p := range outPaths {
		base := filepath.Base(p)
		if strings.HasPrefix(base, "crush_") {
			t.Errorf("archivo de salida %q tiene prefijo crush_ (fue combinado indebidamente)", p)
		}
		if !strings.HasSuffix(p, ".tar.gz") {
			t.Errorf("archivo de salida de directorio %q debería terminar en .tar.gz", p)
		}
	}
}

func TestDoCompressMultipleDirectoriesCombined(t *testing.T) {
	tmpDir := t.TempDir()
	d1 := filepath.Join(tmpDir, "Dir1")
	d2 := filepath.Join(tmpDir, "Dir2")
	if err := os.MkdirAll(d1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(d2, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d1, "f.txt"), []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d2, "f.txt"), []byte("2"), 0644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "out")
	opts := CompressOptions{
		Format:    SevenZ,
		OutputDir: outDir,
		KeepOrig:  true,
		Parallel:  2,
		Combine:   true, // Explicitly combined!
	}

	outPaths, err := DoCompress([]string{d1, d2}, opts)
	if err != nil {
		t.Fatalf("DoCompress combined failed: %v", err)
	}

	if len(outPaths) != 1 {
		t.Fatalf("con Combine=true esperado 1 archivo, pero se obtuvieron %d: %v", len(outPaths), outPaths)
	}
	base := filepath.Base(outPaths[0])
	if !strings.HasPrefix(base, "crush_") {
		t.Errorf("esperado prefijo crush_ al combinar, obtenido %s", base)
	}
}

func TestDoCompressMixedFilesAndDirectoriesIndependent(t *testing.T) {
	tmpDir := t.TempDir()
	d1 := filepath.Join(tmpDir, "MiCarpeta")
	f1 := filepath.Join(tmpDir, "archivo.txt")
	if err := os.MkdirAll(d1, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d1, "sub.txt"), []byte("sub"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f1, []byte("contenido"), 0644); err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(tmpDir, "out")
	opts := CompressOptions{
		Format:    SevenZ,
		OutputDir: outDir,
		KeepOrig:  true,
		Parallel:  2,
		Combine:   false,
	}

	outPaths, err := DoCompress([]string{d1, f1}, opts)
	if err != nil {
		t.Fatalf("DoCompress mixed failed: %v", err)
	}

	if len(outPaths) != 2 {
		t.Fatalf("esperado 2 archivos independientes para mezcla archivo/carpeta, obtenido %d: %v", len(outPaths), outPaths)
	}
	for _, p := range outPaths {
		base := filepath.Base(p)
		if strings.HasPrefix(base, "crush_") {
			t.Errorf("archivo de salida %q tiene prefijo crush_ (fue combinado indebidamente)", p)
		}
	}
}

func TestAdaptive7zDict(t *testing.T) {
	tests := []struct {
		name     string
		fileSize int64
		threads  int
		want     string
	}{
		{
			name:     "small file <= 16MB",
			fileSize: 10 * 1024 * 1024,
			threads:  1,
			want:     "-md=16m",
		},
		{
			name:     "medium file <= 32MB",
			fileSize: 25 * 1024 * 1024,
			threads:  2,
			want:     "-md=32m",
		},
		{
			name:     "medium file <= 64MB",
			fileSize: 50 * 1024 * 1024,
			threads:  4,
			want:     "-md=64m",
		},
		{
			name:     "medium file <= 128MB",
			fileSize: 100 * 1024 * 1024,
			threads:  4,
			want:     "-md=128m",
		},
		{
			name:     "zero or unknown size defaults to 256m or clamped",
			fileSize: 0,
			threads:  1,
			want:     "-md=256m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := adaptive7zDict(tt.fileSize, tt.threads)
			if got != tt.want {
				t.Errorf("adaptive7zDict(%d, %d) = %s, want %s", tt.fileSize, tt.threads, got, tt.want)
			}
		})
	}
}

func TestGetDirSize(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	data1 := []byte("hello world")
	data2 := []byte("longer test data string 1234567890")
	if err := os.WriteFile(filepath.Join(tmpDir, "file1.txt"), data1, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "file2.txt"), data2, 0644); err != nil {
		t.Fatal(err)
	}

	expected := int64(len(data1) + len(data2))
	got, err := GetDirSize(tmpDir)
	if err != nil {
		t.Fatalf("GetDirSize returned error: %v", err)
	}
	if got != expected {
		t.Errorf("GetDirSize = %d, want %d", got, expected)
	}
}

func gunzipFile(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("%s no es gzip válido: %v", path, err)
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("leyendo gzip %s: %v", path, err)
	}
	return data
}

func TestDoCompressSingleFileIsStreamWithoutTar(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "notas.txt")
	content := []byte(strings.Repeat("contenido de prueba\n", 500))
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(tmpDir, "out")

	outPaths, err := DoCompress([]string{src}, CompressOptions{Format: Gz, OutputDir: outDir, KeepOrig: true})
	if err != nil {
		t.Fatalf("DoCompress: %v", err)
	}
	want := filepath.Join(outDir, "notas.txt.gz")
	if len(outPaths) != 1 || outPaths[0] != want {
		t.Fatalf("outPaths = %v, want [%s]", outPaths, want)
	}
	if got := gunzipFile(t, want); !bytes.Equal(got, content) {
		t.Errorf("el .gz de un archivo simple debe contener el archivo directo (sin tar): %d bytes vs %d originales", len(got), len(content))
	}
}

func TestDoCompressSingleDirectoryIsTarredAutomatically(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar no disponible")
	}
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "proyecto")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("bbb"), 0644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(tmpDir, "out")

	outPaths, err := DoCompress([]string{dir}, CompressOptions{Format: Gz, OutputDir: outDir, KeepOrig: true})
	if err != nil {
		t.Fatalf("DoCompress: %v", err)
	}
	want := filepath.Join(outDir, "proyecto.tar.gz")
	if len(outPaths) != 1 || outPaths[0] != want {
		t.Fatalf("outPaths = %v, want [%s]", outPaths, want)
	}
	tr := tar.NewReader(bytes.NewReader(gunzipFile(t, want)))
	found := false
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		if strings.HasSuffix(h.Name, "b.txt") {
			found = true
		}
	}
	if !found {
		t.Error("el .tar.gz de una carpeta debe contener un tar con b.txt")
	}
}

func TestDoCompressSingleFilesWithSameStemDoNotCollide(t *testing.T) {
	tmpDir := t.TempDir()
	outDir := filepath.Join(tmpDir, "out")
	var outs []string
	for _, name := range []string{"datos.txt", "datos.csv"} {
		src := filepath.Join(tmpDir, name)
		if err := os.WriteFile(src, []byte("contenido de "+name), 0644); err != nil {
			t.Fatal(err)
		}
		p, err := DoCompress([]string{src}, CompressOptions{Format: Gz, OutputDir: outDir, KeepOrig: true})
		if err != nil {
			t.Fatalf("DoCompress %s: %v", name, err)
		}
		outs = append(outs, p...)
	}
	for i, want := range []string{"datos.txt.gz", "datos.csv.gz"} {
		if filepath.Base(outs[i]) != want {
			t.Errorf("salida %d = %s, want %s (sin colisión ni sufijo _1)", i, outs[i], want)
		}
	}
}

func TestDoCompressSingleFileStreamRoundTrip(t *testing.T) {
	tests := []struct {
		format  Format
		tool    string
		wantExt string
	}{
		{Gz, "gzip", ".txt.gz"},
		{Xz, "xz", ".txt.xz"},
		{Zst, "zstd", ".txt.zst"},
		{Bz2, "bzip2", ".txt.bz2"},
	}
	for _, tt := range tests {
		t.Run(tt.format.String(), func(t *testing.T) {
			if _, err := exec.LookPath(tt.tool); err != nil {
				t.Skipf("%s no disponible", tt.tool)
			}
			tmpDir := t.TempDir()
			src := filepath.Join(tmpDir, "doc.txt")
			content := []byte(strings.Repeat("round trip ", 1000))
			if err := os.WriteFile(src, content, 0644); err != nil {
				t.Fatal(err)
			}
			outDir := filepath.Join(tmpDir, "out")
			outPaths, err := DoCompress([]string{src}, CompressOptions{Format: tt.format, OutputDir: outDir, KeepOrig: true})
			if err != nil {
				t.Fatalf("DoCompress: %v", err)
			}
			if len(outPaths) != 1 || !strings.HasSuffix(outPaths[0], tt.wantExt) || strings.Contains(outPaths[0], ".tar.") {
				t.Fatalf("outPaths = %v, want sufijo %s sin .tar.", outPaths, tt.wantExt)
			}
			extractDir := filepath.Join(tmpDir, "extract")
			if err := DoDecompress(outPaths, DecompressOptions{OutputDir: extractDir, Force: true}); err != nil {
				t.Fatalf("DoDecompress: %v", err)
			}
			got, err := os.ReadFile(filepath.Join(extractDir, "doc.txt"))
			if err != nil {
				t.Fatalf("doc.txt no se recuperó con su nombre original: %v", err)
			}
			if !bytes.Equal(got, content) {
				t.Error("el contenido extraído no coincide con el original")
			}
		})
	}
}

func TestDoCompressSingleFileSplit(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "grande.bin")
	data := make([]byte, 3*1024*1024)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, data, 0644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(tmpDir, "out")
	outPaths, err := DoCompress([]string{src}, CompressOptions{Format: Gz, OutputDir: outDir, KeepOrig: true, SplitSize: 1})
	if err != nil {
		t.Fatalf("DoCompress: %v", err)
	}
	if len(outPaths) != 1 || !strings.HasSuffix(outPaths[0], "grande.bin.gz") {
		t.Fatalf("outPaths = %v, want .../grande.bin.gz", outPaths)
	}
	if len(globSplitParts(outPaths[0])) == 0 {
		t.Errorf("se esperaban partes divididas junto a %s", outPaths[0])
	}
}

func TestBuildCompressCmdBrotliQualityFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts CompressOptions
		want string
	}{
		{"por defecto", CompressOptions{Format: Br}, "11"},
		{"fast", CompressOptions{Format: Br, CompressionOpts: "-fast"}, "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := buildCompressCmd(tc.opts).Args[1:]
			found := false
			for i, a := range args {
				if len(a) > 2 && a[0] == '-' && a[1] != '-' && a[1] >= '0' && a[1] <= '9' {
					t.Errorf("brotli no acepta calidad pegada (%q): se interpretaría como flags sueltos", a)
				}
				if a == "-q" && i+1 < len(args) && args[i+1] == tc.want {
					found = true
				}
			}
			if !found {
				t.Errorf("args = %v, se esperaba '-q %s'", args, tc.want)
			}
		})
	}
}
