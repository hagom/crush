# Especificación de Diseño: Detección Inteligente de Hardware (NVMe/SSD vs HDD Mecánico)

## 1. Contexto y Justificación

`crush` aprovecha al máximo los núcleos de CPU disponibles (`NCPU()`) paralelizando archivos y comprimiendo con herramientas multihilo.
Sin embargo, el medio físico de almacenamiento subyacente condiciona drásticamente la estrategia óptima de E/S:

1. **Unidades de Estado Sólido (NVMe / SATA SSD / RAM disks):**
   - Latencia de acceso aleatorio casi nula (<0.02 ms).
   - Altísima concurrencia de colas I/O (ej. colas NVMe con hasta 64,000 comandos simultáneos).
   - Paralelizar lecturas/escrituras concurrentes de 8, 16 o 32 archivos a la vez satura la tasa de transferencia PCIe/SATA (2,000 - 7,000 MB/s) con 100% de eficiencia.

2. **Discos Rígidos Mecánicos (HDD / Rotacionales):**
   - Dependen del movimiento físico del cabezal magnético (*head seek latency* ~10 ms por salto).
   - Si se lanzan 16 o 32 hilos concurrentes leyendo o escribiendo archivos en diferentes zonas del plato magnético, el cabezal entra en *disk thrashing* salvaje: la velocidad de lectura cae de ~150 MB/s secuencial a 1-5 MB/s aleatorio, y la CPU pasa el 98% de su tiempo ociosa en estado `iowait`.
   - **Estrategia óptima para HDD:** Minimizar la concurrencia a nivel de archivos (1 o 2 archivos simultáneos) para mantener accesos continuos/secuenciales, y concentrar el 100% de los núcleos de CPU en el compresor multihilo de cada archivo (`zstd -T0`, `pigz -p N`, `xz -T0`, etc.).

---

## 2. Mecanismo de Detección en Espacio de Usuario (Linux stdlib)

En Linux, el kernel expone la característica rotacional de cada dispositivo de bloques en el archivo:
`/sys/block/<dev>/queue/rotational` (o `/sys/class/block/<dev>/queue/rotational`)
- `0`: No rotacional (NVMe, SSD, eMMC, zram/ramdisk).
- `1`: Rotacional (HDD mecánico magnético).

### Desafío: Sistemas de Archivos con Dispositivos Virtuales / Subvolúmenes
En sistemas de archivos como **btrfs** (o ZFS, overlayfs o montajes loop), `syscall.Stat(path).Dev` devuelve un número de dispositivo anónimo (`major: 0`), lo que impide mapear directamente a `/sys/dev/block/`.

### Solución Robusta y Ligera (Zero-Deps, 100% Go stdlib):
1. **Resolución de Punto de Montaje (`/proc/self/mountinfo`):**
   - Se lee `/proc/self/mountinfo` (o `/proc/mounts`) para encontrar la entrada con el prefijo de ruta de montaje más largo que contenga `path`.
   - Se extrae la ruta del dispositivo fuente (ej: `/dev/sda1` para `/baul`, `/dev/nvme0n1p3` para `/home`).
2. **Consulta a Sysfs (`/sys/class/block`):**
   - Para cualquier dispositivo o partición (`devName = filepath.Base(devicePath)`):
     - Si existe `/sys/class/block/<devName>/queue/rotational`, se lee (disco entero).
     - Si no, se prueba `/sys/class/block/<devName>/../queue/rotational` (partición hija).
     - Se parsea el byte: `'1'` $\implies$ Rotacional (`true`), `'0'` $\implies$ No rotacional (`false`).
3. **Caché en Memoria (`sync.Map`):**
   - Los dispositivos de bloques no cambian de tipo en caliente durante la ejecución del comando. Se almacena el resultado por punto de montaje para que futuras consultas tengan costo $\approx 0$ ns.
4. **Fallback Universal Seguro:**
   - En sistemas no-Linux (macOS, BSD, Windows) o si `/proc/self/mountinfo` / sysfs no está accesible (contenedores restringidos), devuelve `false` (no rotacional) preservando el comportamiento actual.

---

## 3. Puntos de Integración en `crush`

1. **Analizador de Compresibilidad (`analyzeTarget` en `analyze.go`):**
   - Actualmente: `workers := NCPU()` (hasta 32 workers).
   - Optimización: Si `IsRotational(target)` es verdadero, limitar `workers` a un máximo de 2 (o 4 si $N \ge 16$). Esto evita que el escaneo de 40,000 archivos bloquee el cabezal del HDD magnético.

2. **Compresión Paralela (`compressParallelTasks` en `compress.go`):**
   - Actualmente: `maxWorkers := 8` (para multihilo).
   - Optimización: Si `IsRotational(firstFilePath)` es verdadero:
     - `maxWorkers` se acota a 1 o 2 workers concurrentes de archivos.
     - `DynamicThreadPool` asigna todas las fichas disponibles (`NCPU()`) al compresor multihilo del archivo activo. De esta forma, el HDD lee un archivo de forma secuencial a máxima tasa de transferencia y la CPU lo comprime a tope sin contención mecánica.

3. **Descompresión en Lote (`DoDecompress` en `decompress.go`):**
   - Si el directorio de extracción destino está en un HDD mecánico (`IsRotational(destDir)`), acotar los workers de extracción concurrentes para evitar escrituras fragmentadas simultáneas en el plato magnético.

---

## 4. Filosofía Ponytail y Rigor TDD

- **Cero dependencias:** Solo biblioteca estándar de Go (`os`, `bufio`, `strings`, `filepath`, `sync`).
- **Arquitectura inyectable para testing:** Función inyectable o helper que permita simular contenidos de `mountinfo` y `sysfs` en tests unitarios sin requerir permisos de root ni hardware físico específico.
- **Pruebas Table-Driven:**
  - Caso NVMe (`rotational: 0`).
  - Caso HDD (`rotational: 1`).
  - Caso btrfs / subvolumen.
  - Caso archivo en tmpfs / zram.
  - Caso sysfs inaccesible / fallback.
