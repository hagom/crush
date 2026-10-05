package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// DoDiff compares members of two archives and prints additions, deletions, and modifications.
func DoDiff(file1, file2 string, password string, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	if file1 == "" || file2 == "" {
		return fmt.Errorf("debe especificar dos archivos comprimidos para comparar: crush -diff ARCHIVO1 ARCHIVO2")
	}

	members1, err := ListArchiveMembers(file1, password)
	if err != nil {
		return fmt.Errorf("listando miembros de %s: %w", file1, err)
	}
	members2, err := ListArchiveMembers(file2, password)
	if err != nil {
		return fmt.Errorf("listando miembros de %s: %w", file2, err)
	}

	map1 := make(map[string]ArchiveMember)
	for _, m := range members1 {
		p := strings.TrimPrefix(m.Path, "/")
		map1[p] = m
	}

	map2 := make(map[string]ArchiveMember)
	for _, m := range members2 {
		p := strings.TrimPrefix(m.Path, "/")
		map2[p] = m
	}

	allKeys := make(map[string]bool)
	for k := range map1 {
		allKeys[k] = true
	}
	for k := range map2 {
		allKeys[k] = true
	}

	sortedKeys := make([]string, 0, len(allKeys))
	for k := range allKeys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	fmt.Fprintf(out, "%s=== Comparación: %s ↔ %s ===%s\n", Bold, file1, file2, NC)

	addedCount := 0
	removedCount := 0
	modifiedCount := 0
	unchangedCount := 0

	for _, k := range sortedKeys {
		m1, in1 := map1[k]
		m2, in2 := map2[k]

		if in1 && !in2 {
			removedCount++
			if m1.IsDir {
				fmt.Fprintf(out, "  %s- %s/%s\n", Red, k, NC)
			} else {
				fmt.Fprintf(out, "  %s- %s (-%s)%s\n", Red, k, FormatSize(m1.Size), NC)
			}
		} else if !in1 && in2 {
			addedCount++
			if m2.IsDir {
				fmt.Fprintf(out, "  %s+ %s/%s\n", Green, k, NC)
			} else {
				fmt.Fprintf(out, "  %s+ %s (+%s)%s\n", Green, k, FormatSize(m2.Size), NC)
			}
		} else {
			if m1.IsDir && m2.IsDir {
				unchangedCount++
			} else if m1.Size != m2.Size {
				modifiedCount++
				diff := m2.Size - m1.Size
				diffStr := "+" + FormatSize(diff)
				if diff < 0 {
					diffStr = "-" + FormatSize(-diff)
				}
				fmt.Fprintf(out, "  %s~ %s (%s → %s, %s)%s\n", Yellow, k, FormatSize(m1.Size), FormatSize(m2.Size), diffStr, NC)
			} else {
				unchangedCount++
			}
		}
	}

	fmt.Fprintf(out, "\nResumen: +%d añadido(s), -%d eliminado(s), ~%d modificado(s), %d sin cambios\n",
		addedCount, removedCount, modifiedCount, unchangedCount)

	return nil
}
