# COMPRESOR

Herramienta multi-formato de compresión y descompresión vía pipe, con soporte para 13 formatos, detección automática de gestor de paquetes, dry-run, split, exclusión de patrones y verificación de integridad.

## Instalación

```bash
# Compilar desde fuente (Go 1.21+)
go build -o compresor .

# O simplemente instalar dependencias y usar el script Bash
# (ver rama bash/)
```

## Formatos

| Formato | Tool | Algoritmo |
|---------|------|-----------|
| tar.gz / tgz | pigz/gzip | DEFLATE |
| tar.xz / txz | xz | LZMA2 |
| tar.bz2 / tbz2 | lbzip2/pbzip2/bzip2 | BWT |
| tar.bz3 | bzip3 | BWT mejorado |
| tar.zst / tzst | zstd | Zstandard |
| tar.lz / tlz | plzip | LZMA |
| tar.lrz | lrzip | LZMA+ZPAQ |
| .zip | zip/unzip | DEFLATE |
| .7z | 7zz/7z/7za | LZMA2 |
| .tar | tar | Solo empaquetado |
| .rar | rar/unrar | RAR |
| .lz4 | lz4 | LZ4 |
| .br | brotli | Brotli |

## Uso rápido

```bash
# Compresión
compresor -c -f gz documento.txt
compresor -c -f xz -v archivo.tar
compresor -c -f zip -o /tmp/ varios_archivos.txt

# Descompresión
compresor -d archivo.tar.gz
compresor -d -o /tmp/ archivo.zip

# Utilidades
compresor -t *.tar.gz      # verificar integridad
compresor -l archivo.7z     # listar contenido
compresor -r archivo.txt.gz | head  # leer a stdout

# Instalar dependencias faltantes
compresor --install
```

## Ramas

- **main** — documentación del proyecto
- **bash** — versión original en Bash Script (estable)
- **go** — versión migrada a Go (desarrollo activo)

## Historial del proyecto

El proyecto nació como un script Bash (`compresor.sh`) y se migró a Go para obtener tipado fuerte, tests nativos y un binario estático sin dependencias externas. La versión Go replica toda la funcionalidad del Bash y añade tests unitarios.

Ver `COMPRESOR.md` y `AGENTS.md` para más detalles.

## Licencia

MIT
