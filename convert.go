package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var errFallbackTempConvert = errors.New("fallback to temp dir conversion")

func cappedThreadsForBudget(f Format, size int64, budget, limit int) int {
	threads := resolveThreadCapForBudget(f, size, budget)
	if limit > 0 && limit < threads {
		threads = limit
	}
	return threads
}

func createStagedConvOut(outPath string) (stageDir, stageOut string, outFile *os.File, err error) {
	stageDir, err = os.MkdirTemp(filepath.Dir(outPath), ".crush-conv-*")
	if err != nil {
		return "", "", nil, fmt.Errorf("creando directorio temporal de salida: %w", err)
	}
	stageOut = filepath.Join(stageDir, filepath.Base(outPath))
	outFile, err = os.Create(stageOut)
	if err != nil {
		_ = os.RemoveAll(stageDir)
		return "", "", nil, fmt.Errorf("creando archivo de salida: %w", err)
	}
	return stageDir, stageOut, outFile, nil
}

var convertTempDir = os.TempDir

func selectConvertTempBase(srcFile, outPath string) string {
	needed := EstimateUncompressedSize(srcFile)
	if needed <= 0 {
		if fi, err := os.Stat(srcFile); err == nil && fi.Size() > 0 {
			if fi.Size() > math.MaxInt64/4 {
				needed = math.MaxInt64
			} else {
				needed = fi.Size() * 4
			}
		}
	}
	if needed > 0 {
		margin := needed / 10
		if needed > math.MaxInt64-margin {
			needed = math.MaxInt64
		} else {
			needed += margin
		}
	}

	tmpBase := convertTempDir()
	avail := getDiskAvail(tmpBase)
	if avail >= 0 && needed > 0 && avail < needed {
		fallback := filepath.Dir(outPath)
		WriteWarning("Espacio insuficiente en %s (%s disponibles, %s estimados); usando %s para extracción temporal", tmpBase, FormatSize(avail), FormatSize(needed), fallback)
		return fallback
	}
	return tmpBase
}

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
	stageDir, stageOut, outFile, err := createStagedConvOut(outPath)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageDir)

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
		decompThreads := cappedThreadsForBudget(srcInfo.Format, srcSize, memTotal, opts.ThreadLimit)
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

		compThreads := cappedThreadsForBudget(targetFormat, srcSize, memTotal, opts.ThreadLimit)
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
	decompThreads := cappedThreadsForBudget(srcInfo.Format, srcSize, stageBudget, opts.ThreadLimit)
	compThreads := cappedThreadsForBudget(targetFormat, srcSize, stageBudget, opts.ThreadLimit)

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

	tmpBase := selectConvertTempBase(srcFile, outPath)
	tmpDir, err := os.MkdirTemp(tmpBase, ".crush_conv_*")
	if err != nil {
		return fmt.Errorf("creando directorio temporal para conversión: %w", err)
	}
	_ = os.Chmod(tmpDir, 0o700)
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
	compThreads := cappedThreadsForBudget(targetFormat, dirSize, memTotal, opts.ThreadLimit)

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

func convertZipToTarStream(srcFile, outPath string, targetFormat Format, opts ConvertOptions) error {
	if opts.Password != "" {
		return errFallbackTempConvert
	}

	zr, err := zip.OpenReader(srcFile)
	if err != nil {
		return errFallbackTempConvert
	}
	defer zr.Close()

	var estSize int64
	for _, f := range zr.File {
		if f.Flags&0x1 != 0 || (f.Method != zip.Store && f.Method != zip.Deflate) {
			return errFallbackTempConvert
		}
		if f.UncompressedSize64 > uint64(math.MaxInt64) {
			return fmt.Errorf("entrada zip demasiado grande: %s", f.Name)
		}
		estSize = addClampedInt64(estSize, int64(f.UncompressedSize64))
	}

	stageDir, stageOut, outFile, err := createStagedConvOut(outPath)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageDir)

	outClosed := false
	defer func() {
		if !outClosed {
			_ = outFile.Close()
		}
	}()

	var tarDest io.Writer = outFile
	var pw *os.File
	var compCmd *exec.Cmd
	compWaited := false

	if targetFormat != Tar {
		memTotal := getMemLimit()
		compThreads := cappedThreadsForBudget(targetFormat, estSize, memTotal, opts.ThreadLimit)
		var pr *os.File
		pr, pw, err = newAsyncOSPipe()
		if err != nil {
			return fmt.Errorf("creando pipe asíncrono para %s: %w", targetFormat, err)
		}
		compCmd = buildCompressCmd(CompressOptions{Format: targetFormat, ThreadLimit: compThreads, MemBudgetMB: memTotal})
		compCmd.Stdin = pr
		compCmd.Stdout = outFile
		compCmd.Stderr = newLimitedBuffer(maxStderrBytes)

		if err := compCmd.Start(); err != nil {
			_ = pr.Close()
			_ = pw.Close()
			return fmt.Errorf("iniciando compresor %s: %w", targetFormat, err)
		}
		_ = pr.Close()

		defer func() {
			if pw != nil {
				_ = pw.Close()
			}
			if compCmd != nil && compCmd.Process != nil && !compWaited {
				_ = compCmd.Process.Kill()
				_ = compCmd.Wait()
			}
		}()
		tarDest = pw
	}

	bw := bufio.NewWriterSize(tarDest, streamBufSize)
	tw := tar.NewWriter(bw)
	copyBuf := make([]byte, streamBufSize)

	reapCompOnErr := func(writeErr error) error {
		_ = tw.Close()
		_ = bw.Flush()
		if pw != nil {
			_ = pw.Close()
			pw = nil
		}
		if compCmd != nil && !compWaited {
			compWaited = true
			if compErr := augmentErr(compCmd, compCmd.Wait()); compErr != nil {
				return fmt.Errorf("comprimiendo flujo %s: %w", targetFormat, compErr)
			}
		}
		return writeErr
	}

	for _, f := range zr.File {
		rel, ok := sanitizeArchiveRelPath(f.Name)
		if !ok {
			continue
		}
		relSlash := filepath.ToSlash(rel)
		isDir := f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/")
		mode := f.Mode()
		perm := mode.Perm()
		if perm == 0 {
			if isDir {
				perm = 0o755
			} else {
				perm = 0o644
			}
		}
		modTime := f.Modified
		if modTime.IsZero() {
			modTime = f.ModTime()
		}

		switch {
		case isDir:
			hdr := &tar.Header{
				Name:     strings.TrimSuffix(relSlash, "/") + "/",
				Typeflag: tar.TypeDir,
				Mode:     int64(perm),
				ModTime:  modTime,
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return reapCompOnErr(err)
			}
		case mode&os.ModeSymlink != 0:
			rc, err := f.Open()
			if err != nil {
				return reapCompOnErr(fmt.Errorf("abriendo enlace simbólico %s en zip: %w", f.Name, err))
			}
			targetBytes, rErr := io.ReadAll(io.LimitReader(rc, 4096))
			_ = rc.Close()
			if rErr != nil {
				return reapCompOnErr(fmt.Errorf("leyendo enlace simbólico %s en zip: %w", f.Name, rErr))
			}
			hdr := &tar.Header{
				Name:     relSlash,
				Typeflag: tar.TypeSymlink,
				Linkname: string(targetBytes),
				Mode:     int64(perm),
				ModTime:  modTime,
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return reapCompOnErr(err)
			}
		default:
			hdr := &tar.Header{
				Name:     relSlash,
				Typeflag: tar.TypeReg,
				Size:     int64(f.UncompressedSize64),
				Mode:     int64(perm),
				ModTime:  modTime,
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return reapCompOnErr(err)
			}
			rc, err := f.Open()
			if err != nil {
				return reapCompOnErr(fmt.Errorf("abriendo %s en zip: %w", f.Name, err))
			}
			_, cErr := io.CopyBuffer(tw, rc, copyBuf)
			closeEntryErr := rc.Close()
			if cErr != nil {
				return reapCompOnErr(fmt.Errorf("copiando %s desde zip: %w", f.Name, cErr))
			}
			if closeEntryErr != nil {
				return reapCompOnErr(fmt.Errorf("cerrando entrada %s en zip: %w", f.Name, closeEntryErr))
			}
		}
	}

	if err := tw.Close(); err != nil {
		return reapCompOnErr(err)
	}
	if err := bw.Flush(); err != nil {
		return reapCompOnErr(err)
	}
	if pw != nil {
		if err := pw.Close(); err != nil {
			pw = nil
			return reapCompOnErr(err)
		}
		pw = nil
		compWaited = true
		if err := augmentErr(compCmd, compCmd.Wait()); err != nil {
			return fmt.Errorf("comprimiendo flujo %s: %w", targetFormat, err)
		}
	}

	outClosed = true
	if err := outFile.Close(); err != nil {
		return fmt.Errorf("cerrando archivo de salida: %w", err)
	}
	return publishStagedFile(stageOut, outPath, opts.Force)
}

func convertTarToZipStream(srcFile, outPath string, srcInfo FormatInfo, opts ConvertOptions) error {
	if opts.Password != "" {
		return errFallbackTempConvert
	}

	var srcSize int64
	if fi, err := os.Stat(srcFile); err == nil {
		srcSize = fi.Size()
	}

	var tarSrc io.Reader
	var pr *os.File
	var decompCmd *exec.Cmd
	decompWaited := false

	if srcInfo.Format == Tar {
		inFile, err := os.Open(srcFile)
		if err != nil {
			return err
		}
		defer inFile.Close()
		tarSrc = inFile
	} else {
		memTotal := getMemLimit()
		decompThreads := cappedThreadsForBudget(srcInfo.Format, srcSize, memTotal, opts.ThreadLimit)
		var pw *os.File
		var err error
		pr, pw, err = newAsyncOSPipe()
		if err != nil {
			return fmt.Errorf("creando pipe asíncrono para descompresión tar: %w", err)
		}
		decompCmd = buildDecompressCmdForFileWithMem(srcFile, srcInfo, decompThreads, memTotal)
		if closer, err := attachLz4StdinIfNeeded(decompCmd, srcFile, srcInfo.Format); err != nil {
			_ = pr.Close()
			_ = pw.Close()
			return err
		} else if closer != nil {
			defer closer.Close()
		}
		decompCmd.Stdout = pw
		decompCmd.Stderr = newLimitedBuffer(maxStderrBytes)

		if err := decompCmd.Start(); err != nil {
			_ = pr.Close()
			_ = pw.Close()
			return fmt.Errorf("iniciando descompresión tar: %w", err)
		}
		_ = pw.Close()

		defer func() {
			if pr != nil {
				_ = pr.Close()
			}
			if decompCmd != nil && decompCmd.Process != nil && !decompWaited {
				_ = decompCmd.Process.Kill()
				_ = decompCmd.Wait()
			}
		}()
		tarSrc = pr
	}

	stageDir, stageOut, outFile, err := createStagedConvOut(outPath)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageDir)

	outClosed := false
	defer func() {
		if !outClosed {
			_ = outFile.Close()
		}
	}()

	bw := bufio.NewWriterSize(outFile, streamBufSize)
	zw := zip.NewWriter(bw)
	tr := tar.NewReader(bufio.NewReaderSize(tarSrc, streamBufSize))
	copyBuf := make([]byte, streamBufSize)

	reapDecompOnErr := func(readErr error) error {
		_ = zw.Close()
		if pr != nil {
			_ = pr.Close()
			pr = nil
		}
		if decompCmd != nil && !decompWaited {
			if decompCmd.Process != nil {
				_ = decompCmd.Process.Kill()
			}
			decompWaited = true
			if dErr := augmentErr(decompCmd, decompCmd.Wait()); dErr != nil && !errors.Is(readErr, errFallbackTempConvert) {
				return fmt.Errorf("descomprimiendo tar: %w", dErr)
			}
		}
		return readErr
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return reapDecompOnErr(fmt.Errorf("leyendo flujo tar: %w", err))
		}

		switch hdr.Typeflag {
		case tar.TypeXHeader, tar.TypeXGlobalHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
			continue
		case tar.TypeDir:
			rel, ok := sanitizeArchiveRelPath(hdr.Name)
			if !ok {
				continue
			}
			zh := &zip.FileHeader{
				Name:     strings.TrimSuffix(filepath.ToSlash(rel), "/") + "/",
				Method:   zip.Store,
				Modified: hdr.ModTime,
			}
			zh.SetMode(os.ModeDir | hdr.FileInfo().Mode().Perm())
			if _, err := zw.CreateHeader(zh); err != nil {
				return reapDecompOnErr(err)
			}
		case tar.TypeSymlink:
			rel, ok := sanitizeArchiveRelPath(hdr.Name)
			if !ok {
				continue
			}
			zh := &zip.FileHeader{
				Name:     filepath.ToSlash(rel),
				Method:   zip.Store,
				Modified: hdr.ModTime,
			}
			zh.SetMode(os.ModeSymlink | hdr.FileInfo().Mode().Perm())
			w, err := zw.CreateHeader(zh)
			if err != nil {
				return reapDecompOnErr(err)
			}
			if _, err := io.WriteString(w, hdr.Linkname); err != nil {
				return reapDecompOnErr(err)
			}
		case tar.TypeReg, tar.TypeRegA:
			rel, ok := sanitizeArchiveRelPath(hdr.Name)
			if !ok {
				continue
			}
			zh := &zip.FileHeader{
				Name:     filepath.ToSlash(rel),
				Method:   zip.Deflate,
				Modified: hdr.ModTime,
			}
			zh.SetMode(hdr.FileInfo().Mode().Perm())
			w, err := zw.CreateHeader(zh)
			if err != nil {
				return reapDecompOnErr(err)
			}
			if hdr.Size > 0 {
				if _, err := io.CopyBuffer(w, tr, copyBuf); err != nil {
					return reapDecompOnErr(err)
				}
			}
		default:
			return reapDecompOnErr(errFallbackTempConvert)
		}
	}

	if pr != nil {
		_ = pr.Close()
		pr = nil
	}
	if decompCmd != nil {
		decompWaited = true
		if err := augmentErr(decompCmd, decompCmd.Wait()); err != nil {
			return fmt.Errorf("descomprimiendo tar: %w", err)
		}
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("finalizando zip: %w", err)
	}
	if err := bw.Flush(); err != nil {
		return fmt.Errorf("vaciando búfer zip: %w", err)
	}
	outClosed = true
	if err := outFile.Close(); err != nil {
		return fmt.Errorf("cerrando archivo de salida: %w", err)
	}
	return publishStagedFile(stageOut, outPath, opts.Force)
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
		switch {
		case (isTarSrc && isTarTgt) || (isStreamSrc && isStreamTgt):
			convErr = convertStreamPipe(file, outPath, srcInfo, targetFormat, opts)
		case srcInfo.Format == Zip && isTarTgt:
			convErr = convertZipToTarStream(file, outPath, targetFormat, opts)
			if errors.Is(convErr, errFallbackTempConvert) {
				convErr = convertViaTempDir(file, outPath, srcInfo, targetFormat, opts)
			}
		case isTarSrc && targetFormat == Zip:
			convErr = convertTarToZipStream(file, outPath, srcInfo, opts)
			if errors.Is(convErr, errFallbackTempConvert) {
				convErr = convertViaTempDir(file, outPath, srcInfo, targetFormat, opts)
			}
		default:
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
