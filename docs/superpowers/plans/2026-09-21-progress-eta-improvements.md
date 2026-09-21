# Mejora de Barra de Progreso y Estimación de Tiempo (ETA) en Compresión y Descompresión

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminar las estimaciones de tiempo absurdas (como `157213h45m42s`) y el congelamiento del progreso en compresión y descompresión, implementando una función `formatETA` robusta con ventanas de estabilización y techo máximo, un parser multi-delimitador en `trackProgress` compatible con los caracteres `\b` (backspace) de 7-Zip, y seguimiento de lectura con `countingReader` en pipelines.

**Architecture:**
1. Crear una función `formatETA(elapsed time.Duration, current, total int64) string` en `util.go` que retorne `"--:--"` durante la fase inicial de arranque (`elapsed < 3s` o `pct < 1%`), límite superior `">24h"` para tiempos desmedidos, y formateo conciso (`Xs`, `XmYs`, `XhYmZs`).
2. Actualizar `trackProgress` para delimitar y parsear porcentajes no solo con `\r`, sino también con `\b` (`\x08`) y `\n`, permitiendo que herramientas como 7-Zip en Linux reporten su progreso real en tiempo real durante compresión y extracción.
3. Evitar que `pollFileProgress` sobreescriba regresivamente el progreso con tamaños parciales en disco (ej. cabecera de 32 bytes de 7z) mediante actualización monótona y exclusión cuando hay tracking activo.
4. En `compressSingleFile`, implementar `countingReader` sobre el flujo de entrada para medir con precisión el 0%-100% de los datos procesados en lugar de medir el archivo comprimido resultante.

**Tech Stack:** Go stdlib (`time`, `sync/atomic`, `bufio`, `io`, `fmt`, `os`, `os/exec`), testing con race detector (`go test -race`).

## Global Constraints
- Solo biblioteca estándar de Go (`stdlib`). Cero dependencias externas.
- Desarrollo TDD estricto: test en rojo primero, implementación mínima en verde, y refactor.
- Trabajo aislado en git worktree: rama `feat/progress-eta-improvements` en `../crush-progress-eta`.
- Verificación pre-commit obligatoria: `go test -v -race ./...` y `go vet ./...` en verde con 0 errores.
- Mantener compatibilidad con los 13 formatos soportados por crush.

---

### Task 1: Crear Worktree y Rama Dedicada

**Files:**
- Create: worktree en `../crush-progress-eta` con rama `feat/progress-eta-improvements`

- [ ] **Step 1: Crear rama y worktree**

```bash
git branch feat/progress-eta-improvements main
git worktree add ../crush-progress-eta feat/progress-eta-improvements
```

- [ ] **Step 2: Verificar estado del worktree**

```bash
cd ../crush-progress-eta && git status && go test ./...
```

---

### Task 2: Función formatETA y Formateo Confiable de Tiempos

**Files:**
- Modify: `util.go`
- Test: `util_test.go`

**Interfaces:**
- Produces: `formatETA(elapsed time.Duration, current, total int64) string`

- [ ] **Step 1: Escribir tests unitarios table-driven que fallen (Red)**

En `util_test.go`:
```go
func TestFormatETA(t *testing.T) {
	tests := []struct {
		name     string
		elapsed  time.Duration
		current  int64
		total    int64
		expected string
	}{
		{
			name:     "arranque temprano menor a 3 segundos",
			elapsed:  1 * time.Second,
			current:  1024,
			total:    1024 * 1024,
			expected: "--:--",
		},
		{
			name:     "porcentaje inicial menor a 1%",
			elapsed:  5 * time.Second,
			current:  32,
			total:    1400 * 1024 * 1024,
			expected: "--:--",
		},
		{
			name:     "tiempo estimado absurdo mayor a 24 horas",
			elapsed:  4 * time.Second,
			current:  1024 * 1024,
			total:    1000 * 1024 * 1024 * 1024, // 1TB a 250KB/s
			expected: ">24h",
		},
		{
			name:     "progreso normal en segundos",
			elapsed:  10 * time.Second,
			current:  20 * 1024 * 1024,
			total:    30 * 1024 * 1024, // 2MB/s -> 10MB restantes = 5s
			expected: "5s",
		},
		{
			name:     "progreso normal minutos y segundos",
			elapsed:  60 * time.Second,
			current:  60 * 1024 * 1024,
			total:    1000 * 1024 * 1024, // 1MB/s -> 940s = 15m40s
			expected: "15m40s",
		},
		{
			name:     "progreso normal horas minutos segundos",
			elapsed:  60 * time.Second,
			current:  10 * 1024 * 1024,
			total:    730 * 1024 * 1024, // 10MB/min -> 720MB restantes = 72min = 1h12m00s
			expected: "1h12m00s",
		},
		{
			name:     "completado o valores invalidos",
			elapsed:  10 * time.Second,
			current:  100,
			total:    100,
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatETA(tc.elapsed, tc.current, tc.total)
			if got != tc.expected {
				t.Errorf("formatETA() = %q, esperado %q", got, tc.expected)
			}
		})
	}
}
```

- [ ] **Step 2: Ejecutar test y verificar que falle (Red)**

```bash
go test -v -run TestFormatETA ./...
```
Esperado: error de compilación (`undefined: formatETA`).

- [ ] **Step 3: Implementar formatETA en util.go y actualizar fileLine y globalBarLine (Green)**

En `util.go`:
```go
func formatETA(elapsed time.Duration, current, total int64) string {
	if current <= 0 || total <= 0 || current >= total {
		return ""
	}
	if elapsed < 3*time.Second {
		return "--:--"
	}
	pct := float64(current) * 100.0 / float64(total)
	if pct < 1.0 {
		return "--:--"
	}
	rate := float64(current) / elapsed.Seconds()
	if rate <= 0 {
		return "--:--"
	}
	remainingSecs := float64(total-current) / rate
	remaining := time.Duration(remainingSecs * float64(time.Second))

	if remaining > 24*time.Hour {
		return ">24h"
	}
	if remaining < time.Minute {
		return fmt.Sprintf("%ds", int(remaining.Seconds()))
	}
	if remaining < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(remaining.Minutes()), int(remaining.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm%02ds", int(remaining.Hours()), int(remaining.Minutes())%60, int(remaining.Seconds())%60)
}
```

Reemplazar en `fileLine` (líneas 925-931):
```go
		if current > 0 && fp.Size > 0 && current < fp.Size {
			elapsed := time.Since(fp.StartTime())
			if eta := formatETA(elapsed, current, fp.Size); eta != "" {
				line += "  " + eta
			}
		}
```

Reemplazar en `globalBarLine` (líneas 889-892):
```go
	if total > 0 && current > 0 && current < total {
		if eta := formatETA(elapsed, current, total); eta != "" {
			line += fmt.Sprintf("  %s restantes", eta)
		}
	}
```

- [ ] **Step 4: Ejecutar tests y verificar que pasen (Pass)**

```bash
go test -v -run TestFormatETA ./...
```

- [ ] **Step 5: Commit del cambio de formatETA**

```bash
git add util.go util_test.go
git commit -m "fix(progress): implementar formatETA con ventana de estabilización y techo máximo"
```

---

### Task 3: Parser Multi-Delimitador en trackProgress (Compatibilidad 7-Zip \b, \r, \n)

**Files:**
- Modify: `util.go`
- Test: `util_test.go`

- [ ] **Step 1: Escribir test que simule el stream de 7-Zip con backspaces (\b)**

En `util_test.go`:
```go
func TestTrackProgressSevenZipBackspaces(t *testing.T) {
	pt := NewProgressTracker(1000, 1)
	fp := &FileProgress{Name: "test.bin", Size: 1000}
	fp.SetStatus("active")

	// 7z stream simulado con \x08 (backspaces), \r y \n
	rawOutput := "\nScanning drive...\n  0%\x08\x08\x08\x08 25% + test.bin\x08\x08\x08\x08\x08\x08\x08\x08 75%\n100%\n"
	r := strings.NewReader(rawOutput)

	trackProgress(r, pt, 1000, fp)

	if fp.Current.Load() != 1000 {
		t.Errorf("esperado fp.Current = 1000, obtenido %d", fp.Current.Load())
	}
	if pt.current.Load() != 1000 {
		t.Errorf("esperado pt.current = 1000, obtenido %d", pt.current.Load())
	}
}
```

- [ ] **Step 2: Ejecutar test y comprobar que falle con la implementación anterior (Red)**

```bash
go test -v -run TestTrackProgressSevenZipBackspaces ./...
```
Esperado: Fallo porque `ReadString('\r')` no procesa los porcentajes separados por `\x08` o `\n`.

- [ ] **Step 3: Implementar parser multi-delimitador en trackProgress (Green)**

En `util.go`:
```go
func trackProgress(r io.Reader, pt *ProgressTracker, fileSize int64, fp *FileProgress) {
	if pt == nil || fileSize == 0 {
		return
	}
	if fp != nil {
		fp.hasExternalProgress = true
	}
	br := bufio.NewReader(r)
	lastPct := -1
	var token strings.Builder

	for {
		b, err := br.ReadByte()
		if err != nil {
			if token.Len() > 0 {
				pct := parsePercent(token.String())
				if pct >= 0 && pct > lastPct {
					delta := int64(float64(pct-lastPct) / 100.0 * float64(fileSize))
					if delta > 0 {
						pt.Add(delta)
						if fp != nil {
							fp.Current.Add(delta)
						}
					}
					lastPct = pct
				}
			}
			break
		}

		if b == '\r' || b == '\n' || b == '\x08' {
			if token.Len() > 0 {
				pct := parsePercent(token.String())
				if pct >= 0 && pct > lastPct {
					delta := int64(float64(pct-lastPct) / 100.0 * float64(fileSize))
					if delta > 0 {
						pt.Add(delta)
						if fp != nil {
							fp.Current.Add(delta)
						}
					}
					lastPct = pct
				}
				token.Reset()
			}
		} else {
			token.WriteByte(b)
		}
	}

	if lastPct >= 0 && lastPct < 100 {
		delta := int64(float64(100-lastPct) / 100.0 * float64(fileSize))
		if delta > 0 {
			pt.Add(delta)
			if fp != nil {
				fp.Current.Add(delta)
			}
		}
	}
}
```

- [ ] **Step 4: Ejecutar test y verificar que pase (Pass)**

```bash
go test -v -run TestTrackProgressSevenZipBackspaces ./...
```

- [ ] **Step 5: Commit del parser multi-delimitador**

```bash
git add util.go util_test.go
git commit -m "fix(progress): soportar delimitadores \b, \r, \n en trackProgress para 7-Zip"
```

---

### Task 4: Protección Monótona en pollFileProgress y countingReader para Pipes

**Files:**
- Modify: `util.go`, `compress.go`
- Test: `util_test.go`, `compress_test.go`

- [ ] **Step 1: Test para pollFileProgress que asegure no regresión y respeto a hasExternalProgress**

En `util_test.go`:
```go
func TestPollFileProgressMonotonic(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "poll_test_*.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Write(make([]byte, 32))
	tmpFile.Close()

	fp := &FileProgress{Name: "test", Size: 1000}
	fp.SetStatus("active")
	fp.SetOutPath(tmpFile.Name())
	fp.Current.Store(500) // Ya tiene 500 bytes por tracking

	pollFileProgress(fp)

	if fp.Current.Load() != 500 {
		t.Errorf("pollFileProgress sobreescribió progreso mayor (esperado 500, obtenido %d)", fp.Current.Load())
	}
}
```

- [ ] **Step 2: Implementar countingReader y actualización monótona en util.go y compress.go**

En `util.go`:
```go
type countingReader struct {
	r  io.Reader
	pt *ProgressTracker
	fp *FileProgress
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	if n > 0 {
		if cr.pt != nil {
			cr.pt.Add(int64(n))
		}
		if cr.fp != nil {
			cr.fp.Current.Add(int64(n))
		}
	}
	return n, err
}
```

En `pollFileProgress` (`util.go`):
```go
func pollFileProgress(fp *FileProgress) {
	if fp == nil || fp.Status() != "active" || fp.OutPath() == "" || fp.Size <= 0 {
		return
	}
	if fp.hasExternalProgress {
		return
	}
	fi, err := os.Stat(fp.OutPath())
	if err != nil {
		return
	}
	newSize := fi.Size()
	for {
		curr := fp.Current.Load()
		if newSize <= curr {
			break
		}
		if fp.Current.CompareAndSwap(curr, newSize) {
			break
		}
	}
}
```

En `compress.go` (`compressSingleFile`):
Para formatos tar-based con pipe (`gz`, `xz`, `bz2`, etc.):
Envolver `inFile` con `countingReader` en lugar de envolver `outFile` con `countingWriter`, para que el progreso refleje los bytes no comprimidos leídos (0% a 100% exacto):
```go
		var inReader io.Reader = inFile
		if opts.Progress != nil {
			inReader = &countingReader{r: inFile, pt: opts.Progress, fp: fp}
		}
		compressCmd.Stdin = inReader
		compressCmd.Stdout = writer
```

- [ ] **Step 3: Ejecutar todos los tests del paquete con -race**

```bash
go test -v -race ./...
```

- [ ] **Step 4: Commit de la protección monótona y countingReader**

```bash
git add util.go util_test.go compress.go
git commit -m "fix(compress): usar countingReader en input pipe y actualización monótona en pollFileProgress"
```

---

### Task 5: Verificación Global y Limpieza

**Files:**
- Merge: `feat/progress-eta-improvements` -> `main`
- Cleanup: eliminar worktree y rama
- Docs: actualizar `AGENTS.md` y compilar binario final

- [ ] **Step 1: Ejecutar suite de pruebas completa y go vet en el worktree**

```bash
go test -v -race ./...
go vet ./...
```

- [ ] **Step 2: Mergear a main y eliminar worktree**

```bash
cd /baul/aplicaciones/crush
git merge feat/progress-eta-improvements --no-edit
git worktree remove ../crush-progress-eta
git branch -d feat/progress-eta-improvements
```

- [ ] **Step 3: Actualizar AGENTS.md y compilar binario**

Actualizar `AGENTS.md` registrando la corrección del cálculo de ETA y soporte multi-delimitador en `trackProgress`.
```bash
make build
sudo install -m 755 crush /usr/local/bin/
```
