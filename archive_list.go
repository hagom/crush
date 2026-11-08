package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ArchiveMember struct {
	Path     string
	Size     int64
	Modified time.Time
	Mode     os.FileMode
	IsDir    bool
}

// ListArchiveMembers returns a list of members in the given archive.
func ListArchiveMembers(file string, password string) ([]ArchiveMember, error) {
	info, err := DetectFormat(file)
	if err != nil {
		return nil, fmt.Errorf("detectando formato de %s: %w", file, err)
	}

	lower := strings.ToLower(file)

	// 1. Standard library Zip reader
	if info.Format == Zip && password == "" {
		if r, err := zip.OpenReader(file); err == nil {
			defer r.Close()
			var members []ArchiveMember
			for _, f := range r.File {
				members = append(members, ArchiveMember{
					Path:     f.Name,
					Size:     int64(f.UncompressedSize64),
					Modified: f.Modified,
					Mode:     f.Mode(),
					IsDir:    f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/"),
				})
			}
			return members, nil
		}
	}

	// 2. Standard library uncompressed Tar
	if info.Format == Tar && !info.IsStream() {
		f, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return listFromTarReader(tar.NewReader(f))
	}

	// 3. Standard library .tar.gz / .tgz (fastest, no subprocess)
	if (strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz")) && password == "" {
		f, err := os.Open(file)
		if err == nil {
			defer f.Close()
			gzr, err := gzip.NewReader(f)
			if err == nil {
				defer gzr.Close()
				members, err := listFromTarReader(tar.NewReader(gzr))
				if err == nil && len(members) > 0 {
					return members, nil
				}
			}
		}
	}

	// 4. Other tar.* formats using pipeCmdFor into tar.NewReader
	if info.IsTar {
		cmd, closer := pipeCmdFor(info, file)
		if cmd != nil {
			stdout, err := cmd.StdoutPipe()
			if err == nil {
				if err := cmd.Start(); err == nil {
					members, _ := listFromTarReader(tar.NewReader(stdout))
					_ = cmd.Wait()
					if closer != nil {
						closer.Close()
					}
					if len(members) > 0 {
						return members, nil
					}
				}
			}
			if closer != nil {
				closer.Close()
			}
		}

		// Fallback to tar -tf if streaming failed
		if members, ok := listTarMembers(file); ok {
			var res []ArchiveMember
			for _, m := range members {
				isDir := strings.HasSuffix(m, "/")
				res = append(res, ArchiveMember{
					Path:  m,
					Size:  0,
					IsDir: isDir,
				})
			}
			return res, nil
		}
	}

	// 5. 7z and password-protected Zip
	if info.Format == SevenZ || info.Format == Zip {
		return listSevenZipDetailed(file, password)
	}

	// 6. Rar
	if info.Format == Rar {
		if members, ok := listRarMembers(file, password); ok {
			var res []ArchiveMember
			for _, m := range members {
				res = append(res, ArchiveMember{
					Path:  m,
					Size:  0,
					IsDir: strings.HasSuffix(m, "/"),
				})
			}
			return res, nil
		}
	}

	// 7. Single stream files (.gz, .xz, .zst, etc.)
	if info.IsStream() && !info.IsTar {
		baseName := stripCompressionExt(filepath.Base(file))
		size := EstimateUncompressedSize(file)
		if size <= 0 {
			if fi, err := os.Stat(file); err == nil {
				size = fi.Size()
			}
		}
		return []ArchiveMember{
			{
				Path:  baseName,
				Size:  size,
				IsDir: false,
			},
		}, nil
	}

	return nil, fmt.Errorf("no se pudo listar el contenido de %s", file)
}

func listFromTarReader(tr *tar.Reader) ([]ArchiveMember, error) {
	var members []ArchiveMember
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return members, err
		}
		isDir := hdr.Typeflag == tar.TypeDir || strings.HasSuffix(hdr.Name, "/")
		members = append(members, ArchiveMember{
			Path:     hdr.Name,
			Size:     hdr.Size,
			Modified: hdr.ModTime,
			Mode:     hdr.FileInfo().Mode(),
			IsDir:    isDir,
		})
	}
	return members, nil
}

func listSevenZipDetailed(file string, password string) ([]ArchiveMember, error) {
	sevenz := sevenzBin()
	if !hasTool(sevenz) {
		return nil, fmt.Errorf("herramienta 7z no disponible")
	}
	args := []string{"l", "-slt", "-ba"}
	if password != "" {
		args = append(args, "-p"+password)
	} else {
		args = append(args, "-p-")
	}
	args = append(args, "--", file)

	cmd := execCommand(sevenz, args...)
	cmd.Stdin = getNullFile()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var members []ArchiveMember
	var currentPath string
	var currentSize int64
	var currentModified time.Time
	var currentMode os.FileMode
	var currentIsDir bool

	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			if currentPath != "" {
				members = append(members, ArchiveMember{
					Path:     currentPath,
					Size:     currentSize,
					Modified: currentModified,
					Mode:     currentMode,
					IsDir:    currentIsDir,
				})
				currentPath = ""
				currentSize = 0
				currentModified = time.Time{}
				currentMode = 0
				currentIsDir = false
			}
			continue
		}
		if strings.HasPrefix(line, "Path = ") {
			currentPath = strings.TrimPrefix(line, "Path = ")
		} else if strings.HasPrefix(line, "Size = ") {
			s, _ := strconv.ParseInt(strings.TrimPrefix(line, "Size = "), 10, 64)
			currentSize = s
		} else if strings.HasPrefix(line, "Modified = ") {
			modStr := strings.TrimPrefix(line, "Modified = ")
			if t, err := time.ParseInLocation("2006-01-02 15:04:05", modStr, time.Local); err == nil {
				currentModified = t
			}
		} else if strings.HasPrefix(line, "Folder = ") {
			currentIsDir = strings.TrimPrefix(line, "Folder = ") == "+"
		} else if strings.HasPrefix(line, "Attributes = ") {
			attrs := strings.TrimPrefix(line, "Attributes = ")
			if strings.Contains(attrs, "D") {
				currentIsDir = true
			}
		}
	}
	if currentPath != "" {
		members = append(members, ArchiveMember{
			Path:     currentPath,
			Size:     currentSize,
			Modified: currentModified,
			Mode:     currentMode,
			IsDir:    currentIsDir,
		})
	}
	_ = cmd.Wait()

	if len(members) == 0 {
		if password == "" {
			if resolved, err := GetPasswordManager().ResolvePassword(file, SevenZ, ""); err == nil && resolved != "" {
				return listSevenZipDetailed(file, resolved)
			}
		}
		return nil, fmt.Errorf("no se encontraron miembros en %s", file)
	}
	return members, nil
}
