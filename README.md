# COMPRESOR

Herramienta multi-formato de compresión y descompresión vía pipe.
Soporta 13 formatos usando versiones **multihilo** para aprovechar todos los núcleos del CPU.

## Requisitos

- **Go 1.21+** (solo para compilar)
- **Linux** (Debian/Ubuntu o RedHat/CentOS/Fedora)
- Los compresores se instalan automáticamente con `--install`

## Instalación

### Opción 1: compilar desde fuente (recomendada)

```bash
# Clonar
git clone <repo> && cd compresor

# Compilar e instalar en /usr/local/bin
sudo make install
# O paso a paso:
make build
sudo install -m 755 compresor /usr/local/bin/

# Instalar dependencias del sistema (apt/dnf/yum)
compresor --install-deps
```

### Opción 2: instalación todo-en-uno

```bash
# Compila, copia el binario e instala todas las herramientas faltantes
compresor --install          # binario + dependencias
compresor --install-deps     # solo dependencias (si ya tienes el binario)
```

### Opción 3: solo el binario

```bash
make build          # genera ./compresor
./compresor -h      # usar directamente
```

### Desinstalar

```bash
compresor --uninstall
# o manual:
sudo rm /usr/local/bin/compresor
```

## Formatos soportados

Los formatos tar-pipe comprimen múltiples archivos en un tar y lo comprimen en pipeline.
Los formatos nativos (zip, 7z, rar) empaquetan y comprimen en un solo paso.

### Tar-pipe (agrupan archivos en .tar.*)

| Formato | Compresor | Hilos | Ratio | Velocidad |
|---------|-----------|-------|-------|-----------|
| tar.gz / tgz | pigz | todos | media | rápida |
| tar.xz / txz | xz -T0 | auto | alta | lenta |
| tar.bz2 / tbz2 | lbzip2/pbzip2 | todos | alta | media |
| tar.bz3 | bzip3 -j N | todos | muy alta | media |
| tar.zst / tzst | zstd -T0 | auto | media-alta | rápida |
| tar.lz / tlz | plzip | todos | alta | media |
| tar.lrz | lrzip -p N | todos | máxima | lenta |
| tar.lz4 | lz4 | — | baja | ultrarrápida |
| tar.br | brotli | — | alta | lenta |

### Nativos (formato propio)

| Formato | Compresor | Hilos | Algoritmo |
|---------|-----------|-------|-----------|
| .zip | 7z -mmt=on | auto | DEFLATE |
| .7z | 7zz/7z -mmt=on | auto | LZMA2 |
| .rar | rar -mtN | todos | RAR |
| .tar | tar | — | solo empaqueta |

## Uso

```bash
# Comprimir
compresor -c -f gz documento.txt              # → documento.tar.gz
compresor -c -f xz -v carpeta/                 # → carpeta.tar.xz (verbose)
compresor -c -f zst -o /salida/ archivo.iso    # → /salida/archivo.tar.zst

# Descomprimir (detección automática de formato por extensión)
compresor -d archivo.tar.gz                    # → ./
compresor -d -o /tmp/ archivo.zip              # → /tmp/

# Verificar integridad
compresor -t *.tar.gz

# Listar contenido de un comprimido
compresor -l archivo.7z

# Leer contenido a stdout (sin descomprimir a disco)
compresor -r archivo.txt.gz | head

# Modo simulacro (ver qué haría sin ejecutar)
compresor -c -f xz -n carpeta/
```

## Opciones

| Flag | Descripción | Defecto |
|------|-------------|---------|
| `-c -f FORMATO` | Comprimir en el formato indicado | — |
| `-d` | Descomprimir (detecta formato por extensión) | — |
| `-l` | Listar contenido del archivo comprimido | — |
| `-t` | Verificar integridad | — |
| `-r` | Leer contenido a stdout | — |
| `-o DIR` | Directorio de salida | `.` |
| `-k` | Conservar archivos originales | off (los borra) |
| `-v` | Modo verbose (muestra comandos) | off |
| `-p` | Barra de progreso (requiere `pv`) | off |
| `-n` | Modo simulacro (dry-run) | off |
| `-s N` | Dividir en partes de N MB | off |
| `-T N` | Hilos de compresión (0 = auto) | auto |
| `-exclude patrón` | Excluir archivos (repetible) | — |
| `-force` | Sobrescribir sin preguntar | off |
| `-opts "flags"` | Flags extra para el compresor | — |

## Ejemplos

```bash
# Comprimir un directorio con ratio máximo (lrzip)
compresor -c -f lrz -v carpeta_de_fotos/

# Comprimir rápido con zstd y barra de progreso
compresor -c -f zst -p video.mp4

# Comprimir y dividir en partes de 10 MB
compresor -c -f gz -s 10 archivo_grande.bin
# Genera: archivo_grande.tar.gz.part00, .part01, ...

# Excluir archivos .log y .tmp
compresor -c -f xz --exclude='*.log' --exclude='*.tmp' carpeta/

# Descomprimir conservando el original
compresor -d -k archivo.zip

# Verificar todos los comprimidos del directorio
compresor -t *.tar.* *.zip *.7z
```

## Ramas del repositorio

- **main** — versión en Go (desarrollo activo, recomendada)
- **bash** — versión original en Bash Script (estable, mantenimiento)

## Estructura del proyecto

```
compresor/
├── main.go         # CLI, flags, dispatch de modos
├── compress.go     # compresión (tar-pipe, zip, 7z, rar, tar)
├── decompress.go   # descompresión multi-formato
├── format.go       # detección y mapeo de formatos
├── test_cmd.go     # verificación de integridad
├── util.go         # NCPU, logging, colores, pipeline
├── pkgmgr.go       # instalación de dependencias (apt/dnf/yum)
├── compresor.sh    # versión Bash (rama bash)
├── Makefile
└── *_test.go       # tests unitarios
```

## Licencia

MIT
