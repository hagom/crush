# Plan de Implementación: Detección Inteligente de Hardware (NVMe/SSD vs HDD Mecánico)

## Tareas

### Tarea 1: Módulo de Detección de Discos Rotacionales (`hardware_linux.go` y `hardware_other.go`)
- [ ] Crear interfaz/función pura `IsRotational(path string) bool` y `detectRotationalLinux(path string, mountinfoPath string, sysfsBasePath string) bool`.
- [ ] Implementar soporte multiplataforma: `hardware_linux.go` para Linux y `hardware_other.go` (stub `return false`) para otros sistemas operativos.
- [ ] Cachear resultados en un `sync.Map` para evitar relecturas de `/proc/self/mountinfo`.
- [ ] Tests unitarios en `hardware_test.go` simulando estructuras de `mountinfo` y `sysfs`:
  - NVMe / SSD (`rotational = 0`)
  - HDD tradicional (`rotational = 1`)
  - Subvolúmenes btrfs y particiones hijas (`sda1/../queue/rotational`)
  - Fallbacks ante errores de I/O o archivos inexistentes

### Tarea 2: Integración en el Analizador (`analyze.go`)
- [ ] En `analyzeTarget`: si `IsRotational(target)` es verdadero, limitar los workers a 2 (evitando saltos del cabezal magnético).
- [ ] Tests en `analyze_test.go` verificando que respeta el límite en dispositivos rotacionales.

### Tarea 3: Integración en Compresión y Descompresión (`compress.go` y `decompress.go`)
- [ ] En `compressParallelTasks`: si la unidad es rotacional, acotar `maxWorkers` a 1 o 2 archivos simultáneos, concentrando los hilos de CPU en el compresor multihilo mediante `DynamicThreadPool`.
- [ ] En `DoDecompress`: acotar workers de descompresión si el directorio destino es rotacional.
- [ ] Tests de integración verificando la asignación de workers según el tipo de disco.

### Tarea 4: Verificación Completa
- [ ] Ejecutar `go test -race ./...` (0 carreras, todos los tests pasando).
- [ ] Ejecutar `go vet ./...`.
- [ ] Recompilar e instalar en `/usr/local/bin/crush`.
