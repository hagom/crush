#!/usr/bin/env bash
set -euo pipefail

# ==============================================================================
# compresor — Compresor / Descompresor universal multiproceso (Bash)
# ==============================================================================
#
# Versión:  2.1
#
# Uso:
#   compresor -c <formato> [opciones] <archivos/carpetas...>   Comprimir
#   compresor -d [opciones] <archivos...>                       Descomprimir
#   compresor -t [opciones] <archivos...>                       Verificar
#
# Formatos soportados (todos multiproceso):
#   gz, xz, bz2, bz3, zst, lz, lrz, zip, 7z, tar
#
# Características:
#   - Multiproceso automático en todos los formatos
#   - Instalación batch de dependencias multi-distro
#   - Logging a /var/log/compresor/ con rotación
#   - Modo test (-t), dry-run (--dry-run), exclude (--exclude), split (--split)
#   - Compresión individual (-i) o agrupada (default)
#   - Detección de memoria RAM y espacio en disco
#   - Verificación de integridad en 2 capas (compresión + tar)
#
# Dependencias comunes: pigz, xz, lbzip2/pbzip2, bzip3, zstd, plzip, lrzip,
#                       zip, unzip, p7zip, tar, pv (opcional)
# ==============================================================================

# --- NCPU: Núcleos de CPU disponibles (cgroup-aware) ---
# 1. getconf: número de CPUs online
# 2. cgroup v2: /sys/fs/cgroup/cpu.max
# 3. cgroup v1: /sys/fs/cgroup/cpu/cpu.cfs_{quota,period}_us
# 4. fallback: 1
LOG_DIR="/var/log/compresor"
MAX_LOGS=10
if ! NCPU=$(getconf _NPROCESSORS_ONLN 2>/dev/null) || [[ "$NCPU" -lt 1 ]]; then
    if [[ -r /sys/fs/cgroup/cpu.max ]]; then
        read -r c _ < /sys/fs/cgroup/cpu.max
        [[ "$c" != "max" ]] && NCPU=$c
    fi
fi
if [[ -z "$NCPU" || "$NCPU" -lt 1 ]] && [[ -r /sys/fs/cgroup/cpu/cpu.cfs_quota_us ]]; then
    q=$(</sys/fs/cgroup/cpu/cpu.cfs_quota_us)
    p=$(</sys/fs/cgroup/cpu/cpu.cfs_period_us)
    [[ "$q" -gt 0 ]] && NCPU=$(( (q + p - 1) / p ))
fi
: "${NCPU:=1}"

# --- Colores ---
readonly GREEN='\033[0;32m' RED='\033[0;31m' YELLOW='\033[1;33m'
readonly BLUE='\033[0;34m' BOLD='\033[1m' NC='\033[0m'

# --- _lower: convertir string a minúsculas ---
# $1: string → stdout: en minúsculas
_lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

# --- _pv_pipe: barra de progreso vía pv, o cat si no está disponible ---
# $1: bytes totales para la barra de progreso
# stdin → stdout: data, con barra de progreso en stderr si pv existe
_pv_pipe() { if command -v pv &>/dev/null; then pv -f -s "$1"; else cat; fi; }

# --- _get_format_info: mapping central de extensión → tool, flags, tipo ---
# $1: nombre de archivo
# stdout: tool|pipe_flags|direct_flags|is_tar|test_flag
#   tool        : binario a usar (ej: pigz, xz, lbzip2)
#   pipe_flags  : flags para descompresión a stdout (pipe a tar)
#   direct_flags: flags para descompresión directa a archivo
#   is_tar      : 1 si requiere pipe a tar -xvf / tar -t, 0 si no
#   test_flag   : flags para test de integridad (-t, t, -tf, etc.)
# Retorna 0 si reconoce el formato, 1 si no
# NOTA: Esta función es la ÚNICA fuente de verdad para mapear formato→herramienta.
#       Cualquier nuevo formato debe agregarse aquí y en _ext_for_format.
_get_format_info() {
    local file=$1 ext
    ext=$(_lower "$file")
    case "$ext" in
        *.tar.gz|*.tgz)       echo "pigz|-dc|-dk|1|-t" ;;
        *.tar.xz|*.txz)       echo "xz|-dc -T0|-d -T0 -k|1|-t" ;;
        *.tar.bz2|*.tbz2)     echo "$BZIP2_BIN|-dc|-dk|1|-t" ;;
        *.tar.bz3)            echo "bzip3|-dc -j $NCPU|-d -j $NCPU|1|-t" ;;
        *.tar.zst|*.tzst)     echo "zstd|-dc -T0|-d -T0|1|-t" ;;
        *.tar.lz|*.tlz)       echo "plzip|-dc --threads=$NCPU|-dk --threads=$NCPU|1|-t" ;;
        *.tar.lrz)            echo "lrzip|-d -p $NCPU -o -|-d -k -p $NCPU|1|-t" ;;
        *.lrz)                echo "lrzip|-d -p $NCPU -o -|-d -k -p $NCPU|0|-t" ;;
        *.zst)                echo "zstd|-dc -T0|-d -T0|0|-t" ;;
        *.xz)                 echo "xz|-dc -T0|-d -T0 -k|0|-t" ;;
        *.gz)                 echo "pigz|-dc|-dk|0|-t" ;;
        *.bz2)                echo "$BZIP2_BIN|-dc|-dk|0|-t" ;;
        *.bz3)                echo "bzip3|-dc -j $NCPU|-d -j $NCPU|0|-t" ;;
        *.lz)                 echo "plzip|-dc --threads=$NCPU|-dk --threads=$NCPU|0|-t" ;;
        *.zip)                echo "unzip|-o|-o|0|-t" ;;
        *.7z)                 echo "$SEVENZ_BIN|x|x|0|t" ;;
        *.rar)                echo "$RAR_BIN|x|x|0|t" ;;
        *.tar)                echo "tar|-xf|-xf|0|-tf" ;;
        *)                    return 1 ;;
    esac
}

# --- Variables globales ---
# MISSING_DEPS[]   : dependencias acumuladas para instalar
# EXCLUDE_PATTERNS[]: patrones --exclude para tar
# INPUTS[]         : archivos/ carpetas a procesar
# MODE             : compress | decompress | test
# FORMAT           : formato de compresión (gz, xz, ...)
# CUSTOM_NAME      : nombre personalizado para archivo de salida (-n)
# DELETE_ORIG      : 1 = borrar original al terminar (-r)
# DRY_RUN          : 1 = solo mostrar comandos (--dry-run)
# SPLIT_SIZE       : tamaño para split (--split=100M)
# INDIVIDUAL       : 1 = comprimir cada archivo por separado (-i)
# LOG_FILE         : ruta del archivo de log
# PKG_MANAGER      : apt | dnf | yum | pacman | zypper | apk
# PKG_UPDATE       : comando de actualización de repos
# PKG_INSTALL      : comando de instalación de paquetes
# SEVENZ_BIN       : binario 7z disponible (7zz | 7z | 7za)
# BZIP2_BIN        : binario bzip2 disponible (lbzip2 | pbzip2)
# RAR_BIN          : binario rar disponible (rar | unrar)
# CLEANUP_FILE     : archivo temporal a limpiar en caso de interrupción
MISSING_DEPS=()
EXCLUDE_PATTERNS=()
INPUTS=()
MODE=""
FORMAT=""
CUSTOM_NAME=""
DELETE_ORIG=0
DRY_RUN=0
SPLIT_SIZE=""
INDIVIDUAL=0
LOG_FILE=""
PKG_MANAGER=""
PKG_UPDATE=""
PKG_INSTALL=""
SEVENZ_BIN=""
BZIP2_BIN=""
RAR_BIN=""
CLEANUP_FILE=""

# ==============================================================================
# FUNCIONES AUXILIARES — Dependencias, sistema, formateo
# ==============================================================================

# --- detect_pkg_manager: detecta gestor de paquetes del sistema ---
# Salida: setea PKG_MANAGER, PKG_UPDATE, PKG_INSTALL
#         apt / dnf / yum / pacman / zypper / apk
detect_pkg_manager() {
    if command -v apt &>/dev/null; then
        PKG_MANAGER="apt"
        PKG_UPDATE="apt update -y"
        PKG_INSTALL="apt install -y"
    elif command -v dnf &>/dev/null; then
        PKG_MANAGER="dnf"
        PKG_UPDATE="dnf check-update -y || true"
        PKG_INSTALL="dnf install -y"
    elif command -v yum &>/dev/null; then
        PKG_MANAGER="yum"
        PKG_UPDATE="yum check-update -y || true"
        PKG_INSTALL="yum install -y"
    elif command -v pacman &>/dev/null; then
        PKG_MANAGER="pacman"
        PKG_UPDATE="pacman -Sy"
        PKG_INSTALL="pacman -S --noconfirm"
    elif command -v zypper &>/dev/null; then
        PKG_MANAGER="zypper"
        PKG_UPDATE="zypper refresh"
        PKG_INSTALL="zypper install -y"
    elif command -v apk &>/dev/null; then
        PKG_MANAGER="apk"
        PKG_UPDATE="apk update"
        PKG_INSTALL="apk add"
    fi
}

# --- tool_to_package: mapea nombre de herramienta → paquete según gestor ---
# $1: tool name (xz, 7z, ...)
# stdout: nombre del paquete en el gestor actual
tool_to_package() {
    local tool=$1
    # Default: mismo nombre del tool (funciona en pacman, apk, dnf, yum)
    # Excepciones por distro
    case "${PKG_MANAGER}:${tool}" in
        apt:xz)     echo "xz-utils"   ;;
        apt:7z)     echo "p7zip-full" ;;
        apt:rar)    echo "rar"         ;;
        dnf:7z|yum:7z|zypper:7z|apk:7z) echo "p7zip" ;;
        pacman:7z)  echo "7zip"       ;;
        pacman:rar) echo "rar"        ;;
        *)          echo "$tool"      ;;
    esac
}

# --- check_sudo: verifica si hay acceso sudo (o si ya es root) ---
# Retorna 0 si hay sudo disponible (o es root), 1 si no
check_sudo() {
    if [[ "$(id -u)" -eq 0 ]]; then
        return 0
    fi
    if ! command -v sudo &>/dev/null; then
        printf "${YELLOW}[Sistema] 'sudo' no está instalado. No se pueden instalar dependencias automáticamente.${NC}\n" >&2
        return 1
    fi
    if ! sudo -n true 2>/dev/null; then
        printf "${YELLOW}[Sistema] No se tienen permisos sudo sin contraseña.${NC}\n" >&2
        printf "${YELLOW}         Ejecuta con sudo o configura NOPASSWD en sudoers.${NC}\n" >&2
        return 1
    fi
    return 0
}

# --- ensure_tool: verifica herramienta y acumula en MISSING_DEPS si falta ---
# $1: tool name (para tool_to_package)
# $2: binario a buscar (default: $1)
# $@: nombres alternativos del binario
# Retorna 0 si ya está instalado, 1 si se agregó a MISSING_DEPS
ensure_tool() {
    local tool=$1
    local bin="${2:-$1}"
    shift 2 || true
    local alternatives=("$@")

    if command -v "$bin" &>/dev/null; then
        return 0
    fi
    for alt in "${alternatives[@]}"; do
        if command -v "$alt" &>/dev/null; then
            return 0
        fi
    done

    local pkg
    pkg=$(tool_to_package "$tool")
    for dep in "${MISSING_DEPS[@]}"; do
        [[ "$dep" == "$pkg" ]] && return 0
    done
    MISSING_DEPS+=("$pkg")
    printf "${YELLOW}[Sistema] Dependencia faltante: %s${NC}\n" "$pkg"
}

# --- install_missing_deps: instala todas las dependencias acumuladas ---
# Usa sudo si es necesario. Llama update + install en un solo comando batch.
# Exit 1 si no hay sudo disponible.
install_missing_deps() {
    [[ ${#MISSING_DEPS[@]} -eq 0 ]] && return 0

    printf "${YELLOW}[Sistema] Instalando dependencias: %s${NC}\n" "${MISSING_DEPS[*]}"

    if ! check_sudo; then
        printf "${RED}[Error] No se pueden instalar dependencias sin permisos.${NC}\n" >&2
        printf "${YELLOW}Instala manualmente: %s${NC}\n" "${MISSING_DEPS[*]}" >&2
        exit 1
    fi

    local -a install_cmd update_cmd
    if [[ "$(id -u)" -eq 0 ]]; then
        IFS=' ' read -ra install_cmd <<< "$PKG_INSTALL"
        IFS=' ' read -ra update_cmd <<< "$PKG_UPDATE"
    else
        install_cmd=(sudo)
        IFS=' ' read -ra tmp <<< "$PKG_INSTALL"
        install_cmd+=("${tmp[@]}")
        update_cmd=(sudo bash -c "$PKG_UPDATE")
    fi

    if ! "${install_cmd[@]}" "${MISSING_DEPS[@]}"; then
        printf "${YELLOW}[Sistema] Reintentando tras actualizar repositorios...${NC}\n"
        "${update_cmd[@]}"
        if ! "${install_cmd[@]}" "${MISSING_DEPS[@]}"; then
            printf "${RED}[Error] No se pudieron instalar las dependencias.${NC}\n" >&2
            exit 1
        fi
    fi

    printf "${GREEN}[Sistema] Dependencias instaladas correctamente.${NC}\n"
}

# --- register_compress_tools: acumula dependencias para comprimir según FORMAT ---
# Según FORMAT actual, llama a ensure_tool con los binarios correctos.
register_compress_tools() {
    case "$FORMAT" in
        gz)   ensure_tool pigz ;;
        xz)   ensure_tool xz ;;
        bz2)  ensure_tool "$BZIP2_BIN" "$BZIP2_BIN" pbzip2 ;;
        bz3)  ensure_tool bzip3 ;;
        zst)  ensure_tool zstd ;;
        lz)   ensure_tool plzip ;;
        lrz)  ensure_tool lrzip ;;
        zip)  ensure_tool zip ; ensure_tool 7z "$SEVENZ_BIN" ;;
        7z)   ensure_tool 7z "$SEVENZ_BIN" ;;
        rar)  ensure_tool rar ;;
        tar)  ensure_tool tar ;;
    esac
}

# --- register_decompress_tool: acumula dependencias para descomprimir según extensión ---
# $1: nombre de archivo
# Usa _get_format_info para determinar qué tool necesita.
register_decompress_tool() {
    local file=$1 info
    info=$(_get_format_info "$file") || return 0
    IFS='|' read -r tool _ _ _ _ <<< "$info"
    ensure_tool "$tool"
}

# --- get_mem_limit: obtiene 70% de RAM disponible en MiB ---
# Fuentes: /proc/meminfo MemAvailable → free -k → MemFree+Cached
# Fallback: 1024 MiB si no se puede detectar
# stdout: MiB disponibles para el compresor (xz --memory=)
get_mem_limit() {
    local mem_kb

    if [[ -r /proc/meminfo ]]; then
        mem_kb=$(awk '/^MemAvailable:/{print $2}' /proc/meminfo)
    fi

    if [[ -z "$mem_kb" ]]; then
        mem_kb=$(free -k 2>/dev/null | awk '/^Mem:/{print $7}')
    fi

    if [[ -z "$mem_kb" ]]; then
        mem_kb=$(awk '/^MemFree:/{f=$2} /^Cached:/{c=$2} END{print f+c}' /proc/meminfo 2>/dev/null)
    fi

    if [[ -z "$mem_kb" || "$mem_kb" -le 0 ]]; then
        echo 1024
        return
    fi

    echo $((mem_kb * 70 / 100 / 1024))
}

# --- format_size: muestra bytes en formato legible ---
# $1: bytes
# Usa numfmt si disponible; fallback manual B/KiB/MiB/GiB/TiB
# stdout: "42.00 MiB" (ejemplo)
format_size() {
    local bytes=$1
    if command -v numfmt &>/dev/null; then
        numfmt --to=iec-i --suffix=B "$bytes" 2>/dev/null || echo "${bytes}B"
    else
        local units=("B" "KiB" "MiB" "GiB" "TiB" "PiB")
        local unit=0
        local size=$bytes
        while [[ $size -gt 1024 && $unit -lt 5 ]]; do
            size=$((size / 1024))
            unit=$((unit + 1))
        done
        echo "${size} ${units[$unit]}"
    fi
}

# --- _calc_pct: calcula (orig - final) / orig * 100 ---
# $1: bytes originales
# $2: bytes finales
# stdout: "45.32" (porcentaje con 2 decimales)
# Retorna "0.00" si orig ≤ 0 o final ≤ 0
_calc_pct() {
    local orig=$1 final=$2
    if [[ "$orig" -gt 0 && -n "$final" && "$final" -gt 0 ]]; then
        awk -v orig="$orig" -v final="$final" 'BEGIN {printf "%.2f", (orig - final) / orig * 100}'
    else
        printf "0.00"
    fi
}

# --- estimate_uncompressed_size: estima tamaño descomprimido de un archivo ---
# $1: archivo comprimido
# Soporta: .gz, .xz, .zst, .bz2, .lz, .zip
# Fallback: 0 si no se puede estimar
# stdout: bytes (entero)
estimate_uncompressed_size() {
    local file=$1
    local f_lower
    f_lower=$(_lower "$file")

    case "$f_lower" in
        *.gz|*.tgz)
            gzip -l -- "$file" 2>/dev/null | awk 'NR==2 {print $2}'
            return
            ;;
        *.xz|*.txz)
            xz -l --robot -- "$file" 2>/dev/null | awk -F'\t' '/^file/ && $5 ~ /^[0-9]+$/{print $5}'
            return
            ;;
        *.zst|*.tzst)
            zstd -l -- "$file" 2>/dev/null | awk '/^[[:space:]]*[0-9]+[[:space:]]/ && !/Frames/ && $5 ~ /^[0-9]+$/{sum+=$5} END{if(sum>0) print sum}'
            return
            ;;
        *.zip)
            unzip -l -- "$file" 2>/dev/null | tail -1 | awk '{print $1}'
            return
            ;;
        *.7z)
            "$SEVENZ_BIN" l -slt -- "$file" 2>/dev/null | awk '/^Size = / {sum+=$3} END {print int(sum)}'
            return
            ;;
        *.lrz)
            lrzip -i -- "$file" 2>/dev/null | awk '/Decompressed file size/{print $NF}'
            return
            ;;
        *.tar)
            stat -c%s -- "$file" 2>/dev/null
            return
            ;;
        *.bz2|*.tbz2|*.bz3|*.lz|*.tlz)
            stat -c%s -- "$file" 2>/dev/null | awk '{print $1 * 6}'
            return
            ;;
        *)
            stat -c%s -- "$file" 2>/dev/null | awk '{print $1 * 4}'
            return
            ;;
    esac
}

# --- _get_avail_bytes: bytes disponibles en filesystem ---
# $1: directorio (default .)
# stdout: bytes disponibles (entero)
_get_avail_bytes() {
    local dir=${1:-.} avail
    avail=$(df -B1 --output=avail "$dir" 2>/dev/null | awk 'NR==2 {print $1}' | tr -d ' ')
    [[ -z "$avail" || "$avail" == "avail" ]] && avail=$(df "$dir" 2>/dev/null | awk 'NR>1 {print int($4) * 1024}')
    echo "${avail:-0}"
}

# --- check_decompress_space: verifica espacio antes de descomprimir ---
# $1: archivo comprimido
# Estima tamaño descomprimido + margen, lo compara con espacio disponible.
# Retorna 0 si hay espacio suficiente, 1 si no.
check_decompress_space() {
    local file=$1
    local compressed_size uncomp_size est_size avail_bytes

    compressed_size=$(stat -c%s -- "$file" 2>/dev/null || echo 0)
    [[ "$compressed_size" -eq 0 ]] && return 0

    uncomp_size=$(estimate_uncompressed_size "$file")
    est_size=${uncomp_size:-$((compressed_size * 4))}
    avail_bytes=$(_get_avail_bytes ".")

    if [[ "$est_size" -gt "$avail_bytes" ]]; then
        printf "${RED}[Sin espacio] %s necesita ~%s, disponible %s. Saltando.${NC}\n" \
            "$file" "$(format_size "$est_size")" "$(format_size "$avail_bytes")" >&2
        return 1
    fi
    if [[ "$est_size" -gt "$((avail_bytes * 80 / 100))" ]]; then
        printf "${YELLOW}[Advertencia] %s necesita ~%s, espacio disponible %s (muy justo).${NC}\n" \
            "$file" "$(format_size "$est_size")" "$(format_size "$avail_bytes")" >&2
    fi
    return 0
}

# --- _test_compression_layer: test rápido de integridad (interno, sin output) ---
# $1: archivo
# Usa el test_flag de _get_format_info para verificar.
# Retorna 0 si íntegro, 1 si no.
_test_compression_layer() {
    local file=$1 info
    info=$(_get_format_info "$file") || return 1
    IFS='|' read -r tool _ _ _ tflag <<< "$info"
    if [[ "$tool" == "tar" ]]; then
        $tool $tflag "$file" 2>/dev/null
    else
        $tool $tflag -- "$file" 2>/dev/null
    fi
}

# --- _test_compress_layer: test de compresión pre-descompresión (con output) ---
# $1: archivo
# Wrapper con mensaje coloreado alrededor de _test_compression_layer.
# Retorna 0 si íntegro, 1 si no.
_test_compress_layer() {
    local file=$1
    printf "${BLUE}[Verificando] %s...${NC} " "$file"
    if _test_compression_layer "$file"; then
        printf "${GREEN}[OK]${NC}\n"
        return 0
    else
        printf "${RED}[CORRUPTO] Saltando.${NC}\n" >&2
        return 1
    fi
}

# --- get_unique_name: genera nombre de archivo sin colisiones ---
# $1: nombre base
# $2: extensión
# Si el archivo ya existe, añade _1, _2, ... hasta encontrar uno libre.
# stdout: nombre único (ej: "backup.tar.gz" o "backup_1.tar.gz")
get_unique_name() {
    local base_name=$1 ext=$2 counter=1 final_name

    if [[ "$base_name" == *."$ext" ]]; then
        base_name="${base_name%.$ext}"
    fi

    final_name="${base_name}.${ext}"
    if [[ -e "$final_name" ]]; then
        while [[ -e "${base_name}_${counter}.${ext}" ]]; do
            counter=$((counter + 1))
        done
        final_name="${base_name}_${counter}.${ext}"
    fi
    echo "$final_name"
}

# --- check_disk_space: verifica espacio y retorna 1 si insuficiente ---
# $1: bytes necesarios
# $2: directorio a verificar
# Retorna 0 si hay espacio, 1 si no.
check_disk_space() {
    local needed=$1 dir=$2 available
    available=$(_get_avail_bytes "$dir")
    if [[ "$available" -lt "$needed" ]]; then
        printf "${RED}[Error] Espacio insuficiente en disco.${NC}\n" >&2
        printf "${YELLOW}  Necesario: %s${NC}\n" "$(format_size "$needed")" >&2
        printf "${YELLOW}  Disponible: %s${NC}\n" "$(format_size "$available")" >&2
        return 1
    fi
}

# --- setup_logging: configura log a /var/log/compresor/ y mantiene MAX_LOGS rotaciones ---
# Redirige stdout+stderr a tee (pantalla + archivo de log).
# Purgua logs antiguos cuando supera MAX_LOGS archivos.
setup_logging() {
    local log_dir="$LOG_DIR"
    if ! mkdir -p "$log_dir" 2>/dev/null; then
        log_dir="/tmp/compresor"
        mkdir -p "$log_dir" 2>/dev/null || true
    fi

    LOG_FILE="${log_dir}/compresor_$(date +%Y%m%d_%H%M%S).log"

    find "$log_dir" -name 'compresor_*.log' -type f -printf '%T@ %p\n' 2>/dev/null | \
        sort -rn | tail -n +$((MAX_LOGS + 1)) | \
        while read -r _ file; do rm -f -- "$file" 2>/dev/null; done || true

    exec > >(tee -a "$LOG_FILE") 2>&1
}

# --- cleanup: elimina archivo temporal al recibir señal (Ctrl+C, etc.) ---
# Se ejecuta en exit, SIGINT, SIGTERM.
cleanup() {
    if [[ -n "${CLEANUP_FILE:-}" && -f "${CLEANUP_FILE:-}" ]]; then
        printf "\n${YELLOW}[Sistema] Interrupción detectada. Limpiando archivo incompleto: %s${NC}\n" "$CLEANUP_FILE"
        rm -f -- "$CLEANUP_FILE" 2>/dev/null
    fi
    exit 1
}

# --- list_compressors: muestra tabla comparativa de formatos ---
# No recibe args. Imprime en pantalla y exit 0.
list_compressors() {
    printf "${BLUE}=== Tabla de Eficiencia de Compresores (Todos Multiproceso) ===${NC}\n"
    printf "\n"
    printf "${YELLOW}--- Alta Compresión (Mayor tiempo) ---${NC}\n"
    printf "1. ${GREEN}lrz${NC} : ${YELLOW}Máxima${NC} (LRZIP). Ideal para archivos ENORMES (>1GB). Usa lzma+rzip.\n"
    printf "2. ${GREEN}7z${NC}  : ${YELLOW}Ultra${NC} (LZMA2). Excelente ratio, alto uso de CPU/RAM.\n"
    printf "3. ${GREEN}xz${NC}  : ${YELLOW}Excelente${NC} (LZMA). Estándar moderno en Linux.\n"
    printf "4. ${GREEN}lz${NC}  : ${YELLOW}Excelente${NC} (LZIP/plzip). Similar a xz, enfocado en integridad.\n"
    printf "\n"
    printf "${YELLOW}--- Compresión Balanceada ---${NC}\n"
    printf "5. ${GREEN}zst${NC} : ${YELLOW}Muy Rápido${NC} (Zstandard). Mejor balance velocidad/ratio moderno.\n"
    printf "6. ${GREEN}bz3${NC} : ${YELLOW}Muy Alto${NC} (Bzip3). Eficiente para texto y código.\n"
    printf "7. ${GREEN}bz2${NC} : ${YELLOW}Alto${NC} (lbzip2). Clásico, buena relación peso/tiempo.\n"
    printf "\n"
    printf "${YELLOW}--- Alta Velocidad ---${NC}\n"
    printf "8. ${GREEN}gz${NC}  : ${YELLOW}Rápido${NC} (pigz). El más compatible y rápido.\n"
    printf "9. ${GREEN}rar${NC} : ${YELLOW}Alta${NC} (RAR). Propietario, amplia compatibilidad.\n"
    printf "10. ${GREEN}zip${NC} : ${YELLOW}Básico${NC}. Compatibilidad universal (Windows/Mac/Linux).\n"
    printf "\n"
    printf "${YELLOW}--- Sin compresión ---${NC}\n"
    printf "11. ${GREEN}tar${NC} : Solo empaquetado sin compresión.\n"
    printf "\n"
    printf "${BLUE}Nota:${NC} Todos los formatos usan multiprocesamiento automático.\n"
    exit 0
}

# --- usage: muestra ayuda detallada (--help) ---
# Sin args. Imprime en pantalla y exit 0.
usage() {
    printf "${BLUE}======================================================${NC}\n"
    printf "${GREEN}      COMPRESOR / DESCOMPRESOR UNIVERSAL (BASH)      ${NC}\n"
    printf "${BLUE}======================================================${NC}\n"
    printf "\n"
    printf "${YELLOW}MODO COMPRESIÓN:${NC}\n"
    printf "  %s -c <formato> [opciones] <archivos/carpetas...>\n" "$0"
    printf "  ${BLUE}Ejemplo (agrupar):${NC} %s -c zst -n backup_fotos ./mis_fotos\n" "$0"
    printf "  ${BLUE}Ejemplo (individual):${NC} %s -c gz -i file1.txt file2.txt\n" "$0"
    printf "\n"
    printf "${YELLOW}MODO DESCOMPRESIÓN:${NC}\n"
    printf "  %s -d [opciones] <archivos...>\n" "$0"
    printf "  ${BLUE}Ejemplo:${NC} %s -d archivo1.zst archivo2.tar.gz\n" "$0"
    printf "\n"
    printf "${YELLOW}MODO TEST:${NC}\n"
    printf "  %s -t <archivos...>\n" "$0"
    printf "  ${BLUE}Ejemplo:${NC} %s -t archivo.tar.zst archivo.zip\n" "$0"
    printf "\n"
    printf "${YELLOW}OPCIONES:${NC}\n"
    printf "  ${GREEN}-c <fmt>${NC}    : Comprimir (formatos: gz, xz, bz2, bz3, zst, lz, lrz, rar, zip, 7z, tar)\n"
    printf "  ${GREEN}-d${NC}          : Descomprimir (detecta formato automáticamente)\n"
    printf "  ${GREEN}-t${NC}          : Verificar integridad de archivos comprimidos\n"
    printf "  ${GREEN}-r${NC}          : ${RED}Borrar original${NC} al finalizar (solo si no hubo errores)\n"
    printf "  ${GREEN}-n nombre${NC}   : Nombre personalizado para el archivo de salida\n"
    printf "  ${GREEN}-l${NC}          : Ver tabla comparativa de compresores\n"
    printf "  ${GREEN}-h${NC}          : Mostrar esta ayuda\n"
    printf "  ${GREEN}--dry-run${NC}   : Previsualizar sin ejecutar\n"
    printf "  ${GREEN}--exclude PAT${NC}: Excluir patrones (ej: --exclude=.git --exclude=*.log)\n"
    printf "  ${GREEN}--split TAM${NC}  : Dividir en volúmenes (ej: --split=100M, --split=1G)\n"
    printf "  ${GREEN}-i${NC}          : Comprimir cada archivo por separado en vez de agruparlos\n"
    printf "  ${GREEN}--install${NC}    : Copiar script a /usr/local/bin/compresor\n"
    printf "  ${GREEN}--uninstall${NC}  : Eliminar /usr/local/bin/compresor\n"
    printf "\n"
}

# ==============================================================================
# LÓGICA DE COMPRESIÓN — Empaquetado y compresión de archivos
# ==============================================================================

# --- _ext_for_format: mapea FORMAT → extensión de archivo ---
# $FORMAT (global) → stdout: extensión (gz → "tar.gz", zip → "zip")
_ext_for_format() {
    case $FORMAT in
        zip|7z|rar|tar) echo "$FORMAT" ;;
        *)          echo "tar.$FORMAT" ;;
    esac
}

# --- _print_compress_report: imprime tabla resumen de compresión ---
# $1: nombre archivo final
# $2: tamaño original (humano)
# $3: tamaño final (humano)
# $4: porcentaje de ahorro
# $5: número de hilos
_print_compress_report() {
    local file=$1 orig=$2 final=$3 pct=$4 ncpu=$5
    printf "\n${GREEN}=== Reporte de Compresión ===${NC}\n"
    printf "${BLUE}Archivo Salida:${NC}    ${YELLOW}%s${NC}\n" "$file"
    printf "${BLUE}Tamaño Original:${NC}   ${RED}%s${NC}\n" "$orig"
    printf "${BLUE}Tamaño Final:${NC}      ${GREEN}%s${NC}\n" "$final"
    printf "${BLUE}Ahorro de espacio:${NC} ${GREEN}%s%%${NC}\n" "$pct"
    printf "${BLUE}Hilos utilizados:${NC}  ${GREEN}%s${NC}\n" "$ncpu"
    printf "${GREEN}===========================${NC}\n"
}

# --- _delete_originals: elimina archivos originales (usado con -r) ---
# $@: paths a eliminar (recursivo, forzado)
_delete_originals() {
    local items=("$@")
    for item in "${items[@]}"; do
        rm -rf -- "$item"
        printf "${YELLOW}[Info] Original eliminado: %s${NC}\n" "$item"
    done
}

# --- _compress_items: comprime un lote de archivos en un solo archivo ---
# $@: rutas a comprimir
# Usa FORMAT global para elegir tool, EXTRA_FLAGS para personalizar.
# Maneja dry-run, exclude, split, verificación post-compresión.
# Retorna 0 si éxito, 1 si fallo.
_compress_items() {
    local -a items=("$@")
    [[ ${#items[@]} -eq 0 ]] && return 1

    local ext mem_mb FINAL_FILE TOTAL_ORIG_BYTES=0 ORIG_HUMAN
    mem_mb=$(get_mem_limit)
    ext=$(_ext_for_format)

    local base_name
    if [[ "$INDIVIDUAL" == 1 || -z "$CUSTOM_NAME" ]]; then
        base_name="${items[0]%/}"
    else
        base_name="$CUSTOM_NAME"
    fi
    FINAL_FILE=$(get_unique_name "$base_name" "$ext")

    if [[ "$DRY_RUN" == 1 ]]; then
        printf "${YELLOW}[DRY-RUN] Comando:${NC}\n"
        case $FORMAT in
            gz)  printf "  tar -cvf - %s | pigz -9 > %s\n" "${items[*]}" "$FINAL_FILE" ;;
            xz)  printf "  tar -cvf - %s | xz -9e -T0 --memory=%sMiB > %s\n" "${items[*]}" "$mem_mb" "$FINAL_FILE" ;;
            bz2) printf "  tar -cvf - %s | %s -9 > %s\n" "${items[*]}" "$BZIP2_BIN" "$FINAL_FILE" ;;
            bz3) printf "  tar -cvf - %s | bzip3 -j %s > %s\n" "${items[*]}" "$NCPU" "$FINAL_FILE" ;;
            zst) printf "  tar -cvf - %s | zstd -19 -T0 -o %s\n" "${items[*]}" "$FINAL_FILE" ;;
            lz)  printf "  tar -cvf - %s | plzip -9 --threads=%s > %s\n" "${items[*]}" "$NCPU" "$FINAL_FILE" ;;
            lrz) printf "  tar -cvf - %s | lrzip -L 9 -z -p %s -o %s\n" "${items[*]}" "$NCPU" "$FINAL_FILE" ;;
            zip) printf "  %s a -tzip -mx=9 -mmt=on %s %s\n" "$SEVENZ_BIN" "$FINAL_FILE" "${items[*]}" ;;
            7z)  printf "  %s a -mx=9 -md=128m -ms=on -mmt=on %s %s\n" "$SEVENZ_BIN" "$FINAL_FILE" "${items[*]}" ;;
            rar) printf "  %s a -m5 -mt%s %s %s\n" "$RAR_BIN" "$NCPU" "$FINAL_FILE" "${items[*]}" ;;
            tar) printf "  tar -cvf %s %s\n" "$FINAL_FILE" "${items[*]}" ;;
        esac
        if [[ -n "$SPLIT_SIZE" ]]; then
            printf "  split -b %s %s %s.part.\n" "$SPLIT_SIZE" "$FINAL_FILE" "$FINAL_FILE"
        fi
        printf "${GREEN}[DRY-RUN] Modo simulación activado. No se ejecutó nada.${NC}\n"
        exit 0
    fi

    for item in "${items[@]}"; do
        if [[ ! -e "$item" ]]; then
            printf "${RED}[Error] Archivo/Carpeta no encontrado: %s${NC}\n" "$item" >&2
            return 1
        fi
        TOTAL_ORIG_BYTES=$((TOTAL_ORIG_BYTES + $(du -sb -- "$item" | cut -f1)))
    done
    ORIG_HUMAN=$(format_size "$TOTAL_ORIG_BYTES")

    local disk_needed
    if [[ "$FORMAT" == "tar" ]]; then
        disk_needed=$((TOTAL_ORIG_BYTES + TOTAL_ORIG_BYTES / 10))
    else
        disk_needed=$((TOTAL_ORIG_BYTES * 20 / 100))
    fi
    [[ $disk_needed -lt 1048576 ]] && disk_needed=1048576
    check_disk_space "$disk_needed" "$(dirname "$FINAL_FILE" 2>/dev/null || echo .)" || return 1

    printf "${BLUE}[Info] Comprimiendo ${YELLOW}%s${BLUE} de datos en formato ${GREEN}%s${BLUE} (%s hilos)...${NC}\n" \
        "$ORIG_HUMAN" "$FORMAT" "$NCPU"

    CLEANUP_FILE="$FINAL_FILE"

    local -a tar_exclude
    for pattern in "${EXCLUDE_PATTERNS[@]}"; do
        tar_exclude+=(--exclude="$pattern")
    done

    local CMD_EXIT=0
    case $FORMAT in
        gz)
            tar "${tar_exclude[@]}" -cvf - -- "${items[@]}" | _pv_pipe "$TOTAL_ORIG_BYTES" | pigz -9 > "$FINAL_FILE" || CMD_EXIT=$?
            ;;
        xz)
            tar "${tar_exclude[@]}" -cvf - -- "${items[@]}" | _pv_pipe "$TOTAL_ORIG_BYTES" | xz -9e -T0 --memory="${mem_mb}MiB" > "$FINAL_FILE" || CMD_EXIT=$?
            ;;
        bz2)
            tar "${tar_exclude[@]}" -cvf - -- "${items[@]}" | _pv_pipe "$TOTAL_ORIG_BYTES" | "$BZIP2_BIN" -9 > "$FINAL_FILE" || CMD_EXIT=$?
            ;;
        bz3)
            tar "${tar_exclude[@]}" -cvf - -- "${items[@]}" | _pv_pipe "$TOTAL_ORIG_BYTES" | bzip3 -j "$NCPU" > "$FINAL_FILE" || CMD_EXIT=$?
            ;;
        zst)
            tar "${tar_exclude[@]}" -cvf - -- "${items[@]}" | _pv_pipe "$TOTAL_ORIG_BYTES" | zstd -19 -T0 -o "$FINAL_FILE" || CMD_EXIT=$?
            ;;
        lz)
            tar "${tar_exclude[@]}" -cvf - -- "${items[@]}" | _pv_pipe "$TOTAL_ORIG_BYTES" | plzip -9 --threads="$NCPU" > "$FINAL_FILE" || CMD_EXIT=$?
            ;;
        lrz)
            tar "${tar_exclude[@]}" -cvf - -- "${items[@]}" | _pv_pipe "$TOTAL_ORIG_BYTES" | lrzip -L 9 -z -p "$NCPU" -o "$FINAL_FILE" || CMD_EXIT=$?
            ;;
        zip)
            "$SEVENZ_BIN" a -tzip -mx=9 -mmt=on "$FINAL_FILE" -- "${items[@]}" || CMD_EXIT=$?
            ;;
        7z)
            "$SEVENZ_BIN" a -mx=9 -md=128m -ms=on -mmt=on "$FINAL_FILE" -- "${items[@]}" || CMD_EXIT=$?
            ;;
        rar)
            "$RAR_BIN" a -m5 -mt"$NCPU" "$FINAL_FILE" -- "${items[@]}" || CMD_EXIT=$?
            ;;
        tar)
            tar "${tar_exclude[@]}" -cvf "$FINAL_FILE" -- "${items[@]}" || CMD_EXIT=$?
            ;;
    esac

    if [[ $CMD_EXIT -eq 0 && -f "$FINAL_FILE" ]]; then
        CLEANUP_FILE=""

        local FINAL_SIZE FINAL_BYTES PERCENTAGE REPORT_FILE="$FINAL_FILE"
        FINAL_SIZE=$(du -sh -- "$FINAL_FILE" 2>/dev/null | cut -f1)
        FINAL_BYTES=$(du -sb -- "$FINAL_FILE" 2>/dev/null | cut -f1)
        PERCENTAGE=$(_calc_pct "$TOTAL_ORIG_BYTES" "$FINAL_BYTES")

        if [[ -n "$SPLIT_SIZE" ]]; then
            printf "${BLUE}[Info] Dividiendo archivo en volúmenes de %s...${NC}\n" "$SPLIT_SIZE"
            split -b "$SPLIT_SIZE" -- "$FINAL_FILE" "${FINAL_FILE}.part."
            rm -f -- "$FINAL_FILE"
            printf "${GREEN}[OK] Archivo dividido en %s.part.aa, %s.part.ab, ...${NC}\n" "$FINAL_FILE" "$FINAL_FILE"
            FINAL_FILE="${FINAL_FILE}.part.aa"
        fi

        _print_compress_report "$REPORT_FILE" "$ORIG_HUMAN" "$FINAL_SIZE" "$PERCENTAGE" "$NCPU"

        if [[ "$DELETE_ORIG" == 1 ]]; then
            _delete_originals "${items[@]}"
        fi
        return 0
    else
        printf "${RED}[Fatal] La compresión falló. Verifica espacio en disco o permisos.${NC}\n" >&2
        rm -f -- "$FINAL_FILE" 2>/dev/null
        return 1
    fi
}

# ==============================================================================
# LÓGICA DE COMPRESIÓN — Dispatcher (individual o bundle)
# ==============================================================================

# --- do_compress: entry point de compresión ---
# Decide si comprimir individualmente (-i) o en bundle.
# Itera INPUTS[] y llama a _compress_items por cada uno.
do_compress() {
    if [[ "$INDIVIDUAL" == 1 && ${#INPUTS[@]} -gt 1 ]]; then
        local has_error=0
        for item in "${INPUTS[@]}"; do
            printf "${BLUE}══════════════════════════════════════════════${NC}\n"
            printf "${BLUE}  Archivo: ${YELLOW}%s${NC}\n" "$item"
            printf "${BLUE}══════════════════════════════════════════════${NC}\n"
            _compress_items "$item" || has_error=1
        done
        exit $has_error
    else
        _compress_items "${INPUTS[@]}" || exit 1
    fi
}

# ==============================================================================
# LÓGICA DE DESCOMPRESIÓN — Detección y extracción multi-formato
# ==============================================================================

# --- do_decompress: entry point de descompresión ---
# 1. Verifica espacio total disponible
# 2. Por cada archivo: test de integridad → verifica espacio → descomprime
# Soporta detección automática de formato por extensión.
do_decompress() {
    # Verificación de espacio TOTAL antes de empezar
    local total_compressed=0 total_needed=0 avail_bytes
    for f in "${INPUTS[@]}"; do
        [[ ! -f "$f" ]] && continue
        local c_size u_size
        c_size=$(stat -c%s -- "$f" 2>/dev/null || echo 0)
        u_size=$(estimate_uncompressed_size "$f")
        u_size=${u_size:-$((c_size * 4))}
        total_compressed=$((total_compressed + c_size))
        total_needed=$((total_needed + u_size))
    done

    avail_bytes=$(_get_avail_bytes ".")

    printf "${BLUE}══════════════════════════════════════════════${NC}\n"
    printf "${BLUE}  Verificación de espacio para descompresión${NC}\n"
    printf "${BLUE}══════════════════════════════════════════════${NC}\n"
    printf "${BLUE}Archivos a procesar:${NC}  %s\n" "${#INPUTS[@]}"
    printf "${BLUE}Tamaño comprimido:${NC}   %s\n" "$(format_size "$total_compressed")"
    printf "${BLUE}Tamaño final est.:${NC}   %s\n" "$(format_size "$total_needed")"
    printf "${BLUE}Espacio disponible:${NC}  %s\n" "$(format_size "$avail_bytes")"

    if [[ $total_needed -gt $avail_bytes ]]; then
        printf "${RED}[Error] Espacio insuficiente. Necesitas ~%s, hay %s. Abortando.${NC}\n" \
            "$(format_size "$total_needed")" "$(format_size "$avail_bytes")" >&2
        exit 1
    fi
    if [[ $total_needed -gt $((avail_bytes * 80 / 100)) ]]; then
        printf "${YELLOW}[Advertencia] Espacio muy justo: %s disponible para ~%s necesarios.${NC}\n" \
            "$(format_size "$avail_bytes")" "$(format_size "$total_needed")" >&2
    fi
    printf "${GREEN}[OK] Espacio suficiente.${NC}\n"
    printf '%s\n' "------------------------------------------------------"

    local has_error=0
    for file in "${INPUTS[@]}"; do
        if [[ ! -f "$file" ]]; then
            printf "${RED}[Saltando] '%s' no es un archivo válido.${NC}\n" "$file" >&2
            has_error=1
            continue
        fi

        # Test de integridad previo
        if ! _test_compress_layer "$file"; then
            has_error=1
            continue
        fi

        # Verificar espacio disponible por archivo (red de seguridad)
        if ! check_decompress_space "$file"; then
            has_error=1
            continue
        fi

        printf "${BLUE}[Procesando] Archivo: ${YELLOW}%s${BLUE} (%s hilos)${NC}\n" "$file" "$NCPU"

        local SUCCESS=0 info
        info=$(_get_format_info "$file") || { printf "${RED}[Error] Formato desconocido o no soportado para: %s${NC}\n" "$file" >&2; SUCCESS=1; }
        if [[ $SUCCESS -eq 0 ]]; then
            IFS='|' read -r tool pipe_flags direct_flags is_tar _ <<< "$info"
            if [[ "$is_tar" == 1 ]]; then
                $tool $pipe_flags -- "$file" | tar -xvf - || SUCCESS=$?
            elif [[ "$tool" == "tar" ]]; then
                $tool $direct_flags "$file" || SUCCESS=$?
            else
                $tool $direct_flags -- "$file" || SUCCESS=$?
            fi
        fi

        if [[ $SUCCESS -eq 0 ]]; then
            printf "${GREEN}[Éxito] Archivo descomprimido correctamente.${NC}\n"
            if [[ "$DELETE_ORIG" == 1 ]]; then
                rm -f -- "$file"
                printf "${YELLOW}[Info] Archivo original eliminado (-r activado).${NC}\n"
            else
                printf "${BLUE}[Info] Archivo original conservado.${NC}\n"
            fi
        else
            has_error=1
            printf "${RED}[Fallo] Error al descomprimir '%s'. El original NO se ha borrado.${NC}\n" "$file" >&2
        fi
        printf '%s\n' "------------------------------------------------------"
    done
    exit $has_error
}

# ==============================================================================
# LÓGICA DE TEST DE INTEGRIDAD — Verifica archivos sin extraer
# ==============================================================================

# --- do_test_file: test de integridad de un solo archivo ---
# $1: archivo
# 1. Test de capa de compresión (test_flag de _get_format_info)
# 2. Para .tar.*: test extra de tar -t
# Retorna 0 si íntegro, 1 si no.
do_test_file() {
    local file=$1 info
    printf "${BLUE}[Test] Verificando integridad: ${YELLOW}%s${NC}... " "$file"

    # Test capa de compresión
    if ! _test_compression_layer "$file"; then
        printf "${RED}[CORRUPTO O INVÁLIDO]${NC}\n"
        return 1
    fi

    # Para .tar.* y .tar: test extra de integridad del tar
    info=$(_get_format_info "$file") || { printf "${GREEN}[OK]${NC}\n"; return 0; }
    IFS='|' read -r tool pipe_flags _ is_tar _ <<< "$info"
    if [[ "$is_tar" == 1 ]]; then
        $tool $pipe_flags -- "$file" 2>/dev/null | tar -t >/dev/null 2>&1 || { printf "${RED}[CORRUPTO O INVÁLIDO]${NC}\n"; return 1; }
    elif [[ "$tool" == "tar" ]]; then
        tar -tf "$file" >/dev/null 2>&1 || { printf "${RED}[CORRUPTO O INVÁLIDO]${NC}\n"; return 1; }
    fi

    printf "${GREEN}[OK]${NC}\n"
    return 0
}

# --- do_test: entry point de test (-t) — verifica toda la lista INPUTS[] ---
# Itera INPUTS[], llama a do_test_file. Exit 1 si algún archivo está corrupto.
do_test() {
    local has_errors=0
    for file in "${INPUTS[@]}"; do
        if [[ ! -f "$file" ]]; then
            printf "${RED}[Saltando] '%s' no es un archivo válido.${NC}\n" "$file" >&2
            has_errors=1
            continue
        fi
        do_test_file "$file" || has_errors=1
    done
    exit $has_errors
}

# ==============================================================================
# PROCESAMIENTO DE ARGUMENTOS — Parseo y validación de opciones
# ==============================================================================

# --- Detección de binarios del sistema antes de procesar args ---
# 7zz/7z/7za: búsqueda en orden de preferencia
# lbzip2/pbzip2: búsqueda en orden de preferencia
if command -v 7zz &>/dev/null; then SEVENZ_BIN="7zz"
elif command -v 7z &>/dev/null; then SEVENZ_BIN="7z"
elif command -v 7za &>/dev/null; then SEVENZ_BIN="7za"
else SEVENZ_BIN="7z"; fi

if command -v lbzip2 &>/dev/null; then BZIP2_BIN="lbzip2"
elif command -v pbzip2 &>/dev/null; then BZIP2_BIN="pbzip2"
else BZIP2_BIN="lbzip2"; fi

if command -v rar &>/dev/null; then RAR_BIN="rar"
elif command -v unrar &>/dev/null; then RAR_BIN="unrar"
else RAR_BIN="rar"; fi

detect_pkg_manager

PARSED_ARGS=$(getopt -o 'c:dhrn:lti' -l 'help,dry-run,exclude:,split:,test,individual,install,uninstall' -- "$@") || { usage; exit 1; }
eval set -- "$PARSED_ARGS"

while true; do
    case "$1" in
        -c)
            FORMAT="$2"
            MODE="compress"
            shift 2
            ;;
        -d)
            MODE="decompress"
            shift
            ;;
        -t|--test)
            MODE="test"
            shift
            ;;
        -r)
            DELETE_ORIG=1
            shift
            ;;
        -n)
            CUSTOM_NAME="$2"
            shift 2
            ;;
        -l)
            list_compressors
            ;;
        -h|--help)
            usage; exit 0
            ;;
        --dry-run)
            DRY_RUN=1
            shift
            ;;
        --exclude)
            EXCLUDE_PATTERNS+=("$2")
            shift 2
            ;;
        --split)
            SPLIT_SIZE="$2"
            shift 2
            ;;
        -i|--individual)
            INDIVIDUAL=1
            shift
            ;;
        --install)
            SCRIPT_SRC=$(readlink -f "$0" 2>/dev/null || realpath "$0" 2>/dev/null || printf '%s' "$0")
            DEST="/usr/local/bin/compresor"
            DIR=$(dirname "$DEST")
            if [[ -w "$DIR" ]]; then
                install -m 755 "$SCRIPT_SRC" "$DEST"
            else
                sudo install -m 755 "$SCRIPT_SRC" "$DEST"
            fi
            printf "${GREEN}[OK] Script instalado en %s${NC}\n" "$DEST"
            exit 0
            ;;
        --uninstall)
            DEST="/usr/local/bin/compresor"
            if [[ -f "$DEST" ]]; then
                if [[ -w "$(dirname "$DEST")" ]]; then
                    rm -f "$DEST"
                else
                    sudo rm -f "$DEST"
                fi
                printf "${GREEN}[OK] Eliminado: %s${NC}\n" "$DEST"
            else
                printf "${YELLOW}[Info] No existe: %s${NC}\n" "$DEST"
            fi
            exit 0
            ;;
        --)
            shift
            break
            ;;
        *)
            usage; exit 1
            ;;
    esac
done

INPUTS=("$@")

# --- Normalizar inputs que empiezan con - para evitar que se interpreten como opciones ---
for i in "${!INPUTS[@]}"; do
    if [[ "${INPUTS[$i]}" == -* ]]; then
        INPUTS[$i]="./${INPUTS[$i]}"
    fi
done

# --- Validaciones de modo y argumentos ---
if [[ -z "$MODE" ]]; then
    printf "${RED}[Error] Debes especificar un modo de operación (-c, -d, -t).${NC}\n" >&2
    usage; exit 1
fi

if [[ "$MODE" == "compress" ]]; then
    if [[ -z "$FORMAT" ]]; then
        printf "${RED}[Error] Falta el formato de compresión.${NC}\n" >&2
        usage; exit 1
    fi
    if [[ ${#INPUTS[@]} -eq 0 ]]; then
        printf "${RED}[Error] No se especificaron archivos/carpetas para comprimir.${NC}\n" >&2
        usage; exit 1
    fi
fi

if [[ "$MODE" != "compress" && ${#INPUTS[@]} -eq 0 ]]; then
    printf "${RED}[Error] No se especificaron archivos para procesar.${NC}\n" >&2
    usage; exit 1
fi

# --- if compress: registrar tools por FORMAT; else por extensión de cada input ---
if [[ "$MODE" == "compress" ]]; then
    register_compress_tools
else
    for file in "${INPUTS[@]}"; do
        register_decompress_tool "$file"
    done
fi

install_missing_deps

# --- Configurar logging y trap de interrupción ---
setup_logging
trap cleanup INT TERM

# ==============================================================================
# DESPACHO PRINCIPAL — Ejecuta según el modo seleccionado
# ==============================================================================
case "$MODE" in
    compress)
        do_compress
        ;;
    decompress)
        do_decompress
        ;;
    test)
        do_test
        ;;
esac
