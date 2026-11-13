# TODO — Ideas y Mejoras Futuras para crush

Este documento registra el historial de análisis de propuestas técnicas y su estado de resolución para `crush`.

---

## 1. Rendimiento y Eficiencia

### Detección Automática de Entropía Previa (Shannon Entropy)
- **Estado:** ✅ **IMPLEMENTADA**
- **Descripción:** Análisis instantáneo de los primeros 64 KiB de cabecera mediante la fórmula de entropía de Shannon ($H = -\sum p_i \log_2 p_i$). Si los datos presentan alta entropía ($\ge 7.90$ bits/byte en muestras $\ge 4$ KiB), detecta que el archivo ya está comprimido o cifrado.
- **Acción implementada:**
  - En modo `-f auto`: Asigna automáticamente formato ultrarrápido (`lz4`) emitiendo un aviso limpio: `ℹ 'archivo' ya parece estar comprimido; usando compresión rápida (lz4).`
  - En formatos pesados manuales (`-f xz`, `-f 7z`, etc.): Emite advertencia preventiva inmediata: `⚠ 'archivo' ya parece estar comprimido o cifrado; la compresión apenas reducirá su tamaño.`

### Listado Human-Readable y Elegante de Archivos (`crush -l`)
- **Estado:** ✅ **IMPLEMENTADA**
- **Descripción:** Estandarización y formateo visual de la salida de `crush -l`. Elimina los banners y volcados crudos de herramientas subyacentes (`7z l`, `unzip`, `tar`), sustituyéndolos por una tabla estructurada con permisos (`-rw-r--r--`, `drwxr-xr-x`), tamaños métricos dinámicos (`FormatSize`), fecha y hora de modificación, carpetas coloreadas y resumen de archivos, carpetas, tamaño sin comprimir y ratio de compresión/ahorro.

### Modo Reanudación de Lotes Interrumpidos (`--resume`)
- **Estado:** ✅ **IMPLEMENTADA**
- **Descripción:** Mecanismo resiliente y minimalista (*Ponytail*) para reanudar operaciones interrumpidas en lotes de compresión (`crush -c --resume`) y descompresión (`crush -d --resume`) sin archivos de estado intermedios ni metadatos ocultos.
- **Acción implementada:**
  - **En compresión:** Comprueba si el archivo comprimido destino ya existe y verifica su integridad real (`testArchiveIntegrity`). Si el archivo está completo e íntegro, lo omite con aviso `ℹ [reanudado]`. Si el archivo existe pero quedó truncado o corrupto por una interrupción previa, lo elimina y recomprime limpiamente con aviso `⚠ [reanudando]`.
  - **En descompresión:** Comprueba el estado de extracción consultando el directorio destino contra los miembros y tamaños reales del archivo (`ListArchiveMembers`). Si todos los archivos ya existen y coinciden en tamaño, omite el archivo con aviso `ℹ [reanudado]`. Si la extracción quedó incompleta, reanuda permitiendo sobrescribir las partes truncas y finalizando la extracción sin arrojar errores de colisión.
  - Si todos los elementos del lote ya estaban concluidos, emite de inmediato: `✓ Todos los archivos ya estaban completados. Nada que reanudar.` sin consumir CPU ni I/O.

### Compresión Incremental por Fecha / Referencia (`--newer` / `--newer-than`)
- **Estado:** ✅ **IMPLEMENTADA**
- **Descripción:** Filtrado temporal de archivos para copias de seguridad incrementales rápidas y precisas. Permite comprimir exclusivamente los archivos modificados después de una fecha/hora, duración relativa (`24h`, `7d`, `30m`), o con posterioridad a la marca de tiempo de un archivo de referencia (`backup.tar.gz`).
- **Acción implementada:**
  - **Parser polimórfico (`ParseCutoffTime` en `util.go`):** Reconoce duraciones relativas estándar (`24h`, `30m`, `7d`), fechas absolutas (`YYYY-MM-DD`, `YYYY-MM-DD HH:MM:SS`, RFC3339) y marcas de tiempo de archivos de referencia vía `os.Stat(ref).ModTime()`.
  - **Integración nativa con `tar`:** Envío del flag de kernel/sistema `--newer-mtime="YYYY-MM-DD HH:MM:SS"` directamente a los flujos tar (`tar.gz`, `tar.xz`, etc.) sin sobrecosto de I/O.
  - **Soporte transparente en formatos contenedor:** En `7z`, `zip` y `rar`, recolección recursiva respetando rutas relativas (`collectNewerFiles`) y descartando directorios sin cambios.
  - **Filtrado temprano zero-waste:** Si ningún archivo califica para compresión, emite mensaje informativo y finaliza con código de salida 0 sin crear archivos vacíos ni consumir I/O.
  - **Completado de shell:** Añadido soporte en bash, zsh y fish para `--newer` y `--newer-than` (con autocompletado de archivos en este último).

---

## 2. Seguridad y Robustez

### Sandbox de Extracción contra Zip-Slip (`os.OpenRoot`) — Opción C
- **Estado:** ✅ **IMPLEMENTADA** (`cb75b71`, `64f0beb`)
- **Descripción:** Confinamiento de toda escritura y comprobación de extracción al directorio destino, combinando el sandbox nativo de Go para las rutas escritas por `crush` con el endurecimiento de las herramientas externas.
- **Acción implementada:**
  - **Saneamiento de rutas (`sanitizeArchiveRelPath` en `decompress.go`):** normaliza `\` a `/`, aplica `path.Clean` y `filepath.IsLocal`; descarta miembros con `../`, rutas absolutas o `..\`, aceptando nombres legítimos como `foo..bar.txt`. Lo consumen `resolveOutputs` e `isArchiveFullyExtracted`.
  - **Escrituras dentro de `*os.Root`:** `decompressSingle` (casos `.lrz` y formatos de flujo), `checkAndApplyIsoExtensionInRoot` e `isArchiveFullyExtracted` operan con `os.OpenRoot(dir)` y `root.Lstat`; `validateOutputName` rechaza nombres de salida no locales.
  - **Sin sobrescritura implícita:** `openDecompressOutput` usa `O_EXCL` salvo con `-force`/`--resume`; `ensureLrzOutputSafe` rechaza symlinks existentes; `decompressFile` usa `os.Lstat` para detectar salidas previas.
  - **Extractores externos endurecidos:** `buildTarExtractArgs` (`--keep-old-files`, `--wildcards --` con filtro), `build7zExtractArgs` (`-aos`/`-aoa`), `unzip -n`/`-o`, `rar -o-`/`-o+`; separador `--` y `safeFlagArg` en `tar`, `7z`, `unzip`, `rar`, `listTarMembers`, `listRarMembers` y `test_cmd.go`; todas las invocaciones vía `execCommand`.

### Auditoría Completa de Bugs y Vulnerabilidades
- **Estado:** ✅ **CORREGIDA** (rama `fix/audit-critical-bugs`, commits `9ab444e` a `5082c5d`)
- **Acción implementada:**
  - **Conversión (`9ab444e`):** ruta de salida relativa resuelta a absoluta y verificación del destino antes de borrar el origen.
  - **Compresión (`aaf44e2`):** conservación de originales en modo incremental, protección frente a `rar -df` y respeto de exclusiones y corte temporal en contenedores y en `-a`.
  - **Pipes (`ac778e4`):** `waitPipePair` evita bloqueos de `io.Pipe` cuando el consumidor falla antes de tiempo.
  - **CLI y watcher (`25180c1`):** `reorderArgs` tolera argumentos vacíos y `--`; validación de conflictos entre todos los modos; watcher inotify con `epoll` cancelable.
  - **Seguridad, lock y logs (`8c7dd24`):** lock y logs en directorios privados (`0700`/`0600`), enmascarado de contraseñas en logs verbose (`redactArgs`), corrección de carrera en el log y de la comprobación de contraseñas con archivos de 0 bytes.
  - **Núcleo (`5082c5d`):** techo de hilos también en compresión secuencial (`resolveThreadCap`), `FormatSize` en Go puro, tope de desbordamiento en sumas ZIP64, distinción entre disco lleno y error de `df`, progreso seguro con UTF-8, `GetUniqueName` con `Lstat` sin límite de contador, `ParseFormatFromExt` sobre el nombre base y `crush -r` en streaming (`CompressReadTo`).

---

## 3. Propuestas Evaluadas y Descartadas

### Extracción Quirúrgica de Miembros a Stdout (`crush --cat`)
- **Estado:** ❌ **DESCARTADA**
- **Motivo:** Principio de minimalismo y pragmatismo CLI (*Ponytail* / YAGNI). La extracción quirúrgica a `stdout` sobre archivos masivos o contenedores complejos resulta redundante y propensa a cuellos de botella frente a las opciones existentes: `crush -r` para flujos directos y `crush -d -filter` para extracción selectiva limpia.

### Registros de Recuperación (*Recovery Records* / Paridad PAR2)
- **Estado:** ❌ **DESCARTADA**
- **Motivo:** Rompe el principio de minimalismo (*Ponytail*). La paridad Reed-Solomon añade complejidad matemática innecesaria en la biblioteca estándar sin aportar al objetivo principal de `crush` (compresión/descompresión de alto rendimiento). La detección de integridad ya está cubierta de forma completa y criptográfica mediante SHA-256 (`-hash`, `crush -verify` y `crush -t`).

### Modo Servidor / Socket Streaming UNIX (`crush serve`)
- **Estado:** ❌ **DESCARTADA**
- **Motivo:** Sobreingeniería clara (YAGNI). La sobrecarga de inicio del binario en Linux es de apenas 2 milisegundos. `crush` ya soporta streaming UNIX directo mediante tuberías estándar (`|`) y FIFOs (`mkfifo`) con aceleración en espacio de kernel `splice(2)` zero-copy y buffers de 1 MiB (`F_SETPIPE_SZ`). Introducir un daemon en segundo plano añadiría complejidad de gestión de procesos sin beneficio real de rendimiento.

---

## 4. Propuestas Pospuestas para Futuras Versiones

### Modo Fondo / Prioridad Amigable (`--idle` / `--low-priority`)
- **Estado:** ⏸️ **POSPUESTA**
- **Descripción:** Ejecución de tareas pesadas de compresión/descompresión en segundo plano sin penalizar la interactividad del sistema ni provocar congelamientos en el escritorio o juegos.
- **Detalle técnico previsto:**
  - Prioridad de CPU amigable mediante `nice +19` (`syscall.Setpriority(syscall.PRIO_PROCESS, 0, 19)`), cediendo ciclos de procesador al instante ante aplicaciones interactivas.
  - Prioridad de E/S de disco de fondo mediante `IOPRIO_CLASS_IDLE` (`syscall.Syscall(SYS_ioprio_set, ...)` en Linux), accediendo al almacenamiento solo cuando ninguna otra aplicación esté solicitando lecturas/escrituras.
  - Herencia automática a toda la cadena de subprocesos (`tar`, `pigz`, `xz`, `7z`, `zstd`, etc.) mediante semántica estándar UNIX sin necesidad de invocar utilidades externas (`nice`/`ionice`).
  - Implementación minimalista sin dependencias externas (100% Go stdlib).

