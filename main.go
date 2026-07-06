package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	// Flags
	compressFlag := flag.Bool("c", false, "Comprimir archivos")
	decompressFlag := flag.Bool("d", false, "Descomprimir archivos")
	listFlag := flag.Bool("l", false, "Listar contenido de archivo comprimido")
	testFlag := flag.Bool("t", false, "Verificar integridad de archivos comprimidos")
	readFlag := flag.Bool("r", false, "Leer contenido de archivo comprimido a stdout")
	helpFlag := flag.Bool("h", false, "Mostrar ayuda")
	installFlag := flag.Bool("install", false, "Instalar herramientas de compresión faltantes")

	formatStr := flag.String("f", "", "Formato de compresión (gz, xz, bz2, zst, lz, lrz, zip, 7z, tar, rar, bz3, lz4, br)")
	outputDir := flag.String("o", ".", "Directorio de salida")
	dryRun := flag.Bool("n", false, "Modo simulacro (no ejecutar)")
	keepOrig := flag.Bool("k", false, "Conservar archivos originales")
	verbose := flag.Bool("v", false, "Modo verbose")
	progress := flag.Bool("p", false, "Mostrar barra de progreso")
	force := flag.Bool("force", false, "Sobrescribir archivos existentes")
	quick := flag.Bool("quick", false, "Verificación rápida (no verificar cada archivo)")
	threadCount := flag.Int("T", 0, "Número de hilos (0=auto)")
	splitSize := flag.Int("s", 0, "Dividir en partes de N MB (solo compresión)")
	compressionOpts := flag.String("opts", "", "Opciones adicionales para la herramienta de compresión")

	var exclude multiFlag
	flag.Var(&exclude, "exclude", "Patrón de exclusión (repetible)")

	flag.Parse()

	defer CloseLog()
	if err := SetupLogging(); err != nil {
		fmt.Fprintf(os.Stderr, "Error configurando logging: %v\n", err)
	}

	// Handle -h / no args
	if *helpFlag || (flag.NFlag() == 0 && flag.NArg() == 0 && !*installFlag) {
		printHelp()
		return
	}

	// Handle --install
	if *installFlag {
		handleInstall()
		return
	}

	// Get files from args or stdin
	var files []string
	if flag.NArg() > 0 {
		files = flag.Args()
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

	// Handle -t (test)
	if *testFlag {
		opts := TestOptions{
			Verbose: *verbose,
			Quick:   *quick,
		}
		if err := DoTest(files, opts); err != nil {
			os.Exit(1)
		}
		return
	}

	// Handle -c (compress)
	if *compressFlag {
		format, err := ParseFormat(*formatStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			printHelp()
			os.Exit(1)
		}

		opts := CompressOptions{
			Format:          format,
			DryRun:          *dryRun,
			Verbose:         *verbose,
			OutputDir:       *outputDir,
			SplitSize:       *splitSize,
			Progress:        *progress,
			KeepOrig:        *keepOrig,
			Threads:         *threadCount,
			CompressionOpts: *compressionOpts,
			Exclude:         exclude,
		}
		if err := DoCompress(files, opts); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Handle -d (decompress)
	if *decompressFlag {
		opts := DecompressOptions{
			DryRun:    *dryRun,
			Verbose:   *verbose,
			OutputDir: *outputDir,
			KeepOrig:  *keepOrig,
			Progress:  *progress,
			Force:     *force,
		}
		if err := DoDecompress(files, opts); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// If we get here, no mode flag was specified
	fmt.Fprintln(os.Stderr, "Error: debe especificar un modo de operación (-c, -d, -l, -t, -r)")
	printHelp()
	os.Exit(1)
}

func handleInstall() {
	// Detect which tools are missing
	var neededTools []string
	allTools := []string{"pigz", "xz", "lbzip2", "pbzip2", "bzip3", "zstd", "plzip",
		"lrzip", "zip", "unzip", "p7zip", "rar", "tar", "numfmt", "pv", "getconf"}

	for _, tool := range allTools {
		if !hasTool(tool) {
			neededTools = append(neededTools, tool)
		}
	}

	if len(neededTools) == 0 {
		fmt.Println("Todas las herramientas están instaladas.")
		return
	}

	mgr := DetectPkgManager()
	if mgr == nil {
		fmt.Fprintln(os.Stderr, "Error: No se pudo detectar el gestor de paquetes")
		os.Exit(1)
	}

	fmt.Printf("Detectado gestor de paquetes: %s\n", mgr.Name)
	remaining := InstallMissingDeps(neededTools, mgr)
	if remaining != nil {
		fmt.Fprintf(os.Stderr, "Error: No se pudieron instalar: %v\n", remaining)
		os.Exit(1)
	}
}

func printHelp() {
	w := func(c, s string) { fmt.Print(c, s, NC) }

	w(Bold+Blue, "COMPRESOR  Herramienta multi-formato de compresión y descompresión\n\n")
	w(Bold+Blue, "Uso:\n")
	w(Yellow, "  compresor -c -f FORMATO [opciones] archivo...\n")
	w(Yellow, "  compresor -d [opciones] archivo...\n")
	w(Yellow, "  compresor -l archivo...\n")
	w(Yellow, "  compresor -t archivo...\n")
	w(Yellow, "  compresor -r archivo...\n")
	w(Yellow, "  compresor --install\n\n")
	w(Bold+Blue, "Opciones de modo:\n")
	w(Yellow, "  -c"); fmt.Print("                   Comprimir archivos\n")
	w(Yellow, "  -d"); fmt.Print("                   Descomprimir archivos\n")
	w(Yellow, "  -l"); fmt.Print("                   Listar contenido de archivo comprimido\n")
	w(Yellow, "  -t"); fmt.Print("                   Verificar integridad de archivos comprimidos\n")
	w(Yellow, "  -r"); fmt.Print("                   Leer contenido de archivo comprimido a stdout\n")
	w(Yellow, "  -h"); fmt.Print("                   Mostrar esta ayuda\n\n")
	w(Bold+Blue, "Opciones generales:\n")
	w(Yellow, "  -f FORMATO"); fmt.Print("           Formato de compresión (gz, xz, bz2, zst, lz, lrz, zip, 7z, tar, rar, bz3, lz4, br)\n")
	w(Yellow, "  -o DIRECTORIO"); fmt.Print("        Directorio de salida (por defecto: .)\n")
	w(Yellow, "  -n"); fmt.Print("                   Modo simulacro (dry-run)\n")
	w(Yellow, "  -k"); fmt.Print("                   Conservar archivos originales\n")
	w(Yellow, "  -v"); fmt.Print("                   Modo verbose\n")
	w(Yellow, "  -p"); fmt.Print("                   Mostrar barra de progreso\n")
	w(Yellow, "  -force"); fmt.Print("               Sobrescribir archivos existentes\n")
	w(Yellow, "  -quick"); fmt.Print("               Verificación rápida (no verificar cada archivo individualmente)\n")
	w(Yellow, "  -T N"); fmt.Print("                 Número de hilos (0 = auto)\n")
	w(Yellow, "  -s N"); fmt.Print("                 Dividir en partes de N MB (solo compresión)\n")
	w(Yellow, "  -opts \"opciones\""); fmt.Print("     Opciones adicionales para la herramienta de compresión\n")
	w(Yellow, "  -exclude patrón"); fmt.Print("      Patrón de exclusión (se puede repetir)\n")
	w(Yellow, "  --install"); fmt.Print("            Instalar herramientas de compresión faltantes\n\n")
	w(Bold+Blue, "Ejemplos:\n")
	w(Yellow, "  compresor -c -f gz documento.txt\n")
	w(Yellow, "  compresor -c -f xz -v -p archivo.tar\n")
	w(Yellow, "  compresor -c -f zip -o /tmp/ varios_archivos.txt\n")
	w(Yellow, "  compresor -c -f zst -s 10 archivo_grande.iso\n")
	w(Yellow, "  compresor -d archivo.tar.gz\n")
	w(Yellow, "  compresor -d -o /tmp/ archivo.zip\n")
	w(Yellow, "  compresor -t *.tar.gz\n")
	w(Yellow, "  compresor -l archivo.7z\n")
	w(Yellow, "  compresor -r archivo.txt.gz | head\n")
	w(Yellow, "  compresor --install\n")
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
