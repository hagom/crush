package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func sanitizeTerminalText(s string) string {
	needsClean := false
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x20 || b == 0x7f || b >= 0x80 {
			needsClean = true
			break
		}
	}
	if !needsClean {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		if r == unicode.ReplacementChar {
			sb.WriteRune('?')
		} else if unicode.IsControl(r) {
			sb.WriteByte('?')
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func normalizeArchiveMemberKey(raw string) string {
	norm := strings.ReplaceAll(raw, "\\", "/")
	norm = strings.TrimLeft(norm, "/")
	if norm == "" {
		return ""
	}
	clean := path.Clean(norm)
	clean = strings.TrimPrefix(clean, "./")
	clean = strings.TrimLeft(clean, "/")
	if clean == "" || clean == "." {
		return ""
	}
	return clean
}

var errStopIter = errors.New("stop iteration")

const (
	listScannerInitBuf = 64 * 1024
	listScannerMaxBuf  = 1024 * 1024
)

type ArchiveMember struct {
	Path     string
	Size     int64
	Modified time.Time
	Mode     os.FileMode
	IsDir    bool
}

func ListArchiveMembers(file string, password string) ([]ArchiveMember, error) {
	var members []ArchiveMember
	err := IterArchiveMembers(file, password, func(m ArchiveMember) error {
		members = append(members, m)
		return nil
	})
	return members, err
}

func iterZipReader(file string, fn func(ArchiveMember) error) (bool, error) {
	r, err := zip.OpenReader(file)
	if err != nil {
		return false, err
	}
	defer r.Close()
	for _, f := range r.File {
		err := fn(ArchiveMember{
			Path:     f.Name,
			Size:     int64(f.UncompressedSize64),
			Modified: f.Modified,
			Mode:     f.Mode(),
			IsDir:    f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/"),
		})
		if err != nil {
			if errors.Is(err, errStopIter) {
				return true, nil
			}
			return true, err
		}
	}
	return true, nil
}

func IterArchiveMembers(file string, password string, fn func(ArchiveMember) error) error {
	if fn == nil {
		fn = func(ArchiveMember) error { return nil }
	}

	info, err := DetectFormat(file)
	if err != nil {
		return fmt.Errorf("detectando formato de %s: %w", file, err)
	}

	lower := strings.ToLower(file)

	if info.Format == Zip && (password == "" || !hasTool(sevenzBin())) {
		if opened, zErr := iterZipReader(file, fn); opened {
			return zErr
		} else if !hasTool(sevenzBin()) {
			return zErr
		}
	}

	if info.Format == Tar && !info.IsStream() {
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err, fromCB := iterFromTarReader(tar.NewReader(f), fn)
		if fromCB && errors.Is(err, errStopIter) {
			return nil
		}
		return err
	}

	if (strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz")) && password == "" {
		if f, err := os.Open(file); err == nil {
			if gzr, err := gzip.NewReader(f); err == nil {
				count, iterErr, fromCB := iterFromTarReader(tar.NewReader(gzr), fn)
				_ = gzr.Close()
				_ = f.Close()
				if fromCB {
					if errors.Is(iterErr, errStopIter) {
						return nil
					}
					return iterErr
				}
				if iterErr == nil || count > 0 {
					return iterErr
				}
			} else {
				_ = f.Close()
			}
		}
	}

	if info.IsTar {
		cmd, closer := pipeCmdFor(info, file)
		if cmd != nil {
			if cmd.Stdin == nil {
				cmd.Stdin = getNullFile()
			}
			if stdout, err := cmd.StdoutPipe(); err == nil {
				if err := cmd.Start(); err == nil {
					count, iterErr, fromCB := iterFromTarReader(tar.NewReader(stdout), fn)
					var waitErr error
					if fromCB || iterErr != nil {
						stopSubprocess(cmd, stdout)
					} else {
						waitErr = cmd.Wait()
					}
					if closer != nil {
						_ = closer.Close()
						closer = nil
					}
					if fromCB {
						if errors.Is(iterErr, errStopIter) {
							return nil
						}
						return iterErr
					}
					if iterErr == nil && waitErr == nil {
						return nil
					}
					if count > 0 {
						if iterErr != nil {
							return iterErr
						}
						return waitErr
					}
				}
			}
			if closer != nil {
				_ = closer.Close()
			}
		}

		_, err, fromCB := iterTarFallback(file, fn)
		if fromCB {
			if errors.Is(err, errStopIter) {
				return nil
			}
			return err
		}
		return err
	}

	if info.Format == SevenZ || info.Format == Zip {
		return iterSevenZipDetailed(file, info.Format, password, fn)
	}

	if info.Format == Rar {
		if !hasTool(rarBin()) {
			return fmt.Errorf("herramienta %s no disponible", rarBin())
		}
		_, err, fromCB := iterRarStream(file, password, fn)
		if fromCB {
			if errors.Is(err, errStopIter) {
				return nil
			}
			return err
		}
		return err
	}

	if info.IsStream() && !info.IsTar {
		fi, err := os.Stat(file)
		if err != nil {
			return err
		}
		baseName := stripCompressionExt(filepath.Base(file))
		size := EstimateUncompressedSize(file)
		if size <= 0 {
			size = fi.Size()
		}
		err = fn(ArchiveMember{
			Path:  baseName,
			Size:  size,
			IsDir: false,
		})
		if errors.Is(err, errStopIter) {
			return nil
		}
		return err
	}

	return fmt.Errorf("no se pudo listar el contenido de %s", file)
}

func stopSubprocess(cmd *exec.Cmd, stdout io.ReadCloser) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if stdout != nil {
		_ = stdout.Close()
	}
	_ = cmd.Wait()
}

func streamCmdLines(cmd *exec.Cmd, onLine func(string) error) (error, bool) {
	if cmd.Stdin == nil {
		cmd.Stdin = getNullFile()
	}
	if cmd.Stderr == nil {
		cmd.Stderr = newLimitedBuffer(maxStderrBytes)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err, false
	}
	if err := cmd.Start(); err != nil {
		return err, false
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, listScannerInitBuf), listScannerMaxBuf)
	for sc.Scan() {
		if cbErr := onLine(sc.Text()); cbErr != nil {
			stopSubprocess(cmd, stdout)
			return cbErr, true
		}
	}
	scanErr := sc.Err()
	waitErr := augmentErr(cmd, cmd.Wait())
	if scanErr != nil {
		return scanErr, false
	}
	return waitErr, false
}

func iterLineMembers(cmd *exec.Cmd, fn func(ArchiveMember) error) (int, error, bool) {
	count := 0
	err, fromCB := streamCmdLines(cmd, func(raw string) error {
		line := strings.TrimSpace(raw)
		if line == "" {
			return nil
		}
		count++
		return fn(ArchiveMember{
			Path:  line,
			Size:  0,
			IsDir: strings.HasSuffix(line, "/"),
		})
	})
	if fromCB {
		return count, err, true
	}
	if err != nil {
		return count, err, false
	}
	return count, nil, false
}

func iterTarFallback(file string, fn func(ArchiveMember) error) (int, error, bool) {
	lower := strings.ToLower(file)
	args := []string{"-tf"}
	switch {
	case strings.HasSuffix(lower, ".tar.bz3"):
		args = []string{"-I", "bzip3 -dc", "-tf"}
	case strings.HasSuffix(lower, ".tar.br"):
		args = []string{"-I", "brotli", "-tf"}
	case strings.HasSuffix(lower, ".tar.lrz"):
		args = []string{"-I", "lrzip -d -p 1 -o -", "-tf"}
	case strings.HasSuffix(lower, ".tar.lz4"):
		args = []string{"-I", "lz4 -dc", "-tf"}
	}
	args = append(args, safeFlagArg(file), "--")
	return iterLineMembers(execCommand("tar", args...), fn)
}

func iterRarStream(file string, password string, fn func(ArchiveMember) error) (int, error, bool) {
	tool := rarBin()
	if !hasTool(tool) {
		return 0, nil, false
	}
	args := []string{"lb"}
	if password != "" {
		args = append(args, "-p"+password)
	} else {
		args = append(args, "-p-")
	}
	args = append(args, "--", safeFlagArg(file))
	return iterLineMembers(execCommand(tool, args...), fn)
}

func iterFromTarReader(tr *tar.Reader, fn func(ArchiveMember) error) (int, error, bool) {
	count := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return count, nil, false
		}
		if err != nil {
			return count, err, false
		}
		count++
		isDir := hdr.Typeflag == tar.TypeDir || strings.HasSuffix(hdr.Name, "/")
		if cbErr := fn(ArchiveMember{
			Path:     hdr.Name,
			Size:     hdr.Size,
			Modified: hdr.ModTime,
			Mode:     hdr.FileInfo().Mode(),
			IsDir:    isDir,
		}); cbErr != nil {
			return count, cbErr, true
		}
	}
}

func iterSevenZipDetailed(file string, format Format, password string, fn func(ArchiveMember) error) error {
	sevenz := sevenzBin()
	if !hasTool(sevenz) {
		return fmt.Errorf("herramienta 7z no disponible")
	}
	args := []string{"l", "-slt", "-ba"}
	if password != "" {
		args = append(args, "-p"+password)
	} else {
		args = append(args, "-p-")
	}
	args = append(args, "--", safeFlagArg(file))

	var cur ArchiveMember
	emitted := 0
	emit := func() error {
		if cur.Path == "" {
			return nil
		}
		m := cur
		cur = ArchiveMember{}
		emitted++
		return fn(m)
	}

	err, fromCB := streamCmdLines(execCommand(sevenz, args...), func(raw string) error {
		line := strings.TrimSpace(raw)
		if line == "" {
			return emit()
		}
		switch {
		case strings.HasPrefix(line, "Path = "):
			cur.Path = strings.TrimPrefix(line, "Path = ")
		case strings.HasPrefix(line, "Size = "):
			cur.Size, _ = strconv.ParseInt(strings.TrimPrefix(line, "Size = "), 10, 64)
		case strings.HasPrefix(line, "Modified = "):
			modStr := strings.TrimPrefix(line, "Modified = ")
			if t, parseErr := time.ParseInLocation("2006-01-02 15:04:05", modStr, time.Local); parseErr == nil {
				cur.Modified = t
			}
		case strings.HasPrefix(line, "Folder = "):
			cur.IsDir = strings.TrimPrefix(line, "Folder = ") == "+"
		case strings.HasPrefix(line, "Attributes = "):
			attrs := strings.TrimPrefix(line, "Attributes = ")
			for _, field := range strings.Fields(attrs) {
				if field == "D" || (len(field) >= 1 && field[0] == 'd' && len(field) >= 10) {
					cur.IsDir = true
				}
				if len(field) >= 10 && field[0] == 'l' {
					cur.Mode |= os.ModeSymlink
				}
			}
			if strings.Contains(attrs, "D") {
				cur.IsDir = true
			}
		}
		return nil
	})
	if fromCB {
		if errors.Is(err, errStopIter) {
			return nil
		}
		return err
	}
	if err == nil {
		if cbErr := emit(); cbErr != nil {
			if errors.Is(cbErr, errStopIter) {
				return nil
			}
			return cbErr
		}
	}

	if emitted == 0 {
		if password == "" {
			if resolved, resolveErr := GetPasswordManager().ResolvePassword(file, format, ""); resolveErr == nil && resolved != "" {
				return iterSevenZipDetailed(file, format, resolved, fn)
			}
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("no se encontraron miembros en %s", file)
	}
	return err
}
