package main

import (
	"fmt"
	"io"
	"os"
	"sort"
)

func collectDiffMemberMap(file, password string) (map[string]ArchiveMember, error) {
	mMap := make(map[string]ArchiveMember)
	err := IterArchiveMembers(file, password, func(m ArchiveMember) error {
		k := normalizeArchiveMemberKey(m.Path)
		if k == "" {
			return nil
		}
		if prev, exists := mMap[k]; exists && prev.IsDir {
			m.IsDir = true
		}
		mMap[k] = m
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listando miembros de %s: %w", file, err)
	}
	return mMap, nil
}

// DoDiff compares members of two archives and prints additions, deletions, and modifications.
func DoDiff(file1, file2 string, password string, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	if file1 == "" || file2 == "" {
		return fmt.Errorf("debe especificar dos archivos comprimidos para comparar: crush -diff ARCHIVO1 ARCHIVO2")
	}

	map1, err := collectDiffMemberMap(file1, password)
	if err != nil {
		return err
	}
	map2, err := collectDiffMemberMap(file2, password)
	if err != nil {
		return err
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

	fmt.Fprintf(out, "%s=== Comparación: %s ↔ %s ===%s\n", Bold, sanitizeTerminalText(file1), sanitizeTerminalText(file2), NC)

	addedCount := 0
	removedCount := 0
	modifiedCount := 0
	unchangedCount := 0

	for _, k := range sortedKeys {
		m1, in1 := map1[k]
		m2, in2 := map2[k]
		displayKey := sanitizeTerminalText(k)

		if in1 && !in2 {
			removedCount++
			if m1.IsDir {
				fmt.Fprintf(out, "  %s- %s/%s\n", Red, displayKey, NC)
			} else {
				fmt.Fprintf(out, "  %s- %s (-%s)%s\n", Red, displayKey, FormatSize(m1.Size), NC)
			}
		} else if !in1 && in2 {
			addedCount++
			if m2.IsDir {
				fmt.Fprintf(out, "  %s+ %s/%s\n", Green, displayKey, NC)
			} else {
				fmt.Fprintf(out, "  %s+ %s (+%s)%s\n", Green, displayKey, FormatSize(m2.Size), NC)
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
				fmt.Fprintf(out, "  %s~ %s (%s → %s, %s)%s\n", Yellow, displayKey, FormatSize(m1.Size), FormatSize(m2.Size), diffStr, NC)
			} else {
				unchangedCount++
			}
		}
	}

	fmt.Fprintf(out, "\nResumen: +%d añadido(s), -%d eliminado(s), ~%d modificado(s), %d sin cambios\n",
		addedCount, removedCount, modifiedCount, unchangedCount)

	return nil
}
