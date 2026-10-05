# New Features Suite Implementation Plan (Convert, Tree, Find, Diff, Clean, Notify)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement 6 high-value features for crush: Transcoding (`-convert`), Tree View (`-tree`), Batch Search (`-find`), Archive Diff (`-diff`), Clean Sanitization (`-clean`), and Desktop Notifications (`-notify`), plus documenting Entropy Detection in `TODO.md`.

**Architecture:** Pure Go stdlib with zero external dependencies. Features are decoupled into dedicated SRP units (`archive_list.go`, `tree.go`, `find.go`, `diff.go`, `convert.go`, `notify.go`). Shared archive member inspection serves as the foundation for Tree, Find, and Diff. All external processes run through the injectable `execCommand` abstraction for 100% testability.

**Tech Stack:** Go 1.21+ stdlib (`os`, `io`, `path/filepath`, `exec`, `strings`, `sync`, `time`, `fmt`).

## Global Constraints

- 100% pure Go standard library (no external dependencies in `go.mod`).
- TDD required: failing test (RED) -> minimal implementation (GREEN) -> refactor -> commit.
- Verification commands before completion: `go test -race ./...` and `go vet ./...` must pass with 0 errors.
- External commands must be invoked via `execCommand` so unit tests can mock them.
- Preserve single-instance lock (`/tmp/crush.lock`) for mutating operations (`-convert`). Read-only operations (`-tree`, `-find`, `-diff`) are exempt.

---

### Task 1: Unified Archive Member Listing (`archive_list.go`)

**Files:**
- Create: `archive_list.go`
- Create: `archive_list_test.go`

**Interfaces:**
- Produces:
  ```go
  type ArchiveMember struct {
      Path  string
      Size  int64
      IsDir bool
  }
  func ListArchiveMembers(file string, password string) ([]ArchiveMember, error)
  ```

- [ ] **Step 1: Write the failing test in `archive_list_test.go`**

```go
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestListArchiveMembersTar(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "sample.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	hdr1 := &tar.Header{Name: "dir/", Mode: 0755, Typeflag: tar.TypeDir}
	tw.WriteHeader(hdr1)
	hdr2 := &tar.Header{Name: "dir/hello.txt", Mode: 0644, Size: 12, Typeflag: tar.TypeReg}
	tw.WriteHeader(hdr2)
	tw.Write([]byte("hello world\n"))
	tw.Close()
	f.Close()

	members, err := ListArchiveMembers(tarPath, "")
	if err != nil {
		t.Fatalf("ListArchiveMembers failed: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("got %d members, want 2", len(members))
	}
	if members[0].Path != "dir/" || !members[0].IsDir {
		t.Errorf("member[0] mismatch: %+v", members[0])
	}
	if members[1].Path != "dir/hello.txt" || members[1].Size != 12 || members[1].IsDir {
		t.Errorf("member[1] mismatch: %+v", members[1])
	}
}

func TestListArchiveMembersZip(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "sample.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("doc.txt")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("documentation"))
	zw.Close()
	f.Close()

	members, err := ListArchiveMembers(zipPath, "")
	if err != nil {
		t.Fatalf("ListArchiveMembers failed: %v", err)
	}
	if len(members) != 1 || members[0].Path != "doc.txt" || members[0].Size != 13 {
		t.Errorf("zip member mismatch: %+v", members)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestListArchiveMembers`
Expected: FAIL with `undefined: ListArchiveMembers`

- [ ] **Step 3: Implement `archive_list.go`**

Implement `ListArchiveMembers` using Go stdlib `archive/tar`, `archive/zip`, and external tool fallbacks (`7z`, `rar`, compressed `tar.*`) with `execCommand`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v -run TestListArchiveMembers .`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add archive_list.go archive_list_test.go
git commit -m "feat(inspect): unificar extraccion de metadatos de miembros de archivos con ListArchiveMembers"
```

---

### Task 2: Tree View Feature (`tree.go` & CLI `-tree`)

**Files:**
- Create: `tree.go`
- Create: `tree_test.go`
- Modify: `main.go`

**Interfaces:**
- Consumes: `ListArchiveMembers(file, password string) ([]ArchiveMember, error)`
- Produces: `func DoTree(files []string, password string, out io.Writer) error`

- [ ] **Step 1: Write the failing test in `tree_test.go`**

```go
package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoTreeHierarchy(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "tree_test.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	tw.WriteHeader(&tar.Header{Name: "src/", Mode: 0755, Typeflag: tar.TypeDir})
	tw.WriteHeader(&tar.Header{Name: "src/main.go", Mode: 0644, Size: 100, Typeflag: tar.TypeReg})
	tw.Write(make([]byte, 100))
	tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0644, Size: 50, Typeflag: tar.TypeReg})
	tw.Write(make([]byte, 50))
	tw.Close()
	f.Close()

	var buf bytes.Buffer
	err = DoTree([]string{tarPath}, "", &buf)
	if err != nil {
		t.Fatalf("DoTree failed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "tree_test.tar") {
		t.Errorf("missing header in output: %s", out)
	}
	if !strings.Contains(out, "src") || !strings.Contains(out, "main.go") {
		t.Errorf("missing tree nodes in output: %s", out)
	}
	if !strings.Contains(out, "1 directorio") || !strings.Contains(out, "2 archivo") {
		t.Errorf("missing summary in output: %s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestDoTreeHierarchy`
Expected: FAIL with `undefined: DoTree`

- [ ] **Step 3: Implement `tree.go` and wire into `main.go`**

Implement node tree parser, indentation formatting (`├──`, `└──`, `│   `), color output, and summary line. Wire `-tree` flag into `main.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v -run TestDoTree .`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tree.go tree_test.go main.go
git commit -m "feat(tree): agregar vista jerarquica en arbol con flag -tree"
```

---

### Task 3: Batch Search in Archives (`find.go` & CLI `-find`)

**Files:**
- Create: `find.go`
- Create: `find_test.go`
- Modify: `main.go`

**Interfaces:**
- Consumes: `ListArchiveMembers`
- Produces: `func DoFind(pattern string, files []string, password string, out io.Writer) (int, error)`

- [ ] **Step 1: Write the failing test in `find_test.go`**

```go
package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoFindMatches(t *testing.T) {
	tmpDir := t.TempDir()
	tar1 := filepath.Join(tmpDir, "backup1.tar")
	f1, _ := os.Create(tar1)
	tw1 := tar.NewWriter(f1)
	tw1.WriteHeader(&tar.Header{Name: "database.sql", Mode: 0644, Size: 1024, Typeflag: tar.TypeReg})
	tw1.WriteHeader(&tar.Header{Name: "index.html", Mode: 0644, Size: 200, Typeflag: tar.TypeReg})
	tw1.Close()
	f1.Close()

	var buf bytes.Buffer
	matches, err := DoFind("*.sql", []string{tar1}, "", &buf)
	if err != nil {
		t.Fatalf("DoFind failed: %v", err)
	}
	if matches != 1 {
		t.Errorf("got %d matches, want 1", matches)
	}
	out := buf.String()
	if !strings.Contains(out, "database.sql") || strings.Contains(out, "index.html") {
		t.Errorf("unexpected output: %s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestDoFindMatches`
Expected: FAIL with `undefined: DoFind`

- [ ] **Step 3: Implement `find.go` and wire into `main.go`**

Implement concurrent search over archives matching filenames or paths via pattern matching (`filepath.Match` and case-insensitive substring), formatting output with highlighted matches. Wire `-find` flag into `main.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v -run TestDoFind .`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add find.go find_test.go main.go
git commit -m "feat(find): agregar busqueda de archivos en lotes de comprimidos con -find"
```

---

### Task 4: Archive Comparison (`diff.go` & CLI `-diff`)

**Files:**
- Create: `diff.go`
- Create: `diff_test.go`
- Modify: `main.go`

**Interfaces:**
- Consumes: `ListArchiveMembers`
- Produces: `func DoDiff(file1, file2 string, password string, out io.Writer) error`

- [ ] **Step 1: Write the failing test in `diff_test.go`**

```go
package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoDiffComparison(t *testing.T) {
	tmpDir := t.TempDir()
	tar1 := filepath.Join(tmpDir, "v1.tar")
	f1, _ := os.Create(tar1)
	tw1 := tar.NewWriter(f1)
	tw1.WriteHeader(&tar.Header{Name: "common.txt", Mode: 0644, Size: 10, Typeflag: tar.TypeReg})
	tw1.WriteHeader(&tar.Header{Name: "removed.txt", Mode: 0644, Size: 20, Typeflag: tar.TypeReg})
	tw1.WriteHeader(&tar.Header{Name: "modified.txt", Mode: 0644, Size: 30, Typeflag: tar.TypeReg})
	tw1.Close()
	f1.Close()

	tar2 := filepath.Join(tmpDir, "v2.tar")
	f2, _ := os.Create(tar2)
	tw2 := tar.NewWriter(f2)
	tw2.WriteHeader(&tar.Header{Name: "common.txt", Mode: 0644, Size: 10, Typeflag: tar.TypeReg})
	tw2.WriteHeader(&tar.Header{Name: "added.txt", Mode: 0644, Size: 40, Typeflag: tar.TypeReg})
	tw2.WriteHeader(&tar.Header{Name: "modified.txt", Mode: 0644, Size: 50, Typeflag: tar.TypeReg})
	tw2.Close()
	f2.Close()

	var buf bytes.Buffer
	err := DoDiff(tar1, tar2, "", &buf)
	if err != nil {
		t.Fatalf("DoDiff failed: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "+ added.txt") {
		t.Errorf("missing added item in output: %s", out)
	}
	if !strings.Contains(out, "- removed.txt") {
		t.Errorf("missing removed item in output: %s", out)
	}
	if !strings.Contains(out, "~ modified.txt") {
		t.Errorf("missing modified item in output: %s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestDoDiffComparison`
Expected: FAIL with `undefined: DoDiff`

- [ ] **Step 3: Implement `diff.go` and wire into `main.go`**

Implement set diffing between member maps, formatting additions (`+` green), deletions (`-` red), and modifications (`~` yellow) with delta size, plus summary count. Wire `-diff` flag into `main.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v -run TestDoDiff .`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add diff.go diff_test.go main.go
git commit -m "feat(diff): anadir comparador de archivos comprimidos con flag -diff"
```

---

### Task 5: Clean Sanitization Mode (`-clean` / `--clean`)

**Files:**
- Modify: `compress.go`
- Modify: `main.go`
- Modify: `compress_test.go`

**Interfaces:**
- Produces: `DefaultCleanExcludes []string` and `opts.Clean bool`

- [ ] **Step 1: Write the failing test in `compress_test.go`**

```go
func TestCompressCleanSanitization(t *testing.T) {
	origExec := execCommand
	defer func() { execCommand = origExec }()

	var capturedArgs []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "tar" {
			capturedArgs = append([]string(nil), args...)
		}
		return origExec(name, args...)
	}

	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "data.txt")
	_ = os.WriteFile(f1, []byte("content"), 0644)

	opts := CompressOptions{
		Format:    Tar,
		Clean:     true,
		OutputDir: tmpDir,
		KeepOrig:  true,
	}
	_, err := DoCompress([]string{f1}, opts)
	if err != nil {
		t.Fatalf("DoCompress with Clean failed: %v", err)
	}

	foundGit := false
	foundDSStore := false
	for _, a := range capturedArgs {
		if strings.Contains(a, ".git") {
			foundGit = true
		}
		if strings.Contains(a, ".DS_Store") {
			foundDSStore = true
		}
	}
	if !foundGit || !foundDSStore {
		t.Errorf("clean mode did not inject default excludes, got args: %v", capturedArgs)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestCompressCleanSanitization`
Expected: FAIL with `unknown field Clean in struct literal of type CompressOptions`

- [ ] **Step 3: Implement `DefaultCleanExcludes` and inject into `opts.Exclude`**

Add `Clean bool` to `CompressOptions`. When `opts.Clean` is true, inject OS/VCS/cache exclusions (`*.DS_Store`, `Thumbs.db`, `.git`, `node_modules`, `__pycache__`, etc.). Wire `-clean` / `--clean` flag into `main.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v -run TestCompressCleanSanitization .`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add compress.go main.go compress_test.go
git commit -m "feat(compress): anadir modo de sanitizacion automatica con flag -clean"
```

---

### Task 6: Desktop Notifications & Terminal Bell (`notify.go`)

**Files:**
- Create: `notify.go`
- Create: `notify_test.go`
- Modify: `main.go`

**Interfaces:**
- Produces: `func NotifyTaskComplete(title, message string, elapsed time.Duration, forceNotify bool) bool`

- [ ] **Step 1: Write the failing test in `notify_test.go`**

```go
package main

import (
	"os/exec"
	"testing"
	"time"
)

func TestNotifyTaskCompleteCallsNotifySend(t *testing.T) {
	origExec := execCommand
	defer func() { execCommand = origExec }()

	called := false
	var capturedArgs []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name == "notify-send" {
			called = true
			capturedArgs = append([]string(nil), args...)
			return exec.Command("true")
		}
		return origExec(name, args...)
	}

	ok := NotifyTaskComplete("crush", "Compresión finalizada", 15*time.Second, false)
	if !ok || !called {
		t.Errorf("NotifyTaskComplete did not invoke notify-send for >= 10s duration")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestNotifyTaskCompleteCallsNotifySend`
Expected: FAIL with `undefined: NotifyTaskComplete`

- [ ] **Step 3: Implement `notify.go` and hook into operations in `main.go`**

Check duration threshold (>10s) or explicit `forceNotify` flag (`-notify`), trigger `notify-send` via `execCommand`, and emit terminal bell `\a` to interactive terminal.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v -run TestNotifyTaskComplete .`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add notify.go notify_test.go main.go
git commit -m "feat(notify): anadir notificaciones de escritorio y campana para tareas largas con -notify"
```

---

### Task 7: Format Transcoding / Conversion (`convert.go` & CLI `-convert`)

**Files:**
- Create: `convert.go`
- Create: `convert_test.go`
- Modify: `main.go`

**Interfaces:**
- Produces: `func DoConvert(files []string, targetFormat Format, opts ConvertOptions) error`

- [ ] **Step 1: Write the failing test in `convert_test.go`**

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDoConvertTarGzToTarZst(t *testing.T) {
	if !hasTool("pigz") && !hasTool("gzip") {
		t.Skip("gzip tool not available")
	}
	if !hasTool("zstd") {
		t.Skip("zstd not available")
	}

	tmpDir := t.TempDir()
	srcFile := filepath.Join(tmpDir, "data.txt")
	_ = os.WriteFile(srcFile, []byte("transcoding content test across streaming pipes"), 0644)

	// Create initial .tar.gz
	origArchive, err := DoCompress([]string{srcFile}, CompressOptions{
		Format:    Gz,
		OutputDir: tmpDir,
		KeepOrig:  true,
		Combine:   true,
	})
	if err != nil || len(origArchive) == 0 {
		t.Fatalf("setup compress failed: %v", err)
	}

	// Convert to zst
	opts := ConvertOptions{
		KeepOrig: true,
	}
	err = DoConvert(origArchive, Zst, opts)
	if err != nil {
		t.Fatalf("DoConvert failed: %v", err)
	}

	expectedOut := filepath.Join(tmpDir, stripCompressionExt(filepath.Base(origArchive[0]))+".tar.zst")
	if _, err := os.Stat(expectedOut); os.IsNotExist(err) {
		t.Errorf("converted file %s not created", expectedOut)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestDoConvertTarGzToTarZst`
Expected: FAIL with `undefined: DoConvert`

- [ ] **Step 3: Implement `convert.go` and wire into `main.go`**

Implement direct stream piping (`decompCmd | compCmd`) for stream-to-stream conversion, and ephemeral temp directory fallback for container conversion. Wire `-convert` / `-recompress` flag into `main.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v -run TestDoConvert .`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add convert.go convert_test.go main.go
git commit -m "feat(convert): anadir transcodificacion directa entre formatos con -convert"
```

---

### Task 8: TODO List for Entropy Detection & Project Documentation

**Files:**
- Create: `TODO.md`
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `main.go` (help text and command examples)

- [ ] **Step 1: Create `TODO.md`**

Document Shannon Entropy Pre-Compression Detection architecture, mathematical formula $H(X)$, byte frequency histogram, 64 KB block sampling, and heuristic bypassing for already compressed files (JPEG, MP4, APK, GZ, ZST).

- [ ] **Step 2: Update `README.md` and `AGENTS.md`**

Update feature lists, flag tables, and command examples for `-convert`, `-tree`, `-find`, `-diff`, `-clean`, and `-notify`.

- [ ] **Step 3: Run full verification suite**

Run: `go test -race ./...` and `go vet ./...`
Expected: All tests pass with 0 warnings, 0 race conditions.

- [ ] **Step 4: Commit**

```bash
git add TODO.md README.md AGENTS.md main.go
git commit -m "docs: actualizar documentacion de nuevas funcionalidades (-convert, -tree, -find, -diff, -clean, -notify) y TODO de entropia"
```
