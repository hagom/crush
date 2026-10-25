package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	ErrPasswordSkipped = errors.New("descompresión omitida por el usuario (sin contraseña)")
	globalPasswordMu   sync.Mutex
	globalPasswordMgr  *PasswordManager
	isTerminalFunc     = isTerminal
)

func GetPasswordManager() *PasswordManager {
	globalPasswordMu.Lock()
	defer globalPasswordMu.Unlock()
	if globalPasswordMgr == nil {
		globalPasswordMgr = NewPasswordManager("")
	}
	return globalPasswordMgr
}

func SetPasswordManager(pm *PasswordManager) func() {
	globalPasswordMu.Lock()
	old := globalPasswordMgr
	globalPasswordMgr = pm
	globalPasswordMu.Unlock()
	return func() {
		globalPasswordMu.Lock()
		globalPasswordMgr = old
		globalPasswordMu.Unlock()
	}
}

type PasswordManager struct {
	mu            sync.RWMutex
	promptMu      sync.Mutex
	passwords     []string
	sessionCached string
	filePath      string
	promptFunc    func(file string) (string, error)
}

func defaultPasswordFilePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(configDir, "crush", "passwords.txt")
}

func NewPasswordManager(filePath string) *PasswordManager {
	if filePath == "" {
		filePath = defaultPasswordFilePath()
	}
	pm := &PasswordManager{
		filePath: filePath,
	}
	pm.load()
	return pm
}

func (pm *PasswordManager) load() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	f, err := os.Open(pm.filePath)
	if err != nil {
		return
	}
	defer f.Close()

	var list []string
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		p := strings.TrimSpace(scanner.Text())
		if p != "" && !seen[p] {
			seen[p] = true
			list = append(list, p)
		}
	}
	pm.passwords = list
}

func (pm *PasswordManager) saveLocked() {
	if pm.filePath == "" {
		return
	}
	dir := filepath.Dir(pm.filePath)
	_ = os.MkdirAll(dir, 0700)

	var sb strings.Builder
	for _, p := range pm.passwords {
		sb.WriteString(p)
		sb.WriteString("\n")
	}
	_ = os.WriteFile(pm.filePath, []byte(sb.String()), 0600)
}

func (pm *PasswordManager) AddSuccessful(pass string) {
	if pass == "" {
		return
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.sessionCached = pass

	var newList []string
	newList = append(newList, pass)
	for _, p := range pm.passwords {
		if p != pass {
			newList = append(newList, p)
		}
	}
	pm.passwords = newList
	pm.saveLocked()
}

func (pm *PasswordManager) GetSessionCached() string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.sessionCached
}

func (pm *PasswordManager) GetCandidates(cliPassword string) []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var candidates []string
	seen := make(map[string]bool)

	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			candidates = append(candidates, p)
		}
	}

	if cliPassword != "" {
		add(cliPassword)
	}
	if pm.sessionCached != "" {
		add(pm.sessionCached)
	}
	for _, p := range pm.passwords {
		add(p)
	}

	return candidates
}

func formatSupportsPassword(f Format) bool {
	return f == SevenZ || f == Zip || f == Rar
}

func checkArchivePassword(file string, format Format, pass string) bool {
	if !formatSupportsPassword(format) {
		return true
	}
	switch format {
	case SevenZ:
		sevenz := sevenzBin()
		if !hasTool(sevenz) {
			return false
		}
		cmd := execCommand(sevenz, "t", "-p"+pass, "-y", file)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		return cmd.Run() == nil

	case Zip:
		sevenz := sevenzBin()
		if hasTool(sevenz) {
			cmd := execCommand(sevenz, "t", "-p"+pass, "-y", file)
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			return cmd.Run() == nil
		}
		args := []string{"-t"}
		if pass != "" {
			args = append(args, "-P", pass)
		}
		args = append(args, file)
		cmd := execCommand("unzip", args...)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		return cmd.Run() == nil

	case Rar:
		rar := rarBin()
		if !hasTool(rar) {
			return false
		}
		cmd := execCommand(rar, "t", "-p"+pass, "-y", file)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		return cmd.Run() == nil
	}
	return false
}

func archiveRequiresPassword(file string, format Format) bool {
	return !checkArchivePassword(file, format, "")
}

func (pm *PasswordManager) ResolvePassword(file string, format Format, cliPassword string) (string, error) {
	if !formatSupportsPassword(format) {
		return "", nil
	}

	if !archiveRequiresPassword(file, format) {
		return "", nil
	}

	if cliPassword != "" {
		if checkArchivePassword(file, format, cliPassword) {
			pm.AddSuccessful(cliPassword)
			return cliPassword, nil
		}
		return "", fmt.Errorf("contraseña incorrecta para %s", filepath.Base(file))
	}

	candidates := pm.GetCandidates("")
	for _, p := range candidates {
		if checkArchivePassword(file, format, p) {
			pm.AddSuccessful(p)
			return p, nil
		}
	}

	// Si ninguna contraseña conocida funciona, pedir al usuario de forma sincronizada
	pm.promptMu.Lock()
	defer pm.promptMu.Unlock()

	// Doble verificación: mientras esperaba el lock, ¿otro hilo resolvió la contraseña?
	cached := pm.GetSessionCached()
	if cached != "" && checkArchivePassword(file, format, cached) {
		return cached, nil
	}

	promptFn := pm.promptFunc
	if promptFn == nil {
		promptFn = defaultPromptUserPassword
	}

	base := filepath.Base(file)
	var resolved string
	var resolveErr error

	SuspendActiveProgress(func() {
		for attempt := 1; attempt <= 3; attempt++ {
			pass, err := promptFn(file)
			if err != nil {
				resolveErr = err
				return
			}
			pass = strings.TrimSpace(pass)
			if pass == "" {
				resolveErr = fmt.Errorf("%w: %s", ErrPasswordSkipped, base)
				return
			}

			if checkArchivePassword(file, format, pass) {
				pm.AddSuccessful(pass)
				resolved = pass
				return
			}

			if attempt < 3 {
				fmt.Fprintf(os.Stderr, "  %s✗ Contraseña incorrecta para '%s' (intento %d/3).%s\n", Red, base, attempt, NC)
			}
		}
		resolveErr = fmt.Errorf("contraseña incorrecta para %s tras 3 intentos", base)
	})

	if resolveErr != nil {
		return "", resolveErr
	}
	return resolved, nil
}

func defaultPromptUserPassword(file string) (string, error) {
	if !isTerminalFunc() {
		return "", fmt.Errorf("el archivo %s requiere contraseña (terminal no interactiva)", filepath.Base(file))
	}
	base := filepath.Base(file)
	fmt.Fprintf(os.Stderr, "\n%s🔒 '%s' está protegido con contraseña.%s\n", Yellow, base, NC)
	prompt := fmt.Sprintf("  Ingrese contraseña para '%s' (o Enter para omitir): ", base)
	pass, err := readPasswordFunc(prompt)
	if err != nil {
		return "", err
	}
	return pass, nil
}
