# Plan de Implementación: Actualización Go 1.27, Descompresión Adaptativa por Espacio y README Moderno

## 1. Objetivos

1. **Actualización de Go en `go.mod` y CI:**
   - Actualizar `go.mod` a `go 1.27`.
   - Ajustar el flujo de CI en `.github/workflows/ci.yml` para compilar y testear con `1.27` / toolchain moderno.
   - Verificar compatibilidad de compilador y tests sin warnings.

2. **Soporte de argumento de directorio en `crush -d <directorio>`:**
   - Permitir `crush -d <directorio>` de forma idéntica a ejecutar `crush -d` dentro de dicho directorio.
   - Escaneo automático con `FindDecompressibleFiles(dir)`, visualización de elementos detectados y prompt interactivo `PromptDecompressAll`.

3. **Descompresión adaptativa por capacidad y tandas iterativas (Capacity-Aware Waves):**
   - Eliminar el fallo prematuro por suma total de espacio requerido cuando el espacio libre disponible en disco es menor al 100% de la cola pero suficiente para procesar subconjuntos de archivos.
   - Algoritmo de planificación por tandas (*adaptive waves*):
     1. Medir espacio disponible en el directorio destino (`GetAvailBytes`).
     2. Seleccionar el grupo de archivos cuyos requerimientos acumulados quepan holgadamente dentro del espacio disponible actual.
     3. Si ningún archivo cabe, emitir advertencia detallada de espacio insuficiente con cálculo exacto de GB faltantes.
     4. Si al menos uno cabe, ejecutar la descompresión de esa tanda.
     5. Al concluir la tanda, con los archivos originales eliminados (`!opts.KeepOrig`, comportamiento por omisión) o espacio liberado, volver a medir el espacio real libre en disco.
     6. Evaluar la siguiente tanda con los archivos restantes.
     7. Repetir hasta procesar todos los archivos posibles o reportar con precisión los que no pudieron descomprimirse por espacio físico insuficiente.

4. **Modernización estética del `README.md`:**
   - Look visual moderno, profesional y de alta gama para GitHub (badges estilizados en shields.io, callouts informativos `> [!TIP]`, tablas comparativas limpias, showcase visual de características).

5. **Actualización de `AGENTS.md` e Instalación Global:**
   - Registrar la nueva funcionalidad y arquitectura en `AGENTS.md`.
   - Compilar e instalar globalmente en `/usr/local/bin/crush` con permisos de ejecución.
