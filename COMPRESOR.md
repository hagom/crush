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

#### Críticos

1. **InstallMissingDeps no instala nada** — `pkgmgr.go:174`
   `var finalPkg *ToolInfo` se inicializa como nil y nunca se asigna `toolInfo`. Todos los paquetes se saltan con `if finalPkg == nil { continue }`.

2. **isToolInstalled query sin split** — `pkgmgr.go:126`
   `exec.Command(mgr.Query, pkg)` recibe `"dpkg-query -W -f=${Status}"` como un solo argumento (el binario). Debería usar `strings.Fields()` como hace `runCmd`. Afecta todos los gestores de paquetes.

#### Graves

3. **-p + pv produce archivos sin comprimir** — `compress.go:275,300,309`
   Cuando se activa `-p` y `pv` está disponible, el código reemplaza el compresor real (pigz/bzip3/zstd) con `pv` puro. El archivo resultante es un pipe-through sin compresión.

4. **KeepOrig ignorado en rutas relativas** — `compress.go:182`
   `if !strings.HasPrefix(f, '/') { continue }` salta archivos con ruta relativa. `-k` (keep original) solo funciona con rutas absolutas; con relativas los originales nunca se eliminan (o se conservan todos).

#### Medios

5. **.tar.lz4 y .tar.br mal detectados** — `format.go:100,120`
   Las extensiones `.tar.lz4` y `.tar.br` caen en los cases `.lz4`/`.br` antes de llegar a `.tar.*`, resultando en `IsTar: false`. No se crea el pipeline tar → compresor.

6. **splitWriter.Close() puede panic** — `decompress.go:338`
   `w.base.(*os.File)` sin ok-check. Si `base` no es `*os.File`, panic en runtime.

7. **Logging muerto** — `main.go:131`
   `SetupLogging()` y `CloseLog()` nunca se llaman desde `main()`. `logFile` siempre nil; `WriteLog`/`WriteLogf` escriben doble a stdout.

#### Leves

8. **expandGlobs traga errores** — `compress.go:233`
   `filepath.Glob` error se descarta con `_`, archivos con patrón inválido se omiten sin aviso.

9. **lrz configurado dos veces** — `compress.go:319,369`
   `case 'lrz'` en switch y luego `if ext == 'lrz'` al final. El case en switch es código muerto.

10. **decompressSingle sin multithreading** — `decompress.go:268`
    Falta `-T0` para zstd y `-j N` para bzip3 en descompresión, inconsistente con el resto del código.

### Pendientes / Mejoras futuras

- [ ] Fixear los 10 bugs conocidos (empezando por críticos)
- [ ] Tests con mock de exec.Command (table-driven)
- [ ] Comando `--bench` para medir velocidad por formato
- [ ] Soporte para compresión paralela de múltiples archivos
- [ ] Integración continua (GitHub Actions)
- [ ] Publicar binarios precompilados (releases)
