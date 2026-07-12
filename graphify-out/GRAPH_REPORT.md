# Graph Report - /baul/aplicaciones/crush  (2026-07-10)

## Corpus Check
- Corpus is ~15,279 words - fits in a single context window. You may not need a graph.

## Summary
- 141 nodes · 361 edges · 9 communities (8 shown, 1 thin omitted)
- Extraction: 66% EXTRACTED · 34% INFERRED · 0% AMBIGUOUS · INFERRED: 121 edges (avg confidence: 0.8)
- Token cost: 0 input · 0 output

## Community Hubs (Navigation)
- Utilities & Helpers
- Decompression Pipeline
- Package Management
- CLI & Logging
- Tool Detection & Testing
- Compression Core
- Compression Primitives
- Format Parsing
- Binary Entrypoint

## God Nodes (most connected - your core abstractions)
1. `WriteLogf()` - 21 edges
2. `DoCompress()` - 20 edges
3. `main()` - 18 edges
4. `hasTool()` - 14 edges
5. `CompressOptions` - 13 edges
6. `decompressTar()` - 12 edges
7. `compressSingleFile()` - 11 edges
8. `decompressSingle()` - 11 edges
9. `sevenzBin()` - 11 edges
10. `decompressFile()` - 10 edges

## Surprising Connections (you probably didn't know these)
- `main()` --calls--> `CloseLog()`  [INFERRED]
  main.go → util.go
- `main()` --calls--> `SetupLogging()`  [INFERRED]
  main.go → util.go
- `DoCompress()` --calls--> `ReadFileLines()`  [INFERRED]
  compress.go → pkgmgr.go
- `DoCompress()` --calls--> `CalcPct()`  [INFERRED]
  compress.go → util.go
- `DoCompress()` --calls--> `CheckDiskSpace()`  [INFERRED]
  compress.go → util.go

## Import Cycles
- None detected.

## Communities (9 total, 1 thin omitted)

### Community 0 - "Utilities & Helpers"
Cohesion: 0.13
Nodes (22): CalcPct(), CheckDiskSpace(), CloseLog(), effectiveThreads(), FormatSize(), GetAvailBytes(), GetMemLimit(), GetUniqueName() (+14 more)

### Community 1 - "Decompression Pipeline"
Cohesion: 0.18
Nodes (17): DecompressOptions, FormatInfo, splitWriter, decompressFile(), decompressSingle(), decompressTar(), DoDecompress(), getDirSize() (+9 more)

### Community 2 - "Package Management"
Cohesion: 0.22
Nodes (18): PkgManager, ToolInfo, contains(), DetectPkgManager(), findToolInfo(), InstallMissingDeps(), isToolInstalled(), runCmd() (+10 more)

### Community 3 - "CLI & Logging"
Cohesion: 0.20
Nodes (15): removeFiles(), multiFlag, copyFile(), doInstallCompletion(), flagTakesValue(), handleInstall(), handleInstallDeps(), handleUninstall() (+7 more)

### Community 4 - "Tool Detection & Testing"
Cohesion: 0.24
Nodes (15): TestOptions, DetectFormat(), CompressRead(), File, ListCompressed(), DoTest(), T, TestTestNoFiles() (+7 more)

### Community 5 - "Compression Core"
Cohesion: 0.23
Nodes (12): compressModeDesc(), DoCompress(), expandGlobs(), T, TestCompressDryRun(), TestCompressModeDesc(), TestCompressNoFiles(), TestExpandGlobs() (+4 more)

### Community 6 - "Compression Primitives"
Cohesion: 0.46
Nodes (13): Cmd, buildCompressCmd(), compress7z(), compressItems(), compressParallel(), compressPlainTar(), compressRar(), compressSingleFile() (+5 more)

### Community 7 - "Format Parsing"
Cohesion: 0.42
Nodes (8): ParseFormat(), T, TestDetectFormat(), TestExtForFormat(), TestFormatInfo(), TestFormatString(), TestParseFormat(), TestSevenZ()

## Knowledge Gaps
- **1 isolated node(s):** `crush`
  These have ≤1 connection - possible missing edges or undocumented components.
- **1 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `main()` connect `CLI & Logging` to `Utilities & Helpers`, `Decompression Pipeline`, `Tool Detection & Testing`, `Compression Core`, `Format Parsing`?**
  _High betweenness centrality (0.218) - this node is a cross-community bridge._
- **Why does `WriteLogf()` connect `CLI & Logging` to `Utilities & Helpers`, `Decompression Pipeline`, `Package Management`, `Tool Detection & Testing`, `Compression Core`, `Compression Primitives`?**
  _High betweenness centrality (0.201) - this node is a cross-community bridge._
- **Why does `DoCompress()` connect `Compression Core` to `Utilities & Helpers`, `CLI & Logging`, `Compression Primitives`?**
  _High betweenness centrality (0.167) - this node is a cross-community bridge._
- **Are the 20 inferred relationships involving `WriteLogf()` (e.g. with `compress7z()` and `compressParallel()`) actually correct?**
  _`WriteLogf()` has 20 INFERRED edges - model-reasoned connections that need verification._
- **Are the 12 inferred relationships involving `DoCompress()` (e.g. with `ExtForFormat()` and `ReadFileLines()`) actually correct?**
  _`DoCompress()` has 12 INFERRED edges - model-reasoned connections that need verification._
- **Are the 11 inferred relationships involving `main()` (e.g. with `DoCompress()` and `removeFiles()`) actually correct?**
  _`main()` has 11 INFERRED edges - model-reasoned connections that need verification._
- **Are the 12 inferred relationships involving `hasTool()` (e.g. with `buildCompressCmd()` and `compressTarPipe()`) actually correct?**
  _`hasTool()` has 12 INFERRED edges - model-reasoned connections that need verification._