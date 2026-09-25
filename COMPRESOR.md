# CRUSH — Historial de desarrollo

## Estado actual

Versión Go de crush, herramienta multi-formato de compresión y descompresión vía pipes UNIX de alto rendimiento con auto-paralelismo.
Desarrollo activo en rama `main`.
~14.600 líneas, 429 tests nativos pasando con race detector (`-race`). 0 bugs conocidos.

---

## Rama main — crush (Go 1.21+)

Implementación 100% pura en Go con biblioteca estándar (`stdlib`), sin dependencias externas en `go.mod`.

### Estructura

```
crush/
├── main.go          # CLI flags, dispatch (-c, -d, -l, -t, -r, -watch, -verify, --bench, --install)
├── format.go        # FormatInfo, ParseFormat, DetectFormat, ExtForFormat
├── compress.go      # DoCompress, compressItems, tar-pipe, -hash, -p, -sparse
├── decompress.go    # DoDecompress, splitWriter, -p, -filter
├── watcher.go       # Watcher, DoWatch, loop con stdlib
├── watcher_linux.go # Backend inotify (IN_CLOSE_WRITE, IN_MOVED_TO)
├── watcher_other.go # Backend fallback por sondeo
├── bench.go         # DoBench, BenchmarkFormat, GenerateBenchmarkDataset, FormatBenchTable
├── test_cmd.go      # DoTest, TestFile, -verify con checksum SHA-256
├── util.go          # NCPU, GetMemLimit, FormatSize, pipeline, lockedWriter, execCommand, SHA-256
├── util_linux.go    # F_SETPIPE_SZ (1 MiB) y splice(2) zero-copy
├── util_other.go    # Fallbacks de pipe y splice
├── pkgmgr.go        # DetectPkgManager, InstallMissingDeps, list helpers
├── Makefile
├── .github/workflows/ci.yml  # GitHub Actions: test matrix Go 1.21-1.23, race detector, build
├── *_test.go        # Tests unitarios y de integración table-driven
└── mock_test.go     # Tests con mocks de exec.Command (patrón TestHelperProcess)
```

### Funcionalidades Implementadas

- [x] Soporte para 13 formatos: `gz`, `xz`, `bz2`, `bz3`, `zst`, `lz`, `lrz`, `zip`, `7z`, `tar`, `rar`, `lz4`, `br`.
- [x] Compresión máxima real y auto-paralelismo (`NCPU()`) sin flags manuales.
- [x] Planificación LPT (*Longest Processing Time first*) para compresión y descompresión concurrente.
- [x] Distribución dinámica proporcional de hilos (`AllocateThreadsProportional`) y pool de tokens (`DynamicThreadPool`).
- [x] Compresión interactiva del directorio actual (`crush -c` sin argumentos con confirmación interactiva).
- [x] Adición y actualización in-place de archivos/carpetas en comprimidos existentes (`-a` / `-u`) para zip, 7z, rar, tar y tar.*.
- [x] Streaming directo sin `.tar` temporales a disco y zero-copy en Linux vía `splice(2)`.
- [x] Ampliación de capacidad de tuberías Linux a 1 MiB (`F_SETPIPE_SZ`).
- [x] Descompresión interactiva y recursiva con agrupación de partes.
- [x] Modo observador continuo de directorios (`-watch DIR`) con inotify y polling fallback.
- [x] Generación y verificación de checksums criptográficos SHA-256 (`-hash`, `-verify`).
- [x] Cifrado y protección con contraseña (`-p`, `-password`) en 7z, zip y rar con entrada oculta.
- [x] Soporte para archivos dispersos (*sparse files*) en tar (`-sparse`, `-S`).
- [x] Filtro de extracción selectiva por patrón glob (`-filter`).
- [x] División y descompresión continua en partes (`-s`).
- [x] Barra de progreso tabular en tiempo real con ETA monótono.
- [x] Suite de benchmarking integrada (`--bench`).
- [x] Tests unitarios y de integración exhaustivos (429 tests nativos pasando con race detector).
- [x] Pipeline de CI/CD en GitHub Actions con matriz Go 1.21-1.23.
- [x] Instalador de binario (`--install`) y dependencias multiplataforma (`--install-deps`).
