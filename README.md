<div align="center">

# ⚡ CRUSH

**Herramienta multi-formato de compresión y descompresión vía pipes UNIX de ultra alto rendimiento con auto-paralelismo y gestión adaptativa de recursos.**

[![CI](https://github.com/hagom/crush/actions/workflows/ci.yml/badge.svg)](https://github.com/hagom/crush/actions)
[![Go Version](https://img.shields.io/badge/Go-1.27+-00ADD8?style=flat&logo=go)](https://golang.org)
[![Tests](https://img.shields.io/badge/tests-315%20passing%20%7C%20race%20detector-brightgreen)](https://github.com/hagom/crush)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
[![Formats](https://img.shields.io/badge/formats-13%20supported-blueviolet)](https://github.com/hagom/crush)
[![Zero Dependencies](https://img.shields.io/badge/dependencies-0%20(pure%20Go%20stdlib)-success)](go.mod)

</div>

---

## Índice

1. [Características](#características)
   - [Núcleo de compresión y rendimiento](#núcleo-de-compresión-y-rendimiento)
   - [Gestión adaptativa de espacio y lotes](#gestión-adaptativa-de-espacio-y-lotes)
   - [Inspección y herramientas avanzadas](#inspección-y-herramientas-avanzadas)
2. [Requisitos](#requisitos)
3. [Instalación](#instalación)
   - [Compilación del binario](#compilación-del-binario)
   - [Herramientas de compresión del sistema](#herramientas-de-compresión-del-sistema)
   - [Autocompletado de shell](#autocompletado-de-shell)
   - [Desinstalación](#desinstalación)
4. [Formatos soportados](#formatos-soportados)
5. [Guía rápida de uso](#guía-rápida-de-uso)
   - [Compresión](#compresión)
   - [Compresión incremental](#compresión-incremental)
   - [Descompresión](#descompresión)
   - [Reanudación de lotes interrumpidos](#reanudación-de-lotes-interrumpidos)
   - [Agregar a comprimidos existentes](#agregar-a-comprimidos-existentes)
   - [Modo observador](#modo-observador)
   - [Inspección, transcodificación y pipes](#inspección-transcodificación-y-pipes)
6. [Referencia de comandos](#referencia-de-comandos)
   - [Modos de operación](#modos-de-operación)
   - [Modificadores](#modificadores)
   - [Combinaciones de modos](#combinaciones-de-modos)
7. [Seguridad](#seguridad)
   - [Extracción confinada (anti Zip-Slip)](#extracción-confinada-anti-zip-slip)
   - [Argumentos seguros para herramientas externas](#argumentos-seguros-para-herramientas-externas)
   - [Contraseñas](#contraseñas)
   - [Instancia única y archivos de bloqueo](#instancia-única-y-archivos-de-bloqueo)
   - [Registros (logs)](#registros-logs)
8. [Arquitectura](#arquitectura)
9. [Desarrollo y pruebas](#desarrollo-y-pruebas)
10. [Documentación relacionada](#documentación-relacionada)
11. [Licencia](#licencia)

---

## Características

> [!TIP]
> `crush` detecta automáticamente la cantidad de núcleos (`NCPU()`) y la memoria RAM libre del sistema para saturar el procesador sin requerir flags manuales de hilos (`-j`), y sin riesgo de desbordar la memoria (OOM) o el disco.

### Núcleo de compresión y rendimiento

- **13 formatos soportados:** `gz`, `xz`, `bz2`, `bz3`, `zst`, `lz`, `lrz`, `zip`, `7z`, `tar`, `rar`, `lz4`, `br`, más el modo adaptativo `auto`.
- **Auto-paralelismo y compresión máxima:** perfiles de mayor ratio multihilo disponibles (`zstd --ultra -22`, `7z -mx=9 -md=...` adaptativo a RAM, `bzip3 -b 64`, `lz4 -9` LZ4HC, `pigz -p N`, `lbzip2 -n N`). Si la versión multihilo no está instalada, cae en la serial (`gzip`, `bzip2`, `lzip`) sin fallar.
- **Distribución dinámica proporcional de hilos (`DynamicThreadPool`):** reparto ponderado de núcleos según el peso en bytes de cada archivo (algoritmo Hamilton-Hare), con techos por formato, por tamaño de payload y por memoria disponible, tanto en modo paralelo como secuencial.
- **Planificación LPT:** ordenamiento *Longest Processing Time first* en compresión y descompresión.
- **Streaming directo y zero-copy con Linux `splice(2)`:** transferencia entre descriptores en espacio de kernel y buffers de pipe ampliados a 1 MiB (`F_SETPIPE_SZ`).
- **Detección de hardware:** en discos mecánicos (HDD) limita los workers concurrentes para evitar saltos de cabezal; en SSD/NVMe mantiene el paralelismo máximo.
- **Progreso en tiempo real:** barra multi-archivo con ETA, desde el segundo cero, con tratamiento correcto de nombres UTF-8.

### Gestión adaptativa de espacio y lotes

- **Compresión individual y concurrente por defecto:** cada archivo o carpeta genera su propio comprimido en paralelo, salvo que se use `-C` (combinar).
- **Compresión multi-formato concurrente (`-F`):** `crush -c -F gz,xz,zst archivo.txt` en una sola cola global de workers.
- **Modo adaptativo (`-f auto`):** texto/código → `xz`/`7z`, multimedia → `zstd`, carpetas → `7z`/`tar.zst`, datos incompresibles → `lz4`.
- **Detección previa de entropía de Shannon:** si los datos ya están comprimidos o cifrados ($H \ge 7.90$ b/B), usa `lz4` en `-f auto` o advierte si se fuerza un formato pesado.
- **Descompresión adaptativa por tandas (capacity-aware waves):** si el espacio libre es menor que el total requerido, procesa por tandas lo que cabe y re-evalúa el espacio tras cada tanda. Distingue "disco lleno" de "no se pudo medir el espacio".
- **Escaneo interactivo de directorios:** `crush -c` y `crush -d [directorio]` detectan los elementos procesables, muestran sus tamaños y piden confirmación.
- **Compresión incremental (`--newer` / `--newer-than`):** solo archivos modificados después de una fecha, duración relativa o archivo de referencia.
- **Reanudación de lotes (`--resume`):** omite lo ya completado e íntegro y rehace lo truncado, sin archivos de estado.

### Inspección y herramientas avanzadas

- **Analizador de compresibilidad (`--analyze`):** entropía + micro-sonda DEFLATE en memoria para estimar el ahorro y recomendar formato sin escribir nada en disco.
- **Listado tabular (`-l`):** permisos, tamaños, fecha y resumen con ratio de compresión.
- **Vista en árbol (`-tree`), búsqueda (`-find`) y comparación (`-diff`)** dentro de comprimidos, sin extraer a disco.
- **Transcodificación directa (`-convert` / `-recompress`):** `descompresor | compresor` por streaming; el original solo se elimina después de verificar el destino.
- **Agregar/actualizar in-place (`-a` / `-u`):** en `zip`, `7z`, `rar`, `tar` y recompresión atómica para `tar.*`.
- **Checksums SHA-256 (`-hash` / `-verify`).**
- **Sanitización (`-clean`):** excluye `.DS_Store`, `Thumbs.db`, `.git`, `node_modules`, `__pycache__`, `.venv`, etc.
- **Modo observador (`-watch`):** `inotify` + `epoll` nativos en Linux (fallback por sondeo en otras plataformas).
- **Notificaciones (`-notify`):** `notify-send` y campana de terminal al terminar.
- **Benchmark integrado (`--bench`).**

---

## Requisitos

- **Go 1.27+** para compilar (solo biblioteca estándar; `go.mod` sin dependencias).
- **Linux** como plataforma principal (Debian/Ubuntu y Fedora/RHEL como objetivos). Funciones como `splice(2)`, `inotify` y la detección de hardware tienen fallback en otras plataformas.
- **GNU tar** y las herramientas de compresión de cada formato (se instalan con `crush --install-deps`).

---

## Instalación

### Compilación del binario

```bash
git clone https://github.com/hagom/crush.git
cd crush

make build                 # go build -o crush .
sudo ./crush --install     # copia el binario a /usr/local/bin/crush
# alternativa: sudo make install
```

### Herramientas de compresión del sistema

`crush` detecta el gestor de paquetes e instala las herramientas multihilo que falten:

```bash
sudo crush --install-deps
```

> [!NOTE]
> Gestores soportados: `apt-get` (Debian/Ubuntu), `dnf` / `yum` (Fedora/RHEL/CentOS), `zypper` (openSUSE), `pacman` (Arch), `emerge` (Gentoo) y `apk` (Alpine).

### Autocompletado de shell

```bash
sudo crush --completion        # auto-detecta bash, zsh o fish
sudo crush --completion zsh    # shell específica
```

### Desinstalación

```bash
sudo crush --uninstall
```

---

## Formatos soportados

| Formato | Tipo | Compresor multihilo | Fallback serial | Ratio | Perfil de velocidad |
| :--- | :---: | :--- | :--- | :---: | :--- |
| `tar.zst` / `tzst` | Tar-Pipe | `zstd -T0` (`--ultra -22`) | `zstd` | ⭐⭐⭐⭐ | ⚡⚡⚡⚡⚡ Ultrarrápida |
| `7z` | Contenedor | `7zz` / `7z` (`-mmt=on`) | — | ⭐⭐⭐⭐⭐ | ⚡⚡⚡ Equilibrada |
| `tar.xz` / `txz` | Tar-Pipe | `xz -T0` (`-9`) | `xz` | ⭐⭐⭐⭐⭐ | ⚡⚡ Lenta (máximo ratio) |
| `tar.gz` / `tgz` | Tar-Pipe | `pigz` (`-p N`) | `gzip` | ⭐⭐⭐ | ⚡⚡⚡⚡ Rápida |
| `tar.bz2` / `tbz2` | Tar-Pipe | `lbzip2` / `pbzip2` | `bzip2` | ⭐⭐⭐⭐ | ⚡⚡⚡ Equilibrada |
| `tar.bz3` | Tar-Pipe | `bzip3 -j N` (`-b 64`) | — | ⭐⭐⭐⭐⭐ | ⚡⚡ Media |
| `tar.lz` / `tlz` | Tar-Pipe | `plzip` | `lzip` | ⭐⭐⭐⭐ | ⚡⚡ Media |
| `tar.lrz` | Tar-Pipe | `lrzip -p N` | — | ⭐⭐⭐⭐⭐ | ⚡ Lenta (uso intensivo de RAM) |
| `tar.lz4` | Tar-Pipe | `lz4` (`-9` LZ4HC) | — | ⭐⭐ | ⚡⚡⚡⚡⚡ Tiempo real |
| `tar.br` | Tar-Pipe | `brotli` | — | ⭐⭐⭐⭐ | ⚡⚡ Media |
| `zip` | Contenedor | `7z` / `zip` | — | ⭐⭐⭐ | ⚡⚡⚡ Rápida |
| `rar` | Contenedor | `rar` (`-mtN`) | — | ⭐⭐⭐⭐ | ⚡⚡⚡ Equilibrada |
| `tar` | Tar | `tar` | — | — | 💾 Limitado por I/O (sin compresión) |

> [!NOTE]
> La división en partes (`-s`) solo aplica a formatos de flujo (`gz`, `xz`, `bz2`, `bz3`, `zst`, `lz`, `lz4`, `br` y `tar.*`). Las contraseñas (`-p`) solo aplican a contenedores (`7z`, `zip`, `rar`).

---

## Guía rápida de uso

### Compresión

```bash
# Compresión interactiva de la carpeta actual (muestra elementos y pide confirmación)
crush -c

# Cada archivo o carpeta genera su propio comprimido, en paralelo
crush -c -f zst video.mp4 documento.pdf
crush -c -f 7z carpeta1/ carpeta2/

# Selección automática del formato según el contenido
crush -c -f auto codigo.go video.mp4 fotos/

# Varios formatos a la vez
crush -c -F gz,xz,zst archivo.txt

# Combinar varios elementos en un único comprimido
crush -c -C -f 7z -o /backup/ archivo1.bin archivo2.bin

# Excluir .git, node_modules, etc.
crush -c -clean -f tar.xz mi_proyecto/

# Cifrado con contraseña (7z, zip, rar); sin valor la pide de forma oculta
crush -c -f 7z -p secreto confidencial.pdf
crush -c -f 7z -p confidencial.pdf

# Dividir en partes de 50 MB
crush -c -f zst -s 50 iso_gigante.raw

# Comprimir y verificar; los originales se borran solo si la verificación pasa
crush -c -f gz -t documento.txt
```

### Compresión incremental

```bash
crush -c -f gz --newer 24h proyecto/                       # duración relativa (30m, 24h, 7d)
crush -c -f tar.zst --newer 2026-01-01 proyecto/           # fecha absoluta (también "YYYY-MM-DD HH:MM:SS" o RFC3339)
crush -c -f tar.gz --newer-than backup.tar.gz src/         # más recientes que un archivo de referencia
```

> [!IMPORTANT]
> En modo incremental los archivos originales **siempre se conservan**, aunque no se indique `-k`. Si ningún archivo califica, `crush` informa y termina con código 0 sin crear archivos vacíos.

### Descompresión

```bash
crush -d                                   # interactiva en el directorio actual (recursiva)
crush -d ~/Descargas                       # interactiva en una carpeta concreta
crush -d juego.part01.rar                  # archivos concretos, en paralelo
crush -d *.tar.gz *.7z
crush -d -o /opt/software/ paquete.tar.xz  # directorio destino
crush -d -filter "*.log" registros.tar.gz  # extracción selectiva por patrón glob
crush -d -t archivo.7z                     # verificar integridad antes de extraer
crush -d -force archivo.7z                 # sobrescribir archivos existentes
```

> [!NOTE]
> Sin `-force`, la extracción **no sobrescribe** archivos existentes en ningún formato (ver [Seguridad](#extracción-confinada-anti-zip-slip)).

### Reanudación de lotes interrumpidos

```bash
crush -c -f xz --resume datos/*     # omite comprimidos completos e íntegros; rehace los truncados
crush -d --resume *.7z              # omite archivos ya extraídos por completo; completa los parciales
```

### Agregar a comprimidos existentes

```bash
crush -a comprimido.zip archivo.txt carpeta/   # zip, 7z, rar y tar in-place
crush -a respaldo.tar.gz nuevo.log             # tar.* con recompresión atómica
```

### Modo observador

```bash
crush -watch /backup/inbox -c -f zst        # comprimir archivos entrantes
crush -watch /descargas -d -o /extraidos/   # descomprimir archivos entrantes
```

### Inspección, transcodificación y pipes

```bash
crush -l paquete.7z                          # listado tabular
crush -tree backup.tar.gz                    # vista en árbol
crush -find "*.sql" backups/*.7z             # búsqueda dentro de comprimidos
crush -diff release_v1.zip release_v2.zip    # comparar dos comprimidos
crush -t *.tar.gz                            # verificar integridad
crush -verify paquete.tar.xz                 # integridad + checksum .sha256
crush -convert -f zst archivo.tar.gz         # transcodificar tar.gz → tar.zst

crush -r dump.sql.gz | mysql -u root base_datos          # descomprimir a stdout (streaming)
cat archivo.txt | crush -c -f gz > archivo.txt.gz         # comprimir desde stdin
cat archivo.txt.gz | crush -d -f gz > archivo.txt         # descomprimir desde stdin

crush --analyze                              # estimar compresibilidad del directorio actual
crush --analyze -clean -v /datos/proyecto
crush --bench                                # benchmark de formatos (10 MB por omisión)
crush --bench-size 50 archivo.iso
```

---

## Referencia de comandos

La ayuda completa está en `crush -h`.

### Modos de operación

| Modo | Descripción |
| :--- | :--- |
| `-c [archivos...]` | Comprimir (interactivo si no se pasan argumentos) |
| `-d [directorio\|archivos...]` | Descomprimir (interactivo si no se pasan argumentos) |
| `-a`, `-u ARCHIVO elementos...` | Agregar o actualizar elementos en un comprimido existente |
| `-convert`, `-recompress -f FORMATO archivos...` | Transcodificar a otro formato |
| `-l archivos...` | Listar contenido |
| `-t archivos...` | Verificar integridad |
| `-verify archivos...` | Verificar integridad y checksum `.sha256` si existe |
| `-r archivos...` | Emitir el contenido descomprimido a stdout |
| `-tree ARCHIVO` | Vista en árbol del contenido |
| `-find PATRÓN archivos...` | Buscar archivos por patrón dentro de comprimidos |
| `-diff ARCHIVO1 ARCHIVO2` | Comparar el contenido de dos comprimidos |
| `-watch DIR -c\|-d` | Observar un directorio y procesar archivos entrantes |
| `--analyze [dir...]` | Analizar compresibilidad y estimar ahorro |
| `--bench [archivo]` | Benchmark de velocidad y ratio por formato |
| `--install` / `--install-deps` / `--uninstall` | Instalar binario / dependencias / desinstalar |
| `--completion [bash\|zsh\|fish]` | Instalar autocompletado |
| `--version`, `-h` | Versión y ayuda |

### Modificadores

| Flag | Descripción |
| :--- | :--- |
| `-f FORMATO` | Formato destino (`gz`, `xz`, `bz2`, `bz3`, `zst`, `lz`, `lrz`, `zip`, `7z`, `tar`, `rar`, `lz4`, `br`, `auto`) |
| `-F`, `--formats LISTA` | Varios formatos separados por coma (ej. `gz,xz,zst`) |
| `-o DIRECTORIO` | Directorio de salida |
| `-k` | Conservar originales (por omisión se eliminan tras procesar) |
| `-C` | Combinar varias entradas en un solo comprimido |
| `-s N` | Dividir en partes de N MB (formatos de flujo) |
| `-p`, `-password [PASS]` | Contraseña (7z, zip, rar); sin valor se pide de forma oculta |
| `-hash` | Generar checksum SHA-256 (`.sha256`) |
| `-exclude PATRÓN` | Patrón de exclusión (repetible) |
| `-clean` | Excluir temporales, VCS y dependencias |
| `-filter PATRÓN` | Extracción selectiva por patrón glob |
| `-i ARCHIVO` | Leer la lista de archivos desde un fichero |
| `-opts "opciones"` | Opciones adicionales para el compresor nativo |
| `-sparse`, `-S` / `-no-sparse` | Activar (por omisión) / desactivar archivos dispersos en tar |
| `--newer TIEMPO\|FECHA` | Solo archivos modificados después de una duración o fecha |
| `--newer-than ARCHIVO` | Solo archivos modificados después del archivo de referencia |
| `--resume` | Reanudar lotes interrumpidos (`-c` o `-d`) |
| `-force` | Sobrescribir archivos existentes |
| `-quick` | Verificación rápida |
| `-n` | Simulacro (dry-run) |
| `-v` | Verbose (muestra los comandos ejecutados, con contraseñas enmascaradas) |
| `-notify` | Notificación de escritorio y campana al terminar |
| `--bench-size N` | Tamaño en MB del dataset de benchmark (por omisión: 10) |

### Combinaciones de modos

- Permitidas: `-c -t` / `-c -verify` (comprimir y verificar) y `-d -t` / `-d -verify` (verificar antes de extraer).
- `--resume` requiere `-c` o `-d`. `--newer` / `--newer-than` requieren `-c`, `-a` o `-watch`.
- El resto de combinaciones entre modos (por ejemplo `-c -d`, `-l -r`, `-tree -diff`, `--analyze -c`) se rechazan con un error explícito.

---

## Seguridad

### Extracción confinada (anti Zip-Slip)

- **Rutas de miembros saneadas:** las entradas con `../`, rutas absolutas (`/abs`) o `..\` se descartan antes de calcular salidas o comprobar reanudaciones; nombres legítimos como `foo..bar.txt` se aceptan.
- **Escrituras confinadas con `os.OpenRoot`:** la descompresión de formatos de flujo (`gz`, `xz`, `zst`, `lrz`, etc.), la renombración automática a `.iso` y las comprobaciones de `--resume` se realizan dentro de un `*os.Root` del directorio destino, de modo que un symlink no puede redirigir la escritura fuera de él.
- **Sin seguir symlinks:** las comprobaciones de existencia usan `Lstat`; escribir sobre un symlink existente se rechaza.
- **Sin sobrescritura por omisión:** salvo con `-force` o `--resume`, los archivos de salida se crean en modo exclusivo y los extractores externos se invocan en modo "no sobrescribir":

| Herramienta | Sin `-force` | Con `-force` / `--resume` |
| :--- | :--- | :--- |
| `tar` | `--keep-old-files` | sobrescribe |
| `7z` | `-aos` | `-aoa` |
| `unzip` | `-n` | `-o` |
| `rar` | `-o-` | `-o+` |

### Argumentos seguros para herramientas externas

- Todas las invocaciones a `tar`, `7z`, `unzip` y `rar` separan las opciones de los nombres con `--`, y los nombres que empiezan por `-` se prefijan con `./`, evitando que un archivo llamado `-algo` se interprete como opción.
- Los comandos externos pasan por la abstracción `execCommand`, lo que permite probarlos con mocks.

### Contraseñas

- Registro local en `~/.config/crush/passwords.txt` (permisos `0600`), en texto plano ordenado por frecuencia de uso. Al descomprimir se prueban la última clave válida de la sesión y las registradas; si ninguna sirve, se pide una sola vez y se reutiliza en el resto del lote.
- En modo verbose (`-v`) las contraseñas se enmascaran (`-p***`, `***`) en los comandos `7z`, `zip` y `rar` mostrados por pantalla y escritos en el log.

> [!CAUTION]
> El registro de contraseñas **no está cifrado**: protégelo como cualquier archivo con credenciales.

### Instancia única y archivos de bloqueo

Solo una instancia puede comprimir, descomprimir, agregar, verificar, medir (`--bench`) u observar (`-watch`) a la vez; las demás esperan en cola. Quedan exentos `-l`, `-r` y los modos por pipe. El bloqueo usa `flock(2)` y se libera solo al terminar o morir el proceso. Ruta del archivo de bloqueo, por orden de prioridad:

1. `$CRUSH_LOCK_FILE`
2. `$XDG_RUNTIME_DIR/crush.lock`
3. `<directorio de caché del usuario>/crush/crush.lock` (creado con permisos `0700`)
4. `<tmp>/crush-<uid>.lock`

### Registros (logs)

Cada ejecución escribe un log `crush_<fecha>_<pid>.log` con permisos `0600` en un directorio privado (`0700`):

1. `/var/log/crush` si se ejecuta como root
2. `$XDG_STATE_HOME/crush`
3. `<directorio de caché del usuario>/crush/logs`
4. `<tmp>/crush-log-<uid>`

---

## Arquitectura

`crush` sigue la filosofía **Ponytail** (cero dependencias externas, biblioteca estándar y primitivas del kernel) y los principios **SOLID/DRY** descritos en [AGENTS.md](AGENTS.md).

```text
crush/
├── main.go            # CLI: flags, validación de modos, dispatch, ayuda y autocompletado
├── format.go          # Format/FormatInfo, detección de formato, sufijos tar, entropía
├── compress.go        # DoCompress, tar-pipe, contenedores, -F, -C, -s, -hash, --newer, --resume, -a
├── decompress.go      # DoDecompress, tandas por capacidad, extracción confinada (os.OpenRoot)
├── convert.go         # Transcodificación por streaming (-convert, -recompress)
├── passwords.go       # PasswordManager y registro de contraseñas
├── analyze.go         # Analizador de compresibilidad (--analyze)
├── list.go            # Listado tabular (-l)
├── archive_list.go    # Lectura de miembros de comprimidos sin extraer
├── tree.go            # Vista en árbol (-tree)
├── find.go            # Búsqueda en comprimidos (-find)
├── diff.go            # Comparación de comprimidos (-diff)
├── test_cmd.go        # Verificación de integridad y SHA-256 (-t, -verify)
├── bench.go           # Benchmark de formatos (--bench)
├── notify.go          # Notificaciones (-notify)
├── watcher.go         # Modo observador (-watch)
├── watcher_linux.go   # Backend inotify + epoll
├── watcher_other.go   # Backend por sondeo
├── util.go            # NCPU, memoria, disco, pipelines, progreso, logs, execCommand
├── util_linux.go      # splice(2) y F_SETPIPE_SZ
├── util_other.go      # Fallbacks de pipe
├── hardware_linux.go  # Detección HDD vs SSD/NVMe
├── hardware_other.go  # Fallback de hardware
├── lock.go            # Rutas y mensajes del bloqueo
├── lock_unix.go       # Bloqueo flock(2)
├── lock_other.go      # Fallback sin bloqueo
├── pkgmgr.go          # Gestores de paquetes, instalación de herramientas, lectura a stdout (-r)
└── *_test.go          # Tests unitarios e integración (mocks de exec en mock_test.go)
```

---

## Desarrollo y pruebas

El proyecto se desarrolla con **TDD** (test rojo → implementación mínima → refactor). Antes de cada commit:

```bash
go vet ./...
go test -race ./...
```

Estado actual de la suite: **315 tests** de primer nivel (685 contando subtests) con el detector de carreras. Tres tests se omiten (`SKIP`) según el entorno: `TestCheckCompressTools` y `TestCompressMissingToolNoOutput` (solo corren si `brotli` **no** está instalado, para probar la ausencia de herramienta) y `TestLockHolderProcess` (proceso auxiliar que solo se ejecuta invocado por otro test).

La integración continua ([ci.yml](.github/workflows/ci.yml)) usa Go `1.27.x` en Ubuntu, instala las herramientas de compresión y ejecuta `go vet`, `go test -v -race`, la compilación y un smoke test de la CLI.

---

## Documentación relacionada

- [AGENTS.md](AGENTS.md) — convenciones de código, principios de diseño y estado detallado del proyecto.
- [TODO.md](TODO.md) — propuestas implementadas, descartadas y pospuestas.
- [COMPRESOR.md](COMPRESOR.md) — resumen del historial de desarrollo.
- [docs/superpowers/](docs/superpowers/) — especificaciones de diseño y planes de implementación.

---

## Licencia

Distribuido bajo la **GNU General Public License v3.0 (GPLv3)**. Consulta [LICENSE](LICENSE) para más detalles.
