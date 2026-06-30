# AGENTS.md — compresor

## Descripción

Herramienta multi-formato de compresión y descompresión vía pipe.
Soporta 13 formatos: gz, xz, bz2, bz3, zst, lz, lrz, zip, 7z, tar, rar, lz4, br.

## Ramas

- **main** — documentación (README.md, AGENTS.md, COMPRESOR.md)
- **bash** — versión Bash Script (`compresor.sh`). Estable, solo mantenimiento.
- **go** — versión Go. **Desarrollo activo. Trabajar aquí por defecto.**

## Comandos

```bash
# Compilar
make build          # go build -o compresor .
go build -o compresor .

# Testear
make test           # go test ./... -v
go test ./... -v

# Ejecutar
./compresor -h
./compresor -c -f gz archivo.txt
./compresor -d archivo.tar.gz

# Binario final
make install        # install -m 755 compresor /usr/local/bin/
```

## Convenciones de código

- Solo stdlib de Go. Sin dependencias externas.
- Pipeline de compresión con `exec.Cmd` + `StdoutPipe`.
- Tests table-driven donde sea posible.
- Nombres en camelCase. Errores con `fmt.Errorf`.
- NO agregar comentarios a menos que sea estrictamente necesario.
- Los colores ANSI van en las constantes de `util.go` (Green, Red, Yellow, Blue, Bold, NC).

## Estructura del código Go

```
compresor/
├── main.go        # CLI flags, dispatch (-c, -d, -l, -t, -r, --install)
├── format.go      # FormatInfo, ParseFormat, DetectFormat, ExtForFormat
├── compress.go    # DoCompress, compressItems, tar-pipe
├── decompress.go  # DoDecompress, splitWriter
├── test_cmd.go    # DoTest, TestFile
├── util.go        # NCPU, GetMemLimit, FormatSize, pipeline, logging, colors
├── pkgmgr.go      # DetectPkgManager, InstallMissingDeps, list helpers
├── Makefile
└── *_test.go      # Tests por paquete
```

## Estado actual

- Bash: completo. 59 tests en bats. 1120 líneas. Sin bugs conocidos.
- Go: migración 1:1 completa. 34 tests nativos pasando. 2604 líneas.
- Features implementadas en Go: compresión/descompresión 13 formatos, dry-run, split, exclude, progress bar, colors, logging, install deps, test, list, read.

## Próximos pasos

- [ ] Tests con mock de exec.Command (inyección de dependencias)
- [ ] Benchmarks Bash vs Go
- [ ] Comando `--bench` para medir velocidad por formato
- [ ] Compresión paralela de múltiples archivos
- [ ] CI/CD (GitHub Actions)
- [ ] Publicar binarios precompilados en releases
- [ ] Instrucciones de contribución

## Referencias

- `COMPRESOR.md` — historial completo del desarrollo
- `README.md` — documentación de usuario
