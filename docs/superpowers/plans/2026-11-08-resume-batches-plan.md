# Plan de Implementación: Modo Reanudación de Lotes Interrumpidos (`--resume`)

## Tareas

### Tarea 1: Soporte de CLI y Estructuras de Opciones (`main.go`, `compress.go`, `decompress.go`)
- [ ] Agregar campo `Resume bool` en `CompressOptions` (`compress.go`).
- [ ] Agregar campo `Resume bool` en `DecompressOptions` (`decompress.go`).
- [ ] Registrar flag `-resume` / `--resume` en `main.go`.
- [ ] Agregar `--resume` a autocompletado en `main.go`.
- [ ] Validar compatibilidad de `--resume` con `-c` y `-d`.

### Tarea 2: Reanudación de Lotes en Compresión (`compress.go` y `compress_test.go`)
- [ ] Agregar campo `Skipped bool` en `CompressionTask`.
- [ ] En `generateCompressionTasks`:
  - Si `opts.Resume == true`:
    - Resolver ruta canónica `targetPath` sin `GetUniqueName`.
    - Si existe en disco, invocar test rápido de integridad `testArchiveIntegrity(targetPath, ...)`:
      - Si está íntegro: marcar `task.Skipped = true`, emitir log `ℹ [reanudado] '%s' ya existe y es válido; omitiendo compresión.`
      - Si está roto/truncado: eliminar archivo trunco con `os.RemoveAll(targetPath)`, emitir log `⚠ [reanudando] '%s' existe pero está incompleto o corrupto; recomprimiendo.`, y agregar a compresión normal.
    - Si no existe: asignar `targetPath` y procesar normalmente.
- [ ] En workers de compresión paralela y secuencial:
  - Omitir ejecución de tareas marcadas como `Skipped`, contabilizándolas para el resumen y reporte final.
- [ ] Escribir tests table-driven TDD en `compress_test.go`:
  - Lote completado relanzado con `--resume` (todos omitidos).
  - Lote interrumpido con archivo truncado relanzado con `--resume` (truncado eliminado y recompreso, válidos omitidos).
  - Verificación de no-regresión: sin `--resume`, se usa `GetUniqueName`.

### Tarea 3: Reanudación de Lotes en Descompresión (`decompress.go` y `decompress_test.go`)
- [ ] Crear función auxiliar `isArchiveFullyExtracted(archive string, dir string, info FormatInfo, password string) bool`:
  - Para streams mono-archivo: verificar existencia y tamaño del archivo resultante contra `EstimateUncompressedSize`.
  - Para contenedores (`tar`, `zip`, `7z`, `rar`): listar miembros con `ListArchiveMembers` y verificar que todos los archivos no-directorio existan y coincidan en tamaño.
- [ ] En `DoDecompress`:
  - Si `opts.Resume == true`:
    - Filtrar archivos ya completamente extraídos, emitiendo log `ℹ [reanudado] '%s' ya está descomprimido; omitiendo.`
    - Si la extracción quedó incompleta, activar modo permisivo de sobreescritura/completado de miembros rotos sin arrojar error `el archivo de salida ya existe`.
- [ ] Escribir tests table-driven TDD en `decompress_test.go`:
  - Descompresión completada relanzada con `--resume` (omitido limpiamente).
  - Descompresión truncada/incompleta relanzada con `--resume` (extrae y completa sin error).
  - Verificación de no-regresión: sin `--resume`, lanza error de existencia para flujos.

### Tarea 4: Documentación y Verificación Rigurosa
- [ ] Ejecutar `go test -race ./...` (0 data races, 100% pasando).
- [ ] Ejecutar `go vet ./...`.
- [ ] Actualizar documentación (`TODO.md`, `AGENTS.md`, `README.md` si aplica).
- [ ] Fusión en `point/04-resume-batches` y compilación final.
- [ ] Presentar informe al usuario y esperar autorización explícita antes de cualquier push remoto.
