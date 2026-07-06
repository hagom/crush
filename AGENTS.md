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

# Instalación / desinstalación
./compresor --install             # binario + dependencias del sistema
./compresor --install-deps         # solo dependencias
./compresor --uninstall            # eliminar binario

# Binario final (sin dependencias)
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

- Bash: completo. 59 tests en bats. 1144 líneas. Sin bugs conocidos. Flags: --install (bin + deps), --install-deps, --uninstall.
- Go: migración 1:1 completa. 39 tests nativos pasando. ~2650 líneas.
- Features implementadas en Go: compresión/descompresión 13 formatos, dry-run, split, exclude, progress bar, colors, logging, install deps, test, list, read, --install (bin + deps), --install-deps, --uninstall.

## Próximos pasos

- [ ] Tests con mock de exec.Command (inyección de dependencias)
- [ ] Benchmarks Bash vs Go
- [ ] Comando `--bench` para medir velocidad por formato
- [ ] Compresión paralela de múltiples archivos
- [ ] CI/CD (GitHub Actions)
- [ ] Publicar binarios precompilados en releases
- [ ] Instrucciones de contribución

## MCP Servers

Usar activamente en TODAS las tareas del proyecto, mínimo 2 por interacción. Si no se usan, explicar por qué no aplican.

### codegraph
- `codegraph_explore` para entender flujos y relaciones entre símbolos ANTES de grep/read
- Ejecutar `codegraph init` en el repo si no hay `.codegraph/`

### context-compress
- `batch_execute` para ejecutar múltiples comandos y auto-indexar su salida
- `execute`/`execute_file` con python/shell para análisis de código sin cargarlo al contexto
- `search` para recuperar secciones específicas de resultados indexados
- Preferir sobre bash para comandos con salida >5KB

### memory
- Almacenar hallazgos, bugs y relaciones como entidades/observaciones en el grafo de conocimiento
- Usar `create_entities` para cada bug/file encontrado
- Usar `create_relations` para conectar bugs con archivos y entre sí

### sqlite
- Almacenar resultados estructurados (bugs con severity, file, line, fix_options)
- Usar `append_insight` para registrar conclusiones del análisis

### sequential-thinking
- Estructurar análisis multi-paso antes de emitir conclusiones
- Verificar bugs dudosos en rondas separadas antes de confirmarlos

### chrome-devtools, developer-knowledge, design-mcp, blender
- NO relevantes para este proyecto. Omitir.

## Comportamiento esperado

Actuar como **equipo de desarrollo completo**. Sin necesidad de instrucciones explícitas por paso:

| Rol | Responsabilidad |
|---|---|
| **Arquitecto** | Decidir estructura, worktrees, ramas, orden de merges |
| **Dev** | Implementar fixes y features en Go y/o Bash |
| **Reviewer** | Encontrar bugs, revisar código, sugerir mejoras |
| **Tester** | Agregar tests, verificar que `go test ./...` pase |
| **DevOps** | Worktrees, merge strategy, cleanup |
| **Documentador** | Mantener AGENTS.md actualizado con decisiones |

**Flujo por omisión:** Planificar → Implementar → Testear → Mergear → Documentar. No esperar instrucciones en cada sub-paso. Al terminar un encargo, dejar el repo limpio (rama `main` actualizada, worktrees removidos, ramas fix eliminadas, AGENTS.md reflejando el nuevo estado).

## Referencias

- `COMPRESOR.md` — historial completo del desarrollo
- `README.md` — documentación de usuario
