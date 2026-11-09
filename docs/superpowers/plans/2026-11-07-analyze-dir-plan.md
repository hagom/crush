# Plan de Implementación: Analizador Recursivo de Entropía e Informe de Compresibilidad (`crush -analyze`)

**Rama:** `feat/analyze-dir` en worktree `.worktrees/feat-analyze-dir`  
**Diseño de referencia:** `docs/superpowers/specs/2026-11-07-analyze-entropy-report-design.md`  
**Convenciones:** Go 1.27 puro (`stdlib`), TDD estricto (rojo → verde → refactor), principios SOLID y Ponytail.

---

## Estructura de Archivos

- **Nuevo: `analyze.go`**:
  - `FileAnalysis`, `EntryReport`, `AnalyzeSummary`, `AnalyzeOptions`.
  - `sampleFile`: muestreo de 3 puntos (hasta 96 KiB) en `sync.Pool`.
  - `probeDeflateRatio`: micro-sonda en memoria usando `compress/flate`.
  - `AnalyzeFile`: clasificación heurística por archivo.
  - `DoAnalyze`: recorrido concurrente, agregación jerárquica y renderizado de informe.
  - `renderAnalyzeReport`: tabla visual alineada con alertas para carpetas mixtas.
- **Nuevo: `analyze_test.go`**:
  - Pruebas unitarias y de integración de muestreo, sonda, cálculo, agregación y banderas `-v`, `-clean`, `-exclude`.
- **Modificado: `lock.go`**:
  - Incorporar `Analyze bool` en `lockScope` (`NeedsLock() == false`).
- **Modificado: `main.go`**:
  - Registro de flag `-analyze` y `--analyze`.
  - Validación de exclusión de modos y dispatch a `DoAnalyze`.
  - Actualización de textos de ayuda y ejemplos en `printHelp()`.
- **Modificado: `main_test.go`**:
  - Validación de CLI, concurrencia, flags y cobertura estricta de ayuda y ejemplos.
- **Modificado: `README.md` y `AGENTS.md`**:
  - Documentar el nuevo comando, parámetros y ejemplos.

---

## Tareas de Implementación

### Tarea 1: Motor de Muestreo de 3 Puntos y Micro-Sonda DEFLATE
- [ ] **1.1 Escribir pruebas unitarias fallando en `analyze_test.go` (ROJO):**
  - `TestSampleFile_SmallMediumLarge`: probar muestreo en archivos de 10 KiB, 90 KiB y 500 KiB verificando tamaños de muestra y lecturas `ReadAt`.
  - `TestProbeDeflateRatio`: verificar que texto plano obtenga ratio $\le 0.40$, ceros obtengan $<0.05$ y datos pseudo-aleatorios uniformes obtengan $\ge 0.98$.
- [ ] **1.2 Implementar `sampleFile` y `probeDeflateRatio` en `analyze.go` (VERDE):**
  - Implementar pool de buffers `sync.Pool` para 96 KiB.
  - Implementar lectura de 3 puntos (inicio, centro y fin) con `ReadAt`.
  - Implementar sonda DEFLATE en memoria con `flate.Writer` hacia un contador atómico.
- [ ] **1.3 Verificar que las pruebas pasen:**
  - `go test -v -run "TestSampleFile|TestProbeDeflateRatio" ./...`
- [ ] **1.4 Commit:** `test: add sampling and deflate probe tests` y `feat: implement sampleFile and probeDeflateRatio`.

---

### Tarea 2: Clasificación de Compresibilidad y Formato por Archivo (`AnalyzeFile`)
- [ ] **2.1 Escribir pruebas unitarias en `analyze_test.go` (ROJO):**
  - `TestAnalyzeFile_Classification`:
    - Archivo de texto plano (`sample.txt`) $\to$ Alta compresibilidad, formato `xz` o `7z`, ahorro $>60\%$.
    - Archivo pseudo-aleatorio ($H \ge 7.90$) $\to$ Incompresible, formato `omitir`, ahorro $0\%$.
    - Archivo binario estructurado $\to$ Media compresibilidad, formato `zst`.
    - Archivo vacío (0 bytes) $\to$ Incompresible (`omitir`), ahorro $0\%$.
    - Archivo diminuto ($<512$ bytes) $\to$ Incompresible (`omitir`).
- [ ] **2.2 Implementar `AnalyzeFile` en `analyze.go` (VERDE):**
  - Usar `sampleFile`, `CalculateShannonEntropy` y `probeDeflateRatio`.
  - Mapear reglas de formatos y ratios calibrados.
- [ ] **2.3 Verificar pruebas:**
  - `go test -v -run TestAnalyzeFile ./...`
- [ ] **2.4 Commit:** `feat(analyze): implement file compressibility classification`.

---

### Tarea 3: Recorrido Recursivo, Agregación por Directorio y Detección Mixta (`DoAnalyze`)
- [ ] **3.1 Escribir pruebas unitarias e integración en `analyze_test.go` (ROJO):**
  - `TestDoAnalyze_DirectoryAggregation`:
    - Crear árbol temporal con subcarpetas de texto (`src/`), subcarpetas de video/aleatorio (`media/`) y subcarpetas mixtas (`mixed/`).
    - Validar que `DoAnalyze` calcule sumas correctas, identifique `mixed/` con alerta `selectivo*` y ordene de mayor a menor ahorro.
  - `TestDoAnalyze_FiltersAndClean`:
    - Validar que `-clean` y `-exclude` omitan inodos correspondientes.
  - `TestDoAnalyze_SingleFileTarget`:
    - Validar que analizar un archivo individual emita su informe respectivo sin errores.
- [ ] **3.2 Implementar `DoAnalyze` y renderizado de informe en `analyze.go` (VERDE):**
  - Recorrido con `filepath.WalkDir` sin symlinks.
  - Paralelización con semáforo de descriptores.
  - Agrupación por entrada de primer nivel y cálculo de ponderaciones.
  - Renderizado tabular alineado con tabla de notas para carpetas mixtas y resumen final.
- [ ] **3.3 Verificar pruebas:**
  - `go test -v -run TestDoAnalyze ./...`
- [ ] **3.4 Commit:** `feat(analyze): implement directory aggregation and report rendering`.

---

### Tarea 4: Integración CLI en `main.go`, `lock.go` y Ayuda
- [ ] **4.1 Actualizar `lock.go`:**
  - Agregar `Analyze bool` en `lockScope`. `lockScope.NeedsLock()` retorna `false` cuando solo `Analyze` está activo.
- [ ] **4.2 Integrar flags en `main.go`:**
  - Agregar `analyzeFlag := flag.Bool("analyze", false, "...")`.
  - Exclusión mutua de modos: verificar que `-analyze` no se mezcle con `-c`, `-d`, etc.
  - Dispatch a `DoAnalyze(targets, opts, os.Stdout)`.
  - Actualizar `printHelp()` incluyendo descripción y comandos de ejemplo requeridos por los tests.
- [ ] **4.3 Escribir pruebas de CLI en `main_test.go`:**
  - `TestMainAnalyzeCLI`: ejecutar binario compilado con `-analyze` sobre directorio temporal.
  - Asegurar que `TestHelpEveryCommandLineHasExplanation` y `TestHelpEveryOptionHasDescriptionAndExample` pasen en verde.
- [ ] **4.4 Verificar todas las pruebas de `main`:**
  - `go test -v -run "TestHelp|TestMainAnalyze" ./...`
- [ ] **4.5 Commit:** `feat(cli): integrate -analyze flag and help examples`.

---

### Tarea 5: Auditoría Exhaustiva de Código, Seguridad y Rendimiento
- [ ] **5.1 Ejecución de suite completa con detector de carreras de Go:**
  - `go test -race ./...` (todos los paquetes).
- [ ] **5.2 Análisis estático y linter de Go:**
  - `go vet ./...`
- [ ] **5.3 Auditoría de fugas de recursos y condiciones de carrera:**
  - Verificar cierre de archivos (`defer f.Close()`).
  - Límite de descriptores abiertos concurrentes para no agotar `RLIMIT_NOFILE`.
  - Manejo de rutas vacías, inexistentes o sin permisos (`permission denied`).
- [ ] **5.4 Prueba funcional real en sistema de archivos:**
  - Compilar `./crush` y probar `crush -analyze` sobre directorios reales.
- [ ] **5.5 Actualización de documentación:**
  - Actualizar `README.md` y `AGENTS.md`.
- [ ] **5.6 Commit de cierre del Punto 1:**
  - Preparar integración a la rama base `point/01-analyze-dir`.
