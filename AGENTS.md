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
- **Desarrollo TDD (Test-Driven Development):** todo fix o feature empieza por un test que falle (rojo), luego la implementación mínima (verde) y finalmente refactor. NO se escribe código de producción sin un test fallando antes.
- Los colores ANSI van en las constantes de `util.go` (Green, Red, Yellow, Blue, Bold, NC).

## Estándares de rigor en pruebas

- Todo test debe haber **fallado por la razón correcta** antes de implementar (verificar que falle con la funcionalidad ausente, no por un error de setup).
- Tests **table-driven** cubriendo por cada función: caso feliz, casos límite (input vacío, archivos inexistentes, rutas con `..`), y cada error que devuelve.
- Los tests verifican **comportamiento observable** (salida, error, efecto), no detalles de implementación.
- Un test que pasa con la implementación rota es un test inútil: si un test no detecta el bug que debe detectar, se corrige o se elimina.
- Código concurrente: correr `go test -race ./...`.
- Verificación pre-commit obligatoria: `go test ./...` y `go vet ./...` en verde.
- No mockear lo que no se necesita: los tests de integración con las herramientas reales (tar, pigz, 7z) se mantienen y corren en CI además de los unitarios.

## Filosofía del proyecto

- **Siempre paralelizar** los compresores. Usar versiones multihilo (`pigz`, `lbzip2`/`pbzip2`, `plzip`, `bzip3 -j N`, `xz -T0`, `zstd -T0`, `lrzip -p N`, `7z -mmt=on`, `rar -mtN`) para aprovechar todos los núcleos del CPU.
- **NUNCA agregar flags de paralelismo** (`-j`, `--parallel`, `--jobs`). El programa debe detectar automáticamente `NCPU()` y paralelizar archivos según la cantidad de núcleos disponibles.
- **Siempre comprimir múltiples archivos en paralelo** cuando sean archivos individuales (no directorios). Cada archivo produce su propia salida comprimida.
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

- Go: migración completa. 47 tests nativos pasando. ~3100 líneas.
- Features implementadas: compresión/descompresión 13 formatos, dry-run, split, exclude, progress bar estilo docker pull (global + per-file con barras, porcentajes, ETA, velocidad), colors, logging, install deps, test, list, read, --install (bin + deps), --install-deps, --uninstall, detección de modos conflictivos, expansión de flags combinados (-ptkv).
## Próximos pasos

- [ ] Tests con mock de exec.Command (inyección de dependencias)
- [ ] Benchmarks Go
- [ ] Comando `--bench` para medir velocidad por formato
- [x] Compresión paralela de múltiples archivos (auto NCPU)
- [x] Barra de progreso con ProgressTracker (byte-level en pipe, per-file en archivos)
- [ ] CI/CD (GitHub Actions)
- [ ] Publicar binarios precompilados en releases

## MCP Servers

Usar activamente en TODAS las tareas del proyecto, mínimo 2 por interacción. Si no se usan, explicar por qué no aplican.

### codegraph
- `codegraph_explore` para entender flujos y relaciones entre símbolos ANTES de grep/read
- Ejecutar `codegraph init` en el repo si no hay `.codegraph/`

### graphify
- Grafo de conocimiento del proyecto en `graphify-out/` con 141 nodos, 361 edges, 9 comunidades
- `graphify query "<pregunta>"` para navegar el grafo (BFS/DFS)
- `graphify path "NodoA" "NodoB"` para camino más corto entre conceptos
- `graphify explain "Nodo"` para explicación de un nodo
- `graphify-out/graph.html` — visualización interactiva (abrir en navegador)
- `graphify-out/GRAPH_REPORT.md` — reporte completo con god nodes, comunidades y preguntas sugeridas
- God nodes principales: `WriteLogf()` (21 edges), `DoCompress()` (20), `main()` (18)
- Las 9 comunidades: Compression Primitives (0.46), Format Parsing (0.42), Tool Detection (0.24), Compression Core (0.23), Package Management (0.22), CLI & Logging (0.20), Decompression Pipeline (0.18), Utilities & Helpers (0.13)
- Para re-indexar: `python3 -m graphify.cli .` (solo después de cambios grandes en la estructura)

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

**Flujo por omisión:** Planificar → **Test rojo** → **Verde (implementación mínima)** → **Refactor** → Revisar bugs → Mergear → Documentar.

- **Trabajo en paralelo con worktrees descriptivos:** Por cada tarea (fix o feature), crear un worktree con nombre descriptivo (ej: `fix/mode-conflicts`, `feat/decompress-progress`). Todos los worktrees se trabajan en paralelo usando subagentes simultáneos para ahorrar tiempo. Al terminar cada uno, mergear a `main` y eliminar worktree + rama.
  1. `git branch fix/algo main && git worktree add ../crush-fix-algo fix/algo`
  2. Lanzar subagentes simultáneos, cada uno trabajando en su worktree
  3. Verificar compilación y tests en cada worktree
  4. `git merge fix/algo --no-edit` en `main`
  5. `git worktree remove ../crush-fix-algo && git branch -d fix/algo`
- **Revisión exhaustiva de bugs:** Tras implementar fixes, hacer re-revisión completa del código en busca de bugs restantes. Si se encuentran nuevos bugs, fixearlos y repetir el ciclo. No detenerse hasta que queden **0 bugs conocidos** en todo el proyecto.
- **Commits por fix/feature:** Cada fix o feature debe tener su propio commit. No mezclar cambios distintos en un mismo commit.
- **Test obligatorio antes de commit:** Todo fix o feature debe compilar y pasar `go test ./...` (con `-race` si es concurrente) y `go vet ./...` sin errores, y el test correspondiente debe haberse escrito y visto fallar ANTES de la implementación. Si falla algún test o aparece un bug, debe corregirse hasta que quede 0 bugs antes de hacer commit.
- No esperar instrucciones en cada sub-paso.
- Al terminar un encargo, dejar el repo limpio (rama `main` actualizada, worktrees removidos, ramas fix eliminadas, AGENTS.md reflejando el nuevo estado).

## Referencias

- `COMPRESOR.md` — historial completo del desarrollo
- `README.md` — documentación de usuario
