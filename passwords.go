package main

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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

type PasswordEntry struct {
	Password string
	Count    int
}

type PasswordManager struct {
	mu            sync.RWMutex
	promptMu      sync.Mutex
	passwords     []string
	entries       []PasswordEntry
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

	fi, err := os.Lstat(pm.filePath)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || fi.IsDir() {
		return
	}

	f, err := os.Open(pm.filePath)
	if err != nil {
		return
	}
	defer f.Close()

	var entries []PasswordEntry
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		raw := strings.TrimRight(scanner.Text(), "\r\n")
		var pass string
		count := 1
		if strings.Contains(raw, "\t") {
			parts := strings.Split(raw, "\t")
			if len(parts) == 3 && parts[2] == "b64" {
				if dec, decErr := base64.StdEncoding.DecodeString(parts[0]); decErr == nil {
					pass = string(dec)
				}
				if c, err := strconv.Atoi(parts[1]); err == nil && c > 0 {
					count = c
				}
			} else if len(parts) >= 2 {
				pass = parts[0]
				if c, err := strconv.Atoi(parts[1]); err == nil && c > 0 {
					count = c
				}
			}
		} else {
			pass = strings.TrimSpace(raw)
		}
		if pass != "" && !seen[pass] {
			seen[pass] = true
			entries = append(entries, PasswordEntry{Password: pass, Count: count})
		}
	}
	if scanner.Err() != nil {
		return
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Count > entries[j].Count
	})
	pm.entries = entries
	pm.syncPasswordsLocked()
}

func (pm *PasswordManager) syncPasswordsLocked() {
	list := make([]string, len(pm.entries))
	for i, e := range pm.entries {
		list[i] = e.Password
	}
	pm.passwords = list
}

func (pm *PasswordManager) saveLocked() {
	if pm.filePath == "" {
		return
	}
	dir := filepath.Dir(pm.filePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return
	}
	if filepath.Base(dir) == "crush" {
		_ = os.Chmod(dir, 0700)
	}

	var sb strings.Builder
	for _, e := range pm.entries {
		if strings.ContainsAny(e.Password, "\t\r\n") {
			sb.WriteString(base64.StdEncoding.EncodeToString([]byte(e.Password)))
			sb.WriteString("\t")
			sb.WriteString(strconv.Itoa(e.Count))
			sb.WriteString("\tb64\n")
		} else {
			sb.WriteString(e.Password)
			sb.WriteString("\t")
			sb.WriteString(strconv.Itoa(e.Count))
			sb.WriteString("\n")
		}
	}

	tmpF, err := os.CreateTemp(dir, ".passwords.*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmpF.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.WriteString(tmpF, sb.String()); err != nil {
		_ = tmpF.Close()
		return
	}
	if err := tmpF.Sync(); err != nil {
		_ = tmpF.Close()
		return
	}
	if err := tmpF.Close(); err != nil {
		return
	}
	if err := os.Chmod(tmpPath, 0600); err != nil {
		return
	}
	if err := os.Rename(tmpPath, pm.filePath); err != nil {
		return
	}
	cleanup = false
}

func (pm *PasswordManager) AddSuccessful(pass string) {
	if pass == "" {
		return
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.sessionCached = pass

	found := false
	for i := range pm.entries {
		if pm.entries[i].Password == pass {
			pm.entries[i].Count++
			found = true
			break
		}
	}
	if !found {
		pm.entries = append(pm.entries, PasswordEntry{Password: pass, Count: 1})
	}

	sort.SliceStable(pm.entries, func(i, j int) bool {
		return pm.entries[i].Count > pm.entries[j].Count
	})

	pm.syncPasswordsLocked()
	pm.saveLocked()
}

func (pm *PasswordManager) GetStoredPasswords() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	res := make([]string, len(pm.passwords))
	copy(res, pm.passwords)
	return res
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

func probePasswordCmd(cmd *exec.Cmd) (started, valid bool, exitCode int, stderrStr string) {
	cmd.Stdin = getNullFile()
	if len(cmd.Env) == 0 {
		cmd.Env = append(os.Environ(), "LC_ALL=C")
	}
	errBuf := newLimitedBuffer(4096)
	cmd.Stderr = errBuf
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false, false, -1, ""
	}
	if err := cmd.Start(); err != nil {
		return false, false, -1, ""
	}
	buf := make([]byte, 256)
	n, _ := stdout.Read(buf)
	if n > 0 {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		return true, true, 0, ""
	}
	waitErr := cmd.Wait()
	if waitErr == nil {
		return true, true, 0, errBuf.String()
	}
	code := -1
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		code = exitErr.ExitCode()
	}
	return true, false, code, errBuf.String()
}

func probeStdoutPassword(cmd *exec.Cmd) (started, valid bool) {
	started, valid, _, _ = probePasswordCmd(cmd)
	return started, valid
}

func buildPasswordProbeCmd(file string, format Format, pass string) (toolUsed string, cmd *exec.Cmd) {
	passArg := "-p" + pass
	if pass == "" {
		passArg = "-p-"
	}
	safeFile := safeFlagArg(file)

	switch format {
	case SevenZ:
		sevenz := sevenzBin()
		if !hasTool(sevenz) {
			return "", nil
		}
		return "7z", execCommand(sevenz, "e", "-so", passArg, "-y", "--", safeFile)

	case Zip:
		sevenz := sevenzBin()
		if hasTool(sevenz) {
			return "7z", execCommand(sevenz, "e", "-so", passArg, "-y", "--", safeFile)
		}
		if !hasTool("unzip") {
			return "", nil
		}
		return "unzip", execCommand("unzip", "-p", "-P", pass, "--", safeFile)

	case Rar:
		rar := rarBin()
		if !hasTool(rar) {
			return "", nil
		}
		return rar, execCommand(rar, "p", passArg, "-inul", "--", safeFile)
	}
	return "", nil
}

func checkArchivePassword(file string, format Format, pass string) bool {
	if !formatSupportsPassword(format) {
		return true
	}
	toolUsed, cmd := buildPasswordProbeCmd(file, format, pass)
	if cmd == nil {
		return false
	}
	started, valid, _, _ := probePasswordCmd(cmd)
	if !started && format == Zip && toolUsed == "7z" && hasTool("unzip") {
		uCmd := execCommand("unzip", "-p", "-P", pass, "--", safeFlagArg(file))
		_, valid, _, _ = probePasswordCmd(uCmd)
	}
	return valid
}

func isSevenZipPasswordFailure(stderrStr string) bool {
	low := strings.ToLower(stderrStr)
	if strings.Contains(low, "wrong password") ||
		strings.Contains(low, "cannot open encrypted archive") ||
		strings.Contains(low, "data error in encrypted file") {
		return true
	}
	return strings.TrimSpace(stderrStr) == ""
}

func archiveRequiresPassword(file string, format Format) bool {
	if !formatSupportsPassword(format) {
		return false
	}
	toolUsed, cmd := buildPasswordProbeCmd(file, format, "")
	if cmd == nil {
		return false
	}
	started, valid, exitCode, stderrStr := probePasswordCmd(cmd)
	if !started && format == Zip && toolUsed == "7z" && hasTool("unzip") {
		toolUsed = "unzip"
		cmd = execCommand("unzip", "-p", "-P", "", "--", safeFlagArg(file))
		started, valid, exitCode, stderrStr = probePasswordCmd(cmd)
	}
	if !started || valid {
		return false
	}
	switch toolUsed {
	case "7z":
		return isSevenZipPasswordFailure(stderrStr)
	case "unzip":
		return exitCode == 82 || (exitCode != 9 && strings.Contains(strings.ToLower(stderrStr), "password"))
	default:
		if format == Rar {
			return exitCode == 11 || strings.Contains(strings.ToLower(stderrStr), "password")
		}
		return true
	}
}

func FileRequiresInteractivePassword(file string, cliPassword string) bool {
	info, err := DetectFormat(file)
	if err != nil || !info.Format.IsContainer() || !formatSupportsPassword(info.Format) {
		return false
	}
	if !archiveRequiresPassword(file, info.Format) {
		return false
	}
	pm := GetPasswordManager()
	candidates := pm.GetCandidates(cliPassword)
	for _, p := range candidates {
		if checkArchivePassword(file, info.Format, p) {
			pm.AddSuccessful(p)
			return false
		}
	}
	return true
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
