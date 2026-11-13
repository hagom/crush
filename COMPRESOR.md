# CRUSH — Historial de desarrollo

## Estado actual

Versión Go de crush, herramienta multi-formato de compresión y descompresión vía pipes UNIX de alto rendimiento con auto-paralelismo.
Desarrollo activo en rama `main`.
~27.100 líneas de Go (incluyendo tests), 315 tests de primer nivel (685 contando subtests) pasando con race detector (`-race`). 0 bugs conocidos.

El detalle técnico de cada feature y fix está en [AGENTS.md](AGENTS.md) (sección *Estado actual*) y el registro de propuestas en [TODO.md](TODO.md).

---

## Rama main — crush (Go 1.27)

Implementación 100% pura en Go con biblioteca estándar (`stdlib`), sin dependencias externas en `go.mod`.

### Estructura

```
crush/
├── main.go            # CLI flags, validación de modos, dispatch, ayuda y autocompletado
├── format.go          # FormatInfo, ParseFormat, DetectFormat, ExtForFormat, entropía
├── compress.go        # DoCompress, tar-pipe, contenedores, -hash, -p, -sparse, -clean, --newer, --resume, -a
├── decompress.go      # DoDecompress, tandas por capacidad, extracción confinada con os.OpenRoot
├── convert.go         # Transcodificación por streaming (-convert, -recompress)
├── passwords.go       # PasswordManager y registro de contraseñas
├── analyze.go         # Analizador de compresibilidad (--analyze)
├── list.go            # Listado tabular (-l)
├── archive_list.go    # Lectura de miembros sin extracción a disco
├── tree.go            # Vista en árbol (-tree)
├── find.go            # Búsqueda en comprimidos (-find)
├── diff.go            # Comparación de comprimidos (-diff)
├── notify.go          # Notificaciones de escritorio (-notify)
├── watcher.go         # Watcher, DoWatch
├── watcher_linux.go   # Backend inotify + epoll (IN_CLOSE_WRITE, IN_MOVED_TO)
├── watcher_other.go   # Backend fallback por sondeo
├── bench.go           # DoBench, BenchmarkFormat, GenerateBenchmarkDataset, FormatBenchTable
├── test_cmd.go        # DoTest, TestFile, -verify con checksum SHA-256
├── util.go            # NCPU, memoria, disco, pipeline, progreso, logs, execCommand, SHA-256
├── util_linux.go      # F_SETPIPE_SZ (1 MiB) y splice(2) zero-copy
├── util_other.go      # Fallbacks de pipe y splice
├── hardware_linux.go  # Detección HDD vs SSD/NVMe
├── hardware_other.go  # Fallback de detección de hardware
├── lock.go            # Rutas y mensajes del bloqueo de instancia única
├── lock_unix.go       # AcquireLock con flock(2)
├── lock_other.go      # Fallback sin bloqueo
├── pkgmgr.go          # DetectPkgManager, InstallMissingDeps, CompressReadTo (-r)
├── Makefile
├── .github/workflows/ci.yml  # GitHub Actions: Go 1.27.x, go vet, race detector, build, smoke test
├── *_test.go          # Tests unitarios y de integración table-driven
└── mock_test.go       # Tests con mocks de exec.Command (patrón TestHelperProcess)
```

### Funcionalidades Implementadas

- [x] Soporte para 13 formatos: `gz`, `xz`, `bz2`, `bz3`, `zst`, `lz`, `lrz`, `zip`, `7z`, `tar`, `rar`, `lz4`, `br` y modo `auto`.
- [x] Compresión heterogénea concurrente: matriz multi-formato simultánea (`-F`) con unificación de cola LPT ponderada en `DynamicThreadPool`.
- [x] Modo adaptativo automático (`-f auto`): selección heurística inteligente del formato óptimo por contenido y extensión.
- [x] Detección previa de entropía de Shannon para datos ya comprimidos o cifrados.
- [x] Control atómico de referencias para eliminación segura de originales (`KeepOrig`).
- [x] Compresión máxima real y auto-paralelismo (`NCPU()`) sin flags manuales.
- [x] Planificación LPT (*Longest Processing Time first*) para compresión y descompresión concurrente.
- [x] Distribución dinámica proporcional de hilos (`AllocateThreadsProportionalWithCaps`), balanceo de ventana activa (`DynamicThreadPool`), techos por payload y control de memoria RAM, también en compresión secuencial (`resolveThreadCap`).
- [x] Detección de hardware (HDD vs SSD/NVMe) para ajustar la concurrencia.
- [x] Gestión inteligente de contraseñas con reorganización por frecuencia de uso (MFU) y cola diferida en descompresión.
- [x] Compresión interactiva del directorio actual (`crush -c` sin argumentos con confirmación interactiva).
- [x] Adición y actualización in-place de archivos/carpetas en comprimidos existentes (`-a` / `-u`) para zip, 7z, rar, tar y tar.*.
- [x] Compresión incremental por fecha, duración o archivo de referencia (`--newer` / `--newer-than`).
- [x] Reanudación de lotes interrumpidos (`--resume`).
- [x] Descompresión adaptativa por tandas según el espacio libre en disco.
- [x] Streaming directo sin `.tar` temporales a disco y zero-copy en Linux vía `splice(2)`.
- [x] Ampliación de capacidad de tuberías Linux a 1 MiB (`F_SETPIPE_SZ`).
- [x] Descompresión interactiva y recursiva con agrupación de partes.
- [x] Analizador de compresibilidad (`--analyze`), listado tabular (`-l`), árbol (`-tree`), búsqueda (`-find`) y comparación (`-diff`).
- [x] Transcodificación directa entre formatos (`-convert` / `-recompress`).
- [x] Modo observador continuo de directorios (`-watch DIR`) con inotify + epoll y polling fallback.
- [x] Generación y verificación de checksums criptográficos SHA-256 (`-hash`, `-verify`).
- [x] Cifrado y protección con contraseña (`-p`, `-password`) en 7z, zip y rar con entrada oculta.
- [x] Soporte para archivos dispersos (*sparse files*) en tar (`-sparse`, `-S`, `-no-sparse`).
- [x] Filtro de extracción selectiva por patrón glob (`-filter`).
- [x] División y descompresión continua en partes (`-s`).
- [x] Barra de progreso tabular en tiempo real con ETA monótono y nombres UTF-8.
- [x] Suite de benchmarking integrada (`--bench`).
- [x] Sandbox de extracción contra Zip-Slip (`os.OpenRoot`, `sanitizeArchiveRelPath`, `O_EXCL`, `Lstat`) y extractores externos endurecidos (`--keep-old-files`, `-aos`, `unzip -n`, `rar -o-`, separador `--`).
- [x] Auditoría completa de bugs y vulnerabilidades: lock y logs en directorios privados, contraseñas enmascaradas en logs verbose, pipes sin bloqueos, validación completa de conflictos de modos.
- [x] Tests unitarios y de integración exhaustivos (315 tests de primer nivel, 685 con subtests, race detector).
- [x] Pipeline de CI/CD en GitHub Actions con Go 1.27.x.
- [x] Instalador de binario (`--install`) y dependencias multiplataforma (`--install-deps`).
