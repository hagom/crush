# Compresión Heterogénea Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement concurrent multi-format compression (`-F fmt1,fmt2,...`) and content-adaptive auto compression (`-f auto`) in a single unified queue across `DynamicThreadPool`, with safe atomic `KeepOrig` cleanup and visual format tags in progress reporting.

**Architecture:** Pure Go stdlib with zero external dependencies. Extend `Format` enum with `Auto` and heuristic detection in `format.go`. Decouple compression into discrete `CompressionTask` units in `compress.go`, unifying multi-format and multi-file jobs into a single global LPT-sorted queue scheduled on `DynamicThreadPool` with payload-aware thread caps. Safeguard original file deletion through atomic reference counting per file. Update CLI flags and help text in `main.go`.

**Tech Stack:** Go 1.21+ stdlib (`sync`, `sync/atomic`, `os`, `path/filepath`, `exec`, `io`, `fmt`, `strings`, `sort`).

## Global Constraints

- 100% pure Go standard library (no external dependencies in `go.mod`).
- TDD required: failing test (RED) -> minimal implementation (GREEN) -> refactor -> commit.
- Verification commands before completion: `go test -race ./...` and `go vet ./...` must pass with 0 errors.
- External commands must be invoked via `execCommand` for testability.
- No new parallelism flags: scheduling remains fully automated with `NCPU()`.
- Update all project documentation (`README.md`, `AGENTS.md`, `COMPRESOR.md`).

---

### Task 1: Format Enum Extension and Heuristic Detection (`format.go`)

**Files:**
- Modify: `format.go`
- Modify: `format_test.go`

**Interfaces:**
- Produces:
  ```go
  const Auto Format = 14
  func DetectAutoFormat(path string) Format
  ```
- Consumes:
  `Format`, `formatNames`, `hasTool`

- [ ] **Step 1: Write the failing tests in `format_test.go`**
  - Test `ParseFormat("auto")` returns `Auto, nil`.
  - Test `DetectAutoFormat` for directories returns `SevenZ` (or `Zst`/`Gz` based on tools).
  - Test `DetectAutoFormat` for multimedia/compressed extensions (`.mp4`, `.zip`, `.jpg`) returns `Zst`.
  - Test `DetectAutoFormat` for text/code extensions (`.txt`, `.go`, `.json`, `.sql`) returns `Xz`.
  - Test `DetectAutoFormat` for unknown extension with text contents returns `Xz`.
  - Test `DetectAutoFormat` for unknown extension with binary/null bytes returns `Zst`.

- [ ] **Step 2: Run tests to confirm failure (RED)**
  - Run `go test -run TestDetectAutoFormat ./...`.

- [ ] **Step 3: Implement `Auto` enum, `ParseFormat`, and `DetectAutoFormat` in `format.go` (GREEN)**
  - Add `Auto Format = 14` in `Format` enum.
  - Add `"auto"` to `formatNames`.
  - Implement `DetectAutoFormat(path string) Format`.

- [ ] **Step 4: Run tests to confirm passing (GREEN)**
  - Run `go test -race -run TestDetectAutoFormat ./...`.

- [ ] **Step 5: Refactor and commit**
  - Verify zero lint issues with `go vet ./...`.
  - Git commit: `feat(format): add Auto format enum and DetectAutoFormat heuristic`.

---

### Task 2: Unified Task Queue and Concurrent Heterogeneous Compression (`compress.go`)

**Files:**
- Modify: `compress.go`
- Modify: `compress_test.go`

**Interfaces:**
- Produces:
  ```go
  type CompressionTask struct {
      ID            int
      File          string
      Format        Format
      Size          int64
      ThreadCap     int
      OutPath       string
  }
  func generateCompressionTasks(items []string, opts CompressOptions) ([]CompressionTask, error)
  ```
- Consumes:
  `DetectAutoFormat`, `DynamicThreadPool`, `MaxEffectiveThreads`, `MemorySafeMaxThreads`, `FormatMaxThreads`, `ProgressTracker`

- [ ] **Step 1: Write failing tests in `compress_test.go`**
  - Test `generateCompressionTasks` expands multi-format matrix (`-F gz,xz`) across multiple files into discrete tasks.
  - Test `generateCompressionTasks` resolves `-f auto` per file/directory based on content/type.
  - Test `TestHeterogeneousCompression_ConcurrentMatrix`: files compressed to multiple formats concurrently in single run.
  - Test `TestHeterogeneousCompression_KeepOrigSafety`: original file is only removed if all formats succeed and `KeepOrig` is false.
  - Test `TestHeterogeneousCompression_WorkerTitle`: `FileProgress` labels include format tag `[<fmt>]`.

- [ ] **Step 2: Run tests to confirm failure (RED)**
  - Run `go test -run TestHeterogeneousCompression ./...`.

- [ ] **Step 3: Implement `CompressionTask`, `generateCompressionTasks`, and unified queue in `compress.go` (GREEN)**
  - Define `CompressionTask`.
  - Implement task generator resolving `-f auto` and matrix `-F`.
  - Refactor `compressParallel` to process a slice of `CompressionTask` in a single concurrent pass.
  - Implement atomic reference counter `pendingTasks map[string]*atomic.Int32` and `failedFiles sync.Map` to safely delete originals only when all format tasks for that source file finish successfully.
  - Update `FileProgress` label to include `[<fmt>]`.

- [ ] **Step 4: Run tests to confirm passing (GREEN)**
  - Run `go test -race -run TestHeterogeneousCompression ./...`.

- [ ] **Step 5: Refactor and commit**
  - Verify race conditions with `go test -race ./...`.
  - Git commit: `feat(compress): implement concurrent multi-format matrix and atomic KeepOrig cleanup`.

---

### Task 3: CLI Wiring and Help Text (`main.go`)

**Files:**
- Modify: `main.go`
- Modify: `main_test.go` (or `compress_test.go`)

**Interfaces:**
- CLI:
  `crush -c -f auto [files...]`
  `crush -c -F gz,xz,zst [files...]`
  `crush -c -F auto,xz [files...]`

- [ ] **Step 1: Write test for CLI parsing of `-f auto` and `-F auto,xz`**
  - Verify `-f auto` sets `opts.Format = Auto`.
  - Verify `-F auto,xz` parses correctly into `opts.Formats = []Format{Auto, Xz}`.

- [ ] **Step 2: Run test to confirm failure (RED)**
  - Run `go test -run TestCLIAutoFormat ./...`.

- [ ] **Step 3: Update `main.go` and `printHelp()` (GREEN)**
  - Update format flag validation to allow `auto`.
  - Update help text with examples of `-f auto` and concurrent `-F`.

- [ ] **Step 4: Run all tests to confirm passing (GREEN)**
  - Run `go test -race ./...`.

- [ ] **Step 5: Commit**
  - Git commit: `feat(cli): wire -f auto and concurrent -F multi-format compression`.

---

### Task 4: Complete Documentation Updates

**Files:**
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `COMPRESOR.md`

- [ ] **Step 1: Update `README.md`**
  - Document `-f auto` (adaptive intelligent format selection).
  - Document concurrent `-F fmt1,fmt2,...` matrix execution.
  - Add usage examples and clarify zero-idle CPU parallelism.

- [ ] **Step 2: Update `AGENTS.md`**
  - Record the feature in "Estado actual" and conventions.
  - Document `DetectAutoFormat`, `CompressionTask`, and safe `KeepOrig` atomic ref counting.

- [ ] **Step 3: Update `COMPRESOR.md`**
  - Document the architecture, LPT scheduling of mixed format tasks, and benchmarks/expectations.

- [ ] **Step 4: Commit documentation**
  - Git commit: `docs: document concurrent multi-format and adaptive auto compression`.

---

### Task 5: End-to-End Verification and Push

- [ ] **Step 1: Run comprehensive tests and linting**
  - `go test -race ./...`
  - `go vet ./...`
  - `go build -o crush .`

- [ ] **Step 2: Push to remote repository**
  - `git push origin main`
