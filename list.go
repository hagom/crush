package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func DoList(archivePath string, password string, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}

	fi, err := os.Stat(archivePath)
	if err != nil {
		return fmt.Errorf("accediendo a %s: %w", archivePath, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("%s es un directorio", archivePath)
	}

	info, err := DetectFormat(archivePath)
	if err != nil {
		return fmt.Errorf("detectando formato de %s: %w", archivePath, err)
	}

	var totalUncompressed int64
	var fileCount, dirCount int
	headerWritten := false

	err = IterArchiveMembers(archivePath, password, func(m ArchiveMember) error {
		if !headerWritten {
			writeListHeader(out, archivePath, info, fi)
			headerWritten = true
		}
		if m.IsDir {
			dirCount++
		} else {
			fileCount++
			totalUncompressed += m.Size
		}
		writeListRow(out, m)
		return nil
	})
	if err != nil {
		return err
	}

	if !headerWritten {
		writeListHeader(out, archivePath, info, fi)
	}
	writeListFooter(out, fileCount, dirCount, totalUncompressed, fi.Size())
	return nil
}

func RenderListTable(archivePath string, members []ArchiveMember, info FormatInfo, fi os.FileInfo, out io.Writer) error {
	writeListHeader(out, archivePath, info, fi)

	var totalUncompressed int64
	var fileCount, dirCount int

	for _, m := range members {
		if m.IsDir {
			dirCount++
		} else {
			fileCount++
			totalUncompressed += m.Size
		}
		writeListRow(out, m)
	}

	writeListFooter(out, fileCount, dirCount, totalUncompressed, fi.Size())
	return nil
}

func writeListHeader(out io.Writer, archivePath string, info FormatInfo, fi os.FileInfo) {
	base := sanitizeTerminalText(filepath.Base(archivePath))
	compSizeStr := FormatSize(fi.Size())
	formatName := info.Format.String()

	fmt.Fprintf(out, "\n%s=== Contenido: %s (%s, %s) ===%s\n\n", Bold, base, formatName, compSizeStr, NC)
	fmt.Fprintf(out, "  %-12s %10s  %-16s  %s\n", "Permisos", "Tamaño", "Fecha / Hora", "Nombre")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 78))
}

func writeListRow(out io.Writer, m ArchiveMember) {
	modeStr := "-rw-r--r--"
	if m.IsDir {
		modeStr = "drwxr-xr-x"
	}
	if m.Mode != 0 {
		modeStr = m.Mode.String()
	}

	sizeStr := "-"
	if !m.IsDir {
		sizeStr = FormatSize(m.Size)
	}

	dateStr := "                "
	if !m.Modified.IsZero() {
		dateStr = m.Modified.Format("2006-01-02 15:04")
	}

	displayPath := sanitizeTerminalText(m.Path)
	if m.IsDir {
		displayPath = Blue + Bold + displayPath + NC
	}

	fmt.Fprintf(out, "  %-12s %10s  %-16s  %s\n", modeStr, sizeStr, dateStr, displayPath)
}

func writeListFooter(out io.Writer, fileCount, dirCount int, totalUncompressed, compSize int64) {
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 78))

	var summaryParts []string
	if fileCount == 1 {
		summaryParts = append(summaryParts, "1 archivo")
	} else {
		summaryParts = append(summaryParts, fmt.Sprintf("%d archivos", fileCount))
	}
	if dirCount > 0 {
		if dirCount == 1 {
			summaryParts = append(summaryParts, "1 carpeta")
		} else {
			summaryParts = append(summaryParts, fmt.Sprintf("%d carpetas", dirCount))
		}
	}

	ratioStr := ""
	if totalUncompressed > 0 {
		savings := float64(totalUncompressed-compSize) / float64(totalUncompressed) * 100
		if savings > 0 {
			ratioStr = fmt.Sprintf(" (Ahorro: %.1f%%)", savings)
		} else {
			ratioStr = fmt.Sprintf(" (Ratio: %.1f%%)", float64(compSize)/float64(totalUncompressed)*100)
		}
	}

	fmt.Fprintf(out, "  %s%s | Total sin comprimir: %s%s%s\n\n",
		Green+Bold, strings.Join(summaryParts, ", "), FormatSize(totalUncompressed), ratioStr, NC)
}
