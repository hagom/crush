# Especificación de Diseño: Compresión Incremental por Fecha / Referencia (`--newer` / `--newer-than`)

## 1. Contexto y Justificación

Al realizar respaldos periódicos (backups diarios, semanales o entregas de proyectos), la compresión completa de directorios masivos (decenas o cientos de gigabytes) resulta costosa en tiempo, CPU y almacenamiento, incluso cuando solo una mínima fracción de archivos ha sufrido cambios recientes.

La compresión incremental permite generar un archivo comprimido diferencial que contenga **únicamente los archivos modificados después de una fecha o archivo de referencia**, ahorrando hasta el 99% de tiempo y espacio.

## 2. Principios de Diseño y Filosofía Ponytail

- **Minimalismo y Cero Dependencias:** Todo el cálculo de fechas, duraciones y filtrado se implementa exclusivamente con la biblioteca estándar de Go (`time`, `os`, `path/filepath`).
- **Agnóstico al Formato:** La compresión incremental debe operar de forma consistente en todos los formatos soportados por `crush` (tuberías `tar.*`, contenedores `7z`, `zip`, `rar`, o archivos individuales).
- **Protección contra Archivos Vacíos:** Si tras evaluar el umbral de corte temporal ningún archivo ha sido modificado, `crush` no genera un archivo comprimido corrupto o vacío de 0 bytes; emite un mensaje informativo claro y finaliza limpiamente con código de salida 0.

## 3. Especificación de CLI e Interfaz

### 3.1. Flags
- `--newer <VALOR>` (o `-newer`):
  Acepta:
  - **Duraciones relativas:** `"24h"`, `"48h"`, `"30m"`, `"1h30m"`, `"1d"`, `"7d"`, `"30d"` (donde `Nd` equivale a `N * 24 horas` antes de `time.Now()`).
  - **Fechas absolutas:**
    - `"YYYY-MM-DD"` (ej. `"2026-11-01"`, interpretado a las 00:00:00 hora local)
    - `"YYYY-MM-DD HH:MM:SS"`
    - `"YYYY-MM-DD HH:MM"`
    - `"YYYY/MM/DD"`
    - Formato RFC3339 (`"2006-01-02T15:04:05Z07:00"`)
- `--newer-than <ARCHIVO>` (o `-newer-than`):
  Acepta la ruta a un archivo existente en disco (por ejemplo, el respaldo comprimido anterior `backup_anterior.tar.zst`). Extrae automáticamente su `ModTime()`.

### 3.2. Validaciones
- `--newer` y `--newer-than` son mutuamente excluyentes.
- Solo aplican en modo compresión (`-c`).
- Si el archivo de `--newer-than` no existe, se reporta error inmediato descriptivo.

## 4. Arquitectura y Contratos de Datos

### 4.1. `CompressOptions`
```go
type CompressOptions struct {
    ...
    CutoffTime time.Time
}
```

### 4.2. Función Pura de Parseo (`ParseCutoffTime` en `util.go`)
```go
func ParseCutoffTime(newerStr, newerThanFile string, now time.Time) (time.Time, error)
```
- Parsea duraciones relativas o fechas absolutas.
- Obtiene `ModTime` si se especificó un archivo de referencia.

### 4.3. Filtrado de Archivos en Compresión (`compress.go`)
1. **Detección Previa de Cambios (`HasNewerFiles` / `CollectNewerFiles`):**
   - Para cada elemento pasado a compresión:
     - Si es un archivo regular: comprobar si `fi.ModTime().After(opts.CutoffTime)`.
     - Si es un directorio: recorrer con `filepath.Walk` comprobando si existen archivos regulares con `fi.ModTime().After(opts.CutoffTime)`.
   - Si un elemento no contiene archivos modificados: se omite emitiendo:
     `ℹ [omitido] '%s' no contiene cambios posteriores a %s.`
   - Si ningún elemento en todo el lote tiene cambios:
     `ℹ No se encontraron archivos modificados después de %s. Nada que comprimir.`
2. **Empaquetado Incremental:**
   - **Tuberías Tar (`compressTarPipe` / `compressPlainTar`):**
     Inyección de `--newer-mtime="YYYY-MM-DD HH:MM:SS"` a los argumentos de GNU tar. Tar se encarga nativamente de archivar solo los ficheros modificados.
   - **Contenedores (`compress7z`, `compressZip`, `compressRar`):**
     Si el objetivo es un directorio y tiene filtro incremental, se recolectan los archivos modificados relativos y se pasan al comando de compresión, preservando la jerarquía de carpetas.

## 5. Plan de Pruebas TDD

1. **Table-driven en `util_test.go` para `ParseCutoffTime`:**
   - Duración relativa `"24h"`, `"7d"`.
   - Fecha `"2026-11-01"`, `"2026-11-01 14:30:00"`.
   - Archivo de referencia `--newer-than` con archivo existente y con archivo inexistente.
   - Conflicto cuando se proporcionan ambos flags simultáneamente.
2. **Table-driven en `compress_test.go`:**
   - Compresión con `--newer` de archivos individuales: solo incluye los más recientes.
   - Compresión con `--newer` de directorios: solo incluye los subarchivos modificados.
   - Compresión con `--newer-than`: toma el timestamp de un archivo de referencia.
   - Caso límite: ningún archivo modificado -> no genera comprimido y retorna mensaje limpio.
3. **Verificación Global:**
   - `go test -race ./...` (0 carreras de datos).
   - `go vet ./...` (0 advertencias).
