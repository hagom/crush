package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestPasswordManager_LoadAndSave(t *testing.T) {
	tmpDir := t.TempDir()
	passFile := filepath.Join(tmpDir, "passwords.txt")

	// 1. Guardar contraseñas iniciales
	initial := "pass1\npass2\npass1\n\n  pass3  \n"
	if err := os.WriteFile(passFile, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}

	pm := NewPasswordManager(passFile)
	if len(pm.passwords) != 3 {
		t.Fatalf("esperaba 3 contraseñas deduplicadas, obtuvo %d: %v", len(pm.passwords), pm.passwords)
	}
	if pm.passwords[0] != "pass1" || pm.passwords[1] != "pass2" || pm.passwords[2] != "pass3" {
		t.Errorf("orden o valores inesperados: %v", pm.passwords)
	}

	// 2. AddSuccessful mueve al inicio (MRU) y persiste
	pm.AddSuccessful("pass2")
	if pm.passwords[0] != "pass2" || pm.passwords[1] != "pass1" || pm.passwords[2] != "pass3" {
		t.Errorf("MRU falló al mover pass2 al inicio: %v", pm.passwords)
	}

	// 3. Verificar permisos 0600 en disco
	fi, err := os.Stat(passFile)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("permisos de archivo incorrectos: %v (esperaba 0600)", fi.Mode().Perm())
	}

	// 4. Recargar en nueva instancia
	pm2 := NewPasswordManager(passFile)
	if len(pm2.passwords) != 3 || pm2.passwords[0] != "pass2" {
		t.Errorf("recarga de contraseñas falló: %v", pm2.passwords)
	}
}

func TestPasswordManager_SessionCache(t *testing.T) {
	tmpDir := t.TempDir()
	passFile := filepath.Join(tmpDir, "passwords.txt")
	pm := NewPasswordManager(passFile)

	pm.AddSuccessful("sessionSecret")
	if pm.GetSessionCached() != "sessionSecret" {
		t.Errorf("sessionCached esperaba 'sessionSecret', obtuvo %q", pm.GetSessionCached())
	}

	candidates := pm.GetCandidates("")
	if len(candidates) == 0 || candidates[0] != "sessionSecret" {
		t.Errorf("candidatos esperaba 'sessionSecret' de primero, obtuvo %v", candidates)
	}

	// Con CLI password explícita, cliPassword va primero
	candidatesCli := pm.GetCandidates("cliOverride")
	if len(candidatesCli) < 2 || candidatesCli[0] != "cliOverride" || candidatesCli[1] != "sessionSecret" {
		t.Errorf("candidatos con cli esperaba ['cliOverride', 'sessionSecret'], obtuvo %v", candidatesCli)
	}
}

func TestPasswordManager_ResolvePassword_AutoFromRegistry(t *testing.T) {
	if !hasTool(sevenzBin()) {
		t.Skip("7z no disponible")
	}

	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "file.txt")
	_ = os.WriteFile(f1, []byte("contenido secreto"), 0644)
	arch7z := filepath.Join(tmpDir, "secret.7z")

	// Crear archivo 7z con contraseña "autoPass123"
	cmd := exec.Command(sevenzBin(), "a", "-pautoPass123", "-mhe=on", arch7z, f1)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("error creando 7z: %v, salida: %s", err, string(out))
	}

	passFile := filepath.Join(tmpDir, "passwords.txt")
	// Guardar en el registro una lista que incluye autoPass123
	_ = os.WriteFile(passFile, []byte("otraClave\nautoPass123\notraMas\n"), 0600)

	pm := NewPasswordManager(passFile)

	// No se debe llamar al prompt porque está en el registro
	pm.promptFunc = func(file string) (string, error) {
		t.Fatal("no debió pedir contraseña interactiva si estaba en el registro")
		return "", nil
	}

	resolved, err := pm.ResolvePassword(arch7z, SevenZ, "")
	if err != nil {
		t.Fatalf("ResolvePassword falló: %v", err)
	}
	if resolved != "autoPass123" {
		t.Errorf("esperaba 'autoPass123', obtuvo %q", resolved)
	}

	// Verificar que quedó como sessionCached
	if pm.GetSessionCached() != "autoPass123" {
		t.Errorf("sessionCached no se actualizó: %q", pm.GetSessionCached())
	}
}

func TestPasswordManager_ResolvePassword_PromptAndSave(t *testing.T) {
	if !hasTool(sevenzBin()) {
		t.Skip("7z no disponible")
	}

	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "file.txt")
	_ = os.WriteFile(f1, []byte("contenido"), 0644)
	arch7z := filepath.Join(tmpDir, "prompt.7z")

	cmd := exec.Command(sevenzBin(), "a", "-pnuevaClave456", arch7z, f1)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("error creando 7z: %v, salida: %s", err, string(out))
	}

	passFile := filepath.Join(tmpDir, "passwords.txt")
	pm := NewPasswordManager(passFile)

	promptCalled := 0
	pm.promptFunc = func(file string) (string, error) {
		promptCalled++
		return "nuevaClave456", nil
	}

	// 1. Primer archivo: debe pedirla por prompt
	resolved, err := pm.ResolvePassword(arch7z, SevenZ, "")
	if err != nil {
		t.Fatalf("primer ResolvePassword falló: %v", err)
	}
	if resolved != "nuevaClave456" {
		t.Errorf("esperaba 'nuevaClave456', obtuvo %q", resolved)
	}
	if promptCalled != 1 {
		t.Errorf("esperaba 1 llamada al prompt, obtuvo %d", promptCalled)
	}

	// 2. Segundo archivo con la misma clave: debe reutilizar sessionCached sin volver a pedir
	resolved2, err := pm.ResolvePassword(arch7z, SevenZ, "")
	if err != nil {
		t.Fatalf("segundo ResolvePassword falló: %v", err)
	}
	if resolved2 != "nuevaClave456" {
		t.Errorf("esperaba 'nuevaClave456', obtuvo %q", resolved2)
	}
	if promptCalled != 1 {
		t.Errorf("segundo archivo NO debió llamar al prompt, llamadas: %d", promptCalled)
	}

	// 3. Verificar que se persistió en disco
	data, _ := os.ReadFile(passFile)
	if string(data) != "nuevaClave456\n" {
		t.Errorf("archivo en disco esperado 'nuevaClave456\\n', obtenido %q", string(data))
	}
}

func TestPasswordManager_ResolvePassword_SkipOnEmpty(t *testing.T) {
	if !hasTool(sevenzBin()) {
		t.Skip("7z no disponible")
	}

	tmpDir := t.TempDir()
	f1 := filepath.Join(tmpDir, "file.txt")
	_ = os.WriteFile(f1, []byte("contenido"), 0644)
	arch7z := filepath.Join(tmpDir, "skip.7z")

	cmd := exec.Command(sevenzBin(), "a", "-punknownPass", arch7z, f1)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("error creando 7z: %v, salida: %s", err, string(out))
	}

	passFile := filepath.Join(tmpDir, "passwords.txt")
	pm := NewPasswordManager(passFile)

	// Usuario presiona Enter (cadena vacía) para omitir
	pm.promptFunc = func(file string) (string, error) {
		return "", nil
	}

	_, err := pm.ResolvePassword(arch7z, SevenZ, "")
	if err == nil {
		t.Fatal("esperaba error por omisión, obtuvo nil")
	}
	if !errors.Is(err, ErrPasswordSkipped) {
		t.Errorf("esperaba ErrPasswordSkipped, obtuvo %v", err)
	}
}

func TestPasswordManager_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	passFile := filepath.Join(tmpDir, "passwords.txt")
	pm := NewPasswordManager(passFile)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			pm.AddSuccessful("concurrentPass")
			_ = pm.GetSessionCached()
			_ = pm.GetCandidates("")
		}(i)
	}
	wg.Wait()

	if pm.GetSessionCached() != "concurrentPass" {
		t.Errorf("esperaba 'concurrentPass', obtuvo %q", pm.GetSessionCached())
	}
}
