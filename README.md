# CRUSH

<div align="center">

**Herramienta multi-formato de compresión y descompresión vía pipes UNIX de alto rendimiento con auto-paralelismo.**

[![CI](https://github.com/usuario/crush/actions/workflows/ci.yml/badge.svg)](https://github.com/usuario/crush/actions)
[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://golang.org)
[![Tests](https://img.shields.io/badge/tests-162%20passing%20%7C%20race%20detector-brightgreen)](https://github.com/usuario/crush)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Formats](https://img.shields.io/badge/formats-13%20supported-blueviolet)](https://github.com/usuario/crush)

</div>

---

## Características

- **13 formatos soportados:** `gz`, `xz`, `bz2`, `bz3`, `zst`, `lz`, `lrz`, `zip`, `7z`, `tar`, `rar`, `lz4`, `br`.
- **Detección y descompresión interactiva:** Al invocar `crush -d` sin argumentos, detecta automáticamente todos los archivos comprimidos del directorio actual, muestra sus tamaños y solicita confirmación para descomprimirlos en paralelo.
- **Máximo paralelismo automático (NCPU):** No requiere flags manuales de hilos (`-j`). Detecta automáticamente los núcleos disponibles (`NCPU()`) y optimiza el uso de CPU tanto a nivel de herramienta multihilo (`pigz`, `lbzip2`, `plzip`, `bzip3`, `xz -T0`, `zstd -T0`, `7z -mmt`, `rar -mt`) como a nivel de procesamiento concurrente entre múltiples archivos.
- **Pipeline de streaming en memoria:** Compresión y descompresión en tiempo real vía pipes UNIX (`exec.Cmd` + `StdoutPipe`), eliminando la creación de archivos `.tar` intermedios en disco.
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
# Compresión de archivo individual
crush -c -f gz documento.txt                 # → documento.tar.gz

# Compresión paralela multi-archivo (cada archivo genera su propio comprimido)
crush -c -f 7z *.iso

# Combinar múltiples archivos en un único archivo comprimido
crush -c -C -f 7z archivo1.bin archivo2.bin # → crush_archive.7z

# Comprimir un directorio conservando el original (-k) y en modo detallado (-v)
crush -c -f zst -k -v fotos/                 # → fotos.tar.zst

# Especificar directorio de salida (-o)
crush -c -f xz -o /backup/ base_datos.sql

# Dividir la salida comprimida en volúmenes de 10 MB (-s)
# Compatible con formatos de flujo (gz, xz, bz2, bz3, zst, lz, lz4, br y tar.*); no soportado para lrz, zip, 7z, tar ni rar.
crush -c -f zst -s 10 archivo_pesado.iso      # → archivo_pesado.tar.zst.part00, part01...

# Comprimir excluyendo patrones (-exclude)
crush -c -f zip -exclude "*.log" -exclude "node_modules/*" proyecto/

# Comprimir leyendo la lista de archivos desde un fichero (-i)
crush -c -f gz -i lista_archivos.txt
```

> **Nota sobre originales:** Por defecto, al completar una compresión sin errores, `crush` elimina los archivos de origen. Para conservarlos, usa siempre la opción `-k`.

### Descompresión

La descompresión detecta automáticamente el formato a partir de la extensión del archivo y muestra el progreso de extracción en tiempo real:

```bash
# Descompresión interactiva: detecta todos los comprimidos del directorio y pide confirmación
crush -d

# Descomprimir en el directorio actual
crush -d archivo.tar.gz

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

# Leer contenido comprimido directamente a stdout (útil para tuberías)
crush -r registros.tar.gz | grep "ERROR 500"
crush -r dump.sql.zst | mysql -u root -p base_datos

# Simulación (dry-run): ver los comandos que se ejecutarían sin realizar cambios
crush -c -f xz -n directorio_grande/
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
  crush -c -f FORMATO [opciones] archivo...
  crush -d [opciones] archivo...
  crush -l archivo...
  crush -t archivo...
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
| `-c` | Comprimir archivos. |
| `-d` | Descomprimir archivos (detección automática de formato). |
| `-l` | Listar el contenido de los archivos comprimidos. |
| `-t` | Verificar la integridad de los archivos comprimidos. |
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
| `-f` | `FORMATO` | Formato objetivo (`gz`, `xz`, `bz2`, `bz3`, `zst`, `lz`, `lrz`, `zip`, `7z`, `tar`, `rar`, `lz4`, `br`). | Requerido en `-c` |
| `-o` | `DIR` | Directorio de salida. | `.` |
| `-k` | — | Conservar archivos originales tras compresión. | `false` (los elimina) |
| `-v` | — | Modo verbose (muestra los comandos del sistema invocados). | `false` |
| `-n` | — | Modo simulacro (*dry-run*): muestra qué haría sin ejecutar. | `false` |
| `-force` | — | Sobrescribir archivos destino existentes sin confirmar. | `false` |
| `-quick` | — | Verificación rápida de integridad (no valida cada archivo interno). | `false` |
| `-C` | — | Combinar múltiples archivos en un único archivo comprimido. | `false` (paralelo) |
| `-s` | `N` | Dividir el archivo comprimido en partes de `N` MB (formatos de flujo: `gz`, `xz`, `bz2`, `bz3`, `zst`, `lz`, `lz4`, `br` y `tar.*`; no soportado para `lrz`, `zip`, `7z`, `tar`, `rar`). | `0` (sin división) |
| `-i` | `ARCHIVO` | Leer lista de archivos de entrada desde un fichero o stdin (`-`). | — |
| `-exclude`| `PATRÓN` | Patrón de exclusión glob (puede repetirse). | — |
| `-opts` | `"OPTS"` | Opciones adicionales pasadas directamente a la herramienta subyacente. | — |
| `--bench-size` | `N` | Tamaño en MB del dataset de prueba para `--bench`. | `10` |

---

## Arquitectura y Estructura del Código

El proyecto está diseñado bajo los principios de modularidad, cero dependencias externas y desarrollo guiado por pruebas (TDD):

```text
crush/
├── main.go         # CLI flags, dispatch de comandos, autocompletado y ayuda
├── format.go       # Detección de formatos, extensiones y ordenamiento por ratio
├── compress.go     # Compresión concurrente paralela y streaming tar-pipe
├── decompress.go   # Descompresión multi-formato, splitWriter y tracking de entrada
├── bench.go        # Motor de benchmark determinista y formateo de tablas
├── test_cmd.go     # Verificación de integridad (-t)
├── util.go         # NCPU, límites de memoria RAM, pipeline streaming, ProgressTracker
├── pkgmgr.go       # Gestor multiplataforma de dependencias del sistema
├── Makefile        # Comandos de compilación, testeo e instalación
├── *_test.go       # Tests unitarios y de integración table-driven
└── mock_test.go    # Tests con inyección de dependencias (execCommand) y mocks
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

Este proyecto está bajo la Licencia **MIT**. Consulta el archivo `LICENSE` para más detalles.
