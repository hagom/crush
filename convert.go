package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func safeTarArchiveArg(p string) string {
	if strings.HasPrefix(p, "-") {
		return "./" + p
	}
	if colon := strings.IndexByte(p, ':'); colon != -1 {
		if slash := strings.IndexByte(p, '/'); slash == -1 || colon < slash {
			return "./" + p
		}
	}
	return p
}

func publishStagedFile(stagePath, outPath string, force bool) error {
	if _, err := os.Stat(stagePath); err != nil {
		return fmt.Errorf("el archivo convertido no se generó en %s: %w", outPath, err)
	}
	if force {
		return os.Rename(stagePath, outPath)
	}
	if err := os.Link(stagePath, outPath); err == nil {
		_ = os.Remove(stagePath)
		return nil
	} else if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("el archivo destino ya existe: %s (use -force para sobrescribir)", outPath)
	}
	if _, err := os.Lstat(outPath); err == nil {
		return fmt.Errorf("el archivo destino ya existe: %s (use -force para sobrescribir)", outPath)
	}
	return os.Rename(stagePath, outPath)
}

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

	switch {
	case HasTarSuffix(base) || strings.HasSuffix(lower, ".tar"):
		var stem string
		if HasTarSuffix(base) {
			stem = StripTarSuffix(base)
		} else {
			stem = strings.TrimSuffix(base, filepath.Ext(base))
		}
		if targetFormat.IsContainer() {
			targetBase = stem + "." + targetFormat.String()
		} else {
			targetBase = stem + ".tar." + targetFormat.String()
		}
	case srcFormat.IsContainer():
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		if targetFormat.IsContainer() {
			targetBase = stem + "." + targetFormat.String()
		} else {
			targetBase = stem + ".tar." + targetFormat.String()
		}
	default:
		stem := strings.TrimSuffix(base, filepath.Ext(base))
		targetBase = stem + "." + targetFormat.String()
	}

	return filepath.Join(dir, targetBase)
}

func buildDecompressCmdForFile(srcFile string, info FormatInfo, threadLimit int) *exec.Cmd {
	return buildDecompressCmdForFileWithMem(srcFile, info, threadLimit, getMemLimit())
}

func buildDecompressCmdForFileWithMem(srcFile string, info FormatInfo, threadLimit int, memBudgetMB int) *exec.Cmd {
	budget := effectiveMemBudget(memBudgetMB)
	var srcSize int64
	if fi, err := os.Stat(srcFile); err == nil {
		srcSize = fi.Size()
	}
	capThreads := resolveThreadCapForBudget(info.Format, srcSize, budget)
	threads := threadLimit
	if threads <= 0 || threads > capThreads {
		threads = capThreads
	}
	tStr := strconv.Itoa(threads)

	switch info.Format {
	case Gz:
		if hasTool("pigz") {
			return execCommand("pigz", "-dc", "-p", tStr, "--", srcFile)
		}
		return execCommand("gzip", "-dc", "--", srcFile)
	case Xz:
		args := []string{"-dc", "-T" + tStr}
		if budget > 0 {
			args = append(args, fmt.Sprintf("--memlimit-mt-decompress=%dMiB", max(65, budget)))
		}
		args = append(args, "--", srcFile)
		return execCommand("xz", args...)
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
		args := []string{"-d", "-p", tStr, "-o", "-"}
		if budget > 0 {
			args = append(args, "-m", strconv.Itoa(max(1, budget/100)))
		}
		args = append(args, "--", srcFile)
		return execCommand("lrzip", args...)
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
		return execCommand("cat", "--", srcFile)
	}
}

func attachLz4StdinIfNeeded(cmd *exec.Cmd, srcFile string, format Format) (io.Closer, error) {
	if format != Lz4 {
		return nil, nil
	}
	inF, err := os.Open(srcFile)
	if err != nil {
		return nil, err
	}
	if n := len(cmd.Args); n >= 2 && cmd.Args[n-2] == "--" && cmd.Args[n-1] == srcFile {
		cmd.Args = cmd.Args[:n-2]
	}
	cmd.Stdin = inF
	return inF, nil
}

func convertStreamPipe(srcFile, outPath string, srcInfo FormatInfo, targetFormat Format, opts ConvertOptions) error {
	stageDir, err := os.MkdirTemp(filepath.Dir(outPath), ".crush-conv-*")
	if err != nil {
		return fmt.Errorf("creando directorio temporal de salida: %w", err)
	}
	defer os.RemoveAll(stageDir)

	stageOut := filepath.Join(stageDir, filepath.Base(outPath))
	outFile, err := os.Create(stageOut)
	if err != nil {
		return fmt.Errorf("creando archivo de salida: %w", err)
	}
	outClosed := false
	defer func() {
		if !outClosed {
			_ = outFile.Close()
		}
	}()

	var srcSize int64
	if fi, err := os.Stat(srcFile); err == nil {
		srcSize = fi.Size()
	}
	memTotal := getMemLimit()

	if targetFormat == Tar {
		decompThreads := resolveThreadCapForBudget(srcInfo.Format, srcSize, memTotal)
		if opts.ThreadLimit > 0 && opts.ThreadLimit < decompThreads {
			decompThreads = opts.ThreadLimit
		}
		decompCmd := buildDecompressCmdForFileWithMem(srcFile, srcInfo, decompThreads, memTotal)
		if closer, err := attachLz4StdinIfNeeded(decompCmd, srcFile, srcInfo.Format); err != nil {
			return err
		} else if closer != nil {
			defer closer.Close()
		}
		decompCmd.Stdout = outFile
		decompCmd.Stderr = os.Stderr
		if err := decompCmd.Run(); err != nil {
			return fmt.Errorf("descomprimiendo a tar: %w", err)
		}
		outClosed = true
		if err := outFile.Close(); err != nil {
			return fmt.Errorf("cerrando archivo de salida: %w", err)
		}
		return publishStagedFile(stageOut, outPath, opts.Force)
	}

	if srcInfo.Format == Tar {
		inFile, err := os.Open(srcFile)
		if err != nil {
			return err
		}
		defer inFile.Close()

		compThreads := resolveThreadCapForBudget(targetFormat, srcSize, memTotal)
		if opts.ThreadLimit > 0 && opts.ThreadLimit < compThreads {
			compThreads = opts.ThreadLimit
		}
		compCmd := buildCompressCmd(CompressOptions{Format: targetFormat, ThreadLimit: compThreads, MemBudgetMB: memTotal})
		compCmd.Stdin = inFile
		compCmd.Stdout = outFile
		compCmd.Stderr = os.Stderr
		if err := compCmd.Run(); err != nil {
			return fmt.Errorf("comprimiendo desde tar: %w", err)
		}
		outClosed = true
		if err := outFile.Close(); err != nil {
			return fmt.Errorf("cerrando archivo de salida: %w", err)
		}
		return publishStagedFile(stageOut, outPath, opts.Force)
	}

	stageBudget := 0
	if memTotal > 0 {
		stageBudget = max(128, memTotal/2)
	}
	decompThreads := resolveThreadCapForBudget(srcInfo.Format, srcSize, stageBudget)
	if opts.ThreadLimit > 0 && opts.ThreadLimit < decompThreads {
		decompThreads = opts.ThreadLimit
	}
	compThreads := resolveThreadCapForBudget(targetFormat, srcSize, stageBudget)
	if opts.ThreadLimit > 0 && opts.ThreadLimit < compThreads {
		compThreads = opts.ThreadLimit
	}

	decompCmd := buildDecompressCmdForFileWithMem(srcFile, srcInfo, decompThreads, stageBudget)
	if closer, err := attachLz4StdinIfNeeded(decompCmd, srcFile, srcInfo.Format); err != nil {
		return err
	} else if closer != nil {
		defer closer.Close()
	}
	decompCmd.Stderr = os.Stderr

	compCmd := buildCompressCmd(CompressOptions{Format: targetFormat, ThreadLimit: compThreads, MemBudgetMB: stageBudget})
	compCmd.Stdout = outFile
	compCmd.Stderr = os.Stderr

	decompErr, compErr := runAsyncPipePair(decompCmd, compCmd, nil)
	outClosed = true
	closeErr := outFile.Close()

	if decompErr != nil {
		return fmt.Errorf("fallo descompresor durante pipe: %w", decompErr)
	}
	if compErr != nil {
		return fmt.Errorf("fallo compresor durante pipe: %w", compErr)
	}
	if closeErr != nil {
		return fmt.Errorf("cerrando archivo de salida: %w", closeErr)
	}

	return publishStagedFile(stageOut, outPath, opts.Force)
}

func convertViaTempDir(srcFile, outPath string, srcInfo FormatInfo, targetFormat Format, opts ConvertOptions) error {
	absOut, err := filepath.Abs(outPath)
	if err != nil {
		return fmt.Errorf("resolviendo ruta de salida %s: %w", outPath, err)
	}
	outPath = absOut

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

	stageDir, err := os.MkdirTemp(filepath.Dir(outPath), ".crush-conv-*")
	if err != nil {
		return fmt.Errorf("creando directorio temporal de salida: %w", err)
	}
	defer os.RemoveAll(stageDir)
	stageOut := filepath.Join(stageDir, filepath.Base(outPath))

	dirSize, _ := GetDirSize(tmpDir)
	memTotal := getMemLimit()
	compThreads := resolveThreadCapForBudget(targetFormat, dirSize, memTotal)
	if opts.ThreadLimit > 0 && opts.ThreadLimit < compThreads {
		compThreads = opts.ThreadLimit
	}

	if targetFormat.IsStream() || targetFormat == Tar {
		if targetFormat == Tar {
			cmd := execCommand("tar", "-cf", safeTarArchiveArg(stageOut), "-C", safeFlagArg(tmpDir), "--", ".")
			cmd.Stdout = io.Discard
			cmd.Stderr = newLimitedBuffer(maxStderrBytes)
			if err := augmentErr(cmd, cmd.Run()); err != nil {
				return fmt.Errorf("creando tar final: %w", err)
			}
			return publishStagedFile(stageOut, outPath, opts.Force)
		}

		outFile, err := os.Create(stageOut)
		if err != nil {
			return err
		}
		outClosed := false
		defer func() {
			if !outClosed {
				_ = outFile.Close()
			}
		}()

		tarCmd := execCommand("tar", "-cf", "-", "-C", safeFlagArg(tmpDir), "--", ".")
		tarCmd.Stderr = newLimitedBuffer(maxStderrBytes)
		compCmd := buildCompressCmd(CompressOptions{Format: targetFormat, ThreadLimit: compThreads, MemBudgetMB: memTotal})
		compCmd.Stdout = outFile
		compCmd.Stderr = newLimitedBuffer(maxStderrBytes)

		tarErr, compErr := runAsyncPipePair(tarCmd, compCmd, nil)
		outClosed = true
		closeErr := outFile.Close()

		if tarErr != nil {
			return tarErr
		}
		if compErr != nil {
			return compErr
		}
		if closeErr != nil {
			return fmt.Errorf("cerrando archivo de salida: %w", closeErr)
		}
		return publishStagedFile(stageOut, outPath, opts.Force)
	}

	switch targetFormat {
	case Zip:
		if hasTool("zip") {
			args := []string{"-r"}
			if opts.Password != "" {
				args = append(args, "-P", opts.Password)
			}
			args = append(args, safeFlagArg(stageOut), "--", ".")
			cmd := execCommand("zip", args...)
			cmd.Dir = tmpDir
			cmd.Stdout = io.Discard
			cmd.Stderr = newLimitedBuffer(maxStderrBytes)
			if err := augmentErr(cmd, cmd.Run()); err != nil {
				return fmt.Errorf("comprimiendo zip: %w", err)
			}
			return publishStagedFile(stageOut, outPath, opts.Force)
		}
		if bin := sevenzBin(); hasTool(bin) {
			args := []string{"a", "-tzip", "-mmt=" + threadStr(compThreads)}
			if opts.Password != "" {
				args = append(args, "-p"+opts.Password)
			}
			args = append(args, safeFlagArg(stageOut), "--", ".")
			cmd := execCommand(bin, args...)
			cmd.Dir = tmpDir
			cmd.Stdout = io.Discard
			cmd.Stderr = newLimitedBuffer(maxStderrBytes)
			if err := augmentErr(cmd, cmd.Run()); err != nil {
				return fmt.Errorf("comprimiendo zip con %s: %w", bin, err)
			}
			return publishStagedFile(stageOut, outPath, opts.Force)
		}
		return fmt.Errorf("no hay herramienta disponible para crear zip")
	case SevenZ:
		bin := sevenzBin()
		md := adaptive7zDictForBudget(dirSize, compThreads, memTotal)
		args := []string{"a", md, "-mmt=" + threadStr(compThreads)}
		if opts.Password != "" {
			args = append(args, "-p"+opts.Password, "-mhe=on")
		}
		args = append(args, safeFlagArg(stageOut), "--", ".")
		cmd := execCommand(bin, args...)
		cmd.Dir = tmpDir
		cmd.Stdout = io.Discard
		cmd.Stderr = newLimitedBuffer(maxStderrBytes)
		if err := augmentErr(cmd, cmd.Run()); err != nil {
			return fmt.Errorf("comprimiendo 7z: %w", err)
		}
		return publishStagedFile(stageOut, outPath, opts.Force)
	case Rar:
		if !hasTool("rar") {
			return fmt.Errorf("no se encontró rar para crear archivo rar")
		}
		args := []string{"a", "-mt" + threadStr(compThreads)}
		if opts.Password != "" {
			args = append(args, "-p"+opts.Password)
		}
		args = append(args, safeFlagArg(stageOut), "--", ".")
		cmd := execCommand("rar", args...)
		cmd.Dir = tmpDir
		cmd.Stdout = io.Discard
		cmd.Stderr = newLimitedBuffer(maxStderrBytes)
		if err := augmentErr(cmd, cmd.Run()); err != nil {
			return fmt.Errorf("comprimiendo rar: %w", err)
		}
		return publishStagedFile(stageOut, outPath, opts.Force)
	default:
		return fmt.Errorf("formato contenedor destino no soportado: %s", targetFormat)
	}
}

func DoConvert(files []string, targetFormat Format, opts ConvertOptions) error {
	if len(files) == 0 {
		return fmt.Errorf("no se especificaron archivos para convertir")
	}
	if targetFormat == Auto {
		return fmt.Errorf("-convert requiere un formato de destino concreto (no 'auto')")
	}
	if _, ok := formatNames[targetFormat]; !ok {
		return fmt.Errorf("formato destino no soportado: %s", targetFormat)
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
			if _, err := os.Lstat(outPath); err == nil {
				return fmt.Errorf("el archivo destino ya existe: %s (use -force para sobrescribir)", outPath)
			}
		}

		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return fmt.Errorf("creando directorio destino: %w", err)
		}

		WriteLogf("%sTranscodificando:%s %s → %s\n", BoldBlue, NC, file, outPath)

		isTarSrc := HasTarSuffix(file) || srcInfo.Format == Tar
		isTarTgt := targetFormat == Tar || targetFormat.IsStream()

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

		outFi, statErr := os.Stat(outPath)
		if statErr != nil {
			return fmt.Errorf("el archivo convertido no se generó en %s: %w", outPath, statErr)
		}

		if opts.Hash {
			if _, err := WriteSHA256File(outPath); err != nil {
				WriteWarning("generando checksum para %s: %v", outPath, err)
			}
		}

		WriteSuccess("Convertido con éxito: %s (%s)", outPath, FormatSize(outFi.Size()))

		if !opts.KeepOrig && !os.SameFile(fi, outFi) {
			_ = os.Remove(file)
			_ = os.Remove(file + ".sha256")
		}
	}

	return nil
}
