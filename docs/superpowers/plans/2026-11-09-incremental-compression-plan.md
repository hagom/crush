# Plan de Implementación: Compresión Incremental por Fecha / Referencia (`--newer` / `--newer-than`)

## Tareas

### Tarea 1: Parser de Fechas y Referencias (`util.go` y `util_test.go`)
- [ ] Implementar `ParseCutoffTime(newerStr, newerThanFile string, now time.Time) (time.Time, error)`.
  - Soporte para duraciones relativas (`24h`, `7d`, `30m`).
  - Soporte para formatos estándar de fecha (`YYYY-MM-DD`, `YYYY-MM-DD HH:MM:SS`, RFC3339).
  - Soporte para archivo de referencia con `os.Stat`.
  - Manejo de exclusión mutua y errores.
- [ ] Escribir tests unitarios table-driven en `util_test.go`.

### Tarea 2: Opciones de Compresión y Filtrado (`compress.go`, `compress_test.go`)
- [ ] Agregar campo `CutoffTime time.Time` en `CompressOptions`.
- [ ] Implementar función auxiliar `hasNewerFiles(item string, cutoff time.Time, exclude []string) bool`.
- [ ] Implementar `collectNewerFiles(dir string, cutoff time.Time, exclude []string) ([]string, error)` para contenedores.
- [ ] Integrar `--newer-mtime` en `compressTarPipe` y `compressPlainTar`.
- [ ] Filtrar archivos en `generateCompressionTasks` y `DoCompress`:
  - Omitir elementos sin cambios.
  - Retornar limpiamente si ningún archivo califica.
- [ ] Escribir tests table-driven TDD en `compress_test.go`:
  - Filtrado de archivos sueltos por fecha.
  - Filtrado de contenidos en directorios con tar y contenedores.
  - Uso de `--newer-than` contra archivo testigo.
  - Caso donde nada cambió (0 archivos generados).

### Tarea 3: Capa CLI y Autocompletado (`main.go`)
- [ ] Registrar flags `-newer` / `--newer` y `-newer-than` / `--newer-than` en `main.go`.
- [ ] Validar compatibilidad (solo con `-c`, mutuamente excluyentes).
- [ ] Parsear `CutoffTime` e inyectar en `CompressOptions`.
- [ ] Actualizar `printHelp()` y autocompletado para Bash, Zsh y Fish.

### Tarea 4: Verificación Completa y Documentación
- [ ] Ejecutar `go test -race ./...`.
- [ ] Ejecutar `go vet ./...`.
- [ ] Actualizar `TODO.md`, `AGENTS.md` y `roadmap_mejoras.md`.
- [ ] Presentar resultados al usuario para autorización.
