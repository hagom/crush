# CRUSH — Historial de desarrollo

## Estado actual

Versión Go de crush, compresor multi-formato. Desarrollo activo en rama `main`.
~2750 líneas, 47 tests nativos pasando.

---

## Rama go — crush (Go 1.21+)

Implementación en Go con stdlib (sin dependencias externas).

### Estructura

```
crush/
├── main.go          # CLI flags, dispatch
├── format.go        # FormatInfo, ParseFormat, DetectFormat
├── compress.go      # DoCompress, compressItems, tar-pipe
├── decompress.go    # DoDecompress, splitWriter
├── test_cmd.go      # DoTest, TestFile
├── util.go          # NCPU, RAM, disco, colores, pipeline, logging
├── pkgmgr.go        # DetectPkgManager, InstallMissingDeps
├── Makefile
└── *_test.go        # 47 tests, todos pasando
```

### Bugs conocidos

#### Medio

1. **WriteLog/WriteLogf a stdout** — `util.go:409,416`
   `fmt.Print(s)` escribe a stdout. Para una CLI tool, diagnóstico debe ir a stderr. Rompe tuberías: `crush -r archivo.gz | head` mezcla datos con log.

### Pendientes / Mejoras futuras

- [ ] Zip decompress: usar 7zz -mmt=on como primario (MT) con fallback unzip (ST)
- [ ] Tests con mock de exec.Command (table-driven)
- [ ] Comando `--bench` para medir velocidad por formato
- [ ] Soporte para compresión paralela de múltiples archivos
- [ ] Integración continua (GitHub Actions)
- [ ] Publicar binarios precompilados (releases)
