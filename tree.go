package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

type treeNode struct {
	name     string
	size     int64
	isDir    bool
	children map[string]*treeNode
}

func newTreeNode(name string, isDir bool) *treeNode {
	return &treeNode{
		name:     name,
		isDir:    isDir,
		children: make(map[string]*treeNode),
	}
}

// DoTree generates a hierarchical ASCII/Unicode tree view of archive members.
func DoTree(files []string, password string, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}

	for i, file := range files {
		if i > 0 {
			fmt.Fprintln(out)
		}

		root := newTreeNode("", true)
		var totalSize int64
		var fileCount int
		var dirCount int

		err := IterArchiveMembers(file, password, func(m ArchiveMember) error {
			cleanPath := normalizeArchiveMemberKey(m.Path)
			if cleanPath == "" {
				return nil
			}
			parts := strings.Split(cleanPath, "/")
			curr := root
			for idx, rawPart := range parts {
				if rawPart == "" || rawPart == "." {
					continue
				}
				part := sanitizeTerminalText(rawPart)
				isLast := idx == len(parts)-1
				isPartDir := !isLast || m.IsDir
				child, exists := curr.children[part]
				if !exists {
					child = newTreeNode(part, isPartDir)
					curr.children[part] = child
				} else if isPartDir {
					child.isDir = true
				}
				if isLast {
					child.size = m.Size
					if m.IsDir || len(child.children) > 0 {
						child.isDir = true
					} else {
						child.isDir = false
					}
				}
				curr = child
			}
			if m.IsDir {
				dirCount++
			} else {
				fileCount++
				totalSize += m.Size
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(out, "%s✗ Error listando %s: %v%s\n", Red, file, err, NC)
			continue
		}

		fmt.Fprintf(out, "%s%s%s\n", Bold, sanitizeTerminalText(file), NC)
		renderTree(out, root, "")

		dirWord := "directorios"
		if dirCount == 1 {
			dirWord = "directorio"
		}
		fileWord := "archivos"
		if fileCount == 1 {
			fileWord = "archivo"
		}
		fmt.Fprintf(out, "\n%d %s, %d %s (Descomprimido: %s)\n", dirCount, dirWord, fileCount, fileWord, FormatSize(totalSize))
	}

	return nil
}

func renderTree(out io.Writer, node *treeNode, prefix string) {
	names := make([]string, 0, len(node.children))
	for name := range node.children {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		childI := node.children[names[i]]
		childJ := node.children[names[j]]
		if childI.isDir != childJ.isDir {
			return childI.isDir
		}
		return names[i] < names[j]
	})

	for i, name := range names {
		child := node.children[name]
		isLast := i == len(names)-1
		branch := "├── "
		nextPrefix := prefix + "│   "
		if isLast {
			branch = "└── "
			nextPrefix = prefix + "    "
		}

		if child.isDir {
			fmt.Fprintf(out, "%s%s%s%s/%s\n", prefix, branch, BoldBlue, child.name, NC)
			renderTree(out, child, nextPrefix)
		} else {
			fmt.Fprintf(out, "%s%s%s (%s)\n", prefix, branch, child.name, FormatSize(child.size))
		}
	}
}
