package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestReorderArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "combined short flags",
			args: []string{"crush", "-c", "-f", "gz", "file.txt", "-tkv"},
			want: []string{"crush", "-c", "-f", "gz", "-t", "-k", "-v", "file.txt"},
		},
		{
			name: "flags after positional args",
			args: []string{"crush", "-c", "-f", "7z", "file.txt", "-k", "-v"},
			want: []string{"crush", "-c", "-f", "7z", "-k", "-v", "file.txt"},
		},
		{
			name: "no args",
			args: []string{"crush"},
			want: []string{"crush"},
		},
		{
			name: "no flags",
			args: []string{"crush", "file.txt"},
			want: []string{"crush", "file.txt"},
		},
		{
			name: "combined with single flag",
			args: []string{"crush", "-c", "-f", "gz", "file.txt", "-tv"},
			want: []string{"crush", "-c", "-f", "gz", "-t", "-v", "file.txt"},
		},
		{
			name: "long flags untouched",
			args: []string{"crush", "--force", "file.txt"},
			want: []string{"crush", "--force", "file.txt"},
		},
		{
			name: "flag with value preserved",
			args: []string{"crush", "-f", "gz", "-o", "/tmp/out", "file.txt"},
			want: []string{"crush", "-f", "gz", "-o", "/tmp/out", "file.txt"},
		},
		{
			name: "split flag with value",
			args: []string{"crush", "-c", "-f", "gz", "-s", "10", "file.txt"},
			want: []string{"crush", "-c", "-f", "gz", "-s", "10", "file.txt"},
		},
		{
			name: "completion flag with value after positional",
			args: []string{"crush", "file.txt", "-completion", "bash"},
			want: []string{"crush", "-completion", "bash", "file.txt"},
		},
		{
			name: "add flag with positional archive and files",
			args: []string{"crush", "archive.zip", "new.txt", "-a", "-v"},
			want: []string{"crush", "-a", "-v", "archive.zip", "new.txt"},
		},
		{
			name: "update flag combined short",
			args: []string{"crush", "archive.tar", "file.txt", "-uv"},
			want: []string{"crush", "-u", "-v", "archive.tar", "file.txt"},
		},
		{
			name: "input list flag -i with value",
			args: []string{"crush", "-c", "-f", "gz", "-i", "list.txt"},
			want: []string{"crush", "-c", "-f", "gz", "-i", "list.txt"},
		},
		{
			name: "multi format flag -F with value",
			args: []string{"crush", "-c", "file.txt", "-F", "gz,xz,zst"},
			want: []string{"crush", "-c", "-F", "gz,xz,zst", "file.txt"},
		},
		{
			name: "input list flag -i after positional",
			args: []string{"crush", "extra.txt", "-i", "list.txt"},
			want: []string{"crush", "-i", "list.txt", "extra.txt"},
		},
		{
			name: "watch flag with value",
			args: []string{"crush", "-watch", "/tmp", "-c", "-f", "gz"},
			want: []string{"crush", "-watch", "/tmp", "-c", "-f", "gz"},
		},
		{
			name: "watch flag after positional",
			args: []string{"crush", "extra.txt", "-watch", "/tmp"},
			want: []string{"crush", "-watch", "/tmp", "extra.txt"},
		},
		{
			name: "double dash watch flag",
			args: []string{"crush", "--watch", "/tmp", "-c"},
			want: []string{"crush", "--watch", "/tmp", "-c"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reorderArgs(tt.args)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("reorderArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMainEmptyFilesValidation(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "-l")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("crush -l without files expected error exit code, got 0")
	}
	if !strings.Contains(string(out), "debe especificar archivos") {
		t.Errorf("crush -l without files output = %q, want 'debe especificar archivos'", string(out))
	}
}

func TestInstallHelpText(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "-h")
	out, _ := cmd.CombinedOutput()
	help := string(out)
	found := false
	for _, line := range strings.Split(help, "\n") {
		if strings.Contains(line, "--install") && strings.Contains(line, "Instalar") && !strings.Contains(line, "--install-deps") {
			found = true
			if strings.Contains(line, "herramientas faltantes") {
				t.Errorf("--install help text still mentions 'herramientas faltantes': %s", line)
			}
			if !strings.Contains(line, "/usr/local/bin") {
				t.Errorf("--install help text should mention /usr/local/bin: %s", line)
			}
		}
	}
	if !found {
		t.Errorf("--install flag description not found in help output")
	}
}

func TestInstallBinaryTo(t *testing.T) {
	tmpDir := t.TempDir()
	src := filepath.Join(tmpDir, "crush_dummy")
	if err := os.WriteFile(src, []byte("#!/bin/sh\necho test\n"), 0755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(tmpDir, "bin", "crush")
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatal(err)
	}
	if err := installBinaryTo(src, dest); err != nil {
		t.Fatalf("installBinaryTo failed: %v", err)
	}
	fi, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat dest failed: %v", err)
	}
	if fi.Mode()&0111 == 0 {
		t.Errorf("dest permissions not executable: %v", fi.Mode())
	}
}

func TestMainDecompressScanNoFiles(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "crush_bin")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build falló: %v, salida: %s", err, string(out))
	}

	tmpDir := t.TempDir()
	cmd := exec.Command(binPath, "-d")
	cmd.Dir = tmpDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("crush -d en dir vacío falló: %v, salida: %s", err, string(out))
	}
	if !strings.Contains(string(out), "No se encontraron archivos comprimidos") {
		t.Errorf("salida esperada contenía 'No se encontraron archivos comprimidos', obtenida: %s", string(out))
	}
}

func TestMainDecompressScanCancel(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "crush_bin")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build falló: %v, salida: %s", err, string(out))
	}

	tmpDir := t.TempDir()
	zipFile := filepath.Join(tmpDir, "sample.zip")
	if err := os.WriteFile(zipFile, []byte("dummy zip content"), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(binPath, "-d")
	cmd.Dir = tmpDir
	cmd.Stdin = strings.NewReader("n\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("crush -d cancelado falló con error: %v, salida: %s", err, string(out))
	}
	if !strings.Contains(string(out), "Operación cancelada") {
		t.Errorf("salida esperada contenía 'Operación cancelada', obtenida: %s", string(out))
	}
}

func TestMainDecompressScanConfirm(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "crush_bin")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build falló: %v, salida: %s", err, string(out))
	}

	tmpDir := t.TempDir()
	txtPath := filepath.Join(tmpDir, "hello.txt")
	if err := os.WriteFile(txtPath, []byte("contenido de prueba"), 0644); err != nil {
		t.Fatal(err)
	}
	tarPath := filepath.Join(tmpDir, "hello.tar")
	cmdTar := exec.Command("tar", "-cf", tarPath, "-C", tmpDir, "hello.txt")
	if err := cmdTar.Run(); err != nil {
		t.Skip("tar no disponible para test")
	}
	// Eliminar original para comprobar que se extrae
	os.Remove(txtPath)

	cmd := exec.Command(binPath, "-d")
	cmd.Dir = tmpDir
	cmd.Stdin = strings.NewReader("s\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("crush -d confirmado falló: %v, salida: %s", err, string(out))
	}

	// Verificar que hello.txt fue extraído
	if _, err := os.Stat(txtPath); err != nil {
		t.Errorf("archivo esperado %s no fue extraído tras confirmación: %v, salida: %s", txtPath, err, string(out))
	}
}

func TestMainColoredWarningsAndErrors(t *testing.T) {
	binPath := filepath.Join(t.TempDir(), "crush_bin")
	if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build falló: %v, salida: %s", err, string(out))
	}

	t.Run("Warning -s without -c has Yellow color and prefix", func(t *testing.T) {
		cmd := exec.Command(binPath, "-s", "10", "-l", "dummy.tar")
		out, _ := cmd.CombinedOutput()
		output := string(out)
		wantWarn := Yellow + "⚠ Advertencia: -s solo tiene efecto con -c (ignorado)" + NC
		if !strings.Contains(output, wantWarn) {
			t.Errorf("expected colored warning %q in output, got:\n%s", wantWarn, output)
		}
	})

	t.Run("Warning -f without -c has Yellow color and prefix", func(t *testing.T) {
		cmd := exec.Command(binPath, "-f", "gz", "-l", "dummy.tar")
		out, _ := cmd.CombinedOutput()
		output := string(out)
		wantWarn := Yellow + "⚠ Advertencia: -f solo tiene efecto con -c (ignorado)" + NC
		if !strings.Contains(output, wantWarn) {
			t.Errorf("expected colored warning %q in output, got:\n%s", wantWarn, output)
		}
	})

	t.Run("Error conflict flags has Red color and prefix", func(t *testing.T) {
		cmd := exec.Command(binPath, "-c", "-d", "file.txt")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Error("expected failure on conflicting flags, got exit 0")
		}
		output := string(out)
		wantPrefix := Red + "✗ Error: "
		if !strings.Contains(output, wantPrefix) || !strings.Contains(output, "no se pueden combinar") {
			t.Errorf("expected colored error with %q and 'no se pueden combinar', got:\n%s", wantPrefix, output)
		}
	})

	t.Run("Error missing mode has Red color and prefix", func(t *testing.T) {
		cmd := exec.Command(binPath, "file.txt")
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Error("expected failure on missing mode, got exit 0")
		}
		output := string(out)
		wantPrefix := Red + "✗ Error: "
		if !strings.Contains(output, wantPrefix) || !strings.Contains(output, "debe especificar un modo de operación") {
			t.Errorf("expected colored error with %q and 'debe especificar un modo de operación', got:\n%s", wantPrefix, output)
		}
	})
}

func TestSplitHelpAndFlagDescription(t *testing.T) {
	// 1. Verificar printHelp()
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	os.Stdout = w

	printHelp()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	helpOutput := buf.String()

	foundHelp := false
	expectedDesc := "Dividir en partes de N MB (formatos de flujo: gz, xz, bz2, bz3, zst, lz, lz4, br y tar.*)"
	for _, line := range strings.Split(helpOutput, "\n") {
		if strings.Contains(line, "-s N") && strings.Contains(line, expectedDesc) {
			foundHelp = true
			break
		}
	}
	if !foundHelp {
		t.Errorf("printHelp() does not contain expected -s line with %q, got:\n%s", expectedDesc, helpOutput)
	}

	// 2. Verificar flag -s description en flag usage
	cmd := exec.Command("go", "run", ".", "-invalid-flag-for-test")
	out, _ := cmd.CombinedOutput()
	flagUsage := string(out)

	expectedFlagDesc := "Dividir en partes de N MB (formatos de flujo: gz, xz, bz2, bz3, zst, lz, lz4, br y tar.*)"
	if !strings.Contains(flagUsage, expectedFlagDesc) {
		t.Errorf("crush flag usage does not contain %q, got:\n%s", expectedFlagDesc, flagUsage)
	}
}

func TestSplitAutocompleteScripts(t *testing.T) {
	t.Run("Bash completion conditional -f and -s suggestions", func(t *testing.T) {
		// 1. Must define _crush_completions
		if !strings.Contains(bashCompletion, "_crush_completions") {
			t.Errorf("bashCompletion should define _crush_completions()")
		}
		// 2. Must suggest sizes for -s / --split, and NOT suggest formats for -s
		for _, size := range []string{"10", "50", "100", "500", "1000"} {
			if !strings.Contains(bashCompletion, size) {
				t.Errorf("bashCompletion missing size %q for -s completion", size)
			}
		}
		// 3. Must check COMP_WORDS for -s / --split
		if !strings.Contains(bashCompletion, "COMP_WORDS") || (!strings.Contains(bashCompletion, "-s") && !strings.Contains(bashCompletion, "--split")) {
			t.Errorf("bashCompletion must inspect COMP_WORDS for -s / --split")
		}
		// 4. Must suggest ONLY split_formats when -s is present on -f
		if !strings.Contains(bashCompletion, "split_formats") {
			t.Errorf("bashCompletion should define split_formats with stream formats")
		}

		// Dynamic bash execution test if bash is available
		if bashPath, err := exec.LookPath("bash"); err == nil {
			testScript := `
` + bashCompletion + `
# Test 1: crush -s <TAB> -> should include sizes (10, 50, 100, 500, 1000), but NOT formats (gz, xz, zip, 7z)
COMP_WORDS=("crush" "-s" "")
COMP_CWORD=2
_crush_completions
out_s="${COMPREPLY[*]}"
for expected in 10 50 100 500 1000; do
    if [[ ! " $out_s " =~ " $expected " ]]; then
        echo "FAIL_S_MISSING:$expected"
    fi
done
for forbidden in gz xz bz2 bz3 zst lz lz4 br zip 7z tar rar lrz; do
    if [[ " $out_s " =~ " $forbidden " ]]; then
        echo "FAIL_S_UNEXPECTED_FORMAT:$forbidden"
    fi
done

# Test 2: crush -f <TAB> (no -s) -> should include zip, 7z, tar, rar
COMP_WORDS=("crush" "-f" "")
COMP_CWORD=2
_crush_completions
out_f="${COMPREPLY[*]}"
for expected in zip 7z tar rar gz xz; do
    if [[ ! " $out_f " =~ " $expected " ]]; then
        echo "FAIL_F_MISSING:$expected"
    fi
done

# Test 3: crush -s 10 -f <TAB> -> should ONLY include split formats, NOT zip, 7z, tar, rar
COMP_WORDS=("crush" "-s" "10" "-f" "")
COMP_CWORD=4
_crush_completions
out_split_f="${COMPREPLY[*]}"
for expected in gz xz bz2 bz3 zst lz lz4 br; do
    if [[ ! " $out_split_f " =~ " $expected " ]]; then
        echo "FAIL_SPLIT_F_MISSING:$expected"
    fi
done
for forbidden in zip 7z tar rar lrz; do
    if [[ " $out_split_f " =~ " $forbidden " ]]; then
        echo "FAIL_SPLIT_F_UNEXPECTED:$forbidden"
    fi
done
`
			cmd := exec.Command(bashPath, "-c", testScript)
			out, err := cmd.CombinedOutput()
			if err != nil || len(bytes.TrimSpace(out)) > 0 {
				t.Errorf("bash dynamic completion test failed: %v, output:\n%s", err, string(out))
			}
		}
	})

	t.Run("Zsh completion suggests sizes in MB for -s and filters -f", func(t *testing.T) {
		// Verify definitions
		if !strings.Contains(zshCompletion, "split_formats") {
			t.Errorf("zshCompletion should define split_formats")
		}
		if !strings.Contains(zshCompletion, "split_sizes") {
			t.Errorf("zshCompletion should define split_sizes")
		}
		// Verify sizes have explicit MB specifications
		for _, s := range []string{"10:10 MB", "50:50 MB", "100:100 MB", "500:500 MB", "1000:1000 MB"} {
			if !strings.Contains(zshCompletion, s) {
				t.Errorf("zshCompletion missing size suggestion with MB %q", s)
			}
		}
		// In split state, it must NOT describe formats as choices for -s
		if strings.Contains(zshCompletion, `_describe -t split_formats "formato compatible" split_formats`) {
			t.Errorf("zshCompletion should NOT suggest formats as options for -s")
		}
		// Verify -s completion links to split state
		if !strings.Contains(zshCompletion, "->split") {
			t.Errorf("zshCompletion should transition to ->split state for -s / --split")
		}
		// Verify -f conditional logic when -s is in line
		if !strings.Contains(zshCompletion, "opt_args[-s]") && !strings.Contains(zshCompletion, "-s") {
			t.Errorf("zshCompletion should check for -s / --split when completing -f")
		}
	})

	t.Run("Fish completion suggests sizes in MB for -s and filters -f", func(t *testing.T) {
		// Verify __crush_split_formats definition
		if !strings.Contains(fishCompletion, "__crush_split_formats") {
			t.Errorf("fishCompletion should define __crush_split_formats")
		}
		// Verify -s description and values are sizes, not formats
		for _, size := range []string{"10", "50", "100", "500", "1000"} {
			if !strings.Contains(fishCompletion, size) {
				t.Errorf("fishCompletion -s missing size %q", size)
			}
		}
		// Formats must NOT be suggested for -s
		if strings.Contains(fishCompletion, `-s s -l split -d "Dividir en partes de N MB (formatos de flujo: gz, xz, bz2, bz3, zst, lz, lz4, br)" -xa "10 50 100 500 1000 gz`) {
			t.Errorf("fishCompletion should NOT suggest formats for -s argument")
		}
		// Verify conditional logic for -f when -s is present
		if !strings.Contains(fishCompletion, "__crush_has_split") {
			t.Errorf("fishCompletion should define and use condition for split format filtering on -f")
		}
	})
}

func TestExtractPasswordFlag(t *testing.T) {
	tmpDir := t.TempDir()
	existingFile := filepath.Join(tmpDir, "archivo.txt")
	if err := os.WriteFile(existingFile, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		args         []string
		wantPassword string
		wantPrompt   bool
		wantCleaned  []string
	}{
		{
			name:         "p con valor explicito =",
			args:         []string{"crush", "-c", "-f", "7z", "-p=mypass", existingFile},
			wantPassword: "mypass",
			wantPrompt:   false,
			wantCleaned:  []string{"crush", "-c", "-f", "7z", existingFile},
		},
		{
			name:         "password con valor explicito =",
			args:         []string{"crush", "-c", "-f", "7z", "--password=mypass", existingFile},
			wantPassword: "mypass",
			wantPrompt:   false,
			wantCleaned:  []string{"crush", "-c", "-f", "7z", existingFile},
		},
		{
			name:         "p con password y archivo",
			args:         []string{"crush", "-c", "-f", "7z", "-p", "secretPass", existingFile},
			wantPassword: "secretPass",
			wantPrompt:   false,
			wantCleaned:  []string{"crush", "-c", "-f", "7z", existingFile},
		},
		{
			name:         "p sin argumento (archivo existente sigue)",
			args:         []string{"crush", "-c", "-f", "7z", "-p", existingFile},
			wantPassword: "",
			wantPrompt:   true,
			wantCleaned:  []string{"crush", "-c", "-f", "7z", existingFile},
		},
		{
			name:         "p al final de la linea",
			args:         []string{"crush", "-c", "-f", "7z", existingFile, "-p"},
			wantPassword: "",
			wantPrompt:   true,
			wantCleaned:  []string{"crush", "-c", "-f", "7z", existingFile},
		},
		{
			name:         "combined flag -cp",
			args:         []string{"crush", "-cp", "-f", "7z", existingFile},
			wantPassword: "",
			wantPrompt:   true,
			wantCleaned:  []string{"crush", "-c", "-f", "7z", existingFile},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cleaned, pass, prompt := extractPasswordFlag(tt.args)
			if pass != tt.wantPassword {
				t.Errorf("password got %q, want %q", pass, tt.wantPassword)
			}
			if prompt != tt.wantPrompt {
				t.Errorf("prompt got %v, want %v", prompt, tt.wantPrompt)
			}
			if len(cleaned) != len(tt.wantCleaned) {
				t.Fatalf("cleaned length got %d, want %d: %v", len(cleaned), len(tt.wantCleaned), cleaned)
			}
			for i := range cleaned {
				if cleaned[i] != tt.wantCleaned[i] {
					t.Errorf("cleaned[%d] got %q, want %q", i, cleaned[i], tt.wantCleaned[i])
				}
			}
		})
	}
}

func TestHelpEveryCommandLineHasExplanation(t *testing.T) {
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	os.Stdout = w

	printHelp()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	helpOutput := buf.String()

	lines := strings.Split(helpOutput, "\n")
	var currentSection string
	for lineIdx, line := range lines {
		trimmed := strings.TrimSpace(line)
		var clean []rune
		inEscape := false
		for _, r := range trimmed {
			if r == '\x1b' {
				inEscape = true
				continue
			}
			if inEscape {
				if r == 'm' {
					inEscape = false
				}
				continue
			}
			clean = append(clean, r)
		}
		cleanStr := strings.TrimSpace(string(clean))
		if strings.HasSuffix(cleanStr, ":") {
			currentSection = strings.TrimSuffix(cleanStr, ":")
			continue
		}
		if cleanStr == "" {
			continue
		}
		if currentSection == "Uso" || currentSection == "Ejemplos" {
			if strings.HasPrefix(cleanStr, "crush ") || strings.HasPrefix(cleanStr, "cat ") {
				if !strings.Contains(cleanStr, "#") {
					t.Errorf("Línea %d en sección %q sin explicación: %q", lineIdx+1, currentSection, cleanStr)
				} else {
					parts := strings.SplitN(cleanStr, "#", 2)
					if strings.TrimSpace(parts[1]) == "" {
						t.Errorf("Línea %d en sección %q tiene '#' pero comentario está vacío: %q", lineIdx+1, currentSection, cleanStr)
					}
				}
			}
		}
	}
}

func TestHelpEveryOptionHasDescriptionAndExample(t *testing.T) {
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe failed: %v", err)
	}
	os.Stdout = w

	printHelp()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	io.Copy(&buf, r)
	helpOutput := buf.String()

	// Strip ANSI escape codes
	var clean []rune
	inEscape := false
	for _, r := range helpOutput {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		clean = append(clean, r)
	}
	cleanText := string(clean)

	lines := strings.Split(cleanText, "\n")
	var currentSection string
	var modeOptions []string
	var generalOptions []string
	var exampleCommands []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasSuffix(trimmed, ":") {
			currentSection = strings.TrimSuffix(trimmed, ":")
			continue
		}
		if trimmed == "" {
			continue
		}

		if currentSection == "Opciones de modo" {
			if strings.HasPrefix(trimmed, "-") {
				modeOptions = append(modeOptions, trimmed)
			}
		} else if currentSection == "Opciones generales" {
			if strings.HasPrefix(trimmed, "-") {
				generalOptions = append(generalOptions, trimmed)
			}
		} else if currentSection == "Ejemplos" {
			if strings.HasPrefix(trimmed, "crush ") || strings.HasPrefix(trimmed, "cat ") {
				exampleCommands = append(exampleCommands, trimmed)
			}
		}
	}

	if len(modeOptions) == 0 {
		t.Fatal("No se encontraron opciones de modo en la ayuda")
	}
	if len(generalOptions) == 0 {
		t.Fatal("No se encontraron opciones generales en la ayuda")
	}
	if len(exampleCommands) == 0 {
		t.Fatal("No se encontraron comandos de ejemplo en la ayuda")
	}

	// Required flags that MUST have an example in the Ejemplos section
	requiredFlags := []string{
		"-c", "-d", "-a", "-watch", "-l", "-t", "-verify", "-r", "--bench", "--bench-size",
		"-h", "-f", "-F", "-o", "-n", "-k", "-v", "-force", "-quick", "-s",
		"-hash", "-p", "-opts", "-i", "-C", "-exclude", "-sparse", "-filter", "-tree", "-find", "-diff",
		"--install", "--install-deps", "--uninstall", "--completion", "--version",
	}

	for _, flag := range requiredFlags {
		found := false
		for _, ex := range exampleCommands {
			cmdPart := strings.TrimSpace(strings.SplitN(ex, "#", 2)[0])
			words := strings.Fields(cmdPart)
			for _, w := range words {
				if w == flag || strings.HasPrefix(w, flag+"=") || (strings.HasPrefix(w, flag) && len(flag) > 2) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			t.Errorf("El flag %q no tiene ningún ejemplo de uso en la sección Ejemplos", flag)
		}
	}
}

