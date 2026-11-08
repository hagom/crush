package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// DoList lista de forma visual y legible para humanos el contenido
// de un archivo comprimido, formateando tamaños métricos, permisos,
// fecha/hora y nombre, junto con resumen de compresión.
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

	members, err := ListArchiveMembers(archivePath, password)
	if err != nil {
		return err
	}

	return RenderListTable(archivePath, members, info, fi, out)
}

// RenderListTable formatea e imprime la tabla de listado en formato human-readable.
func RenderListTable(archivePath string, members []ArchiveMember, info FormatInfo, fi os.FileInfo, out io.Writer) error {
	base := filepath.Base(archivePath)
	compSizeStr := FormatSize(fi.Size())
	formatName := info.Format.String()

	// Encabezado
	fmt.Fprintf(out, "\n%s=== Contenido: %s (%s, %s) ===%s\n\n", Bold, base, formatName, compSizeStr, NC)
	fmt.Fprintf(out, "  %-12s %10s  %-16s  %s\n", "Permisos", "Tamaño", "Fecha / Hora", "Nombre")
	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 78))

	var totalUncompressed int64
	var fileCount, dirCount int

	for _, m := range members {
		modeStr := "-rw-r--r--"
		if m.IsDir {
			modeStr = "drwxr-xr-x"
			dirCount++
		} else {
			fileCount++
			totalUncompressed += m.Size
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

		displayPath := m.Path
		if m.IsDir {
			displayPath = Blue + Bold + m.Path + NC
		}

		fmt.Fprintf(out, "  %-12s %10s  %-16s  %s\n", modeStr, sizeStr, dateStr, displayPath)
	}

	fmt.Fprintf(out, "  %s\n", strings.Repeat("─", 78))

	// Resumen de pie
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
		savings := float64(totalUncompressed-fi.Size()) / float64(totalUncompressed) * 100
		if savings > 0 {
			ratioStr = fmt.Sprintf(" (Ahorro: %.1f%%)", savings)
		} else {
			ratioStr = fmt.Sprintf(" (Ratio: %.1f%%)", float64(fi.Size())/float64(totalUncompressed)*100)
		}
	}

	fmt.Fprintf(out, "  %s%s | Total sin comprimir: %s%s%s\n\n",
		Green+Bold, strings.Join(summaryParts, ", "), FormatSize(totalUncompressed), ratioStr, NC)

	return nil
}
