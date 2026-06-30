# COMPRESOR — Historial de desarrollo

## Estado actual

Proyecto migrado de Bash a Go. Ambas versiones son funcionales.
La versión Go es el futuro del proyecto.

---

## Rama bash — compresor.sh

Script original en Bash Script (~1120 líneas).

### Features implementadas

- Compresión/descompresión multi-formato (11 formatos)
- Formato RAR agregado
- Instalación automática de dependencias (apt, dnf, yum, zypper, pacman, emerge, apk)
- Dry-run (-n)
- Split en partes (-s N)
- Exclusión de patrones (--exclude)
- Barra de progreso con pv (-p)
- Verificación de integridad (-t)
- Listado de contenido (-l)
- Lectura a stdout (-r)
- Preservar/eliminar originales (-k)
- Detección de CPUs/RAM/disco
- Colores en output
- Logging a /var/log/compresor/
- 59 tests en bats (test/compresor_test.sh)

### Bugs corregidos (commit 64dcd5e)

1. `check_disk_space` — usaba `return` en vez de `exit`, rompía `-t`
2. Cálculo de espacio en tar — factor duplicado
3. Reporte de split — mostraba líneas vacías
4. `do_decompress` — no trackeaba errores internos
5. `install_missing_deps` — no reintentaba en fallo
6. Indentación en output de compresión

### Últimas acciones antes de migrar a Go

- Refactorización: extraer `_ext_for_format` y `_print_compress_report`
- Documentación añadida a todas las funciones
- `INDIVIDUAL=0` corregido
- RAR format soportado (compresión, descompresión, test condicional)

---

## Rama go — compresor (Go 1.21+)

Migración 1:1 desde Bash a Go con stdlib (sin dependencias externas).

### Estructura

```
compresor/
├── main.go          # CLI flags, dispatch
├── format.go        # FormatInfo, ParseFormat, DetectFormat
├── compress.go      # DoCompress, compressItems, tar-pipe
├── decompress.go    # DoDecompress, splitWriter
├── test_cmd.go      # DoTest, TestFile
├── util.go          # NCPU, RAM, disco, colores, pipeline, logging
├── pkgmgr.go        # DetectPkgManager, InstallMissingDeps
├── Makefile
└── *_test.go        # 34 tests, todos pasando
```

### Pendientes / Mejoras futuras

- [ ] Tests con mock de exec.Command (table-driven)
- [ ] Benchmark entre versiones Bash vs Go
- [ ] Comando `--bench` para medir velocidad por formato
- [ ] Soporte para compresión paralela de múltiples archivos
- [ ] Integración continua (GitHub Actions)
- [ ] Publicar binarios precompilados (releases)
- [ ] Archivos `.gitignore` y `.editorconfig` en raíz
- [ ] Completar `ExtForFormat` para lz4 y br (ya en formatNames)

### Diferencias con Bash

| Aspecto | Bash | Go |
|---------|------|----|
| Tests | 59 tests en bats | 34 tests nativos |
| Dependencias | bats | go test |
| Tipado | Dinámico | Estático |
| Binario | Script | 3.1MB estático |
| Pipeline | Tuberías shell | exec.Cmd + StdoutPipe |
