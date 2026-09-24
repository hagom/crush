# TODO — Ideas y Mejoras Futuras para crush

Este documento recopila las propuestas de diseño, características avanzadas y optimizaciones pendientes para futuras iteraciones del proyecto que fueron descartadas o pospuestas en la ronda actual.

---

## 1. Rendimiento y Eficiencia

### Detección Automática de Entropía Previa
- **Descripción:** Analizar los primeros 64 KiB del archivo de entrada mediante cálculo de entropía de Shannon ($H = -\sum p_i \log_2 p_i$).
- **Objetivo:** Si los datos ya presentan entropía máxima ($\approx 8.0$ bits/byte), lo que indica datos ya comprimidos (`.mp4`, `.mkv`, `.jpg`, `.xz`, `.zip`) o cifrados, advertir al usuario o seleccionar automáticamente un formato ultrarrápido (`lz4` o `tar`) en lugar de saturar la CPU en algoritmos pesados que no reducirán tamaño.
- **Motivo de aplazamiento:** Descartado temporalmente para replantear la heurística y evitar falsos positivos en formatos estructurados mixtos.

---

## 2. Seguridad y Fiabilidad

### Registros de Recuperación (*Recovery Records* / Paridad PAR2)
- **Descripción:** Generación de bloques de paridad tipo Reed-Solomon (mediante utilidades como `par2` o los registros nativos de recuperación de `rar -rr`) para archivos divididos en partes (`-s`) o backups de misión crítica.
- **Objetivo:** Permitir reconstruir automáticamente archivos corruptos o sectores dañados durante transferencias de red o almacenamiento degradado.
- **Motivo de aplazamiento:** Dejado de lado temporalmente a petición del usuario.

---

## 3. Servicios e Integración

### Modo Servidor / Socket Streaming UNIX (`crush serve`)
- **Descripción:** Iniciar un daemon ligero o escuchar en un UNIX Domain Socket local (`/var/run/crush.sock`) para recibir flujos de compresión/descompresión vía pipes sin sobrecarga de invocación de proceso binario repetitivo.
- **Objetivo:** Facilitar la integración continua en scripts de automatización y microservicios locales.
- **Estado:** Pendiente de priorización futura.
