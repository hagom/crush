package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// knownShortFlags lists single-dash short flags that can be combined (-tkv).
// Only single-character flags that take no argument belong here.
var knownShortFlags = map[byte]bool{
	'c': true,
	'd': true,
	'l': true,
	't': true,
	'r': true,
	'h': true,
	'k': true,
	'v': true,
	'n': true,
}

var Version = "dev" // set at build time: go build -ldflags="-X main.Version=x.y.z"

// takesValue reports whether a flag token consumes the next argument as its value.
func flagTakesValue(a string) bool {
	switch {
	case a == "-f", a == "-o", a == "-s", a == "-opts":
		return true
	case a == "-exclude" || strings.HasPrefix(a, "-exclude="):
		return true
	case len(a) > 7 && a[:7] == "-exclude":
		return true
	default:
		return false
	}
}

// reorderArgs expands combined short flags (-tkv → -t -k -v) and moves
// all flags before positional arguments so flag.Parse can see them.
// Flag-value pairs (-f 7z) are kept together.
func reorderArgs(args []string) []string {
	if len(args) < 2 {
		return args
	}
	var out []string
	var positional []string
	skip := false
	for i := 1; i < len(args); i++ {
		if skip {
			skip = false
			continue
		}
		a := args[i]
		if len(a) > 2 && a[0] == '-' && a[1] != '-' && !flagTakesValue(a) {
			// Potential combined short flags: -tkv
			allKnown := true
			for j := 1; j < len(a); j++ {
				if !knownShortFlags[a[j]] {
					allKnown = false
					break
				}
			}
			if allKnown && len(a)-1 >= 2 {
				for j := 1; j < len(a); j++ {
					out = append(out, "-"+string(a[j]))
				}
				continue
			}
		}
		if a[0] == '-' {
			out = append(out, a)
			if flagTakesValue(a) && i+1 < len(args) {
				out = append(out, args[i+1])
				skip = true
			}
		} else {
			positional = append(positional, a)
		}
	}
	result := []string{args[0]}
	result = append(result, out...)
	result = append(result, positional...)
	return result
}

func main() {
	// Handle --help / -help / --version before flag.Parse
	for _, arg := range os.Args[1:] {
		if arg == "--help" || arg == "-help" {
			printHelp()
			return
		}
		if arg == "--version" || arg == "-version" {
			fmt.Printf("crush version %s\n", Version)
			return
		}
	}

	// Reorder args: expand combined short flags (-tkv → -t -k -v)
	// and move all flags before positional args so flag.Parse catches them
	os.Args = reorderArgs(os.Args)

	// Detect --completion without value (auto-install mode) before flag.Parse
	completionInstall := ""
	for i := 1; i < len(os.Args); i++ {
		a := os.Args[i]
		if a == "--completion" || a == "-completion" {
			if i+1 < len(os.Args) && !strings.HasPrefix(os.Args[i+1], "-") {
				completionInstall = os.Args[i+1]
				break
			}
			completionInstall = "auto"
			os.Args = append(os.Args[:i], os.Args[i+1:]...)
			break
		}
	}

	// Flags
	compressFlag := flag.Bool("c", false, "Comprimir archivos")
	decompressFlag := flag.Bool("d", false, "Descomprimir archivos")
	listFlag := flag.Bool("l", false, "Listar contenido de archivo comprimido")
	testFlag := flag.Bool("t", false, "Verificar integridad de archivos comprimidos")
	readFlag := flag.Bool("r", false, "Leer contenido de archivo comprimido a stdout")
	helpFlag := flag.Bool("h", false, "Mostrar ayuda")
	installFlag := flag.Bool("install", false, "Instalar crush en el sistema + herramientas faltantes")
	installDepsFlag := flag.Bool("install-deps", false, "Instalar solo herramientas de compresión faltantes")
	uninstallFlag := flag.Bool("uninstall", false, "Desinstalar crush del sistema")
	completionFlag := flag.String("completion", "", "Instalar autocompletado (bash|zsh|fish, o auto-detectar)")

	formatStr := flag.String("f", "", "Formato de compresión (ver -h para lista ordenada por compresión)")
	outputDir := flag.String("o", ".", "Directorio de salida")
	dryRun := flag.Bool("n", false, "Modo simulacro (no ejecutar)")
	keepOrig := flag.Bool("k", false, "Conservar archivos originales")
	verbose := flag.Bool("v", false, "Modo verbose")
	force := flag.Bool("force", false, "Sobrescribir archivos existentes")
	quick := flag.Bool("quick", false, "Verificación rápida (no verificar cada archivo)")
	splitSize := flag.Int("s", 0, "Dividir en partes de N MB (solo compresión)")
	compressionOpts := flag.String("opts", "", "Opciones adicionales para la herramienta de compresión")

	var exclude multiFlag
	flag.Var(&exclude, "exclude", "Patrón de exclusión (repetible)")

	flag.Parse()

	if *completionFlag != "" {
		completionInstall = *completionFlag
	}
	if completionInstall != "" {
		doInstallCompletion(completionInstall)
		return
	}

	defer CloseLog()
	if err := SetupLogging(); err != nil {
		fmt.Fprintf(os.Stderr, "Error configurando logging: %v\n", err)
	}

	// Handle -h / no args
	if *helpFlag || (flag.NFlag() == 0 && flag.NArg() == 0) {
		printHelp()
		return
	}

	// Check mode conflicts among -c, -d, -l, -t, -r
	// Allowed: -c + -t (compress then test), -d + -t (test before decompress)
	// Everything else with >= 2 modes is a conflict
	hasC := *compressFlag
	hasD := *decompressFlag
	hasL := *listFlag
	hasT := *testFlag
	hasR := *readFlag

	writeModes := 0
	for _, m := range []bool{hasC, hasD} {
		if m {
			writeModes++
		}
	}
	readModes := 0
	for _, m := range []bool{hasL, hasR} {
		if m {
			readModes++
		}
	}

	conflict := false
	var conflictFlags []string

	if writeModes > 1 || (writeModes > 0 && readModes > 0) || readModes > 1 || (hasT && (hasL || hasR)) {
		conflict = true
	}

	if conflict {
		for f, name := range map[*bool]string{compressFlag: "-c", decompressFlag: "-d", listFlag: "-l", testFlag: "-t", readFlag: "-r"} {
			if *f {
				conflictFlags = append(conflictFlags, name)
			}
		}
		fmt.Fprintf(os.Stderr, "Error: los flags %s no se pueden combinar\n", strings.Join(conflictFlags, " + "))
		os.Exit(1)
	}

	// Check --install/--install-deps/--uninstall conflicts
	installModeCount := 0
	if *installFlag {
		installModeCount++
	}
	if *installDepsFlag {
		installModeCount++
	}
	if *uninstallFlag {
		installModeCount++
	}
	if installModeCount > 1 {
		fmt.Fprintln(os.Stderr, "Error: --install, --install-deps y --uninstall son mutuamente excluyentes")
		os.Exit(1)
	}
	if installModeCount > 0 {
		for _, m := range []bool{*compressFlag, *decompressFlag, *listFlag, *testFlag, *readFlag} {
			if m {
				fmt.Fprintln(os.Stderr, "Error: --install/--install-deps/--uninstall no puede combinarse con -c, -d, -l, -t, -r")
				os.Exit(1)
			}
		}
	}

	// -f solo tiene sentido con -c
	if *formatStr != "" && !*compressFlag {
		fmt.Fprintln(os.Stderr, "Warning: -f solo tiene efecto con -c (ignorado)")
	}
	// -s solo tiene sentido con -c
	if *splitSize > 0 && !*compressFlag {
		fmt.Fprintln(os.Stderr, "Warning: -s solo tiene efecto con -c (ignorado)")
	}
	// -n solo tiene sentido con -c o -d
	if *dryRun && !*compressFlag && !*decompressFlag {
		fmt.Fprintln(os.Stderr, "Warning: -n solo tiene efecto con -c o -d (ignorado)")
	}

	// Handle --uninstall
	if *uninstallFlag {
		handleUninstall()
		return
	}

	// Handle --install (binary + deps)
	if *installFlag {
		handleInstall()
		return
	}

	// Handle --install-deps (solo deps)
	if *installDepsFlag {
		handleInstallDeps()
		return
	}

	// Detect stdin pipe mode
	stdinIsPipe := false
	if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		stdinIsPipe = true
	}

	// Get files from args or stdin
	var files []string
	if flag.NArg() > 0 {
		files = flag.Args()
	} else if stdinIsPipe && (*compressFlag || *decompressFlag) {
		// Read from stdin pipe
	} else if *compressFlag || *decompressFlag {
		fmt.Fprintln(os.Stderr, "Error: debe especificar archivos como argumentos")
		os.Exit(1)
	}

	// Handle -l (list)
	if *listFlag {
		for _, f := range files {
			file, err := os.Open(f)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error abriendo %s: %v\n", f, err)
				continue
			}
			if err := ListCompressed(file); err != nil {
				fmt.Fprintf(os.Stderr, "Error listando %s: %v\n", f, err)
			}
			file.Close()
		}
		return
	}

	// Handle -r (read)
	if *readFlag {
		for _, f := range files {
			file, err := os.Open(f)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error abriendo %s: %v\n", f, err)
				continue
			}
			data, err := CompressRead(file)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error leyendo %s: %v\n", f, err)
				file.Close()
				continue
			}
			os.Stdout.Write(data)
			file.Close()
		}
		return
	}

	// Handle -t alone (test only)
	if *testFlag && !*compressFlag && !*decompressFlag {
		opts := TestOptions{
			Verbose: *verbose,
			Quick:   *quick,
		}
		if err := DoTest(files, opts); err != nil {
			os.Exit(1)
		}
		return
	}

	// Handle -c (compress), optionally followed by -t (test)
	if *compressFlag {
		if *formatStr == "" {
				fmt.Fprintf(os.Stderr, "Error: debe especificar formato con -f\n")
				fmt.Fprintf(os.Stderr, "Formatos: gz xz bz2 bz3 zst lz lrz zip 7z tar rar lz4 br\n")
			printHelp()
			os.Exit(1)
		}
		format, err := ParseFormat(*formatStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			printHelp()
			os.Exit(1)
		}

		parallel := NCPU()
		if parallel < 2 {
			parallel = 2
		}
		// When -c -t, defer deletion until after the test to prevent data loss
		skipCleanup := *testFlag && !*keepOrig
		opts := CompressOptions{
			Format:          format,
			DryRun:          *dryRun,
			Verbose:         *verbose,
			OutputDir:       *outputDir,
			SplitSize:       *splitSize,
			KeepOrig:        *keepOrig || skipCleanup,
			Parallel:        parallel,
			CompressionOpts: *compressionOpts,
			Exclude:         exclude,
		}
		var outPath string
		if stdinIsPipe {
			if err := compressStream(os.Stdin, os.Stdout, opts); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		} else {
			outPath, err = DoCompress(files, opts)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}
		if *testFlag && outPath != "" {
			WriteLogf("\n%sVerificando integridad del archivo comprimido...%s\n", Bold, NC)
			if err := DoTest([]string{outPath}, TestOptions{Verbose: *verbose, Quick: *quick}); err != nil {
				os.Exit(1)
			}
			// Test passed — now delete originals if user didn't request -k
			if skipCleanup && len(CompressCleanupFiles) > 0 {
				removed := removeFiles(CompressCleanupFiles, *verbose)
				if removed > 0 {
					WriteLogf("  %sArchivos originales eliminados: %d%s\n", Yellow, removed, NC)
				}
			}
		}
		return
	}

	// Handle -d (decompress), optionally preceded by -t (test)
	if *decompressFlag {
		if *testFlag {
			WriteLogf("%sVerificando integridad antes de descomprimir...%s\n", Bold, NC)
			if err := DoTest(files, TestOptions{Verbose: *verbose, Quick: *quick}); err != nil {
				os.Exit(1)
			}
			WriteLogf("%s✓ Integridad verificada, descomprimiendo...%s\n\n", Green, NC)
		}

		opts := DecompressOptions{
			DryRun:    *dryRun,
			Verbose:   *verbose,
			OutputDir: *outputDir,
			KeepOrig:  *keepOrig,
			Force:     *force,
		}
		if stdinIsPipe {
			if *formatStr == "" && len(files) == 0 {
				fmt.Fprintln(os.Stderr, "Error: modo pipe requiere -f FORMATO (ej: -f gz)")
				os.Exit(1)
			}
			if len(files) == 0 {
				f, _ := ParseFormat(*formatStr)
				info := FormatInfoFromFormat(f)
				if err := decompressStream(os.Stdin, os.Stdout, info); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(1)
				}
			} else {
				if err := DoDecompress(files, opts); err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(1)
				}
			}
		} else {
			if err := DoDecompress(files, opts); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}
		return
	}

	// If we get here, no mode flag was specified
	fmt.Fprintln(os.Stderr, "Error: debe especificar un modo de operación (-c, -d, -l, -t, -r)")
	printHelp()
	os.Exit(1)
}

func installBinary() error {
	src, err := os.Executable()
	if err != nil {
		return fmt.Errorf("error obteniendo ruta del binario: %w", err)
	}
	src, err = filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("error resolviendo ruta: %w", err)
	}

	dest := "/usr/local/bin/crush"

	// Try direct copy
	if err := copyFile(src, dest); err == nil {
		WriteLogf("  %s✓ Binario instalado en %s%s\n", Green, dest, NC)
		return nil
	}

	// Fallback 1: sudo install
	if hasTool("sudo") {
		cmd := exec.Command("sudo", "install", "-m", "755", src, dest)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			WriteLogf("  %s✓ Binario instalado en %s%s\n", Green, dest, NC)
			return nil
		}
	}

	// Fallback 2: pkexec install
	if hasTool("pkexec") {
		cmd := exec.Command("pkexec", "install", "-m", "755", src, dest)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			WriteLogf("  %s✓ Binario instalado en %s%s\n", Green, dest, NC)
			return nil
		}
	}

	return fmt.Errorf("no se pudo instalar en %s (intente con sudo manualmente)", dest)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Chmod(0755); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

func handleInstall() {
	WriteLogf("%sInstalando crush en el sistema...%s\n", Blue, NC)

	if err := installBinary(); err != nil {
		fmt.Fprintf(os.Stderr, "%sError: %v%s\n", Red, err, NC)
		os.Exit(1)
	}

	handleInstallDeps()
}

func handleInstallDeps() {
	var neededTools []string
	allTools := []string{"pigz", "xz", "lbzip2", "pbzip2", "bzip3", "zstd", "plzip",
		"lrzip", "zip", "unzip", "p7zip", "rar", "tar", "numfmt", "pv", "getconf"}

	for _, tool := range allTools {
		if !hasTool(tool) {
			neededTools = append(neededTools, tool)
		}
	}

	if len(neededTools) == 0 {
		WriteLogf("  %s✓ Todas las herramientas están instaladas.%s\n", Green, NC)
		return
	}

	mgr := DetectPkgManager()
	if mgr == nil {
		fmt.Fprintln(os.Stderr, "Error: No se pudo detectar el gestor de paquetes")
		os.Exit(1)
	}

	WriteLogf("  Detectado gestor de paquetes: %s\n", mgr.Name)
	remaining := InstallMissingDeps(neededTools, mgr)
	if remaining != nil {
		fmt.Fprintf(os.Stderr, "%sError: No se pudieron instalar: %v%s\n", Red, remaining, NC)
		os.Exit(1)
	}
}

func doInstallCompletion(shell string) {
	if shell == "auto" {
		shellPath := os.Getenv("SHELL")
		switch {
		case strings.HasSuffix(shellPath, "/bash"):
			shell = "bash"
		case strings.HasSuffix(shellPath, "/zsh"):
			shell = "zsh"
		case strings.HasSuffix(shellPath, "/fish"):
			shell = "fish"
		default:
			fmt.Fprintf(os.Stderr, "Error: no se pudo detectar shell desde $SHELL (%s). Use: crush --completion bash|zsh|fish\n", shellPath)
			os.Exit(1)
		}
	}

	var script, dest string
	switch shell {
	case "bash":
		script = bashCompletion
		dest = "/etc/bash_completion.d/crush"
	case "zsh":
		script = zshCompletion
		dest = "/usr/local/share/zsh/site-functions/_crush"
		if _, err := os.Stat(filepath.Dir(dest)); os.IsNotExist(err) {
			dest = "/usr/share/zsh/site-functions/_crush"
		}
	case "fish":
		script = fishCompletion
		dest = "/etc/fish/completions/crush.fish"
	default:
		fmt.Fprintf(os.Stderr, "Error: shell no soportada: %s (use bash, zsh o fish)\n", shell)
		os.Exit(1)
	}

	WriteLogf("%sInstalando completado para %s...%s\n", Blue, shell, NC)

	tmpFile, err := os.CreateTemp("", "crush-completion-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "%sError: no se pudo crear archivo temporal%s\n", Red, NC)
		os.Exit(1)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.WriteString(script); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "%sError escribiendo archivo temporal%s\n", Red, NC)
		os.Exit(1)
	}
	tmpFile.Close()

	// Ensure parent directory exists
	parentDir := filepath.Dir(dest)
	cmd := exec.Command("sudo", "mkdir", "-p", parentDir)
	cmd.Stderr = os.Stderr
	cmd.Run()

	cmd = exec.Command("sudo", "install", "-m", "644", tmpPath, dest)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmpPath)
		fmt.Fprintf(os.Stderr, "%sError: no se pudo instalar completado en %s%s\n", Red, dest, NC)
		os.Exit(1)
	}
	os.Remove(tmpPath)
	WriteLogf("  %s✓ Autocompletado para %s instalado en %s%s\n", Green, shell, dest, NC)
}

func handleUninstall() {
	dest := "/usr/local/bin/crush"

	if _, err := os.Stat(dest); os.IsNotExist(err) {
		WriteLogf("  %s✗ crush no está instalado en %s%s\n", Yellow, dest, NC)
		return
	}

	// Try direct remove
	if err := os.Remove(dest); err == nil {
		WriteLogf("  %s✓ crush desinstalado de %s%s\n", Green, dest, NC)
		return
	}

	// Fallback 1: sudo
	if hasTool("sudo") {
		cmd := exec.Command("sudo", "rm", "-f", dest)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			WriteLogf("  %s✓ crush desinstalado de %s%s\n", Green, dest, NC)
			return
		}
	}

	// Fallback 2: pkexec
	if hasTool("pkexec") {
		cmd := exec.Command("pkexec", "rm", "-f", dest)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			WriteLogf("  %s✓ crush desinstalado de %s%s\n", Green, dest, NC)
			return
		}
	}

	fmt.Fprintf(os.Stderr, "%sError: no se pudo desinstalar (intente con sudo manualmente)%s\n", Red, NC)
	os.Exit(1)
}

func printHelp() {
	w := func(c, s string) { fmt.Print(c, s, NC) }

	w(BoldBlue, "CRUSH  Herramienta multi-formato de compresión y descompresión\n\n")
	w(BoldBlue, "Uso:\n")
	w(Yellow, "  crush -c -f FORMATO [opciones] archivo...\n")
	w(Yellow, "  crush -d [opciones] archivo...\n")
	w(Yellow, "  crush -l archivo...\n")
	w(Yellow, "  crush -t archivo...\n")
	w(Yellow, "  crush -r archivo...\n")
	w(Yellow, "  crush --install\n")
	w(Yellow, "  crush --install-deps\n")
	w(Yellow, "  crush --uninstall\n\n")
	w(BoldBlue, "Opciones de modo:\n")
	w(Yellow, "  -c")
	fmt.Print("                   Comprimir archivos\n")
	w(Yellow, "  -d")
	fmt.Print("                   Descomprimir archivos\n")
	w(Yellow, "  -l")
	fmt.Print("                   Listar contenido de archivo comprimido\n")
	w(Yellow, "  -t")
	fmt.Print("                   Verificar integridad de archivos comprimidos\n")
	w(Yellow, "  -r")
	fmt.Print("                   Leer contenido de archivo comprimido a stdout\n")
	w(Yellow, "  -h")
	fmt.Print("                   Mostrar esta ayuda\n\n")
	w(BoldBlue, "Opciones generales:\n")

	// Build format list ordered by compression ratio
	var ordered []string
	for _, f := range FormatsByCompression {
		ordered = append(ordered, f.String())
	}
	fmt.Print("  -f FORMATO           Formato de compresión: ")
	fmt.Print(strings.Join(ordered, ", "))
	fmt.Print("\n                       lrz ofrece la máxima compresión\n")

	w(Yellow, "  -o DIRECTORIO")
	fmt.Print("        Directorio de salida (por defecto: .)\n")
	w(Yellow, "  -n")
	fmt.Print("                   Modo simulacro (dry-run)\n")
	w(Yellow, "  -k")
	fmt.Print("                   Conservar archivos originales\n")
	w(Yellow, "  -v")
	fmt.Print("                   Modo verbose\n")
	w(Yellow, "  -force")
	fmt.Print("               Sobrescribir archivos existentes\n")
	w(Yellow, "  -quick")
	fmt.Print("               Verificación rápida (no verificar cada archivo individualmente)\n")
	w(Yellow, "  -s N")
	fmt.Print("                 Dividir en partes de N MB (solo compresión)\n")
	w(Yellow, "  -opts \"opciones\"")
	fmt.Print("     Opciones adicionales para la herramienta de compresión\n")
	w(Yellow, "  -exclude patrón")
	fmt.Print("      Patrón de exclusión (se puede repetir)\n")
	w(Yellow, "  --install")
	fmt.Print("            Instalar crush en el sistema + herramientas faltantes\n")
	w(Yellow, "  --install-deps")
	fmt.Print("        Instalar solo herramientas de compresión faltantes\n")
	w(Yellow, "  --uninstall")
	fmt.Print("          Desinstalar crush del sistema\n")
	w(Yellow, "  --completion")
	fmt.Print("       Instalar autocompletado para la shell actual\n")
	w(Yellow, "  --completion bash|zsh|fish")
	fmt.Print("\n")
	fmt.Print("                       Instalar autocompletado para una shell específica\n\n")
	w(BoldBlue, "Ejemplos:\n")
	w(Yellow, "  crush -c -f gz documento.txt\n")
	w(Yellow, "  crush -c -f gz -t documento.txt                       # comprimir y verificar integridad\n")
	w(Yellow, "  crush -c -f zst -v archivo.tar                          # multihilo auto con todos los núcleos\n")
	w(Yellow, "  crush -c -f xz -k documento.txt                       # conservar original con barra de progreso\n")
	w(Yellow, "  crush -c -f zip -exclude \"*.bak\" dir/                  # comprimir excluyendo archivos .bak\n")
	w(Yellow, "  crush -c -f tar.gz -o /backup/ dir/                   # comprimir directorio a ubicación específica\n")
	w(Yellow, "  crush -c -f zst -s 10 archivo_grande.iso              # dividir en partes de 10 MB\n")
	w(Yellow, "  crush -d archivo.tar.gz\n")
	w(Yellow, "  crush -d -t archivo.7z                                 # testear antes de descomprimir\n")
	w(Yellow, "  crush -d -o /tmp/ archivo.zip\n")
	w(Yellow, "  crush -d -force archivo.7z                             # sobrescribir archivos existentes\n")
	w(Yellow, "  crush -t *.tar.gz\n")
	w(Yellow, "  crush -l archivo.7z\n")
	w(Yellow, "  crush -l *.7z                                           # listar múltiples archivos\n")
	w(Yellow, "  crush -r archivo.txt.gz | grep error                   # leer y filtrar contenido comprimido\n")
	w(Yellow, "  crush --install\n")
	w(Yellow, "  crush --install-deps\n")
	w(Yellow, "  crush --uninstall\n")
	w(Yellow, "  crush --completion                                      # instalar autocompletado (auto-detectar shell)\n")
	w(Yellow, "  cat archivo.txt | crush -c -f gz > archivo.txt.gz       # compresión desde stdin\n")
	w(Yellow, "  cat archivo.txt.gz | crush -d -f gz > archivo.txt      # descompresión desde stdin\n")
	w(Yellow, "  crush --version                                         # mostrar versión\n")
}

// multiFlag implements flag.Value for repeated string flags
type multiFlag []string

func (m *multiFlag) String() string {
	if m == nil {
		return ""
	}
	return strings.Join(*m, ", ")
}

func (m *multiFlag) Set(value string) error {
	*m = append(*m, value)
	return nil
}

const bashCompletion = `# bash completion for crush
_crush() {
    local cur prev words cword
    _init_completion || return

    local formats="gz xz bz2 bz3 zst lz lrz zip 7z rar lz4 br tar"
    local short="-c -d -l -t -r -h -v -k -n -f -o -s -force -quick -opts -exclude"
    local long="--compress --decompress --list --test --read --help --verbose --keep --dry-run --format --output --split --opts --exclude --force --quick --install --install-deps --uninstall --completion --version"

    case "${prev}" in
        -f|--format)
            COMPREPLY=( $(compgen -W "${formats}" -- "${cur}") )
            return 0
            ;;
        --completion)
            COMPREPLY=( $(compgen -W "bash zsh fish" -- "${cur}") )
            return 0
            ;;
        -o|--output|-opts|-exclude)
            COMPREPLY=( $(compgen -f -- "${cur}") )
            return 0
            ;;
        -s|--split)
            COMPREPLY=()
            return 0
            ;;
    esac

    if [[ ${cur} == --* ]]; then
        COMPREPLY=( $(compgen -W "${long}" -- "${cur}") )
    elif [[ ${cur} == -* ]]; then
        COMPREPLY=( $(compgen -W "${short} ${long}" -- "${cur}") )
    else
        _filedir
    fi
} && complete -F _crush crush
`

const zshCompletion = `#compdef crush

_crush() {
    local -a formats
    formats=(
        'gz:Gzip'
        'xz:XZ'
        'bz2:Bzip2'
        'bz3:Bzip3'
        'zst:Zstd'
        'lz:Lzip'
        'lrz:Lrzip'
        'zip:Zip'
        '7z:7-Zip'
        'rar:RAR'
        'lz4:LZ4'
        'br:Brotli'
        'tar:Tar'
    )

    _arguments \
        '(-c -d -l -t -r)'{-c,--compress}'[Comprimir archivos]' \
        '(-c -d -l -t -r)'{-d,--decompress}'[Descomprimir archivos]' \
        '(-c -d -l -t -r)'{-l,--list}'[Listar contenido]' \
        '(-c -d -l -t -r)'{-t,--test}'[Verificar integridad]' \
        '(-c -d -l -t -r)'{-r,--read}'[Leer contenido a stdout]' \
        '(-h --help)'{-h,--help}'[Mostrar ayuda]' \
        '--install[Instalar crush + dependencias]' \
        '--install-deps[Instalar solo dependencias]' \
        '--uninstall[Desinstalar crush]' \
        '--version[Mostrar versión]' \
        '--completion[Generar autocompletado]:shell:(bash zsh fish)' \
        {-f,--format}'[Formato de compresión]:formato:->formats' \
        {-o,--output}'[Directorio de salida]:directorio:_files -/' \
        '--force[Sobrescribir existentes]' \
        '--quick[Verificación rápida]' \
        {-v,--verbose}'[Modo verbose]' \
        {-k,--keep}'[Conservar originales]' \
        {-n,--dry-run}'[Modo simulacro]' \
        {-s,--split}'[Dividir en partes MB]:tamaño:' \
        '--opts[Opciones adicionales]:opciones:' \
        '--exclude[Patrón de exclusión]:patrón:' \
        '*:archivo:_files'

    case "$state" in
        formats)
            _describe -t formats "formato" formats
            ;;
    esac
}

_crush "$@"
`

const fishCompletion = `# fish completion for crush

function __crush_formats
    echo gz xz bz2 bz3 zst lz lrz zip 7z rar lz4 br tar
end

# Mode flags (mutually exclusive group)
complete -c crush -n "not __fish_seen_subcommand_from -c -d -l -t -r" -s c -d "Comprimir archivos"
complete -c crush -n "not __fish_seen_subcommand_from -c -d -l -t -r" -s d -d "Descomprimir archivos"
complete -c crush -n "not __fish_seen_subcommand_from -c -d -l -t -r" -s l -d "Listar contenido"
complete -c crush -n "not __fish_seen_subcommand_from -c -d -l -t -r" -s t -d "Verificar integridad"
complete -c crush -n "not __fish_seen_subcommand_from -c -d -l -t -r" -s r -d "Leer contenido a stdout"

# General flags
complete -c crush -s f -d "Formato de compresión" -xa "(__crush_formats)"
complete -c crush -s o -d "Directorio de salida" -xa "(__fish_complete_directories)"
complete -c crush -s force -l force -d "Sobrescribir existentes"
complete -c crush -s quick -l quick -d "Verificación rápida"
complete -c crush -s v -d "Modo verbose"
complete -c crush -s k -d "Conservar originales"
complete -c crush -s n -d "Modo simulacro"
complete -c crush -s s -d "Dividir en partes de N MB"
complete -c crush -s opts -l opts -d "Opciones adicionales"
complete -c crush -s exclude -l exclude -d "Patrón de exclusión" -r
complete -c crush -s install -l install -d "Instalar crush + dependencias"
complete -c crush -s install-deps -l install-deps -d "Instalar solo dependencias"
complete -c crush -s uninstall -l uninstall -d "Desinstalar crush"
complete -c crush -s completion -l completion -d "Generar autocompletado" -xa "bash zsh fish"
complete -c crush -s version -l version -d "Mostrar versión"

# Positional args: files
complete -c crush -f -a "(__fish_complete_files)"
`
