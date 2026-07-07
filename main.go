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

func main() {
	// Handle --help / -help before flag.Parse (flag pkg intercepts --help)
	for _, arg := range os.Args[1:] {
		if arg == "--help" || arg == "-help" {
			printHelp()
			return
		}
	}

	// Flags
	compressFlag := flag.Bool("c", false, "Comprimir archivos")
	decompressFlag := flag.Bool("d", false, "Descomprimir archivos")
	listFlag := flag.Bool("l", false, "Listar contenido de archivo comprimido")
	testFlag := flag.Bool("t", false, "Verificar integridad de archivos comprimidos")
	readFlag := flag.Bool("r", false, "Leer contenido de archivo comprimido a stdout")
	helpFlag := flag.Bool("h", false, "Mostrar ayuda")
	installFlag := flag.Bool("install", false, "Instalar compresor en el sistema + herramientas faltantes")
	installDepsFlag := flag.Bool("install-deps", false, "Instalar solo herramientas de compresión faltantes")
	uninstallFlag := flag.Bool("uninstall", false, "Desinstalar compresor del sistema")

	formatStr := flag.String("f", "", "Formato de compresión (ver -h para lista ordenada por compresión)")
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
	if *helpFlag || (flag.NFlag() == 0 && flag.NArg() == 0) {
		printHelp()
		return
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

func installBinary() error {
	src, err := os.Executable()
	if err != nil {
		return fmt.Errorf("error obteniendo ruta del binario: %w", err)
	}
	src, err = filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("error resolviendo ruta: %w", err)
	}

	dest := "/usr/local/bin/compresor"

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
	WriteLogf("%sInstalando compresor en el sistema...%s\n", Blue, NC)

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

func handleUninstall() {
	dest := "/usr/local/bin/compresor"

	if _, err := os.Stat(dest); os.IsNotExist(err) {
		WriteLogf("  %s✗ compresor no está instalado en %s%s\n", Yellow, dest, NC)
		return
	}

	// Try direct remove
	if err := os.Remove(dest); err == nil {
		WriteLogf("  %s✓ compresor desinstalado de %s%s\n", Green, dest, NC)
		return
	}

	// Fallback 1: sudo
	if hasTool("sudo") {
		cmd := exec.Command("sudo", "rm", "-f", dest)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			WriteLogf("  %s✓ compresor desinstalado de %s%s\n", Green, dest, NC)
			return
		}
	}

	// Fallback 2: pkexec
	if hasTool("pkexec") {
		cmd := exec.Command("pkexec", "rm", "-f", dest)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			WriteLogf("  %s✓ compresor desinstalado de %s%s\n", Green, dest, NC)
			return
		}
	}

	fmt.Fprintf(os.Stderr, "%sError: no se pudo desinstalar (intente con sudo manualmente)%s\n", Red, NC)
	os.Exit(1)
}

func printHelp() {
	w := func(c, s string) { fmt.Print(c, s, NC) }

	w(BoldBlue, "COMPRESOR  Herramienta multi-formato de compresión y descompresión\n\n")
	w(BoldBlue, "Uso:\n")
	w(Yellow, "  compresor -c -f FORMATO [opciones] archivo...\n")
	w(Yellow, "  compresor -d [opciones] archivo...\n")
	w(Yellow, "  compresor -l archivo...\n")
	w(Yellow, "  compresor -t archivo...\n")
	w(Yellow, "  compresor -r archivo...\n")
	w(Yellow, "  compresor --install\n")
	w(Yellow, "  compresor --install-deps\n")
	w(Yellow, "  compresor --uninstall\n\n")
	w(BoldBlue, "Opciones de modo:\n")
	w(Yellow, "  -c"); fmt.Print("                   Comprimir archivos\n")
	w(Yellow, "  -d"); fmt.Print("                   Descomprimir archivos\n")
	w(Yellow, "  -l"); fmt.Print("                   Listar contenido de archivo comprimido\n")
	w(Yellow, "  -t"); fmt.Print("                   Verificar integridad de archivos comprimidos\n")
	w(Yellow, "  -r"); fmt.Print("                   Leer contenido de archivo comprimido a stdout\n")
	w(Yellow, "  -h"); fmt.Print("                   Mostrar esta ayuda\n\n")
	w(BoldBlue, "Opciones generales:\n")

	// Build format list ordered by compression ratio
	var ordered []string
	for _, f := range FormatsByCompression {
		ordered = append(ordered, f.String())
	}
	fmt.Print("  -f FORMATO           Formato de compresión: ")
	fmt.Print(strings.Join(ordered, ", "))
	fmt.Print("\n                       lrz ofrece la máxima compresión\n")

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
	w(Yellow, "  --install"); fmt.Print("            Instalar compresor en el sistema + herramientas faltantes\n")
	w(Yellow, "  --install-deps"); fmt.Print("        Instalar solo herramientas de compresión faltantes\n")
	w(Yellow, "  --uninstall"); fmt.Print("          Desinstalar compresor del sistema\n\n")
	w(BoldBlue, "Ejemplos:\n")
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
	w(Yellow, "  compresor --install-deps\n")
	w(Yellow, "  compresor --uninstall\n")
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
