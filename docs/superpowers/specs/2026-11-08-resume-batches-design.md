# Especificación de Diseño: Modo Reanudación de Lotes Interrumpidos (`--resume`)

## 1. Contexto y Justificación

Al procesar lotes masivos de compresión o descompresión (decenas o cientos de archivos grandes como imágenes ISO, máquinas virtuales, colecciones de medios o copias de seguridad):
1. **Riesgo de interrupción:** Una tarea larga puede ser interrumpida en cualquier momento por el usuario (Ctrl+C / SIGINT), corte de energía, falta temporal de espacio en disco, reinicio del sistema o desconexión de red.
2. **Comportamiento actual sin `--resume`:**
   - **En compresión (`crush -c`):** Al relanzar el comando sobre los mismos archivos, `generateCompressionTasks` detecta que el archivo destino ya existe y llama a `GetUniqueName`, creando copias duplicadas (`archivo_1.7z`, `archivo_2.7z`), desperdiciando almacenamiento y recomprimiendo todo desde cero.
   - **En descompresión (`crush -d`):** Al relanzar el comando, para flujos individuales (`.gz`, `.xz`, etc.) falla de inmediato con el error `el archivo de salida ... ya existe (use --force para sobrescribir)`. Si se fuerza con `--force`, se sobrescribe y extrae absolutamente todo desde el inicio, descartando el trabajo previo completado.

## 2. Principios de Diseño y Filosofía Ponytail

- **Minimalismo y Cero Metadatos Ocultos (Unix Way):** No se crean archivos de estado temporales ni bases de datos (`.crush_resume.json` ni sqlite) que ensucien los directorios del usuario. El propio sistema de archivos y las cabeceras estándar de los archivos comprimidos son la única fuente de verdad.
- **Verificación sobre Suposición:**
  - No basta con que el archivo destino exista; se debe comprobar si está **completo e íntegro**.
  - Si un archivo se interrumpió a mitad de escritura (archivo truncado o corrupto):
    - En compresión: el test rápido de integridad detecta la cabecera/stream roto, elimina el remanente incompleto y lo recomprime limpiamente.
    - En descompresión: si el tamaño del archivo extraído no coincide con el tamaño real sin comprimir del miembro, se reanuda la extracción sobrescribiendo la parte trunca.
  - Si un archivo ya está terminado e íntegro:
    - Se omite con un mensaje informativo claro: `ℹ [reanudado] 'archivo' ya existe y es válido; omitiendo.`
    - Se incluye en el resumen final como omitido/reanudado.

## 3. Arquitectura y Contratos de Datos

### 3.1. Flags de CLI y Opciones
- Flag: `--resume` / `-resume` (en `main.go`).
- En `CompressOptions`:
  ```go
  type CompressOptions struct {
      ...
      Resume bool
  }
  ```
- En `DecompressOptions`:
  ```go
  type DecompressOptions struct {
      ...
      Resume bool
  }
  ```

### 3.2. Lógica de Compresión (`compress.go`)
1. En `generateCompressionTasks(items []string, opts CompressOptions)`:
   - Para cada elemento `item` y formato `fmtChoice`:
     - Determinar la ruta canónica de salida esperada:
       `targetPath := filepath.Join(targetDir, baseName + "." + ext)`
     - Si `opts.Resume == true`:
       - Verificar si `targetPath` existe en el disco (`os.Stat`):
         - Si existe:
           - Evaluar integridad del archivo mediante verificación rápida:
             `status, err := testArchiveIntegrity(targetPath, TestOptions{Quick: true, Password: opts.Password})`
           - Si `status == "OK"` y `err == nil`:
             - El archivo fue completado exitosamente con anterioridad.
             - Marcar la tarea como `Skipped: true`.
             - Emitir log: `ℹ [reanudado] '%s' ya existe y es válido; omitiendo compresión.`
           - Si `status != "OK"` o `err != nil`:
             - El archivo quedó incompleto o corrupto por una interrupción previa.
             - Limpiar el remanente truncado (`os.RemoveAll(targetPath)`).
             - Emitir log: `⚠ [reanudando] '%s' existe pero está incompleto o corrupto; recomprimiendo.`
             - Asignar `task.OutPath = targetPath` (sin sufijos numéricos `_1`).
         - Si no existe:
           - Asignar `task.OutPath = targetPath` y proceder normalmente.
       - Si `opts.Resume == false`:
         - Mantener comportamiento actual llamando a `GetUniqueName`.
2. En la ejecución del lote:
   - Las tareas `Skipped` no lanzan procesos de compresión ni consumen CPU.
   - Si todas las tareas fueron omitidas:
     `✓ Todos los archivos ya estaban completados. Nada que reanudar.`

### 3.3. Lógica de Descompresión (`decompress.go`)
1. Para cada archivo comprimido `f`:
   - Determinar el directorio destino: `dir := ResolveDecompressDir(f, opts.OutputDir)`.
   - Si `opts.Resume == true`:
     - **Flujos individuales (archivos mono-stream como `.gz`, `.xz`, `.zst`, `.bz2`):**
       - Archivo destino esperado: `outputPath := filepath.Join(dir, stripCompressionExt(filepath.Base(f)))`.
       - Si `outputPath` existe:
         - Comparar tamaño actual contra tamaño esperado sin comprimir (`EstimateUncompressedSize(f)`).
         - Si coincide: omitir descompresión (`[reanudado]`).
         - Si no coincide: descompresión en modo reanudación (permite sobrescribir el archivo parcial truncado sin arrojar error de existencia).
     - **Contenedores (`.tar`, `.tar.*`, `.zip`, `.7z`, `.rar`):**
       - Inspeccionar miembros mediante `ListArchiveMembers(f, opts.Password)`.
       - Si todos los miembros no-directorio existen en `dir` con sus tamaños correspondientes `m.Size`:
         - El archivo contenedor ya fue extraído completamente en una ejecución previa.
         - Omitir descompresión (`[reanudado]`).
       - Si faltan miembros o difieren en tamaño:
         - La extracción quedó truncada a mitad de camino.
         - Proceder con la extracción en modo reanudación (`Force` selectivo para sobrescribir archivos rotos y completar los restantes).

## 4. Plan de Pruebas TDD

1. **Table-driven en `compress_test.go`:**
   - `TestCompressResume_AllCompleted`: Comprime lote completo; relanza con `--resume`; verifica que todos se omitan con éxito y ningún archivo sea reescrito.
   - `TestCompressResume_CorruptedOrTruncated`: Comprime lote; simula archivo truncado de 5 bytes; relanza con `--resume`; verifica detección de archivo roto, eliminación limpia y recompresión exitosa.
   - `TestCompressResume_DefaultWithoutResume`: Verifica que sin `--resume` se mantenga el comportamiento de `GetUniqueName` (`_1.ext`).
2. **Table-driven en `decompress_test.go`:**
   - `TestDecompressResume_AllCompleted`: Descomprime lote; relanza con `--resume`; verifica que todos se omitan sin error.
   - `TestDecompressResume_Incomplete`: Simula extracción parcial; relanza con `--resume`; verifica que termine la extracción sin arrojar error de colisión de archivos.
3. **Verificación Global:**
   - `go test -race ./...` (cero condiciones de carrera).
   - `go vet ./...` (cero advertencias estáticas).
