# AGENTS.md — crush

## Descripción

Herramienta multi-formato de compresión y descompresión vía pipe.
Soporta 13 formatos: gz, xz, bz2, bz3, zst, lz, lrz, zip, 7z, tar, rar, lz4, br.

## Ramas

- **main** — versión Go. Desarrollo activo.

## Comandos

```bash
# Compilar
make build          # go build -o crush .
go build -o crush .

# Testear
make test           # go test ./... -v
go test ./... -v

# Ejecutar
./crush -h
./crush -c -f gz archivo.txt
./crush -d archivo.tar.gz

# Instalación / desinstalación
./crush --install             # binario en /usr/local/bin/
./crush --install-deps         # solo dependencias del sistema
./crush --uninstall            # eliminar binario

# Binario final (sin dependencias)
make install        # install -m 755 crush /usr/local/bin/
```

## Convenciones de código

- Solo stdlib de Go. Sin dependencias externas.
- Pipeline de compresión con `exec.Cmd` + `StdoutPipe`.
- Tests table-driven donde sea posible.
- Nombres en camelCase. Errores con `fmt.Errorf`.
- NO agregar comentarios a menos que sea estrictamente necesario.
- **Desarrollo TDD (Test-Driven Development):** todo fix o feature empieza por un test que falle (rojo), luego la implementación mínima (verde) y finalmente refactor. NO se escribe código de producción sin un test fallando antes.
- Los colores ANSI van en las constantes de `util.go` (Green, Red, Yellow, Blue, Bold, NC).

## Estándares de rigor en pruebas

- Todo test debe haber **fallado por la razón correcta** antes de implementar (verificar que falle con la funcionalidad ausente, no por un error de setup).
- Tests **table-driven** cubriendo por cada función: caso feliz, casos límite (input vacío, archivos inexistentes, rutas con `..`), y cada error que devuelve.
- Los tests verifican **comportamiento observable** (salida, error, efecto), no detalles de implementación.
- Un test que pasa con la implementación rota es un test inútil: si un test no detecta el bug que debe detectar, se corrige o se elimina.
- Código concurrente: correr `go test -race ./...`.
- Verificación pre-commit obligatoria: `go test ./...` y `go vet ./...` en verde.
- No mockear lo que no se necesita: los tests de integración con las herramientas reales (tar, pigz, 7z) se mantienen y corren en CI además de los unitarios.

## Filosofía del proyecto

- **Siempre paralelizar** los compresores. Usar versiones multihilo (`pigz`, `lbzip2`/`pbzip2`, `plzip`, `bzip3 -j N`, `xz -T0`, `zstd -T0`, `lrzip -p N`, `7z -mmt=on`, `rar -mtN`) para aprovechar todos los núcleos del CPU.
- **NUNCA agregar flags de paralelismo** (`-j`, `--parallel`, `--jobs`). El programa debe detectar automáticamente `NCPU()` y paralelizar archivos según la cantidad de núcleos disponibles.
- **Siempre comprimir múltiples archivos y carpetas en paralelo de forma individual**, a menos que se especifique explícitamente `-C` (`--combine`). Cada archivo o carpeta produce su propia salida comprimida independiente.
- **Sistemas target**: Debian (apt) y RedHat (dnf/yum). El instalador de dependencias debe priorizar estos gestores.
- Si la versión paralela de un compresor no está disponible, caer en la versión serial (`gzip`, `bzip2`, etc.) como último recurso, nunca fallar.

## Principios de Diseño SOLID

La arquitectura de `crush` aplica rigurosamente los principios SOLID para garantizar mantenibilidad, extensibilidad y desacoplamiento sin dependencias externas:

### 1. SRP — Single Responsibility Principle (Principio de Responsabilidad Única)
- **Contratos de Formato (`format.go`):** La estructura `FormatInfo` y el tipo enum `Format` encapsulan exclusivamente los rasgos, herramientas, flags y nombres base correspondientes a cada formato (`IsContainer()`, `IsStream()`, `ArchiveBaseName()`). Ni `compress.go` ni `decompress.go` deben deducir heurísticas de formato dispersas.
- **Resolución de Directorios (`ResolveDecompressDir` en `util.go`):** Centraliza la regla de negocio para resolver el directorio destino de extracción, manejando limpiamente si el archivo origen se encuentra dentro de carpetas de partición (`_parts` o `_split`) para extraer en el directorio superior sin acoplarse al pipeline de descompresión.
- **Canales de E/S (`lockedWriter`, `countingWriter`, `countingReader` en `util.go`):** Cada adaptador de streaming tiene una única tarea (sincronizar escrituras concurrentes, contar bytes transferidos para progreso o interceptar lectura en tiempo real).

### 2. OCP — Open/Closed Principle (Principio de Abierto/Cerrado)
- **Extensibilidad de Formatos:** Nuevos compresores o formatos se incorporan registrándolos en `format.go` (`formatNames`, `FormatInfoFromFormat`, `knownTarSuffixes`), sin necesidad de modificar switches hardcodeados en `compress.go`, `decompress.go`, `pkgmgr.go` o `test_cmd.go`.
- **Diferenciación Flujo vs Contenedor:** Funciones como `ExtForFormat`, `isTarBased`, `listArchiveOutputs` y validación de `SplitSize` consumen los métodos polimórficos `info.IsContainer()` e `info.IsStream()`, permaneciendo cerradas a modificación cuando se añade un nuevo formato de compresión.

### 3. LSP — Liskov Substitution Principle (Principio de Sustitución de Liskov)
- **Intercambiabilidad de Herramientas:** Las herramientas concurrentes (`pigz`, `lbzip2`, `plzip`, `bzip3`, `xz`, `zstd`, `lrzip`, `7z`, `rar`) y sus fallbacks secuenciales (`gzip`, `bzip2`, `lzip`) respetan idénticos contratos de entrada/salida y comportamiento vía pipes (`-dc`, `-c`, `-f`, stdout/stdin).
- **Lectores y Escritores Estándar:** Cualquier componente que acepte `io.Reader` o `io.Writer` puede recibir indistintamente `os.File`, `bytes.Buffer`, `countingReader`, `countingWriter` o pipes anónimos sin alterar la corrección del programa.

### 4. ISP — Interface Segregation Principle (Principio de Segregación de Interfaces)
- **Interfaces Mínimas de la Stdlib:** Se evitan interfaces monolíticas o sobrecargadas. Se emplean exclusivamente las interfaces elementales de la biblioteca estándar de Go (`io.Reader`, `io.Writer`, `io.Closer`) compuestas según la necesidad (`io.ReadCloser`, `io.WriteCloser`).
- **Opciones Específicas por Operación:** `CompressOptions`, `DecompressOptions` y `TestOptions` segregan claramente las configuraciones de cada modo de ejecución en lugar de compartir un único struct de opciones hinchado.

### 5. DIP — Dependency Inversion Principle (Principio de Inversión de Dependencias)
- **Desacoplamiento de Comandos del Sistema:** El acceso a utilidades externas del sistema operativo (`df`, `free`, procesos de compresión) se realiza mediante la abstracción inyectable `var execCommand = exec.Command` en `util.go`, permitiendo que los tests unitarios (`mock_test.go`, `util_test.go`) simulen entornos y respuestas del sistema sin ejecutar subprocesos reales ni requerir privilegios de root.
- **Inyección de Dependencias en Progreso y Logging:** Las rutinas de ejecución reciben sus sumideros (`pt *ProgressTracker`, `fp *FileProgress`, `io.Writer`) en lugar de depender de instancias globales rígidas.

### 6. Principio DRY — Don't Repeat Yourself (No te repitas)
- **Cero Duplicación de Lógica Algorítmica (`SortByLPT` en `util.go`):** El ordenamiento concurrente LPT (*Longest Processing Time first*) se encapsula en una única función genérica de infraestructura reutilizada tanto por `compressParallel` como por `DoDecompress`, evitando duplicar la lógica de estimación, ordenamiento y manejo de errores.
- **Fuente Única de Verdad para Sufijos (`format.go`):** Toda comprobación o extracción de sufijos tar (`.tar.gz`, `.tar.xz`, etc.) debe consultar exclusivamente la tabla `knownTarSuffixes` mediante `HasTarSuffix` y `StripTarSuffix`. Queda estrictamente prohibido redefinir slices locales de extensiones o encadenar condiciones manuales `strings.HasSuffix`.
- **Cálculo Consolidado de Tamaños de Partes (`PartsTotalSize` / `TotalArchiveSize` en `util.go`):** El cálculo de tamaño acumulado de archivos divididos (*split*) debe invocar las funciones centralizadas de `util.go`, prohibiendo bucles manuales duplicados que sumen `os.Stat` de partes.
- **Detección Centralizada de Particiones (`IsSplitPartsDir` en `util.go`):** La validación de carpetas de fragmentos (`_parts` o `_split`) debe realizarse mediante `IsSplitPartsDir`.

### Directrices para Código Futuro
- Al agregar un formato: actualizar solo `format.go` (definición de enum, nombre, herramientas y extensiones). Evitar agregar `switch format` dispersos fuera de `format.go`.
- Todo cálculo de rutas de extracción debe invocar `ResolveDecompressDir`.
- Cumplimiento estricto del principio DRY: Si una lógica o consulta al sistema de archivos se repite en más de un sitio, debe abstraerse en una función pura o auxiliar en `util.go` o `format.go`.
- Mantener la stdlib pura: no introducir dependencias externas en `go.mod`.
- Todo comando externo susceptible de ser testeado debe invocar `execCommand` para preservar la testeabilidad.

## Estructura del código Go

```
crush/
├── main.go          # CLI flags, dispatch (-c, -d, -l, -t, -r, -watch, -verify, --bench, --install)
├── format.go        # FormatInfo, ParseFormat, DetectFormat, ExtForFormat
├── compress.go      # DoCompress, compressItems, tar-pipe, -hash, -p, -sparse
├── decompress.go    # DoDecompress, splitWriter, -p, -filter
├── watcher.go       # Watcher, DoWatch, loop con stdlib
├── watcher_linux.go # Backend inotify (IN_CLOSE_WRITE, IN_MOVED_TO)
├── watcher_other.go # Backend fallback por sondeo
├── bench.go         # DoBench, BenchmarkFormat, GenerateBenchmarkDataset, FormatBenchTable
├── test_cmd.go      # DoTest, TestFile, -verify con checksum SHA-256
├── util.go          # NCPU, GetMemLimit, FormatSize, pipeline, lockedWriter, execCommand, SHA-256
├── util_linux.go    # F_SETPIPE_SZ (1 MiB) y splice(2) zero-copy
├── util_other.go    # Fallbacks de pipe y splice
├── lock.go          # LockError, lockFilePath, lockScope.NeedsLock (qué modos requieren lock)
├── lock_unix.go     # AcquireLock con flock(2): instancia única, se libera sola al morir el proceso
├── lock_other.go    # Fallback sin bloqueo en plataformas no Unix
├── pkgmgr.go        # DetectPkgManager, InstallMissingDeps, list helpers
├── Makefile
├── .github/workflows/ci.yml  # GitHub Actions: test matrix Go 1.21-1.23, race detector, build
├── *_test.go        # Tests por paquete
└── mock_test.go     # Tests con mocks de exec.Command (patrón TestHelperProcess)
```

## Estado actual

- Go: migración completa. 444 tests nativos pasando con race detector (-race). ~15350 líneas. 0 bugs conocidos.
- Features implementadas y fixes recientes:
  - Eliminación de Latencia de Inicio y Progreso en Tiempo Real:
    - **Medición de Flujo en Entrada (`compressTarPipe` con `io.Pipe` y `countingReader`):** Captura en tiempo real del flujo de datos sin comprimir generado por `tar` antes de ingresar al compresor. La barra de progreso y velocidad se actualizan desde el segundo cero con exactitud matemática al 100%, eliminando la pausa producida por los búferes internos de los compresores (`zstd`, `xz`, `pigz`, etc.).
    - **Diccionario Adaptativo LZMA2 en 7-Zip (`adaptive7zDict`):** Selección dinámica del tamaño de diccionario `-md` acoplado al volumen real de los archivos a comprimir ($\le 16\text{M}, 32\text{M}, 64\text{M}, 128\text{M}, 256\text{M}$) y adaptado a la memoria RAM disponible del sistema, erradicando reservas inútiles de memoria virtual en archivos pequeños/medianos y previniendo colapsos OOM en equipos modestos sin perder ratio de compresión.
    - **Caché en Memoria de Tamaños de Lote y `filepath.WalkDir`:** Reducción drástica de operaciones de I/O en disco al consolidar los tamaños en una sola pasada $O(N)$ compartida con `SortByLPT`, `totalSize` y los workers de compresión, migrando a `filepath.WalkDir` para una lectura ligera de inodos.
    - **Retroalimentación Visual Inmediata:** Visualización activa del estado `iniciando...` en el monitor global y en cada worker mientras se preparan los procesos externos, evitando pantallas estáticas en `0.0%`.
  - Compresión Individual y Paralela de Carpetas por Omisión:
    - **Independencia de Carpetas en Lote:** Corrección del agrupamiento forzado inadvertido. Al comprimir múltiples carpetas o combinaciones de archivos y carpetas sin pasar la opción explícita `-C` (`--combine`), cada carpeta se procesa y comprime de manera individual e independiente en su propio archivo comprimido (`carpeta.7z`, `carpeta.tar.gz`, etc.) en paralelo aprovechando la asignación adaptativa de hilos del sistema.
    - **Cálculo de Tamaño Recursivo de Carpetas (`totalFileSize` y `SortByLPT`):** Integración de `GetDirSize` para calcular con exactitud los bytes reales contenidos en los árboles de directorios al programar colas LPT y reportar el progreso con precisión.
  - Compresión Multi-Formato y Auto-instalación con Fallback Inteligente:
    - **Compresión Simultánea Multi-Formato (`-F` / `--formats` / `ParseFormatList`):** Ejecución en lote para comprimir archivos a múltiples formatos en una sola llamada (ej: `crush -c -F gz,xz,zst archivo.txt`), garantizando la preservación de los ficheros de origen durante todas las etapas intermedias (`KeepOrig` forzado internamente excepto en la última compresión) y reportando el avance y verificación individualizada por formato.
    - **Auto-instalación Preventiva y Fallback Secuencial (`EnsureFormatTool` en `pkgmgr.go`):** Inspección de herramientas multihilo (`pigz`, `lbzip2`, `plzip`, `lz4 -T`, `7z`, `xz`, `zstd`); si la herramienta multihilo preferida no está instalada, `crush` intenta su instalación desatendida mediante el gestor del sistema (`apt`, `dnf`, etc.); si no está en repositorios o falla la instalación, recurre transparentemente a la versión secuencial (`gzip`, `bzip2`, `lzip`) con advertencia en consola; y si ninguna está disponible, emite un aviso de error detallado indicando la ausencia de ambas.
  - Optimización de Distribución Dinámica y Adaptativa de Hilos (Multi-core Máximo):
    - **Distribución Proporcional de Hilos al Tamaño (`AllocateThreadsProportional` en `util.go`):** Eliminación total del cuello de botella por archivos rezagados (*tail latency*) e inanición de CPU (*CPU starvation*). Reparte los núcleos del sistema proporcionalmente al peso en bytes de cada archivo usando el algoritmo de resto mayor (Hamilton-Hare), garantizando que archivos gigantes y pequeños concluyan prácticamente al mismo tiempo con utilización sostenida del 100% del procesador (+84.3% de mejora medida en juegos de PS2 de 3.5 GB y +35.3% en colas desiguales).
    - **Pool Dinámico de Fichas de Hilos (`DynamicThreadPool` en `util.go`):** Semáforo ponderado con token bucket para colas de archivos que superan la ventana concurrente. Los workers adquieren tokens dinámicos al iniciar y los devuelven al finalizar; a medida que la cola se agota, los últimos archivos absorben automáticamente el 100% de los núcleos libres.
    - **Ventana Óptima de Workers (*Sweet-Spot Clamping*):** Límite máximo de 8 procesos paralelos en formatos multi-hilo para evitar colapso del bus de I/O de disco y contención de memoria, maximizando hilos por worker.
    - **Distribución Consciente del Formato (`FormatMaxThreads` en `format.go`):** Detección de capacidades de hilos reales por formato. Asigna hilos dinámicamente según la herramienta: `lz4` aprovecha multihilo automático (`-T`) en versiones modernas (`v1.10+`), mientras que formatos con CLI estrictamente mono-hilo (`tar`, `br`, `gzip`/`bzip2` secuenciales) reciben exactamente 1 hilo por archivo para no malgastar cuota de CPU en workers ociosos y saturar el procesador mediante multiproceso concurrente (`NCPU()` workers).
    - **Inyección Precisa de Hilos en Descompresión (`PipeFlagsForThreads` en `util.go`):** Control estricto de hilos en pipelines de streaming (`pigz -p`, `xz -T`, `zstd -T`, `bzip3 -j`, `plzip --threads`, `lbzip2 -n`, `lrzip -p`, `7z -mmt`), erradicando sobre-suscripción oculta en descompresión paralela.
  - Nuevas Funcionalidades de Compresión e Integración (Interactivo y Adición In-Place):
    - **Compresión Interactiva del Directorio Actual (`crush -c` sin argumentos):** Detección automática mediante `FindCompressibleFiles` de todos los archivos y subdirectorios presentes en la ruta actual (excluyendo automáticamente elementos ocultos, archivos ya comprimidos, fragmentos split y `.sha256`), presentación tabular de elementos y tamaños, y confirmación interactiva con `PromptCompressAll`, usando el formato indicado en `-f` o `gz` por omisión.
    - **Adición y Actualización In-Place a Archivos Comprimidos (`-a` / `-u` / `DoAppend`):** Inserción directa de nuevos archivos o subdirectorios dentro de un archivo comprimido preexistente sin generar un nuevo archivo comprimido en disco, soportado in-place en `.zip`, `.7z`, `.rar`, `.tar` y con recompresión atómica transparente para contenedores `.tar.*` (`.tar.gz`, `.tar.xz`, etc.), manteniendo soporte para contraseñas (`-p`), archivos dispersos (`-sparse`) y sincronización automática del checksum `.sha256`.
  - Suite de Nuevas Funcionalidades (Seguridad, Extracción Avanzada y Watcher):
    - **Modo Observador de Directorios (`-watch <dir>`):** Monitoreo continuo de directorios sin dependencias externas usando `syscall.Inotify` nativo en Linux (`IN_CLOSE_WRITE | IN_MOVED_TO`) y fallback por sondeo en otras plataformas, procesando automáticamente compresión (`-c`) o descompresión (`-d`) de archivos entrantes con apagado limpio ante señales `SIGINT`/`SIGTERM`.
    - **Generación y Verificación de Checksums SHA-256 (`-hash` / `-verify`):** Creación automática de archivos `.sha256` durante la compresión e integración en `DoTest` para comprobar la integridad de archivos comprimidos y validar el checksum contra el fichero `.sha256` si está presente.
    - **Cifrado y Protección por Contraseña (`-p` / `-password`):** Soporte integral de contraseñas para formatos de contenedor (`7z`, `zip`, `rar`) con entrada oculta por terminal si se omite el argumento, protegiendo tanto datos como cabeceras (`-mhe=on` en 7-Zip).
    - **Soporte de Archivos Dispersos (`-sparse` / `-S`):** Activación de `--sparse` en invocaciones a GNU Tar para optimizar espacio al comprimir discos virtuales o archivos dispersos.
    - **Filtro de Extracción Selectiva (`-filter <patrón>`):** Extracción selectiva de subarchivos o patrones glob (ej. `*.txt`, `docs/*`) soportada en contenedores `tar`, `7z`, `zip` y `rar`.
    - **Optimización Zero-Copy en Kernel con Linux `splice(2)` (`splicePipe`):** Transferencia directa de descriptores en espacio de kernel para pipes sin saltos a memoria de usuario en Go, con fallback automático transparente a `io.CopyBuffer`.
  - Refactorización de Arquitectura DRY y Unificación de LPT:
    - **Algoritmo LPT Reutilizable (`SortByLPT` en `util.go`):** Unificación del algoritmo de planificación *Longest Processing Time first* consumido tanto por `compressParallel` como por `DoDecompress`. Elimina la duplicación de código algorítmico y asegura saturación óptima del CPU en compresión y descompresión.
    - **Eliminación de E/S Redundante en Descompresión:** Precálculo y reuso en una sola pasada $O(N)$ del mapa de tamaños (`archiveSizes`), eliminando 3 bucles redundantes que consultaban el disco en `DoDecompress`.
    - **Fuente Única de Sufijos Tar (`format.go`):** Deduplicación de sufijos mediante `HasTarSuffix` y `StripTarSuffix` sobre `knownTarSuffixes`, eliminando el slice duplicado `tarSuffixes` en `decompress.go` y 14 comprobaciones manuales en `DetectFormat`.
    - **Abstracción de Tamaño y Carpetas Split (`util.go`):** Centralización de sumatorias de fragmentos en `PartsTotalSize` y `TotalArchiveSize`, y verificación de directorios particionados con `IsSplitPartsDir`.
  - Optimización de Rendimiento Extremo en Compresión y Descompresión:
    - **Streaming directo sin archivos `.tar` temporales a disco (`decompressTar`):** Descompresión por tubería directa conectando el stdout del descompresor a `tar -xf - -C dir` con monitorización en tiempo real vía `countingReader`. Elimina la creación del `.tar` intermedio en disco, reduciendo el I/O en un 50% y duplicando la velocidad.
    - **Ampliación de buffers de pipes a 1 MiB (`util_linux.go` / `setPipeCapacity`):** Configuración de `F_SETPIPE_SZ` (1048576 bytes) en descriptores de tuberías de Linux en `pipeline()`, reduciendo cambios de contexto entre subprocesos.
    - **Ratios de compresión máxima:** Activación de parámetros extremos (`zstd --ultra -22`, `7z -mx=9 -md=256m -mfb=273` adaptativo a RAM libre, `bzip3 -b 64`, `lz4 -9` LZ4HC por omisión, `pigz -p N`).
    - **Descompresión multihilo optimizada:** Inclusión de `-n <NCPU>` para `lbzip2` y `-p <NCPU>` para `pigz` en descompresión.
  - Preservación de extensiones y rutas de directorio en descompresión:
    - Preservación estricta de extensiones en formatos de flujo (`ArchiveBaseName` / `IsStream`): archivos como `juego.iso` empaquetados en formatos stream (`bz3`, `gz`, `xz`, `zst`, etc.) retienen su extensión original (`juego.iso.bz3`) para que al descomprimirse se recupere `juego.iso` en lugar de un binario sin extensión.
    - Detección mágica preventiva de imágenes ISO 9660: inspección de firma `CD001` en offset 32769 (`0x8001`) al descomprimir archivos individuales para reasignar automáticamente la extensión `.iso` ante archivos legacy desprovistos de extensión.
    - Respeto de rutas de subdirectorios en descompresión interactiva y recursiva: cuando no se explicita `-o`, los archivos en subcarpetas (ej. `dir1/dir2/archivo.7z`) se extraen directamente en su propio subdirectorio (`dir1/dir2/`), y los archivos divididos alojados en `dir1/dir2/archivo_parts/` se desempaquetan en el directorio contenedor de la partición (`dir1/dir2/`).
  - Refactorización de Arquitectura SOLID:
    - Encapsulación de rasgos de formato en `Format` y `FormatInfo` (`format.go`): métodos polimórficos `IsContainer()` (verdadero para Zip, SevenZ, Tar, Rar), `IsStream()` (`!IsContainer()`) y `ArchiveBaseName(inputPath)`.
    - Centralización de resolución de directorios en `ResolveDecompressDir` (`util.go`): regla única para derivar carpetas destino de descompresión desempaquetando directorios residuales de división (`_parts`, `_split`) hacia el directorio padre sin acoplar detalles de partición en `decompress.go`.
    - Cumplimiento estricto de OCP, LSP, ISP y DIP: eliminación de switches de extensiones redundantes en `compress.go`, `decompress.go` y `pkgmgr.go`, segregación de interfaces con Go stdlib, e inversión de dependencias en subprocesos del sistema con `execCommand`.
    - Suite de pruebas unitarias table-driven exhaustiva para los nuevos métodos y contratos SOLID.
  - Ordenamiento natural numérico en unión de fragmentos split: corrección en `globSplitParts` (`util.go`) para ordenar numéricamente los fragmentos `.part*` en lugar de léxicamente, resolviendo el error de flujo corrupto (`exit status 1`) que ocurría en archivos segmentados en 100 o más partes (donde el orden alfabético colocaba `.part100` antes de `.part11`).
  - Prevención de carpetas residuales (`crush_YYYYMMDD_parts`) en compresión paralela: cálculo diferido de rutas de salida en `DoCompress` para evitar crear directorios combinados vacíos cuando se ejecuta compresión multi-archivo paralela, y limpieza garantizada de carpetas vacías si una operación se cancela o falla.
  - Autocompletado inteligente contextual para división en partes (`-s`): sugerencia exclusiva de tamaños comunes de partición en megabytes (`10`, `50`, `100`, `500`, `1000` MB) al pulsar TAB en `-s` / `--split` en Bash, Zsh y Fish con descripciones explícitas de unidad, desacoplando los formatos de `-s` y trasladando el filtrado dinámico de formatos compatibles (`gz`, `xz`, `bz2`, `bz3`, `zst`, `lz`, `lz4`, `br`) a `-f` condicionado a la presencia de `-s` en la línea de comando.
  - Carpeta resultante dedicada para compresión con split (`-s`): empaquetado estructurado dentro de `<OutputDir>/<baseName>_parts` conteniendo el archivo comprimido base y todos sus fragmentos `.part01`, `.part02`, etc., aplicable a compresión individual, paralela y secuencial.
  - Búsqueda recursiva y agrupación inteligente en descompresión interactiva (`crush -d` sin argumentos): recorrido de subdirectorios mediante `filepath.WalkDir` (omitiendo ocultos), unificando archivos divididos para mostrar únicamente el elemento base con su tamaño total consolidado en la lista de selección.
  - Descompresión continua en streaming con `io.MultiReader`: extracción directa de archivos divididos concatenando todas sus partes sin requerir concatenación manual `cat` previa ni espacio en disco adicional.
  - Conteo y reporte de porciones completadas (`Porciones: X / X partes`): visualización clara en el resumen final de compresión y descompresión, así como en el seguimiento en tiempo real del ProgressTracker.
  - Documentación explícita de compatibilidad para división en partes (`-s`): especificación clara en ayuda (`crush -h`), flag usage y autocompletados (Zsh, Fish, Bash) indicando que `-s` opera sobre formatos de flujo (`gz`, `xz`, `bz2`, `bz3`, `zst`, `lz`, `lz4`, `br` y `tar.*`), emitiendo advertencia descriptiva en tiempo de ejecución para formatos no soportados (`7z`, `rar`, `zip`, `tar`, `lrz`).
  - Estandarización de colores ANSI en mensajes de consola: funciones auxiliares `WriteWarning` (amarillo con prefijo `⚠ Advertencia:`), `WriteError` (rojo con prefijo `✗ Error:`), `WriteInfo` (azul con prefijo `ℹ `) y `WriteSuccess` (verde con prefijo `✓ `), con salida thread-safe sobre `lockedWriter` dinámico y eliminación de prefijos redundantes.
  - Protocolo formal de resolución de conflictos entre ramas y worktrees paralelos (rebase sobre `main`, integración semántica no ciega y re-verificación obligatoria post-conflicto con `go test -race` y `go vet`).
  - Refinamiento de verificación y estimación de espacio en disco en compresión y descompresión:
    - Resolución recursiva del primer ancestro existente en `GetAvailBytes` para evitar fallos en directorios de salida que aún no existen en el sistema.
    - Función modular `EstimateCompressedSize` con ratios diferenciados por formato (`Tar` 102%, `Lz4` 60%, `Zip` 50%, `Gz` 40%, `Zst` 35%, `Bz2`/`Rar` 30%, `Br` 28%, `7z`/`Xz`/`Bz3`/`Lz` 25%, `Lrz` 20%) y detección inteligente de ficheros precomprimidos (`.mp4`, `.zip`, `.iso`, etc., estimando 95%).
    - Extracción nativa de tamaño descomprimido en `.zip` vía `archive/zip` de Go stdlib (rápido, sin procesos externos y con soporte Zip64).
    - Soporte de consulta de tamaño uncompressed en `.rar` con fallback inteligente.
    - Ratios realistas en formatos sin tabla central: corrección de `lz4` (2.0x en lugar de 6.0x para evitar sobrestimaciones del 300%), `bz2`/`bz3`/`lz` (4.0x) y `br` (3.5x).
    - Verificación preventiva de espacio por lote agrupado por directorio en `DoDecompress` antes de arrancar los workers paralelos, abortando limpiamente si el espacio libre con 10% de margen no es suficiente.
  - Coloreado por estado en el reporte de progreso multi-archivo: amarillo mientras se procesa (`active`), verde al completar satisfactoriamente (`done`) y rojo si ocurre algún error (`error`), preservando la alineación exacta en columnas.
  - Compresión y descompresión de 13 formatos (gz, xz, bz2, bz3, zst, lz, lrz, zip, 7z, tar, rar, lz4, br).
  - Escaneo interactivo en descompresión: cuando se invoca `crush -d` sin argumentos, detecta automáticamente todos los archivos comprimidos en el directorio actual (excluyendo subdirectorios, ocultos y fragmentos .part), muestra la lista con sus tamaños y solicita confirmación `[s/N]` antes de descomprimirlos en paralelo.
  - Comando `--bench` para medir throughput (MB/s) y ratio de compresión por formato con dataset determinista y verificación SHA256.
  - Benchmarks nativos Go (`go test -bench=.`) para gz, zstd, xz, bz2, zip, 7z, lz, bz3.
  - Inyección de dependencias con `var execCommand = exec.Command` y tests con mocks canónicos (`TestHelperProcess`).
  - `lockedWriter` para serializar escrituras concurrentes a stderr en `pipeline()`.
  - Extracción de `getMemFromFree()` como función testeable independiente de `/proc/meminfo`.
  - CI/CD con GitHub Actions: matriz Go 1.21-1.23, race detector, go vet, build, smoke tests.
  - Corrección de 10 fallos de lógica auditados (BUG-L1 a BUG-L10):
    - `GetUniqueName` atómico y sincronizado en memoria contra colisiones concurrentes en compresión paralela (BUG-L1).
    - Cierre garantizado de descriptores de archivos divididos en `splitWriter` e implementación de `io.Closer` en `countingWriter` (BUG-L2).
    - Protección contra sobreescritura accidental en descompresión sin `--force` en todos los formatos individuales (BUG-L3).
    - Soporte completo de lectura (`crush -r`) y listado (`crush -l`) para `.bz3`, y extracción correcta de `.tar` a stdout con `tar -xOf` (BUG-L4).
    - Validación uniforme de argumentos requeridos en modos `-l`, `-r` y `-t` con mensajes informativos en stderr (BUG-L5).
    - Pipeline resiliente con cancelación `Kill()` y recolección `Wait()` en todos los comandos para prevenir procesos zombis (BUG-L6).
    - Cierre y vaciado de `tarFile` antes de la llamada a `tar -xf` en descompresión (BUG-L7).
    - Formateo estricto en `FormatSize` (1024 B → 1.0 KiB) y reemplazo de subprocess `stat` por `os.Stat` nativo (BUG-L8).
    - Corrección de discrepancia en reporte de hilos (`effectiveThreads`) considerando `opts.ThreadLimit` y capacidades multihilo de cada herramienta (BUG-L9).
    - Corrección de barra de progreso y estimación de tiempo (ETA) en compresión y descompresión (BUG-L10): función `formatETA` con ventana de estabilización (`--:--`) y techo (`>24h`), parser multi-delimitador en `trackProgress` (`\b`, `\r`, `\n`) para 7-Zip, `pollFileProgress` monótono y `countingReader` en flujos de entrada.
  - Separación visual y alineación en columnas del ProgressTracker multi-archivo: línea en blanco divisoria entre barra general y sub-procesos, formateo tabular estricto de tamaños (`%8s / %-8s`) con barra `/` fijada horizontalmente, y columna de tiempos restantes / estado (`%9s` con `✓` en verde, `esperando...` en amarillo o ETA numérico).
  - Estabilización del renderizado en terminal del ProgressTracker multi-archivo: ocultamiento de cursor (`\033[?25l`), restauración garantizada (`\033[?25h`) y retorno de carro antes de salto (`\r\033[%dA`) para evitar duplicación de frames residuales ante scrollback.
  - Tracking uniforme de descompresión en tiempo real: monitoreo porcentual en streams de entrada vía `countingReader` y `pipeCmdForProgress` para `.tar.{gz,xz,bz2,bz3,zst,lz,lz4,br}` y descompresión de archivos individuales.
  - Separación de `--install` (instalación del binario en `/usr/local/bin`) e `--install-deps` (gestión de dependencias del sistema).
  - Corrección previa de sintaxis tar en verificación (BUG-01), derivación multi-archivo a tar-pipe (BUG-02), dependencias lz4/brotli (BUG-03), eliminación de tar intermediario (BUG-04), y soporte integral de `-i` (BUG-05 y BUG-06).
  - Dry-run (`-n`), división (`-s`), exclusión (`-exclude`), barra de progreso estilo docker pull, colores ANSI, logging, instalador de dependencias multiplataforma, autocompletado shell.
## Próximos pasos

- [x] Tests con mock de exec.Command (inyección de dependencias)
- [x] Benchmarks Go
- [x] Comando `--bench` para medir velocidad por formato
- [x] Compresión paralela de múltiples archivos (auto NCPU)
- [x] Barra de progreso con ProgressTracker (byte-level en pipe, per-file en archivos)
- [x] CI/CD (GitHub Actions)
- [ ] Publicar binarios precompilados en releases

## MCP Servers

Usar activamente en TODAS las tareas del proyecto, mínimo 2 por interacción. Si no se usan, explicar por qué no aplican.

### codegraph
- `codegraph_explore` para entender flujos y relaciones entre símbolos ANTES de grep/read
- Ejecutar `codegraph init` en el repo si no hay `.codegraph/`

### graphify
- Grafo de conocimiento del proyecto en `graphify-out/` con 141 nodos, 361 edges, 9 comunidades
- `graphify query "<pregunta>"` para navegar el grafo (BFS/DFS)
- `graphify path "NodoA" "NodoB"` para camino más corto entre conceptos
- `graphify explain "Nodo"` para explicación de un nodo
- `graphify-out/graph.html` — visualización interactiva (abrir en navegador)
- `graphify-out/GRAPH_REPORT.md` — reporte completo con god nodes, comunidades y preguntas sugeridas
- God nodes principales: `WriteLogf()` (21 edges), `DoCompress()` (20), `main()` (18)
- Las 9 comunidades: Compression Primitives (0.46), Format Parsing (0.42), Tool Detection (0.24), Compression Core (0.23), Package Management (0.22), CLI & Logging (0.20), Decompression Pipeline (0.18), Utilities & Helpers (0.13)
- Para re-indexar: `python3 -m graphify.cli .` (solo después de cambios grandes en la estructura)

### context-compress
- `batch_execute` para ejecutar múltiples comandos y auto-indexar su salida
- `execute`/`execute_file` con python/shell para análisis de código sin cargarlo al contexto
- `search` para recuperar secciones específicas de resultados indexados
- Preferir sobre comandos con salida >5KB

### memory
- Almacenar hallazgos, bugs y relaciones como entidades/observaciones en el grafo de conocimiento
- Usar `create_entities` para cada bug/file encontrado
- Usar `create_relations` para conectar bugs con archivos y entre sí

### sqlite
- Almacenar resultados estructurados (bugs con severity, file, line, fix_options)
- Usar `append_insight` para registrar conclusiones del análisis

### sequential-thinking
- Estructurar análisis multi-paso antes de emitir conclusiones
- Verificar bugs dudosos en rondas separadas antes de confirmarlos

### chrome-devtools, developer-knowledge, design-mcp, blender
- NO relevantes para este proyecto. Omitir.

## Skills disponibles

### code-review-excellence
- **Cuándo:** Revisar PRs, cambios grandes, o establecer estándares de review en el equipo.
- **Qué aporta:** Metodología estructurada en 4 fases (contexto → alto nivel → línea por línea → resumen), técnicas de feedback (checklists, preguntas, sugerencias), plantillas, y checklist de seguridad/rendimiento/testing.
- **Flujo:** Fase 1: entender contexto y PR → Fase 2: arquitectura y diseño → Fase 3: línea por línea (lógica, seguridad, rendimiento, mantenibilidad) → Fase 4: resumen y decisión (approve/comment/request changes).
- **Severidad:** Usar 🔴 blocking, 🟡 important, 🟢 nit, 💡 suggestion, 📚 learning, 🎉 praise.

### refactoring-patterns
- **Cuándo:** Mejorar estructura interna sin cambiar comportamiento observable.
- **Qué aporta:** Catálogo Fowler completo de code smells (5 familias: Bloaters, OO Abusers, Change Preventers, Dispensables, Couplers) con sus refactorings correspondientes (Extract Method, Replace Conditional with Polymorphism, etc.).
- **Scoring:** 10/10 structural quality score basado en 8 preguntas de diagnóstico rápido.
- **Flujo seguro:** tests (green) → aplicar una transformación pequeña → tests (green) → commit. Nunca refactorizar en rojo.
- **Principio clave:** Rule of Three — tolera duplicación una vez, anótala dos, refactoriza a la tercera.
- **NO usar para:** reescrituras completas, código sin tests, o código próximo a eliminarse.

## Comportamiento esperado

Actuar como **equipo de desarrollo completo**, que cada actor del equipo sea un subagente. Sin necesidad de instrucciones explícitas por paso:

| Rol | Responsabilidad |
|---|---|
| **Arquitecto** | Decidir estructura, worktrees, ramas, orden de merges |
| **Dev** | Implementar fixes y features en Go |
| **Reviewer** | Encontrar bugs, revisar código, sugerir mejoras |
| **Tester** | Agregar tests, verificar que `go test ./...` pase |
| **DevOps** | Worktrees, merge strategy, cleanup |
| **Documentador** | Mantener AGENTS.md actualizado con decisiones |

**Flujo por omisión:** Planificar → **Test rojo** → **Verde (implementación mínima)** → **Refactor** → Revisar bugs → Mergear → Documentar.

- **Trabajo en paralelo con worktrees descriptivos:** Por cada tarea (fix o feature), crear un worktree con nombre descriptivo (ej: `fix/mode-conflicts`, `feat/decompress-progress`). Todos los worktrees se trabajan en paralelo usando subagentes simultáneos para ahorrar tiempo. Al terminar cada uno, mergear a `main` y eliminar worktree + rama.
  1. `git branch fix/algo main && git worktree add ../crush-fix-algo fix/algo`
  2. Lanzar subagentes simultáneos, cada uno trabajando en su worktree
  3. Verificar compilación y tests en cada worktree
  4. `git merge fix/algo --no-edit` en `main`
  5. `git worktree remove ../crush-fix-algo && git branch -d fix/algo`
- **Protocolo de resolución de conflictos:**
  1. **Rebase obligatorio sobre main:** Cuando varias ramas se trabajan en paralelo y una de ellas se fusiona primero a `main`, las ramas restantes deben actualizarse de inmediato en su propio worktree ejecutando `git rebase main` (o `git merge main`).
  2. **Resolución semántica (no ciega):** Nunca resolver conflictos usando automáticamente `--ours` o `--theirs`. Debe examinarse el diff completo e integrar armónicamente ambos requerimientos conservando los contratos de interfaz y tipos.
  3. **Verificación post-conflicto:** Tras resolver cualquier conflicto en el worktree, es estrictamente obligatorio volver a ejecutar `go test -v -race ./...` y `go vet ./...`. Ninguna rama se mergea a `main` con tests fallando o código roto.
  4. **Merge limpio a main:** `main` solo recibe merges libres de conflictos y completamente verificados.
- **Revisión exhaustiva de bugs:** Tras implementar fixes, hacer re-revisión completa del código en busca de bugs restantes. Si se encuentran nuevos bugs, fixearlos y repetir el ciclo. No detenerse hasta que queden **0 bugs conocidos** en todo el proyecto.
- **Commits por fix/feature:** Cada fix o feature debe tener su propio commit. No mezclar cambios distintos en un mismo commit.
- **Test obligatorio antes de commit:** Todo fix o feature debe compilar y pasar `go test ./...` (con `-race` si es concurrente) y `go vet ./...` sin errores, y el test correspondiente debe haberse escrito y visto fallar ANTES de la implementación. Si falla algún test o aparece un bug, debe corregirse hasta que quede 0 bugs antes de hacer commit.
- No esperar instrucciones en cada sub-paso.
- Al terminar un encargo, dejar el repo limpio (rama `main` actualizada, worktrees removidos, ramas fix eliminadas, AGENTS.md reflejando el nuevo estado).

## Referencias

- `COMPRESOR.md` — historial completo del desarrollo
- `README.md` — documentación de usuario
