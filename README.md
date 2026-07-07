# COMPRESOR

Herramienta multi-formato de compresión y descompresión vía pipe.
Soporta 13 formatos con versiones **multihilo** para aprovechar todos los núcleos del CPU.

## Instalación

```bash
# Compilar e instalar (Go 1.21+)
make install          # go build + install a /usr/local/bin

# O con sudo
sudo make install

# Instalar binario + dependencias del sistema
compresor --install
```

## Formatos (todos multihilo)

| Formato | Compresor | Núcleos | Algoritmo |
|---------|-----------|---------|-----------|
| tar.gz / tgz | pigz | N hilos | DEFLATE |
| tar.xz / txz | xz -T0 | automático | LZMA2 |
| tar.bz2 / tbz2 | lbzip2/pbzip2 | N hilos | BWT |
| tar.bz3 | bzip3 -j N | N hilos | BWT mejorado |
| tar.zst / tzst | zstd -T0 | automático | Zstandard |
| tar.lz / tlz | plzip | N hilos | LZMA |
| tar.lrz | lrzip -p N | N hilos | LZMA+ZPAQ |
| .zip | 7z -mmt=on | automático | DEFLATE |
| .7z | 7zz/7z -mmt=on | automático | LZMA2 |
| .rar | rar -mtN | N hilos | RAR |
| .tar | tar | — | Solo empaquetado |
| .lz4 | lz4 | — | LZ4 |
| .br | brotli | — | Brotli |

## Uso

```bash
# Compresión
compresor -c -f gz documento.txt
compresor -c -f xz -v -p archivo.tar
compresor -c -f zst -o /tmp/ archivo.iso

# Descompresión (detección automática de formato)
compresor -d archivo.tar.gz
compresor -d -o /tmp/ archivo.zip

# Utilidades
compresor -t *.tar.gz       # verificar integridad
compresor -l archivo.7z      # listar contenido
compresor -r archivo.txt.gz  # leer a stdout
compresor --install-deps     # instalar dependencias faltantes
compresor --uninstall        # desinstalar del sistema
```

## Opciones principales

| Flag | Descripción |
|------|-------------|
| `-c -f FORMATO` | Comprimir en el formato indicado |
| `-d` | Descomprimir (detecta formato automáticamente) |
| `-o DIR` | Directorio de salida (defecto: `.`) |
| `-k` | Conservar archivos originales |
| `-v` | Modo verbose |
| `-p` | Barra de progreso (requiere `pv`) |
| `-n` | Modo simulacro (dry-run) |
| `-s N` | Dividir en partes de N MB |
| `-T N` | Forzar N hilos (0 = auto) |
| `-exclude patrón` | Excluir archivos por patrón |
| `-force` | Sobrescribir sin preguntar |
| `-opts "flags"` | Flags extra para el compresor |

## Ramas

- **main** — versión Go (desarrollo activo, recomendada)
- **bash** — versión original Bash (estable, solo mantenimiento)

## Historial

Proyecto nacido como script Bash, migrado a Go para tipado fuerte,
tests nativos y binario estático sin dependencias externas.

Ver `COMPRESOR.md` y `AGENTS.md` para detalles.

## Licencia

MIT
