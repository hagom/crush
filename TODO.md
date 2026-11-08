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

---

## 2. Propuestas Evaluadas y Descartadas

### Extracción Quirúrgica de Miembros a Stdout (`crush --cat`)
- **Estado:** ❌ **DESCARTADA**
- **Motivo:** Principio de minimalismo y pragmatismo CLI (*Ponytail* / YAGNI). La extracción quirúrgica a `stdout` sobre archivos masivos o contenedores complejos resulta redundante y propensa a cuellos de botella frente a las opciones existentes: `crush -r` para flujos directos y `crush -d -filter` para extracción selectiva limpia.

### Registros de Recuperación (*Recovery Records* / Paridad PAR2)
- **Estado:** ❌ **DESCARTADA**
- **Motivo:** Rompe el principio de minimalismo (*Ponytail*). La paridad Reed-Solomon añade complejidad matemática innecesaria en la biblioteca estándar sin aportar al objetivo principal de `crush` (compresión/descompresión de alto rendimiento). La detección de integridad ya está cubierta de forma completa y criptográfica mediante SHA-256 (`-hash`, `crush -verify` y `crush -t`).

### Modo Servidor / Socket Streaming UNIX (`crush serve`)
- **Estado:** ❌ **DESCARTADA**
- **Motivo:** Sobreingeniería clara (YAGNI). La sobrecarga de inicio del binario en Linux es de apenas 2 milisegundos. `crush` ya soporta streaming UNIX directo mediante tuberías estándar (`|`) y FIFOs (`mkfifo`) con aceleración en espacio de kernel `splice(2)` zero-copy y buffers de 1 MiB (`F_SETPIPE_SZ`). Introducir un daemon en segundo plano añadiría complejidad de gestión de procesos sin beneficio real de rendimiento.
