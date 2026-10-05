package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type ConvertOptions struct {
	OutputDir   string
	KeepOrig    bool
	Force       bool
	Verbose     bool
	Password    string
	Hash        bool
	ThreadLimit int
}

func targetPathForConvert(srcFile string, srcFormat, targetFormat Format, outDir string) string {
	base := filepath.Base(srcFile)
	dir := outDir
	if dir == "" {
		dir = filepath.Dir(srcFile)
	}

	var targetBase string
	lower := strings.ToLower(base)

	if HasTarSuffix(base) {
		stem := StripTarSuffix(base)
		if targetFormat == Tar {
			targetBase = stem + ".tar"
		} else if targetFormat.IsContainer() {
			targetBase = stem + "." + targetFormat.String()
		} else {
			targetBase = stem + ".tar." + targetFormat.String()
		}
	} else if strings.HasSuffix(lower, ".tar") {
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		if targetFormat == Tar {
			targetBase = stem + ".tar"
		} else if targetFormat.IsContainer() {
			targetBase = stem + "." + targetFormat.String()
		} else {
			targetBase = stem + ".tar." + targetFormat.String()
		}
	} else if srcFormat.IsContainer() {
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		if targetFormat.IsContainer() {
			targetBase = stem + "." + targetFormat.String()
		} else if targetFormat == Tar {
			targetBase = stem + ".tar"
		} else {
			targetBase = stem + ".tar." + targetFormat.String()
		}
	} else {
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		if targetFormat.IsContainer() {
			targetBase = stem + "." + targetFormat.String()
		} else if targetFormat == Tar {
			targetBase = stem + ".tar"
		} else {
			targetBase = stem + "." + targetFormat.String()
		}
	}

	return filepath.Join(dir, targetBase)
}

func buildDecompressCmdForFile(srcFile string, info FormatInfo, threadLimit int) *exec.Cmd {
	threads := threadLimit
	if threads <= 0 {
		threads = NCPU()
	}
	tStr := fmt.Sprintf("%d", threads)

	switch info.Format {
	case Gz:
		if hasTool("pigz") {
			return execCommand("pigz", "-dc", "-p", tStr, "--", srcFile)
		}
		return execCommand("gzip", "-dc", "--", srcFile)
	case Xz:
		return execCommand("xz", "-dc", "-T"+tStr, "--", srcFile)
	case Bz2:
		bin := bzip2Bin()
		if bin == "lbzip2" {
			return execCommand(bin, "-dc", "-n", tStr, "--", srcFile)
		} else if bin == "pbzip2" {
			return execCommand(bin, "-dc", "-p"+tStr, "--", srcFile)
		}
		return execCommand(bin, "-dc", "--", srcFile)
	case Bz3:
		return execCommand("bzip3", "-dc", "-j", tStr, "--", srcFile)
	case Zst:
		return execCommand("zstd", "-dc", "-T"+tStr, "--", srcFile)
	case Lz:
		if hasTool("plzip") {
			return execCommand("plzip", "-dc", "--threads="+tStr, "--", srcFile)
		}
		return execCommand("lzip", "-dc", "--", srcFile)
	case Lrz:
		return execCommand("lrzip", "-d", "-p", tStr, "-o", "-", "--", srcFile)
	case Lz4:
		args := []string{"-dc"}
		if lz4SupportsThreads() {
			args = append(args, "-T"+tStr)
		}
		args = append(args, "--", srcFile)
		return execCommand("lz4", args...)
	case Br:
		return execCommand("brotli", "-dc", "--", srcFile)
	default:
		return execCommand("cat", srcFile)
	}
}

func convertStreamPipe(srcFile, outPath string, srcInfo FormatInfo, targetFormat Format, opts ConvertOptions) error {
	outFile, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("creando archivo de salida: %w", err)
	}
	defer outFile.Close()

	if targetFormat == Tar {
		decompCmd := buildDecompressCmdForFile(srcFile, srcInfo, opts.ThreadLimit)
		decompCmd.Stdout = outFile
		decompCmd.Stderr = os.Stderr
		if err := decompCmd.Run(); err != nil {
			outFile.Close()
			os.Remove(outPath)
			return fmt.Errorf("descomprimiendo a tar: %w", err)
		}
		return nil
	}

	if srcInfo.Format == Tar {
		inFile, err := os.Open(srcFile)
		if err != nil {
			outFile.Close()
			os.Remove(outPath)
			return err
		}
		defer inFile.Close()

		compCmd := buildCompressCmd(CompressOptions{Format: targetFormat, ThreadLimit: opts.ThreadLimit})
		compCmd.Stdin = inFile
		compCmd.Stdout = outFile
		compCmd.Stderr = os.Stderr
		if err := compCmd.Run(); err != nil {
			outFile.Close()
			os.Remove(outPath)
			return fmt.Errorf("comprimiendo desde tar: %w", err)
		}
		return nil
	}

	pr, pw := io.Pipe()

	decompCmd := buildDecompressCmdForFile(srcFile, srcInfo, opts.ThreadLimit)
	decompCmd.Stdout = pw
	decompCmd.Stderr = os.Stderr

	compCmd := buildCompressCmd(CompressOptions{Format: targetFormat, ThreadLimit: opts.ThreadLimit})
	compCmd.Stdin = pr
	compCmd.Stdout = outFile
	compCmd.Stderr = os.Stderr

	if err := decompCmd.Start(); err != nil {
		pw.Close()
		outFile.Close()
		os.Remove(outPath)
		return fmt.Errorf("iniciando descompresión en pipe: %w", err)
	}

	if err := compCmd.Start(); err != nil {
		_ = decompCmd.Process.Kill()
		pw.Close()
		outFile.Close()
		os.Remove(outPath)
		return fmt.Errorf("iniciando compresión en pipe: %w", err)
	}

	decompErr := decompCmd.Wait()
	_ = pw.CloseWithError(decompErr)

	compErr := compCmd.Wait()
	_ = outFile.Close()

	if decompErr != nil {
		os.Remove(outPath)
		return fmt.Errorf("fallo descompresor durante pipe: %w", decompErr)
	}
	if compErr != nil {
		os.Remove(outPath)
		return fmt.Errorf("fallo compresor durante pipe: %w", compErr)
	}

	return nil
}

func convertViaTempDir(srcFile, outPath string, srcInfo FormatInfo, targetFormat Format, opts ConvertOptions) error {
	tmpDir, err := os.MkdirTemp("", "crush_conv_*")
	if err != nil {
		return fmt.Errorf("creando directorio temporal para conversión: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	decompOpts := DecompressOptions{
		OutputDir: tmpDir,
		Password:  opts.Password,
		Force:     true,
		KeepOrig:  true,
	}
	if err := DoDecompress([]string{srcFile}, decompOpts); err != nil {
		return fmt.Errorf("extrayendo archivo para conversión: %w", err)
	}

	if isTarBased(targetFormat) || targetFormat == Tar {
		if targetFormat == Tar {
			cmd := execCommand("tar", "-cf", outPath, "-C", tmpDir, ".")
			if err := cmd.Run(); err != nil {
				os.Remove(outPath)
				return fmt.Errorf("creando tar final: %w", err)
			}
			return nil
		}

		outFile, err := os.Create(outPath)
		if err != nil {
			return err
		}
		defer outFile.Close()

		tarCmd := execCommand("tar", "-cf", "-", "-C", tmpDir, ".")
		compCmd := buildCompressCmd(CompressOptions{Format: targetFormat, ThreadLimit: opts.ThreadLimit})

		pr, pw := io.Pipe()
		tarCmd.Stdout = pw
		compCmd.Stdin = pr
		compCmd.Stdout = outFile

		if err := tarCmd.Start(); err != nil {
			pw.Close()
			outFile.Close()
			os.Remove(outPath)
			return err
		}
		if err := compCmd.Start(); err != nil {
			_ = tarCmd.Process.Kill()
			pw.Close()
			outFile.Close()
			os.Remove(outPath)
			return err
		}

		tarErr := tarCmd.Wait()
		_ = pw.CloseWithError(tarErr)
		compErr := compCmd.Wait()
		_ = outFile.Close()

		if tarErr != nil {
			os.Remove(outPath)
			return tarErr
		}
		if compErr != nil {
			os.Remove(outPath)
			return compErr
		}
		return nil
	}

	switch targetFormat {
	case Zip:
		if hasTool("zip") {
			args := []string{"-r"}
			if opts.Password != "" {
				args = append(args, "-P", opts.Password)
			}
			args = append(args, outPath, ".")
			cmd := execCommand("zip", args...)
			cmd.Dir = tmpDir
			if err := cmd.Run(); err != nil {
				os.Remove(outPath)
				return fmt.Errorf("comprimiendo zip: %w", err)
			}
			return nil
		}
		if hasTool("7z") {
			args := []string{"a", "-tzip"}
			if opts.Password != "" {
				args = append(args, "-p"+opts.Password)
			}
			args = append(args, outPath, ".")
			cmd := execCommand("7z", args...)
			cmd.Dir = tmpDir
			if err := cmd.Run(); err != nil {
				os.Remove(outPath)
				return fmt.Errorf("comprimiendo zip con 7z: %w", err)
			}
			return nil
		}
		return fmt.Errorf("no hay herramienta disponible para crear zip")
	case SevenZ:
		bin := sevenzBin()
		args := []string{"a"}
		if opts.Password != "" {
			args = append(args, "-p"+opts.Password, "-mhe=on")
		}
		args = append(args, outPath, ".")
		cmd := execCommand(bin, args...)
		cmd.Dir = tmpDir
		if err := cmd.Run(); err != nil {
			os.Remove(outPath)
			return fmt.Errorf("comprimiendo 7z: %w", err)
		}
		return nil
	case Rar:
		bin := rarBin()
		args := []string{"a"}
		if opts.Password != "" {
			args = append(args, "-p"+opts.Password)
		}
		args = append(args, outPath, "*")
		cmd := execCommand(bin, args...)
		cmd.Dir = tmpDir
		if err := cmd.Run(); err != nil {
			os.Remove(outPath)
			return fmt.Errorf("comprimiendo rar: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("formato contenedor destino no soportado: %s", targetFormat)
	}
}

func DoConvert(files []string, targetFormat Format, opts ConvertOptions) error {
	if len(files) == 0 {
		return fmt.Errorf("no se especificaron archivos para convertir")
	}

	for _, file := range files {
		fi, err := os.Stat(file)
		if err != nil {
			return fmt.Errorf("accediendo a %s: %w", file, err)
		}
		if fi.IsDir() {
			return fmt.Errorf("%s es un directorio; -convert opera sobre archivos comprimidos", file)
		}

		srcInfo, err := DetectFormat(file)
		if err != nil {
			return err
		}

		if srcInfo.Format == targetFormat {
			return fmt.Errorf("%s ya está en formato %s", file, targetFormat)
		}

		outPath := targetPathForConvert(file, srcInfo.Format, targetFormat, opts.OutputDir)

		if !opts.Force {
			if _, err := os.Stat(outPath); err == nil {
				return fmt.Errorf("el archivo destino ya existe: %s (use -force para sobrescribir)", outPath)
			}
		}

		WriteLogf("%sTranscodificando:%s %s → %s\n", BoldBlue, NC, file, outPath)

		isTarSrc := HasTarSuffix(file) || srcInfo.Format == Tar
		isTarTgt := targetFormat == Tar || isTarBased(targetFormat)

		isStreamSrc := !isTarSrc && !srcInfo.IsContainer()
		isStreamTgt := !targetFormat.IsContainer() && targetFormat != Tar

		var convErr error
		if (isTarSrc && isTarTgt) || (isStreamSrc && isStreamTgt) {
			convErr = convertStreamPipe(file, outPath, srcInfo, targetFormat, opts)
		} else {
			convErr = convertViaTempDir(file, outPath, srcInfo, targetFormat, opts)
		}

		if convErr != nil {
			return fmt.Errorf("fallo transcodificando %s a %s: %w", file, targetFormat, convErr)
		}

		if opts.Hash {
			if _, err := WriteSHA256File(outPath); err != nil {
				WriteWarning("generando checksum para %s: %v", outPath, err)
			}
		}

		outFi, statErr := os.Stat(outPath)
		var outSize int64
		if statErr == nil {
			outSize = outFi.Size()
		}
		WriteSuccess("Convertido con éxito: %s (%s)", outPath, FormatSize(outSize))

		if !opts.KeepOrig {
			_ = os.Remove(file)
			_ = os.Remove(file + ".sha256")
		}
	}

	return nil
}
