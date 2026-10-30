<div align="center">

# ⚡ CRUSH

**Herramienta multi-formato de compresión y descompresión vía pipes UNIX de ultra alto rendimiento con auto-paralelismo y gestión adaptativa de recursos.**

[![CI](https://github.com/hagom/crush/actions/workflows/ci.yml/badge.svg)](https://github.com/hagom/crush/actions)
[![Go Version](https://img.shields.io/badge/Go-1.27+-00ADD8?style=flat&logo=go)](https://golang.org)
[![Tests](https://img.shields.io/badge/tests-523%20passing%20%7C%20race%20detector-brightgreen)](https://github.com/hagom/crush)
[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](LICENSE)
[![Formats](https://img.shields.io/badge/formats-13%20supported-blueviolet)](https://github.com/hagom/crush)
[![Zero Dependencies](https://img.shields.io/badge/dependencies-0%20(pure%20Go%20stdlib)-success)](go.mod)

<br/>

[Características](#-características) • [Instalación](#-instalación) • [Formatos](#-formatos-soportados) • [Guía Rápida](#-guía-rápida-de-uso) • [Comandos](#-referencia-de-comandos) • [Arquitectura](#-arquitectura)

</div>

---

## 🚀 Características Destacadas

> [!TIP]
> `crush` detecta automáticamente la cantidad de núcleos (`NCPU()`) y la memoria RAM libre del sistema para saturar el 100% del procesador sin requerir flags manuales de hilos (`-j`), y sin riesgo de desbordar la memoria (OOM) o el disco.

### 📦 Núcleo de Compresión & Rendimiento
- **13 formatos soportados:** `gz`, `xz`, `bz2`, `bz3`, `zst`, `lz`, `lrz`, `zip`, `7z`, `tar`, `rar`, `lz4`, `br`.
- **Auto-paralelismo y compresión máxima:** Aplica los perfiles de mayor ratio multihilo disponibles (`zstd --ultra -22`, `7z -mx=9 -md=...` adaptativo a RAM, `bzip3 -b 64`, `lz4 -9` LZ4HC, `pigz -p N`, `lbzip2 -n N`).
- **Distribución dinámica proporcional de hilos (`DynamicThreadPool`):** Reparto ponderado de núcleos según el peso en bytes de cada archivo (algoritmo Hamilton-Hare). Elimina la latencia de cola (*tail latency*) con +84% de aceleración en colas heterogéneas.
- **Planificación LPT inteligente:** Ordenamiento óptimo (*Longest Processing Time first*) tanto en compresión como en descompresión.
- **Streaming directo y Zero-Copy con Linux `splice(2)`:** Transferencia directa entre descriptores de kernel sin copias a memoria de usuario en Go y buffers de pipe ampliados a 1 MiB (`F_SETPIPE_SZ`).
- **Eliminación de latencia de inicio:** Conteo en el flujo de entrada de tar para progreso instantáneo desde el segundo cero y diccionario LZMA2 adaptativo para 7-Zip según la RAM disponible.

### 🧠 Gestión Adaptativa de Espacio & Lotes
- **Descompresión adaptativa por tandas según espacio en disco (Capacity-Aware Waves):** Si el espacio libre en disco es menor al total requerido para descomprimir un lote, `crush` no aborta la operación: programa automáticamente tandas inteligentes con los archivos que sí caben; y al eliminar los archivos comprimidos originales (`!keepOrig`) o liberar espacio, re-evalúa el almacenamiento libre en tiempo real para procesar el resto de la cola.
- **Escaneo interactivo de directorios:** `crush -d [directorio]` y `crush -c` detectan automáticamente todos los elementos procesables (en la ruta actual o en la carpeta indicada como argumento), muestran sus tamaños individuales y solicitan confirmación interactiva.
- **Compresión individual y concurrente por defecto:** Cada archivo o carpeta genera su propio comprimido independiente en paralelo, salvo que se use `-C` (`--combine`).
- **Compresión heterogénea concurrente (`-F` / `--formats`):** Ejecuta simultáneamente múltiples formatos en una sola pasada (`crush -c -F gz,xz,zst archivo.txt`) en una sola cola global de workers sin núcleos ociosos.
- **Detección previa de entropía de Shannon:** Analiza en <0.05 ms la redundancia del archivo. Si detecta datos ya comprimidos o cifrados ($H \ge 7.90$ b/B), asigna automáticamente formato ultrarrápido (`lz4`) en `-f auto` o emite advertencia preventiva si se fuerza un formato pesado, evitando desperdicio masivo de CPU y tiempo.
- **Modo adaptativo inteligente (`-f auto`):** Selección heurística del mejor formato según el tipo de archivo (código/texto $\to$ `xz`/`7z`, multimedia $\to$ `zstd`, carpetas $\to$ `7z`/`tar.zst`, incompresibles $\to$ `lz4`).

### 🛠️ Inspección, Seguridad & Herramientas Avanzadas
- **Registro inteligente y auto-reutilización de contraseñas:** Reutiliza automáticamente contraseñas válidas de la sesión y del registro local cifrado (`~/.config/crush/passwords.txt`), pidiendo contraseña una sola vez y propagándola al resto del lote.
- **Cola diferida de archivos protegidos:** Al descomprimir lotes heterogéneos, procesa de inmediato los archivos sin contraseña o conocidos en paralelo (Fase 1/2) y difiere los protegidos para atención ordenada al final (Fase 2/2).
- **Transcodificación directa (`-convert` / `-recompress`):** Convierte entre formatos por streaming directo (`descompresor | compresor`) sin archivos intermedios en disco.
- **Vista jerárquica en árbol (`-tree`):** Visualiza la estructura interna de cualquier comprimido (`tar`, `zip`, `7z`, `rar`) sin extraer nada a disco.
- **Búsqueda profunda (`-find`):** Búsqueda por patrón glob dentro de archivos comprimidos reportando ruta interna y tamaño.
- **Comparador y diff (`-diff`):** Contrasta dos archivos comprimidos elemento a elemento reportando adiciones, eliminaciones y diferencias de tamaño.
- **Modo sanitización (`-clean`):** Excluye automáticamente artefactos del SO (`.DS_Store`, `Thumbs.db`), control de versiones (`.git`) y dependencias (`node_modules`, `__pycache__`, `.venv`).
- **Modo observador (`-watch`):** Monitoreo continuo de carpetas con `inotify` nativo en Linux para comprimir o descomprimir archivos entrantes.
- **Instancia única con bloqueo (`flock(2)`):** Impide colisiones entre múltiples instancias concurrentes con encolado automático.

---

## 📥 Instalación

### 1. Compilación e instalación del binario

```bash
# Clonar el repositorio
git clone https://github.com/hagom/crush.git
cd crush

# Compilar
make build

# Instalar binario global en /usr/local/bin
sudo ./crush --install
```

### 2. Instalar herramientas de compresión del sistema

`crush` incluye un detector que identifica tu distribución e instala automáticamente las herramientas multihilo optimizadas:

```bash
sudo crush --install-deps
```

> [!NOTE]
> Soporta gestores de paquetes nativos: `apt` (Debian/Ubuntu), `dnf`/`yum` (Fedora/RHEL/CentOS), `pacman` (Arch), `zypper` (openSUSE), `apk` (Alpine) y `brew` (macOS).

### 3. Activar autocompletado en Shell

```bash
sudo crush --completion        # Auto-detecta bash, zsh o fish
```

---

## 📊 Formatos Soportados

| Formato | Tipo | Compresor Multihilo | Fallback Serial | Ratio | Perfil de Velocidad |
| :--- | :---: | :--- | :--- | :---: | :--- |
| `tar.zst` / `tzst` | Tar-Pipe | `zstd -T0` (`--ultra -22`) | `zstd` | ⭐⭐⭐⭐ | ⚡⚡⚡⚡⚡ Ultrarrápida |
| `7z` | Contenedor | `7zz` / `7z` (`-mmt=on`) | — | ⭐⭐⭐⭐⭐ | ⚡⚡⚡ Equilibrada |
| `tar.xz` / `txz` | Tar-Pipe | `xz -T0` (`-9`) | `xz` | ⭐⭐⭐⭐⭐ | ⚡⚡ Lenta (Máximo ratio) |
| `tar.gz` / `tgz` | Tar-Pipe | `pigz` (`-p N`) | `gzip` | ⭐⭐⭐ | ⚡⚡⚡⚡ Rápida |
| `tar.bz2` / `tbz2` | Tar-Pipe | `lbzip2` / `pbzip2` | `bzip2` | ⭐⭐⭐⭐ | ⚡⚡⚡ Equilibrada |
| `tar.bz3` | Tar-Pipe | `bzip3 -j N` (`-b 64`) | — | ⭐⭐⭐⭐⭐ | ⚡⚡ Media |
| `tar.lz` / `tlz` | Tar-Pipe | `plzip` | `lzip` | ⭐⭐⭐⭐ | ⚡⚡ Media |
| `tar.lrz` | Tar-Pipe | `lrzip -p N` | — | ⭐⭐⭐⭐⭐ | ⚡ Lenta (Uso intensivo RAM) |
| `tar.lz4` | Tar-Pipe | `lz4` (`-9` LZ4HC) | — | ⭐⭐ | ⚡⚡⚡⚡⚡ Tiempo real |
| `tar.br` | Tar-Pipe | `brotli` | — | ⭐⭐⭐⭐ | ⚡⚡ Media |
| `zip` | Contenedor | `7z` / `zip` | — | ⭐⭐⭐ | ⚡⚡⚡ Rápida |
| `rar` | Contenedor | `rar` (`-mtN`) | — | ⭐⭐⭐⭐ | ⚡⚡⚡ Equilibrada |
| `tar` | Tar | `tar` | — | — | 💾 I/O Bound (Sin compresión) |

---

## 💡 Guía Rápida de Uso

### 🗜️ Compresión

```bash
# Compresión interactiva de la carpeta actual (pregunta y muestra elementos)
crush -c

# Comprimir archivos individuales o carpetas en paralelo (cada uno genera su comprimido)
crush -c -f zst video.mp4 documento.pdf
crush -c -f 7z carpeta1/ carpeta2/

# Selección heurística del mejor formato (-f auto)
crush -c -f auto codigo.go video.mp4 fotos/

# Compresión concurrente multi-formato simultánea (-F)
crush -c -F gz,xz,zst archivo.txt

# Combinar múltiples elementos en un único archivo comprimido (-C)
crush -c -C -f 7z -o /backup/ archivo1.bin archivo2.bin

# Sanitización automática pre-compresión (excluye .git, node_modules, etc.)
crush -c -clean -f tar.xz mi_proyecto/

# Cifrado con contraseña en contenedores 7z/zip/rar
crush -c -f 7z -p secreto confidencial.pdf

# División en volúmenes de tamaño fijo (-s en MB)
crush -c -f zst -s 50 iso_gigante.raw
```

### 📂 Descompresión

```bash
# Descompresión interactiva en el directorio actual
crush -d

# Descompresión interactiva de una carpeta específica (con gestión adaptativa de espacio)
crush -d /baul/Descargas

# Descomprimir archivos específicos en paralelo
crush -d juego.part01.rar
crush -d *.tar.gz *.7z

# Descomprimir hacia un directorio destino (-o)
crush -d -o /opt/software/ paquete.tar.xz

# Extracción selectiva con filtro glob (-filter)
crush -d -filter "*.log" registros.tar.gz
```

### 🔍 Inspección, Transcodificación y Pipes

```bash
# Vista jerárquica en árbol sin extraer
crush -tree backup.tar.gz

# Búsqueda profunda dentro de comprimidos
crush -find "*.sql" backups/*.7z

# Comparar diferencias entre dos comprimidos
crush -diff release_v1.zip release_v2.zip

# Transcodificación directa entre formatos por streaming
crush -convert -f zst archivo.tar.gz

# Inspección de contenido y verificación de integridad
crush -l paquete.7z
crush -verify paquete.tar.xz

# Streaming a stdout para tuberías UNIX
crush -r dump.sql.gz | mysql -u root base_datos
```

---

## 📋 Referencia de Comandos

```text
Modos Principales:
  crush -c [opciones] [archivos...]       Comprimir elementos
  crush -d [opciones] [directorio|archivos] Descomprimir elementos
  crush -convert -f FORMATO archivos...    Transcodificar entre formatos
  crush -a ARCHIVO elementos...           Agregar archivos in-place
  crush -u ARCHIVO elementos...           Actualizar archivos in-place
  crush -tree ARCHIVO                     Mostrar estructura en árbol
  crush -find PATRÓN archivos...          Buscar ficheros en comprimidos
  crush -diff ARCHIVO1 ARCHIVO2           Comparar diferencias de contenido
  crush -watch DIR -c|-d [opciones]       Modo observador inotify continuo
  crush -l ARCHIVO...                     Listar contenido
  crush -t ARCHIVO...                     Verificar integridad
  crush -verify ARCHIVO...                Verificar integridad + hash SHA-256
  crush -r ARCHIVO...                     Emitir flujo descomprimido a stdout
  crush --bench [archivo]                 Benchmark comparativo de formatos
  crush --install | --install-deps        Instalador de binario y dependencias
  crush --completion [bash|zsh|fish]      Instalador de autocompletado

Modificadores Destacados:
  -f FORMATO        Formato destino (gz, xz, bz2, bz3, zst, lz, lrz, zip, 7z, tar, rar, lz4, br, auto)
  -F FORMATOS       Lista de formatos concurrentes separados por coma (ej: gz,xz,zst)
  -o DIRECTORIO     Directorio destino de salida
  -k                Conservar archivos originales (por omisión se eliminan tras procesar)
  -C                Combinar múltiples entradas en un solo comprimido
  -clean            Sanitizar y excluir temporales, VCS y dependencias (.git, node_modules)
  -p, -password     Cifrar/descifrar con contraseña
  -filter PATRÓN    Filtrar extracción por glob (*.txt)
  -s MB             Dividir en volúmenes de tamaño fijo
  -hash             Generar/verificar checksum SHA-256
  -notify           Notificación de escritorio y campana acústica al concluir
  -force            Sobrescribir sin preguntar
  -n                Simulacro (dry-run)
```

---

## 🏛️ Arquitectura

`crush` se rige por la filosofía **Ponytail** (cero dependencias externas en `go.mod`, máxima eficiencia con primitivas del kernel y biblioteca estándar pura) y diseño **SOLID**:

```text
crush/
├── main.go          # CLI dispatch, flags, completion, escaneo interactivo de carpetas
├── format.go        # Detección polimórfica de formatos, extensiones y complejidades
├── compress.go      # Pipeline tar-pipe streaming, DynamicThreadPool, -hash, -clean, -p
├── decompress.go    # Descompresión adaptativa por capacidad (waves), colas diferidas
├── passwords.go     # PasswordManager, historial seguro ~/.config/crush/passwords.txt
├── archive_list.go  # Lector universal de miembros de archivo sin descompresión en disco
├── tree.go          # Renderizador de árbol visual jerárquico (-tree)
├── find.go          # Motor de búsqueda concurrente de patrones (-find)
├── diff.go          # Comparador estructural elemento a elemento (-diff)
├── convert.go       # Transcodificador streaming zero-copy (-convert, -recompress)
├── watcher.go       # Observador de directorios con backend nativo inotify en Linux
├── test_cmd.go      # Motor de verificación de integridad y hashes SHA-256
├── util.go          # Métodos atómicos, cálculo LPT, GetDiskAvail, memoria y buffers
├── util_linux.go    # Zero-copy splice(2) y optimización F_SETPIPE_SZ (1 MiB)
├── lock_unix.go     # Instancia única con exclusión mutua flock(2)
├── pkgmgr.go        # Gestor desatendido multiplataforma de dependencias de sistema
└── *_test.go        # Suite con 518+ tests unitarios y de integración (-race)
```

### Ejecución de Pruebas

```bash
# Verificación de estilo y análisis estático
go vet ./...

# Suite completa con detector de carreras de Go
go test -v -race ./...
```

---

## 📄 Licencia

Distribuido bajo la Licencia **GNU General Public License v3.0 (GPLv3)**. Consulta [LICENSE](LICENSE) para más detalles.
