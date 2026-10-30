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

---

## 2. Propuestas Evaluadas y Descartadas

### Registros de Recuperación (*Recovery Records* / Paridad PAR2)
- **Estado:** ❌ **DESCARTADA**
- **Motivo:** Rompe el principio de minimalismo (*Ponytail*). La paridad Reed-Solomon añade complejidad matemática innecesaria en la biblioteca estándar sin aportar al objetivo principal de `crush` (compresión/descompresión de alto rendimiento). La detección de integridad ya está cubierta de forma completa y criptográfica mediante SHA-256 (`-hash`, `crush -verify` y `crush -t`).

### Modo Servidor / Socket Streaming UNIX (`crush serve`)
- **Estado:** ❌ **DESCARTADA**
- **Motivo:** Sobreingeniería clara (YAGNI). La sobrecarga de inicio del binario en Linux es de apenas 2 milisegundos. `crush` ya soporta streaming UNIX directo mediante tuberías estándar (`|`) y FIFOs (`mkfifo`) con aceleración en espacio de kernel `splice(2)` zero-copy y buffers de 1 MiB (`F_SETPIPE_SZ`). Introducir un daemon en segundo plano añadiría complejidad de gestión de procesos sin beneficio real de rendimiento.
