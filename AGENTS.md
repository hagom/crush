# AGENTS.md — crush

## Descripción

Herramienta multi-formato de compresión y descompresión vía pipe.
Soporta 13 formatos: gz, xz, bz2, bz3, zst, lz, lrz, zip, 7z, tar, rar, lz4, br.

## Ramas

- **main** — versión Go. Desarrollo activo.

## Comandos

```bash
# Compilar
make build          # go build -o crush .
go build -o crush .

# Testear
make test           # go test ./... -v
go test ./... -v

# Ejecutar
./crush -h
./crush -c -f gz archivo.txt
./crush -d archivo.tar.gz

# Instalación / desinstalación
./crush --install             # binario + dependencias del sistema
./crush --install-deps         # solo dependencias
./crush --uninstall            # eliminar binario

# Binario final (sin dependencias)
make install        # install -m 755 crush /usr/local/bin/
```

## Convenciones de código

- Solo stdlib de Go. Sin dependencias externas.
- Pipeline de compresión con `exec.Cmd` + `StdoutPipe`.
- Tests table-driven donde sea posible.
- Nombres en camelCase. Errores con `fmt.Errorf`.
- NO agregar comentarios a menos que sea estrictamente necesario.
- Los colores ANSI van en las constantes de `util.go` (Green, Red, Yellow, Blue, Bold, NC).

## Filosofía del proyecto

- **Siempre paralelizar** los compresores. Usar versiones multihilo (`pigz`, `lbzip2`/`pbzip2`, `plzip`, `bzip3 -j N`, `xz -T0`, `zstd -T0`, `lrzip -p N`, `7z -mmt=on`, `rar -mtN`) para aprovechar todos los núcleos del CPU.
- **Sistemas target**: Debian (apt) y RedHat (dnf/yum). El instalador de dependencias debe priorizar estos gestores.
- Si la versión paralela de un compresor no está disponible, caer en la versión serial (`gzip`, `bzip2`, etc.) como último recurso, nunca fallar.

## Estructura del código Go

```
crush/
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

- Go: migración completa. 47 tests nativos pasando. ~2850 líneas.
- Features implementadas: compresión/descompresión 13 formatos, dry-run, split, exclude, progress bar (compresión + descompresión), colors, logging, install deps, test, list, read, --install (bin + deps), --install-deps, --uninstall, detección de modos conflictivos, expansión de flags combinados (-ptkv).
- **Bugs conocidos: 6** (2 críticos, 1 grave, 3 medios). Ver `COMPRESOR.md`.

## Fixes realizados (jul 2026)

| Bug | Severidad | Archivo | Fix |
|---|---|---|---|
| Early return en loop de FromFile | CRÍTICO | `compress.go:51` | Movido `return outPath, nil` fuera del for loop |
| zstd/bzip3 DirectFlags sin `-k` | CRÍTICO | `format.go:91,93,107,115` | Añadido `-k` a DirectFlags (tool borraba original antes que crush) |
| lrzip PipeFlags sin `-k` | CRÍTICO | `format.go:97,103` | Añadido `-k` a PipeFlags (tool borraba original en modo pipe) |
| KeepOrig ignorado en rutas relativas | GRAVE | `compress.go:208` | Lógica de borrado extraída a `removeFiles()` + `SkipCleanup` |
| `-c -t` borra originales antes del test | GRAVE | `main.go:306-328` | Ahora testea primero, borra después si el test pasa |
| Stat después de borrar en reporte | MEDIO | `decompress.go:124` | Movido `os.Stat` antes del `os.Remove` |
| `.tar` intermedio no se limpiaba si fallaba | MEDIO | `decompress.go:215` | Añadido `os.Remove(tarName)` antes del return error |

## Próximos pasos

- [ ] **FIX CRÍTICO**: `pkgmgr.go:174` — finalPkg nunca asignado, --install-deps no instala nada
- [ ] **FIX CRÍTICO**: `pkgmgr.go:126` — isToolInstalled pasa query sin split
- [ ] **FIX GRAVE**: `compress.go:275` — -p + pv produce archivos sin comprimir (antiguo; verificar si persiste)
- [ ] **FIX MEDIO**: `format.go:100` — .tar.lz4/.tar.br mal detectados
- [ ] **FIX MEDIO**: `decompress.go:338` — splitWriter.Close() puede panic
- [ ] **FIX MEDIO**: `main.go:131` — logging muerto (SetupLogging/CloseLog no llamados)
- [ ] Tests con mock de exec.Command (inyección de dependencias)
- [ ] Benchmarks Go
- [ ] Comando `--bench` para medir velocidad por formato
- [ ] Compresión paralela de múltiples archivos
- [ ] CI/CD (GitHub Actions)
- [ ] Publicar binarios precompilados en releases

## MCP Servers

Usar activamente en TODAS las tareas del proyecto, mínimo 2 por interacción. Si no se usan, explicar por qué no aplican.

### codegraph
- `codegraph_explore` para entender flujos y relaciones entre símbolos ANTES de grep/read
- Ejecutar `codegraph init` en el repo si no hay `.codegraph/`

### context-compress
- `batch_execute` para ejecutar múltiples comandos y auto-indexar su salida
- `execute`/`execute_file` con python/shell para análisis de código sin cargarlo al contexto
- `search` para recuperar secciones específicas de resultados indexados
- Preferir sobre comandos con salida >5KB

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

## Skills disponibles

### code-review-excellence
- **Cuándo:** Revisar PRs, cambios grandes, o establecer estándares de review en el equipo.
- **Qué aporta:** Metodología estructurada en 4 fases (contexto → alto nivel → línea por línea → resumen), técnicas de feedback (checklists, preguntas, sugerencias), plantillas, y checklist de seguridad/rendimiento/testing.
- **Flujo:** Fase 1: entender contexto y PR → Fase 2: arquitectura y diseño → Fase 3: línea por línea (lógica, seguridad, rendimiento, mantenibilidad) → Fase 4: resumen y decisión (approve/comment/request changes).
- **Severidad:** Usar 🔴 blocking, 🟡 important, 🟢 nit, 💡 suggestion, 📚 learning, 🎉 praise.

### refactoring-patterns
- **Cuándo:** Mejorar estructura interna sin cambiar comportamiento observable.
- **Qué aporta:** Catálogo Fowler completo de code smells (5 familias: Bloaters, OO Abusers, Change Preventers, Dispensables, Couplers) con sus refactorings correspondientes (Extract Method, Replace Conditional with Polymorphism, etc.).
- **Scoring:** 10/10 structural quality score basado en 8 preguntas de diagnóstico rápido.
- **Flujo seguro:** tests (green) → aplicar una transformación pequeña → tests (green) → commit. Nunca refactorizar en rojo.
- **Principio clave:** Rule of Three — tolera duplicación una vez, anótala dos, refactoriza a la tercera.
- **NO usar para:** reescrituras completas, código sin tests, o código próximo a eliminarse.

## Comportamiento esperado

Actuar como **equipo de desarrollo completo**, que cada actor del equipo sea un subagente. Sin necesidad de instrucciones explícitas por paso:

| Rol | Responsabilidad |
|---|---|
| **Arquitecto** | Decidir estructura, worktrees, ramas, orden de merges |
| **Dev** | Implementar fixes y features en Go |
| **Reviewer** | Encontrar bugs, revisar código, sugerir mejoras |
| **Tester** | Agregar tests, verificar que `go test ./...` pase |
| **DevOps** | Worktrees, merge strategy, cleanup |
| **Documentador** | Mantener AGENTS.md actualizado con decisiones |

**Flujo por omisión:** Planificar → Implementar → Testear → Revisar bugs → Mergear → Documentar.

- **Trabajo en paralelo con worktrees descriptivos:** Por cada tarea (fix o feature), crear un worktree con nombre descriptivo (ej: `fix/mode-conflicts`, `feat/decompress-progress`). Todos los worktrees se trabajan en paralelo usando subagentes simultáneos para ahorrar tiempo. Al terminar cada uno, mergear a `main` y eliminar worktree + rama.
  1. `git branch fix/algo main && git worktree add ../crush-fix-algo fix/algo`
  2. Lanzar subagentes simultáneos, cada uno trabajando en su worktree
  3. Verificar compilación y tests en cada worktree
  4. `git merge fix/algo --no-edit` en `main`
  5. `git worktree remove ../crush-fix-algo && git branch -d fix/algo`
- **Revisión exhaustiva de bugs:** Tras implementar fixes, hacer re-revisión completa del código en busca de bugs restantes. Si se encuentran nuevos bugs, fixearlos y repetir el ciclo. No detenerse hasta que queden **0 bugs conocidos** en todo el proyecto.
- **Commits por fix/feature:** Cada fix o feature debe tener su propio commit. No mezclar cambios distintos en un mismo commit.
- **Test obligatorio antes de commit:** Todo fix o feature debe compilar y pasar `go test ./...` sin errores. Si falla algún test o aparece un bug, debe corregirse hasta que quede 0 bugs antes de hacer commit.
- No esperar instrucciones en cada sub-paso.
- Al terminar un encargo, dejar el repo limpio (rama `main` actualizada, worktrees removidos, ramas fix eliminadas, AGENTS.md reflejando el nuevo estado).

## Referencias

- `COMPRESOR.md` — historial completo del desarrollo
- `README.md` — documentación de usuario
