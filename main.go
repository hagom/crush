package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
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
	'C': true,
	'S': true,
	'a': true,
	'u': true,
}

var Version = "dev" // set at build time: go build -ldflags="-X main.Version=x.y.z"

// takesValue reports whether a flag token consumes the next argument as its value.
func flagTakesValue(a string) bool {
	switch {
	case a == "-f", a == "-F", a == "-formats", a == "--formats", a == "-o", a == "-s", a == "-opts", a == "-i", a == "-completion", a == "--completion", a == "-filter", a == "--filter":
		return true
	case a == "-bench-size" || a == "--bench-size":
		return true
	case a == "-watch" || a == "--watch":
		return true
	case a == "-exclude" || strings.HasPrefix(a, "-exclude="):
		return true
	case len(a) > 7 && a[:7] == "-exclude":
		return true
	default:
		return false
	}
}

func extractPasswordFlag(args []string) ([]string, string, bool) {
	if len(args) == 0 {
		return args, "", false
	}
	var cleaned []string
	cleaned = append(cleaned, args[0])
	var password string
	var prompt bool

	hasFromFile := false
	for _, a := range args {
		if a == "-i" {
			hasFromFile = true
			break
		}
	}

	for i := 1; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-p=") || strings.HasPrefix(a, "-password=") || strings.HasPrefix(a, "--password=") {
			idx := strings.Index(a, "=")
			password = a[idx+1:]
			continue
		}
		if a == "-password" || a == "--password" || a == "-p" {
			if i+1 < len(args) {
				next := args[i+1]
				if strings.HasPrefix(next, "-") {
					prompt = true
					continue
				}
				hasMoreNonFlags := false
				for j := i + 2; j < len(args); j++ {
					if !strings.HasPrefix(args[j], "-") {
						hasMoreNonFlags = true
						break
					}
				}
				_, statErr := os.Stat(next)
				if statErr != nil || hasMoreNonFlags || hasFromFile {
					password = next
					i++
					continue
				}
				prompt = true
				continue
			} else {
				prompt = true
				continue
			}
		}
		if len(a) > 2 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(a[1:], 'p') {
			allShort := true
			for j := 1; j < len(a); j++ {
				if a[j] != 'p' && !knownShortFlags[a[j]] {
					allShort = false
					break
				}
			}
			if allShort {
				withoutP := "-" + strings.ReplaceAll(a[1:], "p", "")
				if withoutP != "-" {
					cleaned = append(cleaned, withoutP)
				}
				prompt = true
				continue
			}
		}
		cleaned = append(cleaned, a)
	}
	return cleaned, password, prompt
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

	var cliPassword string
	var promptPassword bool
	os.Args, cliPassword, promptPassword = extractPasswordFlag(os.Args)

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
	addFlag := flag.Bool("a", false, "Agregar archivos o carpetas a un archivo comprimido existente")
	addLongFlag := flag.Bool("add", false, "Agregar archivos o carpetas a un archivo comprimido existente (alias de -a)")
	updateFlag := flag.Bool("u", false, "Agregar o actualizar archivos en un archivo comprimido existente (alias de -a)")
	updateLongFlag := flag.Bool("update", false, "Agregar o actualizar archivos en un archivo comprimido existente (alias de -a)")
	listFlag := flag.Bool("l", false, "Listar contenido de archivo comprimido")
	testFlag := flag.Bool("t", false, "Verificar integridad de archivos comprimidos")
	readFlag := flag.Bool("r", false, "Leer contenido de archivo comprimido a stdout")
	helpFlag := flag.Bool("h", false, "Mostrar ayuda")
	installFlag := flag.Bool("install", false, "Instalar binario crush en /usr/local/bin")
	installDepsFlag := flag.Bool("install-deps", false, "Instalar solo herramientas de compresión faltantes")
	uninstallFlag := flag.Bool("uninstall", false, "Desinstalar crush del sistema")
	completionFlag := flag.String("completion", "", "Instalar autocompletado (bash|zsh|fish, o auto-detectar)")
	benchFlag := flag.Bool("bench", false, "Ejecutar benchmark de formatos de compresión")
	benchSizeFlag := flag.Int("bench-size", 10, "Tamaño en MB del dataset para benchmark (por defecto: 10)")

	formatStr := flag.String("f", "", "Formato de compresión (ver -h para lista ordenada por compresión)")
	formatsMulti := flag.String("F", "", "Comprimir en múltiples formatos separados por coma (ej: gz,xz,zst)")
	formatsMultiLong := flag.String("formats", "", "Comprimir en múltiples formatos separados por coma (alias de -F)")
	outputDir := flag.String("o", ".", "Directorio de salida")
	dryRun := flag.Bool("n", false, "Modo simulacro (no ejecutar)")
	keepOrig := flag.Bool("k", false, "Conservar archivos originales")
	verbose := flag.Bool("v", false, "Modo verbose")
	force := flag.Bool("force", false, "Sobrescribir archivos existentes")
	quick := flag.Bool("quick", false, "Verificación rápida (no verificar cada archivo)")
	combineFlag := flag.Bool("C", false, "Combinar múltiples archivos en un solo archivo comprimido")
	fromFile := flag.String("i", "", "Leer lista de archivos desde fichero")
	var splitSizeVal int
	flag.IntVar(&splitSizeVal, "s", 0, "Dividir en partes de N MB (formatos de flujo: gz, xz, bz2, bz3, zst, lz, lz4, br y tar.*)")
	splitSize := &splitSizeVal
	compressionOpts := flag.String("opts", "", "Opciones adicionales para la herramienta de compresión")
	watchDir := flag.String("watch", "", "Monitorear directorio para procesar archivos nuevos continuamente")

	hashFlag := flag.Bool("hash", false, "Generar archivo de checksum SHA-256 (.sha256)")
	verifyFlag := flag.Bool("verify", false, "Verificar checksum SHA-256 si existe archivo .sha256")

	var exclude multiFlag
	flag.Var(&exclude, "exclude", "Patrón de exclusión (repetible)")

	sparseFlag := flag.Bool("sparse", false, "Soporte para archivos dispersos (sparse) en tar (activo por omisión)")
	sparseShortFlag := flag.Bool("S", false, "Soporte para archivos dispersos (sparse) en tar (activo por omisión, alias de -sparse)")
	noSparseFlag := flag.Bool("no-sparse", false, "Desactivar soporte para archivos dispersos (sparse) en tar")
	filterFlag := flag.String("filter", "", "Filtro de extracción selectiva por patrón")
	treeFlag := flag.Bool("tree", false, "Mostrar vista en árbol jerárquica del contenido del archivo comprimido")
	findFlag := flag.String("find", "", "Buscar archivos por patrón dentro de uno o más archivos comprimidos")
	diffFlag := flag.Bool("diff", false, "Comparar diferencias de contenido entre dos archivos comprimidos")
	cleanFlag := flag.Bool("clean", false, "Modo sanitización: excluir automáticamente temporales, dependencias y basura de SO")

	flag.Parse()

	sparseEnabled := !*noSparseFlag
	if *sparseFlag || *sparseShortFlag {
		sparseEnabled = true
	}
	if *noSparseFlag {
		sparseEnabled = false
	}

	if promptPassword && cliPassword == "" {
		p, err := readPasswordFunc("Ingrese contraseña: ")
		if err != nil {
			WriteError("leyendo contraseña: %v", err)
			os.Exit(1)
		}
		cliPassword = p
	}

	outDirSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "o" {
			outDirSet = true
		}
	})

	// Detect stdin pipe mode (needed before -f/-n warnings)
	stdinIsPipe := false
	if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		stdinIsPipe = true
	}

	if *completionFlag != "" {
		completionInstall = *completionFlag
	}
	if completionInstall != "" {
		doInstallCompletion(completionInstall)
		return
	}

	defer CloseLog()
	if err := SetupLogging(); err != nil {
		WriteWarning("configurando logging: %v", err)
	}

	// Handle -h / no args
	if *helpFlag || (flag.NFlag() == 0 && flag.NArg() == 0) {
		printHelp()
		return
	}

	// Check mode conflicts among -c, -d, -l, -t, -r, -verify
	// Allowed: -c + -t (compress then test), -d + -t (test before decompress)
	// Everything else with >= 2 modes is a conflict
	isAddMode := *addFlag || *addLongFlag || *updateFlag || *updateLongFlag
	hasC := *compressFlag
	hasD := *decompressFlag
	hasA := isAddMode
	hasL := *listFlag
	hasT := *testFlag || *verifyFlag
	hasR := *readFlag

	writeModes := 0
	for _, m := range []bool{hasC, hasD, hasA} {
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

	if writeModes > 1 || (writeModes > 0 && readModes > 0) || readModes > 1 || (hasT && (hasL || hasR || hasA)) {
		conflict = true
	}

	if conflict {
		for _, f := range []struct {
			v    bool
			name string
		}{
			{hasC, "-c"}, {hasD, "-d"}, {hasA, "-a"}, {hasL, "-l"}, {*testFlag, "-t"}, {*verifyFlag, "-verify"}, {hasR, "-r"},
		} {
			if f.v {
				conflictFlags = append(conflictFlags, f.name)
			}
		}
		WriteError("los flags %s no se pueden combinar", strings.Join(conflictFlags, " + "))
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
		WriteError("--install, --install-deps y --uninstall son mutuamente excluyentes")
		os.Exit(1)
	}
	if installModeCount > 0 {
		for _, m := range []bool{hasC, hasD, hasA, hasL, *testFlag, *verifyFlag, hasR, *benchFlag, *watchDir != ""} {
			if m {
				WriteError("--install/--install-deps/--uninstall no puede combinarse con -c, -d, -a, -l, -t, -verify, -r, --bench o -watch")
				os.Exit(1)
			}
		}
	}

	// Check --bench conflicts with operation modes
	if *benchFlag {
		if *watchDir != "" {
			WriteError("--bench no se puede combinar con -watch")
			os.Exit(1)
		}
		for _, m := range []struct {
			v    bool
			name string
		}{
			{hasC, "-c"}, {hasD, "-d"}, {hasA, "-a"}, {hasL, "-l"}, {*testFlag, "-t"}, {*verifyFlag, "-verify"}, {hasR, "-r"},
		} {
			if m.v {
				WriteError("--bench no se puede combinar con %s", m.name)
				os.Exit(1)
			}
		}
	}

	// Validations for -watch
	if *watchDir != "" {
		if hasA {
			WriteError("-watch no se puede combinar con -a o -u")
			os.Exit(1)
		}
		if !*compressFlag && !*decompressFlag {
			WriteError("-watch requiere -c (comprimir) o -d (descomprimir)")
			os.Exit(1)
		}
		fi, err := os.Stat(*watchDir)
		if err != nil || !fi.IsDir() {
			WriteError("directorio de observación no válido: %s", *watchDir)
			os.Exit(1)
		}
		if *compressFlag && *formatStr == "" {
			WriteError("debe especificar formato con -f")
			WriteInfo("Formatos: gz xz bz2 bz3 zst lz lrz zip 7z tar rar lz4 br")
			printHelp()
			os.Exit(1)
		}
	}

	// -f solo tiene sentido con -c (excepto en modo pipe stdin)
	if *formatStr != "" && !*compressFlag && !stdinIsPipe {
		WriteWarning("-f solo tiene efecto con -c (ignorado)")
	}
	// -F solo tiene sentido con -c (excepto en modo pipe stdin)
	if (*formatsMulti != "" || *formatsMultiLong != "") && !*compressFlag && !stdinIsPipe {
		WriteWarning("-F solo tiene efecto con -c (ignorado)")
	}
	// -s solo tiene sentido con -c
	if *splitSize > 0 && !*compressFlag {
		WriteWarning("-s solo tiene efecto con -c (ignorado)")
	}
	// -n solo tiene sentido con -c o -d
	if *dryRun && !*compressFlag && !*decompressFlag {
		WriteWarning("-n solo tiene efecto con -c o -d (ignorado)")
	}
	// -C requiere -o
	if *combineFlag && !outDirSet {
		WriteError("-C requiere -o DIRECTORIO")
		os.Exit(1)
	}

	// Handle --uninstall
	if *uninstallFlag {
		handleUninstall()
		return
	}

	// Handle --install (solo binario en /usr/local/bin)
	if *installFlag {
		handleInstall()
		return
	}

	// Handle --install-deps (solo deps)
	if *installDepsFlag {
		handleInstallDeps()
		return
	}

	// Get files from args, fromFile, or stdin
	var files []string
	if flag.NArg() > 0 {
		files = flag.Args()
	}
	if len(files) == 0 && *fromFile != "" {
		lines, err := ReadFileLines(*fromFile)
		if err != nil {
			WriteError("leyendo archivo de lista %s: %v", *fromFile, err)
			os.Exit(1)
		}
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" {
				if strings.HasPrefix(line, "/") {
					files = append(files, line)
				} else {
					files = append(files, filepath.Join(filepath.Dir(*fromFile), line))
				}
			}
		}
	}
	opMode := *compressFlag || *decompressFlag || hasA || *listFlag || *readFlag || *testFlag || *verifyFlag
	if *watchDir != "" {
		// Modo watcher: los archivos se procesan según se detectan en el directorio
	} else if len(files) == 0 && stdinIsPipe && *formatStr != "" && (*compressFlag || *decompressFlag) {
		// Read from stdin pipe
	} else if len(files) == 0 && *decompressFlag {
		found, err := FindDecompressibleFiles(".")
		if err != nil {
			WriteError("buscando archivos comprimidos: %v", err)
			os.Exit(1)
		}
		if len(found) == 0 {
			WriteInfo("No se encontraron archivos comprimidos en el directorio actual.")
			return
		}
		confirmed, err := PromptDecompressAll(os.Stdin, os.Stdout, found)
		if err != nil || !confirmed {
			if !confirmed {
				WriteInfo("Operación cancelada.")
			}
			return
		}
		fmt.Println()
		files = found
	} else if len(files) == 0 && *compressFlag {
		found, err := FindCompressibleFiles(".")
		if err != nil {
			WriteError("buscando archivos para comprimir: %v", err)
			os.Exit(1)
		}
		if len(found) == 0 {
			WriteInfo("No se encontraron archivos o directorios para comprimir en el directorio actual.")
			return
		}
		fmtChoice := *formatStr
		if *formatsMulti != "" {
			fmtChoice = *formatsMulti
		} else if *formatsMultiLong != "" {
			fmtChoice = *formatsMultiLong
		}
		if fmtChoice == "" {
			fmtChoice = "gz"
			*formatStr = "gz"
		}
		confirmed, err := PromptCompressAll(os.Stdin, os.Stdout, found, fmtChoice)
		if err != nil || !confirmed {
			if !confirmed {
				WriteInfo("Operación cancelada.")
			}
			return
		}
		fmt.Println()
		files = found
	} else if len(files) == 0 && opMode {
		WriteError("debe especificar archivos como argumentos o con -i")
		os.Exit(1)
	}

	lockSc := lockScope{
		Compress:    hasC,
		Decompress:  hasD,
		Add:         hasA,
		Test:        hasT,
		Bench:       *benchFlag,
		Watch:       *watchDir != "",
		List:        hasL,
		Read:        hasR,
		Tree:        *treeFlag,
		Find:        *findFlag != "",
		Diff:        *diffFlag,
		StdinStream: len(files) == 0 && stdinIsPipe && *formatStr != "" && (hasC || hasD),
	}
	if lockSc.NeedsLock() {
		release, err := AcquireLock()
		if err != nil {
			WriteError("%v", err)
			os.Exit(1)
		}
		defer release()
	}

	// Handle --bench
	if *benchFlag {
		customFile := ""
		if len(files) > 0 {
			customFile = files[0]
		}
		if err := DoBench(customFile, *benchSizeFlag); err != nil {
			WriteError("en benchmark: %v", err)
			os.Exit(1)
		}
		return
	}

	// Handle -a / -u (append/update to existing archive)
	if hasA {
		if len(files) < 2 {
			WriteError("debe especificar el archivo comprimido destino y al menos un archivo o carpeta para agregar\nUso: crush -a ARCHIVO_COMPRIMIDO elemento...")
			os.Exit(1)
		}
		targetArchive := files[0]
		itemsToAdd := files[1:]
		effectiveThreads := NCPU()
		opts := CompressOptions{
			Password:        cliPassword,
			Sparse:          sparseEnabled,
			NoSparse:        !sparseEnabled,
			Verbose:         *verbose,
			Hash:            *hashFlag,
			ThreadLimit:     effectiveThreads,
			Clean:           *cleanFlag,
			CompressionOpts: *compressionOpts,
		}
		if err := DoAppend(targetArchive, itemsToAdd, opts); err != nil {
			WriteError("%v", err)
			os.Exit(1)
		}
		WriteSuccess("Archivo comprimido actualizado con éxito: %s", targetArchive)
		return
	}

	// Handle -l (list)
	if *listFlag {
		for _, f := range files {
			file, err := os.Open(f)
			if err != nil {
				WriteError("abriendo %s: %v", f, err)
				continue
			}
			if err := ListCompressed(file); err != nil {
				WriteError("listando %s: %v", f, err)
			}
			file.Close()
		}
		return
	}

	// Handle -tree
	if *treeFlag {
		if len(files) == 0 {
			WriteError("debe especificar al menos un archivo comprimido para mostrar su árbol\nUso: crush -tree ARCHIVO...")
			os.Exit(1)
		}
		if err := DoTree(files, cliPassword, os.Stdout); err != nil {
			WriteError("%v", err)
			os.Exit(1)
		}
		return
	}

	// Handle -find
	if *findFlag != "" {
		if len(files) == 0 {
			WriteError("debe especificar al menos un archivo comprimido para buscar\nUso: crush -find PATRÓN ARCHIVO...")
			os.Exit(1)
		}
		if _, err := DoFind(*findFlag, files, cliPassword, os.Stdout); err != nil {
			WriteError("%v", err)
			os.Exit(1)
		}
		return
	}

	// Handle -diff
	if *diffFlag {
		if len(files) < 2 {
			WriteError("debe especificar dos archivos comprimidos para comparar\nUso: crush -diff ARCHIVO1 ARCHIVO2")
			os.Exit(1)
		}
		if err := DoDiff(files[0], files[1], cliPassword, os.Stdout); err != nil {
			WriteError("%v", err)
			os.Exit(1)
		}
		return
	}

	// Handle -r (read)
	if *readFlag {
		for _, f := range files {
			file, err := os.Open(f)
			if err != nil {
				WriteError("abriendo %s: %v", f, err)
				continue
			}
			data, err := CompressRead(file)
			if err != nil {
				WriteError("leyendo %s: %v", f, err)
				file.Close()
				continue
			}
			os.Stdout.Write(data)
			file.Close()
		}
		return
	}

	// Handle -t alone (test only)
	if (*testFlag || *verifyFlag) && !*compressFlag && !*decompressFlag {
		opts := TestOptions{
			Verbose:  *verbose,
			Quick:    *quick,
			Verify:   *verifyFlag,
			Password: cliPassword,
		}
		if err := DoTest(files, opts); err != nil {
			WriteError("%v", err)
			os.Exit(1)
		}
		return
	}

	// Handle -watch mode
	if *watchDir != "" {
		var mode WatcherMode
		var cOpts CompressOptions
		var dOpts DecompressOptions

		outDir := *outputDir
		if !outDirSet {
			outDir = *watchDir
		}

		if *compressFlag {
			mode = WatchModeCompress
			format, err := ParseFormat(*formatStr)
			if err != nil {
				WriteError("%v", err)
				printHelp()
				os.Exit(1)
			}
			parallel := NCPU()
			if parallel < 2 {
				parallel = 2
			}
			cOpts = CompressOptions{
				Format:          format,
				DryRun:          *dryRun,
				Verbose:         *verbose,
				OutputDir:       outDir,
				SplitSize:       *splitSize,
				KeepOrig:        *keepOrig,
				Parallel:        parallel,
				CompressionOpts: *compressionOpts,
				Exclude:         exclude,
				Combine:         *combineFlag,
			}
		} else if *decompressFlag {
			mode = WatchModeDecompress
			parallel := NCPU()
			if parallel < 2 {
				parallel = 2
			}
			dOpts = DecompressOptions{
				DryRun:    *dryRun,
				Verbose:   *verbose,
				OutputDir: outDir,
				KeepOrig:  *keepOrig,
				Force:     *force,
				Parallel:  parallel,
			}
		}

		wOpts := WatcherOptions{
			Dir:            *watchDir,
			Mode:           mode,
			CompressOpts:   cOpts,
			DecompressOpts: dOpts,
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		WriteInfo("Iniciando observador de directorios en %s...", *watchDir)
		if err := RunWatcher(ctx, wOpts); err != nil && ctx.Err() == nil {
			WriteError("observador finalizado con error: %v", err)
			os.Exit(1)
		}
		WriteInfo("Observador detenido.")
		return
	}

	// Handle -c (compress), optionally followed by -t (test)
	if *compressFlag {
		multiStr := *formatsMulti
		if multiStr == "" {
			multiStr = *formatsMultiLong
		}

		var formats []Format
		if multiStr != "" {
			var err error
			formats, err = ParseFormatList(multiStr)
			if err != nil {
				WriteError("%v", err)
				printHelp()
				os.Exit(1)
			}
		} else if *formatStr != "" {
			format, err := ParseFormat(*formatStr)
			if err != nil {
				WriteError("%v", err)
				printHelp()
				os.Exit(1)
			}
			formats = []Format{format}
		} else {
			WriteError("debe especificar formato con -f o múltiples formatos con -F")
			WriteInfo("Formatos: gz xz bz2 bz3 zst lz lrz zip 7z tar rar lz4 br")
			printHelp()
			os.Exit(1)
		}

		parallel := NCPU()
		if parallel < 2 {
			parallel = 2
		}
		// When -c -t, defer deletion until after the test to prevent data loss
		skipCleanup := (*testFlag || *verifyFlag) && !*keepOrig
		opts := CompressOptions{
			Format:          formats[0],
			Formats:         formats,
			DryRun:          *dryRun,
			Verbose:         *verbose,
			OutputDir:       *outputDir,
			SplitSize:       *splitSize,
			KeepOrig:        *keepOrig || skipCleanup,
			Parallel:        parallel,
			CompressionOpts: *compressionOpts,
			Exclude:         exclude,
			Combine:         *combineFlag,
			FromFile:        *fromFile,
			Hash:            *hashFlag,
			Password:        cliPassword,
			Sparse:          sparseEnabled,
			NoSparse:        !sparseEnabled,
			Clean:           *cleanFlag,
		}
		var outPaths []string
		var err error
		if stdinIsPipe {
			if len(formats) > 1 {
				tmpF, err := os.CreateTemp("", "crush-stdin-*")
				if err != nil {
					WriteError("creando archivo temporal para stdin: %v", err)
					os.Exit(1)
				}
				defer os.Remove(tmpF.Name())
				defer tmpF.Close()
				if _, err := io.Copy(tmpF, os.Stdin); err != nil {
					WriteError("copiando stdin: %v", err)
					os.Exit(1)
				}
				tmpF.Close()
				outPaths, err = DoCompress([]string{tmpF.Name()}, opts)
				if err != nil {
					WriteError("%v", err)
					os.Exit(1)
				}
			} else {
				if err := compressStream(os.Stdin, os.Stdout, opts); err != nil {
					WriteError("%v", err)
					os.Exit(1)
				}
			}
		} else {
			outPaths, err = DoCompress(files, opts)
			if err != nil {
				WriteError("%v", err)
				os.Exit(1)
			}
		}
		if (*testFlag || *verifyFlag) && len(outPaths) > 0 {
			WriteLogf("\n%sVerificando integridad del archivo comprimido...%s\n", Bold, NC)
			if err := DoTest(outPaths, TestOptions{Verbose: *verbose, Quick: *quick, Verify: *verifyFlag, Password: cliPassword}); err != nil {
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
		if *testFlag || *verifyFlag {
			WriteLogf("%sVerificando integridad antes de descomprimir...%s\n", Bold, NC)
			if err := DoTest(files, TestOptions{Verbose: *verbose, Quick: *quick, Verify: *verifyFlag, Password: cliPassword}); err != nil {
				os.Exit(1)
			}
			WriteLogf("%s✓ Integridad verificada, descomprimiendo...%s\n\n", Green, NC)
		}

		parallel := NCPU()
		if parallel < 2 {
			parallel = 2
		}
		decompOutputDir := *outputDir
		if !outDirSet {
			decompOutputDir = ""
		}
		opts := DecompressOptions{
			DryRun:    *dryRun,
			Verbose:   *verbose,
			OutputDir: decompOutputDir,
			KeepOrig:  *keepOrig,
			Force:     *force,
			Parallel:  parallel,
			Password:  cliPassword,
			Filter:    *filterFlag,
		}
		if stdinIsPipe && *formatStr != "" && len(files) == 0 {
			f, err := ParseFormat(*formatStr)
			if err != nil {
				WriteError("%v", err)
				os.Exit(1)
			}
			info := FormatInfoFromFormat(f)
			if err := decompressStream(os.Stdin, os.Stdout, info); err != nil {
				WriteError("%v", err)
				os.Exit(1)
			}
		} else {
			if err := DoDecompress(files, opts); err != nil {
				WriteError("%v", err)
				os.Exit(1)
			}
		}
		return
	}

	// If we get here, no mode flag was specified
	WriteError("debe especificar un modo de operación (-c, -d, -l, -t, -r)")
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

	return installBinaryTo(src, "/usr/local/bin/crush")
}

func installBinaryTo(src, dest string) error {
	// Try direct copy
	if err := copyFile(src, dest); err == nil {
		WriteSuccess("Binario instalado en %s", dest)
		return nil
	}

	// Fallback 1: sudo install
	if hasTool("sudo") {
		cmd := exec.Command("sudo", "install", "-m", "755", src, dest)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			WriteSuccess("Binario instalado en %s", dest)
			return nil
		}
	}

	// Fallback 2: pkexec install
	if hasTool("pkexec") {
		cmd := exec.Command("pkexec", "install", "-m", "755", src, dest)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			WriteSuccess("Binario instalado en %s", dest)
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
	WriteInfo("Instalando crush en el sistema...")

	if err := installBinary(); err != nil {
		WriteError("%v", err)
		os.Exit(1)
	}
}

func handleInstallDeps() {
	var neededTools []string
	allTools := []string{"pigz", "xz", "lbzip2", "pbzip2", "bzip3", "zstd", "plzip",
		"lrzip", "zip", "unzip", "p7zip", "rar", "tar", "lz4", "brotli", "numfmt", "pv", "getconf"}

	for _, tool := range allTools {
		if !hasTool(tool) {
			neededTools = append(neededTools, tool)
		}
	}

	if len(neededTools) == 0 {
		WriteSuccess("Todas las herramientas están instaladas.")
		return
	}

	mgr := DetectPkgManager()
	if mgr == nil {
		WriteError("No se pudo detectar el gestor de paquetes")
		os.Exit(1)
	}

	WriteInfo("Detectado gestor de paquetes: %s", mgr.Name)
	remaining := InstallMissingDeps(neededTools, mgr)
	if remaining != nil {
		WriteError("No se pudieron instalar: %v", remaining)
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
			WriteError("no se pudo detectar shell desde $SHELL (%s). Use: crush --completion bash|zsh|fish", shellPath)
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
		WriteError("shell no soportada: %s (use bash, zsh o fish)", shell)
		os.Exit(1)
	}

	WriteInfo("Instalando completado para %s...", shell)

	tmpFile, err := os.CreateTemp("", "crush-completion-*")
	if err != nil {
		WriteError("no se pudo crear archivo temporal")
		os.Exit(1)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.WriteString(script); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		WriteError("escribiendo archivo temporal")
		os.Exit(1)
	}
	tmpFile.Close()

	// Ensure parent directory exists
	parentDir := filepath.Dir(dest)
	runElevated("mkdir", "-p", parentDir)

	if err := runElevated("install", "-m", "644", tmpPath, dest); err != nil {
		os.Remove(tmpPath)
		WriteError("no se pudo instalar completado en %s", dest)
		os.Exit(1)
	}
	os.Remove(tmpPath)
	WriteSuccess("Autocompletado para %s instalado en %s", shell, dest)
}

func handleUninstall() {
	dest := "/usr/local/bin/crush"

	if _, err := os.Stat(dest); os.IsNotExist(err) {
		WriteWarning("crush no está instalado en %s", dest)
		return
	}

	// Try direct remove
	if err := os.Remove(dest); err == nil {
		WriteSuccess("crush desinstalado de %s", dest)
		return
	}

	// Fallback 1: sudo
	if hasTool("sudo") {
		cmd := exec.Command("sudo", "rm", "-f", dest)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			WriteSuccess("crush desinstalado de %s", dest)
			return
		}
	}

	// Fallback 2: pkexec
	if hasTool("pkexec") {
		cmd := exec.Command("pkexec", "rm", "-f", dest)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			WriteSuccess("crush desinstalado de %s", dest)
			return
		}
	}

	WriteError("no se pudo desinstalar (intente con sudo manualmente)")
	os.Exit(1)
}

func printHelp() {
	w := func(c, s string) { fmt.Print(c, s, NC) }

	w(BoldBlue, "CRUSH  Herramienta multi-formato de compresión y descompresión\n\n")
	w(BoldBlue, "Uso:\n")
	w(Yellow, "  crush -c -f FORMATO [opciones] archivo...              # comprimir archivos en el formato especificado\n")
	w(Yellow, "  crush -c -F FORMATOS [opciones] archivo...             # comprimir archivos en múltiples formatos a la vez\n")
	w(Yellow, "  crush -c [opciones]                                    # compresión interactiva del directorio actual\n")
	w(Yellow, "  crush -d [opciones] archivo...                         # descomprimir archivos con detección automática\n")
	w(Yellow, "  crush -d [opciones]                                    # descompresión interactiva de detectados\n")
	w(Yellow, "  crush -a ARCHIVO_COMPRIMIDO [opciones] elemento...     # agregar elementos a archivo existente\n")
	w(Yellow, "  crush -watch DIRECTORIO -c -f FORMATO [opciones]       # monitorear directorio y comprimir archivos entrantes\n")
	w(Yellow, "  crush -watch DIRECTORIO -d [opciones]                  # monitorear directorio y descomprimir archivos entrantes\n")
	w(Yellow, "  crush -l archivo...                                    # listar contenido de archivos comprimidos\n")
	w(Yellow, "  crush -t archivo...                                    # verificar integridad de archivos comprimidos\n")
	w(Yellow, "  crush -verify archivo...                               # verificar integridad y checksum SHA-256 si existe .sha256\n")
	w(Yellow, "  crush -r archivo...                                    # leer contenido de archivo comprimido a stdout\n")
	w(Yellow, "  crush --install                                        # instalar binario crush en /usr/local/bin\n")
	w(Yellow, "  crush --install-deps                                   # instalar solo herramientas faltantes del sistema\n")
	w(Yellow, "  crush --uninstall                                      # desinstalar binario crush del sistema\n")
	w(Yellow, "  crush --completion [SHELL]                             # instalar autocompletado para la shell (bash/zsh/fish)\n")
	w(Yellow, "  crush --bench [archivo]                                # medir velocidad y ratio de compresión por formato\n")
	w(Yellow, "  crush --version                                        # mostrar versión instalada de crush\n\n")
	w(BoldBlue, "Opciones de modo:\n")
	w(Yellow, "  -c")
	fmt.Print("                   Comprimir archivos (o interactivo si no se pasan argumentos)\n")
	w(Yellow, "  -d")
	fmt.Print("                   Descomprimir archivos (o interactivo si no se pasan argumentos)\n")
	w(Yellow, "  -a, -u")
	fmt.Print("               Agregar o actualizar elementos en archivo comprimido existente\n")
	w(Yellow, "  -watch DIR")
	fmt.Print("           Monitorear directorio para procesar archivos nuevos\n")
	w(Yellow, "  -l")
	fmt.Print("                   Listar contenido de archivo comprimido\n")
	w(Yellow, "  -t")
	fmt.Print("                   Verificar integridad de archivos comprimidos\n")
	w(Yellow, "  -verify")
	fmt.Print("              Verificar integridad y checksum SHA-256 si existe .sha256\n")
	w(Yellow, "  -r")
	fmt.Print("                   Leer contenido de archivo comprimido a stdout\n")
	w(Yellow, "  --bench")
	fmt.Print("              Medir velocidad y ratio de compresión por formato\n")
	w(Yellow, "  --bench-size N")
	fmt.Print("       Tamaño en MB del dataset para benchmark (por defecto: 10)\n")
	w(Yellow, "  -h, --help")
	fmt.Print("           Mostrar esta ayuda y salir\n\n")
	w(BoldBlue, "Opciones generales:\n")

	// Build format list ordered by compression ratio
	var ordered []string
	for _, f := range FormatsByCompression {
		ordered = append(ordered, f.String())
	}
	w(Yellow, "  -f FORMATO")
	fmt.Print("           Formato de compresión: ")
	fmt.Print(strings.Join(ordered, ", "))
	fmt.Print("\n                       lrz ofrece la máxima compresión\n")
	w(Yellow, "  -F, --formats LISTA")
	fmt.Print("  Comprimir en múltiples formatos separados por coma (ej: -F gz,xz,zst)\n")

	w(Yellow, "  -o DIRECTORIO")
	fmt.Print("        Directorio de salida (por defecto: .)\n")
	w(Yellow, "  -n")
	fmt.Print("                   Modo simulacro (dry-run, no ejecutar)\n")
	w(Yellow, "  -k")
	fmt.Print("                   Conservar archivos originales tras compresión\n")
	w(Yellow, "  -v")
	fmt.Print("                   Modo verbose (mostrar comandos ejecutados)\n")
	w(Yellow, "  -force")
	fmt.Print("               Sobrescribir archivos existentes sin confirmación\n")
	w(Yellow, "  -quick")
	fmt.Print("               Verificación rápida (no verificar cada archivo individualmente)\n")
	w(Yellow, "  -s N")
	fmt.Print("                 Dividir en partes de N MB (formatos de flujo: gz, xz, bz2, bz3, zst, lz, lz4, br y tar.*)\n")
	w(Yellow, "  -hash")
	fmt.Print("                Generar archivo de checksum SHA-256 (.sha256)\n")
	w(Yellow, "  -p, -password [PASS]")
	fmt.Print(" Contraseña para cifrado/descifrado (7z, zip, rar)\n")
	w(Yellow, "  -opts \"opciones\"")
	fmt.Print("     Opciones adicionales para la herramienta de compresión\n")
	w(Yellow, "  -i ARCHIVO")
	fmt.Print("           Leer lista de archivos a procesar desde fichero\n")
	w(Yellow, "  -C")
	fmt.Print("                   Combinar múltiples archivos en un solo archivo comprimido\n")
	w(Yellow, "  -exclude patrón")
	fmt.Print("      Patrón de exclusión (se puede repetir)\n")
	w(Yellow, "  -sparse, -S")
	fmt.Print("          Soporte para archivos dispersos (sparse) en tar (activo por omisión)\n")
	w(Yellow, "  -no-sparse")
	fmt.Print("           Desactivar soporte para archivos dispersos (sparse) en tar\n")
	w(Yellow, "  -filter patrón")
	fmt.Print("       Filtro de extracción selectiva por patrón (*.txt, carpetas)\n")
	w(Yellow, "  -tree")
	fmt.Print("                Mostrar vista en árbol jerárquica del contenido\n")
	w(Yellow, "  -find patrón")
	fmt.Print("        Buscar archivos por patrón o nombre dentro de archivos comprimidos\n")
	w(Yellow, "  -diff")
	fmt.Print("               Comparar diferencias de contenido entre dos archivos comprimidos\n")
	w(Yellow, "  -clean")
	fmt.Print("              Sanitización automática: excluir basura de SO, VCS y dependencias\n")
	w(Yellow, "  --install")
	fmt.Print("            Instalar binario crush en /usr/local/bin\n")
	w(Yellow, "  --install-deps")
	fmt.Print("       Instalar solo herramientas de compresión faltantes\n")
	w(Yellow, "  --uninstall")
	fmt.Print("          Desinstalar crush del sistema\n")
	w(Yellow, "  --completion [SHELL]")
	fmt.Print(" Instalar autocompletado para la shell (bash, zsh o fish)\n")
	w(Yellow, "  --version")
	fmt.Print("            Mostrar versión instalada de crush y salir\n\n")
	w(BoldBlue, "Instancia única:\n")
	fmt.Print("  Solo una instancia de crush puede comprimir, descomprimir, agregar, verificar,\n")
	fmt.Print("  medir (--bench) u observar (-watch) a la vez, para no saturar el equipo.\n")
	fmt.Print("  Si inicia otra, se encola esperando a que termine la activa (estilo apt).\n")
	fmt.Print("  El bloqueo (/tmp/crush.lock) se libera solo al terminar o morir el proceso.\n")
	fmt.Print("  Quedan exentos -l, -r y los modos por pipe (stdin → stdout), así que\n")
	fmt.Print("  'crush -r a.gz | crush -c -f xz' sigue funcionando.\n\n")
	w(BoldBlue, "Ejemplos:\n")
	w(Yellow, "  crush -c                                               # compresión interactiva del directorio actual\n")
	w(Yellow, "  crush -d                                               # descompresión interactiva de detectados en subcarpetas\n")
	w(Yellow, "  crush -a comprimido.zip archivo.txt carpeta/           # agregar archivos a zip existente sin recrearlo\n")
	w(Yellow, "  crush -a respaldo.tar.gz nuevo.log                     # agregar a tar.gz existente in-place\n")
	w(Yellow, "  crush -watch /backup/inbox -c -f zst                   # monitorear directorio y comprimir archivos entrantes\n")
	w(Yellow, "  crush -watch /descargas -d -o /extraidos/              # monitorear directorio y descomprimir archivos entrantes\n")
	w(Yellow, "  crush -c -f gz documento.txt                           # compresión básica de archivo en formato gz\n")
	w(Yellow, "  crush -c -F gz,xz,zst documento.txt                    # comprimir en múltiples formatos a la vez\n")
	w(Yellow, "  crush -c -f gz -t documento.txt                        # comprimir y verificar integridad inmediatamente\n")
	w(Yellow, "  crush -c -f gz -hash documento.txt                     # comprimir y generar checksum SHA-256 (.sha256)\n")
	w(Yellow, "  crush -c -f 7z -p secret archivo.txt                   # comprimir cifrado con contraseña fija (7z, zip, rar)\n")
	w(Yellow, "  crush -c -f 7z -p archivo.txt                          # comprimir solicitando contraseña oculta en consola\n")
	w(Yellow, "  crush -d -p secret archivo.7z                          # descomprimir archivo protegido por contraseña\n")
	w(Yellow, "  crush -verify archivo.tar.gz                           # verificar integridad y checksum contra su .sha256\n")
	w(Yellow, "  crush -c -f zst -v archivo.tar                         # compresión verbose mostrando comandos y asignación\n")
	w(Yellow, "  crush -c -f xz -k documento.txt                        # conservar archivo original tras la compresión\n")
	w(Yellow, "  crush -c -f 7z -n *.iso                                # modo simulacro (dry-run, no escribe en disco)\n")
	w(Yellow, "  crush -c -f zip -exclude \"*.bak\" dir/                  # comprimir excluyendo archivos con patrón .bak\n")
	w(Yellow, "  crush -c -clean -f tar.gz proyecto/                    # comprimir excluyendo automáticamente .git, node_modules, etc.\n")
	w(Yellow, "  crush -c -f tar.gz -o /backup/ dir/                    # comprimir enviando el resultado a un directorio\n")
	w(Yellow, "  crush -c -C -f 7z file1.txt file2.txt file3.txt        # combinar múltiples archivos en un único archivo 7z\n")
	w(Yellow, "  crush -c -f zst -s 10 archivo_grande.iso               # dividir flujo comprimido en partes de 10 MB\n")
	w(Yellow, "  crush -c -f tar.gz -sparse disco.raw                   # compresión de archivos dispersos en tar (activo por omisión)\n")
	w(Yellow, "  crush -c -f xz -opts \"-9e\" grande.tar                 # pasar opciones avanzadas al compresor nativo\n")
	w(Yellow, "  crush -c -f 7z -i lista_archivos.txt                   # comprimir archivos leídos desde un fichero de lista\n")
	w(Yellow, "  crush -d archivo.tar.gz                                # descomprimir archivo detectando formato automático\n")
	w(Yellow, "  crush -d -t archivo.7z                                 # verificar integridad antes de descomprimir\n")
	w(Yellow, "  crush -d -o /tmp/ archivo.zip                          # descomprimir extrayendo en directorio destino\n")
	w(Yellow, "  crush -d -force archivo.7z                             # sobrescribir archivos existentes al extraer\n")
	w(Yellow, "  crush -d -quick *.tar.gz                               # descompresión rápida omitiendo test individual previo\n")
	w(Yellow, "  crush -d -filter \"*.txt\" respaldo.tar.gz              # extraer selectivamente solo archivos que coincidan\n")
	w(Yellow, "  crush -t *.tar.gz                                      # verificar integridad física de múltiples archivos\n")
	w(Yellow, "  crush -l archivo.7z                                    # listar contenido detallado de archivo comprimido 7z\n")
	w(Yellow, "  crush -l *.7z                                          # listar contenido de múltiples archivos comprimidos\n")
	w(Yellow, "  crush -tree archivo.tar.gz                             # mostrar contenido del archivo en vista de árbol\n")
	w(Yellow, "  crush -find \"*.sql\" backups/*.tar.gz                    # buscar archivos por patrón dentro de comprimidos\n")
	w(Yellow, "  crush -diff release_v1.zip release_v2.zip               # comparar cambios y diferencias entre dos comprimidos\n")
	w(Yellow, "  crush -r archivo.txt.gz | grep error                   # leer y filtrar contenido comprimido a stdout\n")
	w(Yellow, "  crush --bench                                          # benchmark comparativo de todos los formatos (10 MB)\n")
	w(Yellow, "  crush --bench-size 50 archivo.iso                      # benchmark con dataset de 50 MB o archivo propio\n")
	w(Yellow, "  crush -h                                               # mostrar esta ayuda completa con opciones y ejemplos\n")
	w(Yellow, "  crush --install                                        # instalar binario crush en /usr/local/bin\n")
	w(Yellow, "  crush --install-deps                                   # instalar solo herramientas faltantes del sistema\n")
	w(Yellow, "  crush --uninstall                                      # desinstalar binario crush del sistema\n")
	w(Yellow, "  crush --completion                                     # auto-detectar shell e instalar autocompletado\n")
	w(Yellow, "  crush --completion bash                                # instalar autocompletado para shell específica\n")
	w(Yellow, "  cat archivo.txt | crush -c -f gz > archivo.txt.gz      # compresión desde stdin recibiendo flujo por pipe\n")
	w(Yellow, "  cat archivo.txt.gz | crush -d -f gz > archivo.txt      # descompresión desde stdin emitiendo flujo a stdout\n")
	w(Yellow, "  crush --version                                        # mostrar versión instalada de crush\n")
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
_crush_completions() {
    local cur prev words cword
    if declare -F _init_completion >/dev/null 2>&1; then
        _init_completion || return
    else
        cur="${COMP_WORDS[COMP_CWORD]}"
        prev="${COMP_WORDS[COMP_CWORD-1]}"
    fi

    local formats="gz xz bz2 bz3 zst lz lrz zip 7z rar lz4 br tar"
    local split_formats="gz xz bz2 bz3 zst lz lz4 br"
    local split_sizes="10 50 100 500 1000"
    local short="-c -d -l -t -r -h -v -k -n -f -F -o -s -force -quick -opts -exclude -hash -verify -p -password"
    local long="--compress --decompress --list --test --read --help --verbose --keep --dry-run --format --formats --output --split --opts --exclude --force --quick --install --install-deps --uninstall --completion --version --hash --verify --password"

    local has_split=0
    local w
    for w in "${COMP_WORDS[@]}"; do
        if [[ "$w" == "-s" || "$w" == "--split" || "$w" == --split=* ]]; then
            has_split=1
            break
        fi
    done

    case "${prev}" in
        -f|--format|-F|--formats)
            if [[ $has_split -eq 1 ]]; then
                COMPREPLY=( $(compgen -W "${split_formats}" -- "${cur}") )
            else
                COMPREPLY=( $(compgen -W "${formats}" -- "${cur}") )
            fi
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
            COMPREPLY=( $(compgen -W "${split_sizes}" -- "${cur}") )
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
}
_crush() {
    _crush_completions "$@"
} && complete -F _crush_completions crush
`

const zshCompletion = `#compdef crush

_crush() {
    local -a formats split_formats split_sizes
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
    split_formats=(
        'gz:Gzip (compatible con split)'
        'xz:XZ (compatible con split)'
        'bz2:Bzip2 (compatible con split)'
        'bz3:Bzip3 (compatible con split)'
        'zst:Zstd (compatible con split)'
        'lz:Lzip (compatible con split)'
        'lz4:LZ4 (compatible con split)'
        'br:Brotli (compatible con split)'
    )
    split_sizes=(
        '10:10 MB'
        '50:50 MB'
        '100:100 MB'
        '500:500 MB'
        '1000:1000 MB'
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
        {-F,--formats}'[Comprimir en múltiples formatos separados por coma]:formatos:' \
        {-o,--output}'[Directorio de salida]:directorio:_files -/' \
        '--force[Sobrescribir existentes]' \
        '--quick[Verificación rápida]' \
        {-v,--verbose}'[Modo verbose]' \
        {-k,--keep}'[Conservar originales]' \
        {-n,--dry-run}'[Modo simulacro]' \
        {-s,--split}'[Dividir en partes de N MB (formatos de flujo: gz, xz, bz2, bz3, zst, lz, lz4, br)]:tamaño en MB:->split' \
        '--hash[Generar archivo de checksum SHA-256]' \
        '--verify[Verificar integridad y checksum SHA-256]' \
        {-p,--password}'[Contraseña para cifrado/descifrado]:contraseña:' \
        '--opts[Opciones adicionales]:opciones:' \
        '--exclude[Patrón de exclusión]:patrón:' \
        '*:archivo:_files'

    case "$state" in
        formats)
            if (( ${+opt_args[-s]} || ${+opt_args[--split]} )) || [[ " ${words[*]} " == *" -s "* || " ${words[*]} " == *" --split "* ]]; then
                _describe -t formats "formato compatible con split" split_formats
            else
                _describe -t formats "formato" formats
            fi
            ;;
        split)
            _describe -t split_sizes "tamaño en MB" split_sizes
            ;;
    esac
}

_crush "$@"
`

const fishCompletion = `# fish completion for crush

function __crush_formats
    echo gz xz bz2 bz3 zst lz lrz zip 7z rar lz4 br tar
end

function __crush_split_formats
    echo gz xz bz2 bz3 zst lz lz4 br
end

function __crush_has_split
    if type -q __fish_contains_opt
        __fish_contains_opt -s s -l split; and return 0
    end
    for arg in (commandline -poc)
        if string match -qr '^(-s|--split)$' -- $arg
            return 0
        end
    end
    return 1
end

# Mode flags (mutually exclusive group)
complete -c crush -n "not __fish_seen_subcommand_from -c -d -l -t -r" -s c -d "Comprimir archivos"
complete -c crush -n "not __fish_seen_subcommand_from -c -d -l -t -r" -s d -d "Descomprimir archivos"
complete -c crush -n "not __fish_seen_subcommand_from -c -d -l -t -r" -s l -d "Listar contenido"
complete -c crush -n "not __fish_seen_subcommand_from -c -d -l -t -r" -s t -d "Verificar integridad"
complete -c crush -n "not __fish_seen_subcommand_from -c -d -l -t -r" -s r -d "Leer contenido a stdout"

# General flags
complete -c crush -n "__crush_has_split" -s f -l format -d "Formato de compresión (compatible con split)" -xa "(__crush_split_formats)"
complete -c crush -n "not __crush_has_split" -s f -l format -d "Formato de compresión" -xa "(__crush_formats)"
complete -c crush -s F -l formats -d "Comprimir en múltiples formatos separados por coma" -xa "(__crush_formats)"
complete -c crush -s o -d "Directorio de salida" -xa "(__fish_complete_directories)"
complete -c crush -s force -l force -d "Sobrescribir existentes"
complete -c crush -s quick -l quick -d "Verificación rápida"
complete -c crush -s v -d "Modo verbose"
complete -c crush -s k -d "Conservar originales"
complete -c crush -s n -d "Modo simulacro"
complete -c crush -s s -l split -d "Dividir en partes de N MB (formatos de flujo: gz, xz, bz2, bz3, zst, lz, lz4, br)" -xa "10 50 100 500 1000"
complete -c crush -s hash -l hash -d "Generar archivo de checksum SHA-256 (.sha256)"
complete -c crush -s verify -l verify -d "Verificar integridad y checksum SHA-256 si existe .sha256"
complete -c crush -s p -l password -d "Contraseña para cifrado/descifrado (7z, zip, rar)" -r
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
