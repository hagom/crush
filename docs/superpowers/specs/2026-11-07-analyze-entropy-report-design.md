# Especificación de Diseño: Analizador Recursivo de Entropía e Informe de Compresibilidad (`crush -analyze`)

**Fecha:** 2026-11-07  
**Rama del punto:** `point/01-analyze-dir` (worktree `feat/analyze-dir`)  
**Estado:** Borrador para revisión  

---

## 1. Propósito e Intención

Cuando el usuario tiene directorios grandes o heterogéneos (proyectos de código mezclados con binarios, descargas, respaldos, multimedia o máquinas virtuales), hoy no sabe de antemano qué carpetas o archivos vale la pena comprimir, qué formato le dará el mejor resultado en cada caso, ni cuánto espacio en disco recuperaría realmente antes de lanzar una compresión pesada.

`crush -analyze` añade un modo de **estudio recursivo en solo lectura** que inspecciona un directorio (o lista de rutas), mide la entropía y redundancia real de su contenido en milisegundos sin escribir nada en disco, y entrega un **informe ejecutivo en terminal** indicando:
1. Qué subdirectorios y archivos conviene comprimir y cuáles conviene **omitir** por estar ya comprimidos o cifrados.
2. Qué formato de `crush` es el más recomendable para cada elemento (`7z`, `xz`, `tar.xz`, `zst`, `tar.zst` u `omitir`).
3. Cuánto pasaría a ocupar cada elemento y **cuánto espacio total se ahorraría** (en bytes formateados y porcentaje).

---

## 2. Objetivos, No-Objetivos y Anti-Objetivos

### Objetivos (Goals)
- **Solo lectura absoluta:** Cero archivos creados, modificados o eliminados en disco.
- **Velocidad instantánea en árboles grandes:** Analizar miles de archivos y decenas de gigabytes en pocos milisegundos/segundos mediante muestreo estratificado en paralelo (`NCPU()` workers) sin leer gigabytes completos del disco.
- **Estimación realista de ahorro:** Combinar entropía de Shannon ($H$) con una micro-sonda DEFLATE en memoria (`compress/flate` de la stdlib de Go) para capturar tanto aleatoriedad de bytes como repetición de cadenas/bloques (LZ77).
- **Informe claro y accionable:** Agrupar inteligentemente por subcarpetas de primer nivel y archivos sueltos en la raíz analizada, con opción `-v` para desglosar todos los archivos individuales y alerta explícita en **carpetas mixtas** (carpetas que mezclan archivos muy compresibles con archivos pesados ya comprimidos).
- **Sin bloqueo de instancia:** Exento de `/tmp/crush.lock` en `lockScope` para poder ejecutarse en cualquier momento.

### No-Objetivos (Non-Goals)
- No ejecuta compresión real ni ofrece prompts interactivos para comprimir al finalizar (su única responsabilidad es mostrar el informe).
- No invoca subprocesos externos (`7z`, `xz`, `zstd`, `tar`) para medir ratios; todo el estudio corre en Go puro en memoria.
- No sigue enlaces simbólicos (`symlinks`) para evitar ciclos infinitos y conteo duplicado de tamaño en disco.

### Anti-Objetivos (Anti-Goals)
- **Inundar la pantalla:** Mostrar por omisión 15,000 filas al analizar un repositorio con miles de archivos pequeños sería ilegible; por omisión se sintetiza por unidad lógica del directorio raíz analizado.
- **Falsos positivos por cabeceras engañosas:** Clasificar un `.iso` o base de datos de 4 GiB como "99% compresible" solo porque sus primeros 64 KiB son ceros.
- **Recomendar comprimir gigabytes inútiles:** Recomendar `tar.zst` sobre una carpeta de 20 GiB donde 19.8 GiB son videos `.mkv` incompresibles sin advertir que casi todo el peso debe omitirse.

---

## 3. Restricciones (Constraints)

- **100% Go `stdlib` (Go 1.27):** Cero dependencias externas en `go.mod`. Solo se emplean paquetes nativos (`os`, `io`, `path/filepath`, `math`, `compress/flate`, `sync`, `sort`, `fmt`, `strings`).
- **Principios SOLID y DRY (`AGENTS.md`):**
  - Nuevo archivo cohesivo `analyze.go` (y sus pruebas table-driven en `analyze_test.go`).
  - Reutilizar `CalculateShannonEntropy`, `mediaOrCompressedExts`, `textOrCodeExts`, `hasTool`, `sevenzBin` (`format.go`), `DefaultCleanExcludes` (`compress.go`), y `FormatSize`, `NCPU`, colores ANSI (`util.go`).
  - Cumplir con los contratos de ayuda y autocompletado validados por `main_test.go` (`TestHelpEveryCommandLineHasExplanation`, `TestHelpEveryOptionHasDescriptionAndExample`, scripts de Bash/Zsh/Fish).

---

## 4. Enfoques Evaluados

| Enfoque | Descripción | Ventajas y Compromisos | Decisión |
| :--- | :--- | :--- | :---: |
| **A. Híbrido: Entropía de Shannon + Micro-sonda `compress/flate` en RAM sobre muestra estratificada** | Lee hasta 96 KiB por archivo (en 3 puntos si $>96\text{ KiB}$). Calcula $H$. Si $H \ge 7.90$ b/B descarta al instante (0% ahorro). Si $H < 7.90$ b/B, comprime la muestra en RAM con `flate` hacia un contador de bytes sin tocar disco y calibra según el algoritmo recomendado (`xz`/`7z` vs `zst`). | Captura repetición real de palabras/estructuras (logs, SQL, código, binarios ELF, ISOs) que la entropía de 1 byte ignora. Toma `<0.3 ms` por archivo en paralelo y usa pura stdlib. | **Elegido** *(decisión de diseño)* |
| **B. Curva Matemática Pura de Shannon** | Estima el ahorro únicamente con una función lineal/polinómica de $H \in [0, 8.0]$ y la extensión del archivo. | ~30% menos uso de CPU en muestras compresibles, pero subestima fuertemente el ahorro en texto estructurado, JSON/SQL repetitivo o binarios con tablas de ceros/símbolos. | Descartado |
| **C. Pipeline de Subprocesos hacia `/dev/null`** | Invoca `zstd` / `xz` reales leyendo los archivos enteros. | Exactitud del 100%, pero tarda minutos y satura el disco igual que comprimir de verdad. Rompe el propósito de un estudio rápido. | Descartado |

---

## 5. Diseño Detallado

### 5.1 Interfaz CLI e Integración (`main.go` y `lock.go`)
- **Flag de modo:** `-analyze` (con soporte de `--analyze` nativo del paquete `flag` de Go):
  - `crush -analyze` → analiza el directorio actual (`.`).
  - `crush -analyze /ruta/dir` → analiza `/ruta/dir`.
  - `crush -analyze ruta1 ruta2 archivo.bin` → analiza cada objetivo pasado en orden (si se pasa un archivo individual, muestra el informe de ese archivo).
- **Modificadores compatibles:**
  - `-v`: Además del resumen por entradas de primer nivel, incluye el desglose detallado de todos los archivos internos analizados.
  - `-clean`: Aplica automáticamente `DefaultCleanExcludes` (ignorando `.git`, `node_modules`, `__pycache__`, `target`, `.DS_Store`, etc.) durante el escaneo.
  - `-exclude PATRÓN` (repetible): Excluye del estudio cualquier archivo o subdirectorio que coincida con el patrón glob (misma semántica que `DoCompress`).
- **Exclusión mutua de modos:** `-analyze` es un modo principal independiente; combinarlo con modos de escritura o inspección incompatible (`-c`, `-d`, `-a`, `-convert`, `-watch`, `--bench`, `-l`, `-t`, `-r`, `-tree`, `-find`, `-diff`) devuelve un error claro de uso.
- **`lockScope` (`lock.go`):** Se registra el campo `Analyze bool` en `lockScope`, manteniéndolo fuera de `NeedsLock()` (retorna `false`).

### 5.2 Recorrido Recursivo y Manejo de Casos Límite
1. `filepath.WalkDir` recorre la raíz sin seguir symlinks:
   - Si `d.Type()&os.ModeSymlink != 0` o no es directorio ni archivo regular (`!d.Type().IsRegular()`): se ignora sin error.
   - Si un directorio coincide con `-exclude` / `-clean`: retorna `filepath.SkipDir` para no recorrer miles de inodos innecesarios (ej. `.git` o `node_modules`).
   - Si un directorio o archivo devuelve error de permisos (`os.IsPermission(err)`): incrementa un contador `unreadableCount`, emite una nota suave al final y **continúa** el estudio sin abortar.
   - Archivos regulares de `0 bytes`: cuentan en el número de archivos pero tienen entropía `0.0`, ahorro `0 B` y estado `vacío / omitir`.

### 5.3 Muestreo Estratificado de 3 Puntos (`sampleFile`)
Para cada archivo regular con `size > 0`:
- Se reutilizan buffers de `96 KiB` (`98,304 bytes`) mediante `sync.Pool` en un pool de `NCPU()` workers concurrentes (con límite máximo de 32 descriptores abiertos en simultáneo para no agotar `ulimit -n`).
- **Si `size <= 96 KiB`:** Lectura única desde el inicio (`io.ReadFull` / `Read`).
- **Si `size > 96 KiB`:** Tres lecturas `f.ReadAt` de `32 KiB` cada una concatenadas en el buffer de muestra:
  1. Inicio: `offset = 0`
  2. Centro: `offset = (size - 32768) / 2`
  3. Final: `offset = size - 32768`

### 5.4 Clasificación de Compresibilidad, Formato Recomendado y Estimación de Tamaño

Para cada archivo muestreado con muestra `sample` y tamaño `size`:

1. **Cálculo de Entropía:** `H = CalculateShannonEntropy(sample)` ($0.00 \le H \le 8.00$).
2. **Detección de Incompresibles:**
   - Un archivo se marca como **`Incompressible`** (`Compresible: Nula`, recomendación: `omitir`, `estimatedSize = size`, ahorro `0 B`) si cumple cualquiera de:
     - `len(sample) >= 4096 && H >= 7.90` (datos ya comprimidos o cifrados).
     - Extensión en `mediaOrCompressedExts` **y** `H >= 7.20` (ej. contenedores `.mp4`, `.mkv`, `.jpg`, `.zip`, `.7z`, `.rar`, `.gz`, `.zst` que incluso si tuvieran un pequeño bloque de metadatos siguen siendo ya comprimidos; nota: si un `.iso` o `.pdf` tiene baja entropía real $H < 7.20$, **sí** se evalúa con la micro-sonda porque muchas imágenes `.iso` de PS2/Linux y PDFs sin comprimir reducen un 30%-60%).
     - Archivos diminutos (`size < 512` bytes) donde las cabeceras del contenedor igualarían o superarían el ahorro (`estimatedSize = size`, recomendación `omitir`).
3. **Micro-sonda en RAM (`probeDeflateRatio`) para archivos potencialmente compresibles:**
   - Se pasa `sample` por un `flate.Writer` (nivel `flate.DefaultCompression`, reutilizado vía `sync.Pool` con `Reset(&counter)`) escribiendo a un simple contador de bytes en memoria.
   - `deflateRatio = float64(compressedSampleBytes) / float64(len(sample))`.
   - Si `deflateRatio >= 0.96`: los datos no comprimen en la práctica → se reclasifica como **`Incompressible`** (`omitir`, `estimatedSize = size`), evitando recomendar compresión cuando el ahorro real sería $< 4\%$.
4. **Selección de Nivel y Formato Recomendado:**
   - **Compresibilidad Alta** (`deflateRatio <= 0.45` o (`H < 5.50` y `deflateRatio <= 0.60`), o extensión en `textOrCodeExts` con `deflateRatio < 0.70`):
     - Formato para archivo individual: `xz` (o `7z` si `xz` no está instalado y `7z` sí).
     - Factor de proyección LZMA2 sobre DEFLATE: `estRatio = clamp(deflateRatio * 0.78, 0.01, 0.94)`.
   - **Compresibilidad Media** (resto de casos con `deflateRatio < 0.96`):
     - Formato para archivo individual: `zst` (excelente velocidad/ratio en binarios, assets y entropía media).
     - Factor de proyección Zstandard ultra (`-22`) sobre DEFLATE: `estRatio = clamp(deflateRatio * 0.86, 0.02, 0.95)`.
   - `estimatedSize = int64(math.Round(float64(size) * estRatio))` (nunca mayor a `size`).

### 5.5 Agregación por Directorios y Detección de Carpetas Mixtas

Cuando el objetivo analizado es un directorio `rootDir`:
- Sus hijos directos se agrupan en entradas de informe (`EntryReport`):
  - Cada **subdirectorio directo** `subDir/` agrega recursivamente las métricas de todos los archivos que contiene:
    - `TotalSize`: suma de tamaños de todos sus archivos.
    - `WeightedEntropy`: promedio de entropía ponderado por el peso en bytes de cada archivo ($\frac{\sum H_i \cdot size_i}{\sum size_i}$).
    - `CompressibleSize`: suma de bytes de los archivos internos que sí son compresibles.
    - `IncompressibleSize`: suma de bytes de los archivos internos incompresibles.
    - `EstimatedSize`: suma de `estimatedSize` de sus archivos internos.
  - **Recomendación para un subdirectorio `subDir/`:**
    - Si el ahorro total de la carpeta es `< 5%` (o `CompressibleSize == 0`): estado **`✗ Nula`** → formato **`omitir`**.
    - Si la carpeta es **mixta** (`IncompressibleSize >= 30%` de `TotalSize` y `CompressibleSize >= 1 MiB` e `IncompressibleSize >= 1 MiB`): se marca su formato recomendado con nota **`selectivo (ver nota)`** (ej. `7z / zst*`) y al pie de la tabla se detalla:  
      `ℹ 'subDir/' es mixta: contiene X compresibles (ahorro Y) y Z ya comprimidos que conviene excluir.`
    - Si predomina compresibilidad Alta (ahorro estimado sobre la carpeta $\ge 55\%$): recomienda **`7z`** (si `7z` disponible, o **`tar.xz`**).
    - Si predomina compresibilidad Media (ahorro entre $5\%$ y $55\%$): recomienda **`tar.zst`** (o **`7z`**).
  - Cada **archivo directo** en `rootDir` aparece como fila `FILE` con su recomendación individual (`xz`, `zst`, `omitir`).
- Las filas se ordenan por **mayor ahorro potencial en bytes (descendente)** (y luego por tamaño descendente para los `omitir`), de modo que el usuario vea arriba de inmediato dónde está la mayor ganancia de espacio.
- Con **`-v` (verbose):** además de la tabla principal, se muestra el desglose individual de archivos compresibles e incompresibles dentro de las subcarpetas.

### 5.6 Formato Visual del Informe

Salida escrita en el `io.Writer` inyectado (`os.Stdout` por defecto en CLI para permitir paginar o redirigir con `| less` si el usuario quiere, usando colores ANSI constantes de `util.go`):

```text
=== Informe de Compresibilidad: <ruta> ===

  Elemento                      Tipo   Tamaño Actual   Entropía   Compresible   Formato Rec.   Tamaño Est.   Ahorro Est.
  ──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
  logs-produccion/              DIR         1.40 GiB   4.12 b/B   ✓ Alta        7z               245.0 MiB     1.16 GiB (82.9%)
  base_datos.sql                FILE      820.00 MiB   3.95 b/B   ✓ Alta        xz               138.0 MiB   682.00 MiB (83.2%)
  binarios-build/               DIR       450.00 MiB   6.28 b/B   ✓ Media       tar.zst          215.0 MiB   235.00 MiB (52.2%)
  descargas-mixtas/             DIR         3.20 GiB   7.41 b/B   ◐ Parcial     selectivo*         2.85 GiB   358.40 MiB (10.9%)
  videos-raw/                   DIR         4.00 GiB   7.98 b/B   ✗ Nula        omitir             4.00 GiB        0 B (0.0%)

  Notas sobre carpetas mixtas (*):
    • descargas-mixtas/: 512.00 MiB compresibles (ahorrarían 358.40 MiB con 7z/zst) + 2.70 GiB ya comprimidos que conviene omitir.

=== Resumen del Estudio ===
  Archivos analizados:           1,248 archivos en 14 directorios
  Tamaño total actual:           9.85 GiB
  Ya comprimidos (omitir):       6.70 GiB (no reducirán tamaño)
  Datos compresibles útiles:     3.15 GiB  ──►  751.60 MiB estimados
  Ahorro total estimado:         2.42 GiB (76.7% sobre compresibles | 24.5% del directorio total)
===========================
```

---

## 6. Decisiones Propuestas vs Delegadas al Implementador

### Decisiones de Diseño Propuestas (para validación del usuario)
1. **Nombre del flag:** `-analyze` / `--analyze` (acepta directorio opcional; usa `.` si no se indica).
2. **Motor de estimación:** Enfoque A híbrido (`CalculateShannonEntropy` + micro-sonda `compress/flate` en RAM sobre muestra estratificada de hasta 96 KiB).
3. **Agrupación del informe:** Vista limpia agrupada por hijos directos del directorio analizado (subcarpetas + archivos sueltos) ordenada por mayor ahorro en bytes, con alertas para carpetas mixtas y desglose completo al pasar `-v`.

### Detalles Delegados al Implementador
- Ancho dinámico/truncado elegante de nombres largos de columna en terminal (siguiendo el estilo de `FormatBenchTable` / `ProgressTracker`).
- Ajuste fino de constantes de umbral de truncamiento de nombres y reutilización de `flate.Writer` / buffers en `sync.Pool`.
