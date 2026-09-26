# CRUSH

<div align="center">

**Herramienta multi-formato de compresión y descompresión vía pipes UNIX de alto rendimiento con auto-paralelismo.**

[![CI](https://github.com/usuario/crush/actions/workflows/ci.yml/badge.svg)](https://github.com/usuario/crush/actions)
[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://golang.org)
[![Tests](https://img.shields.io/badge/tests-429%20passing%20%7C%20race%20detector-brightgreen)](https://github.com/usuario/crush)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Formats](https://img.shields.io/badge/formats-13%20supported-blueviolet)](https://github.com/usuario/crush)

</div>

---

## Características

- **13 formatos soportados:** `gz`, `xz`, `bz2`, `bz3`, `zst`, `lz`, `lrz`, `zip`, `7z`, `tar`, `rar`, `lz4`, `br`.
- **Compresión máxima real y paralelismo automático (NCPU):** No requiere flags manuales de hilos (`-j`). Detecta automáticamente los núcleos disponibles (`NCPU()`) y maximiza los ratios de compresión (`zstd --ultra -22`, `7z -mx=9 -md=256m -mfb=273` adaptativo a RAM, `bzip3 -b 64`, `lz4 -9` LZ4HC) y descompresión multihilo (`lbzip2 -n N`, `pigz -p N`).
- **Distribución Dinámica Proporcional de Hilos y Token Pool:** Reparto ponderado de núcleos según el tamaño de cada archivo en bytes (método del resto mayor Hamilton-Hare), eliminando la latencia de cola (*tail latency*) y garantizando una saturación del 100% de la CPU durante todo el lote (+84% de aceleración medida en juegos de PS2 reales y +35% en colas de archivos desiguales).
- **Planificación LPT inteligente (Compresión y Descompresión):** Ordenamiento óptimo descendente por tamaño (*Longest Processing Time first*) tanto al comprimir múltiples archivos como al descomprimir lotes de archivos, eliminando el cuello de botella por archivos rezagados.
- **Compresión simultánea multi-formato (`-F` / `--formats`):** Permite comprimir en múltiples formatos en una sola pasada (ej: `crush -c -F gz,xz,zst archivo.txt`), preservando los archivos originales durante todas las fases intermedias y reportando el avance y verificación de cada formato.
- **Auto-instalación de herramientas multihilo y fallback secuencial inteligente:** Detección de herramientas concurrentes (`pigz`, `lbzip2`, `plzip`, `lz4 -T`, `7z`, `xz`, `zstd`); si la herramienta óptima multihilo no se encuentra en el sistema, `crush` intenta instalarla automáticamente mediante el gestor de paquetes (`apt`, `dnf`, etc.); si no está en los repositorios o no se puede instalar, recurre transparentemente a la herramienta secuencial (`gzip`, `bzip2`, `lzip`) con advertencia en consola; y si ninguna está disponible, emite un aviso de error detallado.
- **Compresión interactiva del directorio actual:** Al ejecutar `crush -c` sin especificar archivos, detecta automáticamente todos los elementos comprimibles en la ruta actual, muestra sus tamaños y solicita confirmación interactiva para comprimirlos (usando `-f`, `-F` o `gz` por omisión).
- **Adición y actualización in-place en archivos comprimidos (`-a` / `-u`):** Inserta nuevos archivos o carpetas directamente dentro de un archivo comprimido preexistente (`.zip`, `.7z`, `.rar`, `.tar`, `.tar.*`) sin generar un archivo nuevo en disco.
- **Modo observador de directorios (`-watch`):** Monitoreo continuo de directorios sin dependencias externas usando `syscall.Inotify` nativo en Linux (`IN_CLOSE_WRITE | IN_MOVED_TO`) y sondeo en otras plataformas, procesando automáticamente compresión (`-c`) o descompresión (`-d`) de archivos entrantes.
- **Generación y verificación de checksums SHA-256 (`-hash`, `-verify`):** Generación automática de archivos `.sha256` durante la compresión e integración en verificación para validar la integridad contra el hash.
- **Cifrado y contraseñas (`-p`, `-password`):** Cifrado seguro para formatos de contenedor (`7z`, `zip`, `rar`) con soporte para prompt interactivo con terminal oculta y cifrado de cabeceras (`-mhe=on`).
- **Soporte para Sparse Files (`-sparse` / `-S`):** Optimización de espacio al empaquetar archivos dispersos en archivos tar.
- **Filtro selectivo de extracción (`-filter`):** Extracción dirigida por patrón glob (`*.txt`, subcarpetas, etc.) compatible con contenedores `tar`, `7z`, `zip` y `rar`.
- **Streaming directo y Zero-Copy con Linux `splice(2)`:** Extracción directa sin archivos `.tar` temporales intermedios y aceleración en espacio de kernel con `splice(2)` y buffers de pipes ampliados a 1 MiB (`F_SETPIPE_SZ`).
- **Eliminación de latencia de inicio y progreso en tiempo real:** Medición directa en el flujo de entrada de pipelines tar, diccionario LZMA2 dinámico adaptativo al tamaño de datos y a la RAM del sistema (evitando sobrecargas de memoria en equipos modestos), y caché de tamaños en una sola pasada para evitar re-escaneos redundantes en disco.
- **Detección y descompresión interactiva:** Al invocar `crush -d` sin argumentos, detecta automáticamente todos los archivos comprimidos del directorio actual, muestra sus tamaños y solicita confirmación para descomprimirlos en paralelo.
- **Barra de progreso tabular en tiempo real:** Interfaz dinámica estilo *Docker-pull* en terminales interactivas, con barra general agregada, sub-barras individuales por archivo con columnas milimétricamente alineadas, velocidad en MB/s y estimación de tiempo restante (ETA) estabilizada.
- **Suite de benchmarking integrada (`--bench`):** Permite evaluar el throughput (MB/s) y el ratio de compresión en tu máquina con datasets deterministas y verificación criptográfica SHA-256.
- **Autocompletado de comandos:** Instalación nativa de completion para Bash, Zsh y Fish.
- **Cero dependencias externas en Go:** 100% biblioteca estándar de Go (`stdlib`).
- **Instalador de dependencias multiplataforma:** Detección y gestión automática de paquetes en Debian/Ubuntu (`apt`), RedHat/Fedora/CentOS (`dnf`/`yum`), Arch Linux (`pacman`), openSUSE (`zypper`), Alpine (`apk`) y macOS (`brew`).

---

## Demostración Visual

Al procesar múltiples archivos en paralelo, `crush` presenta un panel tabular interactivo:

```text
Comprimiendo 5 archivo(s) en paralelo...
  Formato: 7z
  Modo: Compresión 7z (LZMA2)

[====>               ]  25.6%  0/5  387MiB/s  30s restantes

  Jak and Daxter - Th...   [>         ]  14%   208.9MiB / 1.4GiB            59s
  Manhunt (USA).iso        [===>      ]  45%     2.0GiB / 4.4GiB            12s
  Prince of Persia - ...   [          ]   6%   256.8MiB / 3.6GiB          2m19s
  Rayman 2 - Revoluti...   [          ]   8%   385.6MiB / 4.2GiB          1m46s
  Simpsons, The - Hit...   [====>     ]  55%     1.1GiB / 2.0GiB             8s
```

---

## Requisitos

- **Compilación:** Go 1.21 o superior.
- **Sistema operativo:** Linux o macOS.
- **Herramientas del sistema:** Para aprovechar todos los formatos, `crush` utiliza las utilidades del sistema operativo. Si alguna herramienta multihilo no está instalada, `crush` utiliza automáticamente la versión serial como alternativa de respaldo (*fallback*).

---

## Instalación

### 1. Compilar e instalar binario

```bash
# Clonar el repositorio
git clone https://github.com/usuario/crush.git
cd crush

# Compilar
make build

# Instalar binario en /usr/local/bin
sudo ./crush --install
# (o alternativamente: sudo make install)
```

### 2. Instalar herramientas de compresión del sistema

`crush` incluye un detector que identifica tu gestor de paquetes e instala las herramientas necesarias:

```bash
sudo crush --install-deps
```

*Soporta: `apt-get`, `dnf`, `yum`, `pacman`, `zypper`, `apk` y `brew`.*

### 3. Activar autocompletado en tu Shell

```bash
# Auto-detectar la shell actual e instalar
sudo crush --completion

# O para una shell específica:
sudo crush --completion bash
sudo crush --completion zsh
sudo crush --completion fish
```

### Desinstalación

```bash
sudo crush --uninstall
```

---

## Formatos Soportados

### Formatos Tar-Pipe (Agrupan en stream `.tar.*`)

| Formato | Compresor Primario (Multihilo) | Fallback Serial | Ratio de Compresión | Perfil de Velocidad |
|---------|--------------------------------|-----------------|---------------------|---------------------|
| `tar.lrz` | `lrzip -p N` | — | Máxima (RAM) | Lenta |
| `tar.bz3` | `bzip3 -j N` | — | Muy alta | Media |
| `tar.xz` / `txz` | `xz -T0` | `xz` | Alta | Lenta |
| `tar.bz2` / `tbz2` | `lbzip2` / `pbzip2` | `bzip2` | Alta | Media |
| `tar.lz` / `tlz` | `plzip` | `lzip` | Alta | Media |
| `tar.br` | `brotli` | — | Alta | Lenta |
| `tar.zst` / `tzst` | `zstd -T0` | `zstd` | Media-Alta | Muy rápida |
| `tar.gz` / `tgz` | `pigz` | `gzip` | Media | Rápida |
| `tar.lz4` | `lz4` | — | Baja | Ultrarrápida |
| `tar` | `tar` | — | Ninguna (empaqueta) | I/O Bound |

### Formatos Nativos (Contenedor propio)

| Formato | Compresor | Multihilo | Algoritmo |
|---------|-----------|-----------|-----------|
| `.7z` | `7zz` / `7z` | Auto (`-mmt=on`) | LZMA2 |
| `.zip` | `7z` / `zip` | Auto (`-mmt=on` vía 7z) | DEFLATE |
| `.rar` | `rar` | Auto (`-mtN`) | RAR |

---

## Uso y Ejemplos

### Compresión

```bash
# Compresión interactiva: detecta elementos comprimibles en la ruta actual y solicita confirmación
crush -c                                     # Usa gz por defecto
crush -c -f 7z                               # O especificando formato

# Compresión de archivo individual
crush -c -f gz documento.txt                 # → documento.tar.gz

# Compresión paralela multi-archivo y multi-carpeta (cada elemento genera su propio comprimido)
crush -c -f 7z *.iso
crush -c -f 7z carpeta1/ carpeta2/           # → carpeta1.7z y carpeta2.7z en paralelo

# Combinar múltiples archivos o carpetas en un único archivo comprimido (-C)
crush -c -C -f 7z archivo1.bin archivo2.bin # → crush_YYYYMMDD_HHMMSS.7z

# Comprimir un directorio conservando el original (-k) y en modo detallado (-v)
crush -c -f zst -k -v fotos/                 # → fotos.tar.zst

# Especificar directorio de salida (-o)
crush -c -f xz -o /backup/ base_datos.sql

# Dividir la salida comprimida en volúmenes de 10 MB (-s)
# Compatible con formatos de flujo (gz, xz, bz2, bz3, zst, lz, lz4, br y tar.*); no soportado para lrz, zip, 7z, tar ni rar.
# Cada elemento dividido se almacena ordenadamente en su propia carpeta: <baseName>_parts/
crush -c -f zst -s 10 archivo_pesado.iso      # → archivo_pesado_parts/archivo_pesado.tar.zst + .part01...

# Comprimir excluyendo patrones (-exclude)
crush -c -f zip -exclude "*.log" -exclude "node_modules/*" proyecto/

# Comprimir y generar checksum SHA-256 (.sha256)
crush -c -f gz -hash documento.txt

# Comprimir con cifrado por contraseña (-p) en contenedores (7z, zip, rar)
crush -c -f 7z -p secret confidencial.pdf

# Optimizar compresión de archivos dispersos en tar (-sparse / -S)
crush -c -f tar.gz -sparse disco_virtual.raw

# Comprimir leyendo la lista de archivos desde un fichero (-i)
crush -c -f gz -i lista_archivos.txt
```

> **Nota sobre originales:** Por defecto, al completar una compresión sin errores, `crush` elimina los archivos de origen. Para conservarlos, usa siempre la opción `-k`.

### Agregar o Actualizar Archivos In-Place (`-a`, `-u`)

Permite agregar o actualizar archivos o carpetas directamente dentro de un contenedor comprimido existente (`.zip`, `.7z`, `.rar`, `.tar`, `.tar.*`) sin generar un archivo nuevo en disco ni requerir descompresión manual previa:

```bash
# Agregar un archivo a un ZIP existente
crush -a archivo.zip nuevo_documento.txt

# Agregar un directorio completo dentro de un archivo .7z
crush -a respaldo.7z carpeta_fotos/

# Actualizar múltiples archivos en un contenedor tar.gz (con soporte para -p y -hash)
crush -u paquete.tar.gz archivo1.txt archivo2.png
```

### Descompresión

La descompresión detecta automáticamente el formato a partir de la extensión del archivo y muestra el progreso de extracción en tiempo real:

```bash
# Descompresión interactiva: busca recursivamente todos los comprimidos, unifica partes divididas y pide confirmación
crush -d

# Descomprimir en el directorio actual
crush -d archivo.tar.gz

# Descomprimir con contraseña (-p)
crush -d -p secret protegido.7z

# Extracción selectiva por patrón glob (-filter) en tar, 7z, zip, rar
crush -d -filter "*.txt" respaldo.tar.gz
crush -d -filter "docs/*" paquete.7z

# Descomprimir múltiples archivos concurrentemente
crush -d *.zip *.7z

# Descomprimir hacia un directorio destino específico (-o)
crush -d -o /tmp/ descargas.tar.xz

# Forzar sobreescritura de archivos existentes (-force)
crush -d -force paquete.tar.zst
```

### Inspección, Verificación y Pipes

```bash
# Listar contenido de un archivo comprimido
crush -l paquete.7z
crush -l *.tar.gz

# Verificar integridad sin extraer a disco
crush -t backup.tar.xz
crush -t -quick archivo_enorme.7z             # Verificación rápida

# Verificar integridad y validar checksum criptográfico SHA-256 si existe .sha256
crush -verify backup.tar.xz

# Leer contenido comprimido directamente a stdout (útil para tuberías)
crush -r registros.tar.gz | grep "ERROR 500"
crush -r dump.sql.zst | mysql -u root -p base_datos

# Simulación (dry-run): ver los comandos que se ejecutarían sin realizar cambios
crush -c -f xz -n directorio_grande/
```

### Modo Observador de Directorios (`-watch`)

Monitorea continuamente un directorio sin dependencias externas (utilizando `syscall.Inotify` nativo en Linux) para procesar archivos entrantes de forma desatendida:

```bash
# Comprimir automáticamente todo archivo entrante a .tar.zst
crush -watch /inbox -c -f zst -k -o /outbox

# Descomprimir automáticamente cualquier archivo comprimido que se deposite en la carpeta
crush -watch /descargas -d -o /extraidos
```

### Benchmarks de Compresión (`--bench`)

Compara la velocidad (MB/s) y el ratio de compresión de todos los formatos en tu máquina:

```bash
# Benchmark con dataset determinista generado en memoria (10 MB por defecto)
crush --bench

# Benchmark especificando tamaño del dataset en MB
crush --bench --bench-size 50

# Benchmark utilizando un archivo propio del mundo real
crush --bench mi_archivo_de_prueba.iso
```

---

## Referencia de Comandos y Opciones

```text
Uso:
  crush -c [opciones] [archivo...]
  crush -a ARCHIVO_COMPRIMIDO [opciones] elemento...
  crush -u ARCHIVO_COMPRIMIDO [opciones] elemento...
  crush -d [opciones] [archivo...]
  crush -watch DIRECTORIO -c -f FORMATO [opciones]
  crush -watch DIRECTORIO -d [opciones]
  crush -l archivo...
  crush -t archivo...
  crush -verify archivo...
  crush -r archivo...
  crush --install
  crush --install-deps
  crush --uninstall
  crush --completion [bash|zsh|fish]
  crush --bench [archivo]
```

### Modos de Operación

| Opción | Descripción |
|---|---|
| `-c` | Comprimir archivos. Sin argumentos, ejecuta compresión interactiva del directorio actual. |
| `-a`, `-u` | Agregar o actualizar archivos o carpetas dentro de un contenedor comprimido existente (`.zip`, `.7z`, `.rar`, `.tar`, `.tar.*`). |
| `-d` | Descomprimir archivos (detección automática de formato). Sin argumentos, ejecuta descompresión interactiva. |
| `-watch DIR` | Monitorear directorio continuamente para procesar archivos entrantes (`-c` o `-d`). |
| `-l` | Listar el contenido de los archivos comprimidos. |
| `-t` | Verificar la integridad de los archivos comprimidos. |
| `-verify` | Verificar integridad del contenedor y validar checksum SHA-256 si existe `.sha256`. |
| `-r` | Descomprimir y emitir contenido directamente a `stdout`. |
| `--bench` | Ejecutar benchmark comparativo de formatos. |
| `--install` | Instalar el binario `crush` en `/usr/local/bin`. |
| `--install-deps` | Detectar e instalar herramientas de compresión faltantes en el sistema. |
| `--completion` | Instalar autocompletado en el sistema para la shell detectada o especificada. |
| `--uninstall` | Desinstalar `crush` del sistema. |
| `-h`, `--help` | Mostrar mensaje de ayuda. |
| `--version` | Mostrar versión de `crush`. |

### Opciones y Modificadores

| Opción | Argumento | Descripción | Por Defecto |
|---|---|---|---|
| `-f` | `FORMATO` | Formato objetivo (`gz`, `xz`, `bz2`, `bz3`, `zst`, `lz`, `lrz`, `zip`, `7z`, `tar`, `rar`, `lz4`, `br`). | Requerido en `-c` (o `-F`) |
| `-F`, `--formats` | `LISTA` | Comprimir en múltiples formatos separados por coma (ej: `gz,xz,zst`). | — |
| `-o` | `DIR` | Directorio de salida. | `.` |
| `-k` | — | Conservar archivos originales tras compresión. | `false` (los elimina) |
| `-v` | — | Modo verbose (muestra los comandos del sistema invocados). | `false` |
| `-n` | — | Modo simulacro (*dry-run*): muestra qué haría sin ejecutar. | `false` |
| `-force` | — | Sobrescribir archivos destino existentes sin confirmar. | `false` |
| `-quick` | — | Verificación rápida de integridad (no valida cada archivo interno). | `false` |
| `-C` | — | Combinar múltiples archivos en un único archivo comprimido. | `false` (paralelo) |
| `-s` | `N` | Dividir el archivo comprimido en partes de `N` MB (formatos de flujo: `gz`, `xz`, `bz2`, `bz3`, `zst`, `lz`, `lz4`, `br` y `tar.*`; no soportado para `lrz`, `zip`, `7z`, `tar`, `rar`). | `0` (sin división) |
| `-hash` | — | Generar archivo de checksum SHA-256 (`<archivo>.sha256`) durante la compresión. | `false` |
| `-p`, `-password` | `PASS` | Contraseña para cifrado o descifrado (`7z`, `zip`, `rar`). Si se omite argumento, pide contraseña oculta en consola. | — |
| `-sparse`, `-S` | — | Activar soporte para archivos dispersos (*sparse files*) en `tar`. | `false` |
| `-filter` | `PATRÓN` | Filtro de extracción selectiva por patrón glob (`*.txt`, subcarpetas, etc.) en `tar`, `7z`, `zip`, `rar`. | — |
| `-i` | `ARCHIVO` | Leer lista de archivos de entrada desde un fichero o stdin (`-`). | — |
| `-exclude`| `PATRÓN` | Patrón de exclusión glob (puede repetirse). | — |
| `-opts` | `"OPTS"` | Opciones adicionales pasadas directamente a la herramienta subyacente. | — |
| `--bench-size` | `N` | Tamaño en MB del dataset de prueba para `--bench`. | `10` |

---

## Arquitectura y Estructura del Código

El proyecto está diseñado bajo los principios de modularidad, cero dependencias externas y desarrollo guiado por pruebas (TDD):

```text
crush/
├── main.go          # CLI flags, dispatch de comandos, autocompletado y ayuda
├── format.go        # Detección de formatos, extensiones y ordenamiento por ratio
├── compress.go      # Compresión concurrente paralela, streaming tar-pipe, -hash, -p, -sparse
├── decompress.go    # Descompresión multi-formato, splitWriter, -p y -filter
├── watcher.go       # Watcher, DoWatch, loop con stdlib
├── watcher_linux.go # Backend inotify (IN_CLOSE_WRITE, IN_MOVED_TO)
├── watcher_other.go # Backend fallback por sondeo
├── bench.go         # Motor de benchmark determinista y formateo de tablas
├── test_cmd.go      # Verificación de integridad (-t, -verify) con checksums SHA-256
├── util.go          # NCPU, límites RAM, pipeline streaming, ProgressTracker, SHA-256
├── util_linux.go    # F_SETPIPE_SZ (1 MiB) y splice(2) zero-copy
├── util_other.go    # Fallbacks de pipe y splice
├── pkgmgr.go        # Gestor multiplataforma de dependencias del sistema
├── Makefile         # Comandos de compilación, testeo e instalación
├── *_test.go        # Tests unitarios y de integración table-driven
└── mock_test.go     # Tests con inyección de dependencias (execCommand) y mocks
```

### Ejecutar Tests y Verificación

```bash
# Ejecutar suite de pruebas con detector de carreras (-race)
go test -v -race ./...

# Análisis estático
go vet ./...

# Ejecutar benchmarks nativos de Go
go test -bench=. ./...
```

---

## Licencia

Este proyecto está bajo la Licencia **GNU General Public License v3.0 (GPLv3)**. Consulta el archivo [LICENSE](LICENSE) para más detalles.
