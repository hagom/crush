package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type PkgManager struct {
	Name    string
	Install string
	Update  string
	Query   string
	Prune   string
}

var managers = []PkgManager{
	{Name: "apt-get", Install: "apt-get install -y", Update: "apt-get update", Query: "dpkg-query -W -f='${Status}'", Prune: "apt-get autoremove -y"},
	{Name: "dnf", Install: "dnf install -y", Update: "dnf check-update", Query: "rpm -q", Prune: "dnf autoremove -y"},
	{Name: "yum", Install: "yum install -y", Update: "yum check-update", Query: "rpm -q", Prune: "yum autoremove -y"},
	{Name: "zypper", Install: "zypper install -y", Update: "zypper refresh", Query: "rpm -q", Prune: "zypper rm -u"},
	{Name: "pacman", Install: "pacman -S --noconfirm", Update: "pacman -Sy", Query: "pacman -Q", Prune: "pacman -Rns $(pacman -Qdtq) --noconfirm"},
	{Name: "emerge", Install: "emerge --ask=n --oneshot", Update: "emerge --sync", Query: "qlist -I", Prune: "emerge --depclean"},
	{Name: "apk", Install: "apk add", Update: "apk update", Query: "apk info -e", Prune: "apk del"},
}

type ToolInfo struct {
	Name    string
	DebPkg  string
	RpmPkg  string
	ArchPkg string
	Gentoo  string
	ApkPkg  string
	Zypper  string
}

var tools = []ToolInfo{
	// Compression tools
	{Name: "pigz", DebPkg: "pigz", RpmPkg: "pigz", ArchPkg: "pigz", Gentoo: "app-arch/pigz", ApkPkg: "pigz", Zypper: "pigz"},
	{Name: "xz", DebPkg: "xz-utils", RpmPkg: "xz", ArchPkg: "xz", Gentoo: "app-arch/xz-utils", ApkPkg: "xz", Zypper: "xz"},
	{Name: "lbzip2", DebPkg: "lbzip2", RpmPkg: "lbzip2", ArchPkg: "lbzip2", Gentoo: "app-arch/lbzip2", ApkPkg: "lbzip2", Zypper: "lbzip2"},
	{Name: "pbzip2", DebPkg: "pbzip2", RpmPkg: "pbzip2", ArchPkg: "pbzip2", Gentoo: "app-arch/pbzip2", ApkPkg: "pbzip2", Zypper: "pbzip2"},
	{Name: "bzip3", DebPkg: "bzip3", RpmPkg: "bzip3", ArchPkg: "bzip3", Gentoo: "app-arch/bzip3", ApkPkg: "bzip3", Zypper: "bzip3"},
	{Name: "zstd", DebPkg: "zstd", RpmPkg: "zstd", ArchPkg: "zstd", Gentoo: "app-arch/zstd", ApkPkg: "zstd", Zypper: "zstd"},
	{Name: "plzip", DebPkg: "plzip", RpmPkg: "plzip", ArchPkg: "plzip", Gentoo: "app-arch/plzip", ApkPkg: "plzip", Zypper: "plzip"},
	{Name: "lrzip", DebPkg: "lrzip", RpmPkg: "lrzip", ArchPkg: "lrzip", Gentoo: "app-arch/lrzip", ApkPkg: "lrzip", Zypper: "lrzip"},
	{Name: "zip", DebPkg: "zip", RpmPkg: "zip", ArchPkg: "zip", Gentoo: "app-arch/zip", ApkPkg: "zip", Zypper: "zip"},
	{Name: "unzip", DebPkg: "unzip", RpmPkg: "unzip", ArchPkg: "unzip", Gentoo: "app-arch/unzip", ApkPkg: "unzip", Zypper: "unzip"},
	{Name: "p7zip", DebPkg: "p7zip-full", RpmPkg: "p7zip-plugins", ArchPkg: "p7zip", Gentoo: "app-arch/p7zip", ApkPkg: "p7zip", Zypper: "p7zip"},
	{Name: "rar", DebPkg: "rar", RpmPkg: "rar", ArchPkg: "rar", Gentoo: "app-arch/rar", ApkPkg: "rar", Zypper: "rar"},
	// Listing tools
	{Name: "tar", DebPkg: "tar", RpmPkg: "tar", ArchPkg: "tar", Gentoo: "app-arch/tar", ApkPkg: "tar", Zypper: "tar"},
	{Name: "numfmt", DebPkg: "coreutils", RpmPkg: "coreutils", ArchPkg: "coreutils", Gentoo: "sys-apps/coreutils", ApkPkg: "coreutils", Zypper: "coreutils"},
	{Name: "pv", DebPkg: "pv", RpmPkg: "pv", ArchPkg: "pv", Gentoo: "app-shells/pv", ApkPkg: "pv", Zypper: "pv"},
	{Name: "getconf", DebPkg: "libc-bin", RpmPkg: "glibc-utils", ArchPkg: "glibc", Gentoo: "sys-libs/glibc", ApkPkg: "musl-utils", Zypper: "glibc"},
}

func toolPkgSlice(t ToolInfo, mgr PkgManager) string {
	switch mgr.Name {
	case "apt-get":
		return t.DebPkg
	case "dnf", "yum":
		return t.RpmPkg
	case "pacman":
		return t.ArchPkg
	case "emerge":
		return t.Gentoo
	case "apk":
		return t.ApkPkg
	case "zypper":
		return t.Zypper
	default:
		return t.DebPkg
	}
}

func DetectPkgManager() *PkgManager {
	for i := range managers {
		if _, err := exec.LookPath(managers[i].Name); err == nil {
			return &managers[i]
		}
	}

	// Detect by common files
	checks := []struct {
		path  string
		index int
	}{
		{"/usr/bin/apt-get", 0},
		{"/usr/bin/dnf", 1},
		{"/usr/bin/yum", 2},
		{"/usr/bin/zypper", 3},
		{"/usr/bin/pacman", 4},
		{"/usr/bin/emerge", 5},
		{"/sbin/apk", 6},
	}
	for _, c := range checks {
		if _, err := exec.LookPath(c.path); err == nil {
			return &managers[c.index]
		}
	}

	return nil
}

func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s: %w", cmd.String(), strings.TrimSpace(string(out)), err)
	}
	return nil
}

func runCmdWithOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %s: %w", cmd.String(), strings.TrimSpace(string(out)), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func isToolInstalled(mgr *PkgManager, pkg string) bool {
	if mgr == nil {
		return false
	}
	fields := strings.Fields(mgr.Query)
	switch mgr.Name {
	case "apt-get":
		raw, err := runCmdWithOutput(fields[0], append(fields[1:], pkg)...)
		if err != nil {
			return false
		}
		return strings.Contains(raw, "install ok installed")
	case "pacman":
		_, err := runCmdWithOutput(fields[0], append(fields[1:], pkg)...)
		return err == nil
	case "apk":
		_, err := runCmdWithOutput(fields[0], append(fields[1:], pkg)...)
		return err == nil
	default:
		_, err := runCmdWithOutput(fields[0], append(fields[1:], pkg)...)
		return err == nil
	}
}

func contains(list []string, item string) bool {
	for _, s := range list {
		if s == item {
			return true
		}
	}
	return false
}

var installMu = make(chan struct{}, 1)

func InstallMissingDeps(tools_needed []string, mgr *PkgManager) []string {
	if mgr == nil {
		return tools_needed
	}

	installMu <- struct{}{}
	defer func() { <-installMu }()

	var toInstall []string
	for _, tool := range tools_needed {
		toolInfo := findToolInfo(tool)
		if toolInfo == nil {
			continue
		}
		finalPkg := toolInfo
		aliases := []string{tool}
		if tool == "p7zip" {
			aliases = []string{"7zz", "7z", "7za"}
		}
		if tool == "unzip" {
			aliases = []string{"unzip"}
		}
		for _, a := range aliases {
			if _, err := exec.LookPath(a); err == nil {
				finalPkg = nil
				break
			}
		}
		if finalPkg == nil {
			continue
		}
		_ = finalPkg

		pkg := toolPkgSlice(*toolInfo, *mgr)

		if mgr.Name == "apt-get" && !isToolInstalled(mgr, pkg) {
			toInstall = append(toInstall, pkg)
		} else if mgr.Name != "apt-get" {
			toInstall = append(toInstall, pkg)
		}
	}

	if len(toInstall) == 0 {
		return nil
	}

	// Deduplicate
	seen := make(map[string]bool)
	var unique []string
	for _, p := range toInstall {
		if !seen[p] {
			seen[p] = true
			unique = append(unique, p)
		}
	}

	WriteLogf("%sInstalando dependencias...%s\n", Yellow, NC)
	if mgr.Update != "" {
		WriteLogf("  $ %s\n", mgr.Update)
		if err := runCmd(strings.Fields(mgr.Update)[0], strings.Fields(mgr.Update)[1:]...); err != nil {
			WriteLogf("  %s⚠ advertencia: update falló%s\n", Yellow, NC)
		}
	}

	installArgs := strings.Fields(mgr.Install)
	installArgs = append(installArgs, unique...)
	WriteLogf("  $ %s %s\n", installArgs[0], strings.Join(installArgs[1:], " "))

	var lastErr error
	for retry := 0; retry < 3; retry++ {
		if retry > 0 {
			WriteLogf("  %sReintento %d/3...%s\n", Yellow, retry+1, NC)
		}
		if err := runCmd(installArgs[0], installArgs[1:]...); err == nil {
			WriteLogf("  %s✓ Dependencias instaladas%s\n", Green, NC)
			if mgr.Prune != "" {
				pruneArgs := strings.Fields(mgr.Prune)
				runCmd(pruneArgs[0], pruneArgs[1:]...)
			}
			return nil
		} else {
			lastErr = err
		}
	}

	WriteLogf("  %s✗ Error instalando dependencias: %v%s\n", Red, lastErr, NC)
	return unique
}

func findToolInfo(tool string) *ToolInfo {
	for i, t := range tools {
		if t.Name == tool {
			return &tools[i]
		}
	}
	return nil
}

// --- Dedicated sub functions for compressed listing ---

var compressMu = make(chan struct{}, 1)

func CompressRead(f *os.File) ([]byte, error) {
	compressMu <- struct{}{}
	defer func() { <-compressMu }()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	var cmd *exec.Cmd

	switch {
	case strings.HasSuffix(info.Name(), ".gz") || strings.HasSuffix(info.Name(), ".tgz"):
		cmd = exec.Command("pigz", "-dc", "--", f.Name())
		if !hasTool("pigz") {
			cmd = exec.Command("gzip", "-dc", "--", f.Name())
		}
	case strings.HasSuffix(info.Name(), ".bz2") || strings.HasSuffix(info.Name(), ".tbz2"):
		bin := bzip2Bin()
		cmd = exec.Command(bin, "-dc", "--", f.Name())
	case strings.HasSuffix(info.Name(), ".xz") || strings.HasSuffix(info.Name(), ".txz"):
		cmd = exec.Command("xz", "-dc", "--", f.Name())
	case strings.HasSuffix(info.Name(), ".zst") || strings.HasSuffix(info.Name(), ".tzst"):
		cmd = exec.Command("zstd", "-dc", "--", f.Name())
	case strings.HasSuffix(info.Name(), ".lz") || strings.HasSuffix(info.Name(), ".tlz"):
		cmd = exec.Command("plzip", "-dc", "--", f.Name())
	case strings.HasSuffix(info.Name(), ".zip"):
		cmd = exec.Command("unzip", "-p", "--", f.Name())
	case strings.HasSuffix(info.Name(), ".7z"):
		cmd = exec.Command(sevenzBin(), "x", "-so", "--", f.Name())
	case strings.HasSuffix(info.Name(), ".rar"):
		cmd = exec.Command(rarBin(), "p", "--", f.Name())
	case strings.HasSuffix(info.Name(), ".tar"):
		cmd = exec.Command("tar", "-xf", "--", f.Name())
	default:
		return nil, fmt.Errorf("no se puede leer archivo comprimido: %s", info.Name())
	}

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("error leyendo %s: %w", info.Name(), err)
	}
	return out, nil
}

func ListCompressed(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}

	var cmd *exec.Cmd
	name := info.Name()
	fpath := f.Name()

	switch {
	case strings.HasSuffix(name, ".tar.gz") || strings.HasSuffix(name, ".tgz"):
		cmd = exec.Command("tar", "-tzf", fpath)
	case strings.HasSuffix(name, ".tar.xz") || strings.HasSuffix(name, ".txz"):
		cmd = exec.Command("tar", "-tJf", fpath)
	case strings.HasSuffix(name, ".tar.bz2") || strings.HasSuffix(name, ".tbz2"):
		cmd = exec.Command("tar", "-tjf", fpath)
	case strings.HasSuffix(name, ".tar.bz3"):
		cmd = exec.Command("tar", "-I", "bzip3 -dc", "-tf", fpath)
	case strings.HasSuffix(name, ".tar.zst") || strings.HasSuffix(name, ".tzst"):
		cmd = exec.Command("tar", "-I", "zstd -dc", "-tf", fpath)
	case strings.HasSuffix(name, ".tar.lz") || strings.HasSuffix(name, ".tlz"):
		cmd = exec.Command("tar", "--lzip", "-tf", fpath)
	case strings.HasSuffix(name, ".tar.lrz"):
		cmd = exec.Command("tar", "-I", "lrzip -d -p 1 -o -", "-tf", fpath)
	case strings.HasSuffix(name, ".tar"):
		cmd = exec.Command("tar", "-tf", fpath)
	case strings.HasSuffix(name, ".gz"):
		cmd = exec.Command("pigz", "-l", fpath)
		if !hasTool("pigz") {
			cmd = exec.Command("gzip", "-l", fpath)
		}
	case strings.HasSuffix(name, ".bz2"):
		cmd = exec.Command(bzip2Bin(), "-l", fpath)
	case strings.HasSuffix(name, ".xz"):
		cmd = exec.Command("xz", "-l", fpath)
	case strings.HasSuffix(name, ".zst"):
		cmd = exec.Command("zstd", "-l", fpath)
	case strings.HasSuffix(name, ".zip"):
		cmd = exec.Command("unzip", "-l", fpath)
	case strings.HasSuffix(name, ".7z"):
		cmd = exec.Command(sevenzBin(), "l", fpath)
	case strings.HasSuffix(name, ".rar"):
		cmd = exec.Command(rarBin(), "l", fpath)
	case strings.HasSuffix(name, ".lz"):
		cmd = exec.Command("plzip", "-l", fpath)
	case strings.HasSuffix(name, ".lrz"):
		cmd = exec.Command("lrzip", "-i", fpath)
	default:
		return fmt.Errorf("no se puede listar formato: %s", name)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// --- readFile with buffered scanner (lazy line by line) ---

func ReadFileLines(filePath string) ([]string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}
