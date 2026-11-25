//go:build linux

package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	rotationalCache   sync.Map
	defaultMountinfo  = "/proc/self/mountinfo"
	defaultSysfsBlock = "/sys/class/block"
	defaultDevDir     = "/dev"
)

// IsRotational determina si la ruta especificada reside en un dispositivo de almacenamiento
// rotacional (disco duro mecánico magnético / HDD).
// Devuelve true para HDDs mecánicos y false para SSDs, NVMes, RAM disks o si no se puede determinar.
func IsRotational(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return isRotationalLinux(abs, defaultMountinfo, defaultSysfsBlock)
}

func isRotationalLinux(absPath, mountinfoFile, sysfsDir string) bool {
	if strings.HasPrefix(absPath, "/") {
		absPath = filepath.Clean(absPath)
	}
	useCache := (mountinfoFile == defaultMountinfo && sysfsDir == defaultSysfsBlock && defaultDevDir == "/dev")

	// ponytail: cache only by mountPoint so rotationalCache stays O(mounts), never O(paths).
	mountPoint, devSource := findMountForPath(absPath, mountinfoFile)
	if mountPoint != "" && useCache {
		if val, ok := rotationalCache.Load(mountPoint); ok {
			return val.(bool)
		}
	}

	isRot := false
	if devName := resolveBlockDevName(devSource); devName != "" {
		isRot = checkSysfsRotational(devName, sysfsDir)
	}

	if mountPoint != "" && useCache {
		rotationalCache.Store(mountPoint, isRot)
	}
	return isRot
}

func resolveBlockDevName(devSource string) string {
	if devSource == "" {
		return ""
	}
	devPrefix := strings.TrimRight(defaultDevDir, "/") + "/"
	if !strings.HasPrefix(devSource, devPrefix) && !strings.HasPrefix(devSource, "/dev/") {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(devSource); err == nil && strings.HasPrefix(resolved, "/") {
		return filepath.Base(resolved)
	}
	rel := strings.TrimPrefix(devSource, devPrefix)
	rel = strings.TrimPrefix(rel, "/dev/")
	if strings.Contains(rel, "/") {
		return filepath.Base(rel)
	}
	return rel
}

func unescapeMountinfoField(s string) string {
	if strings.IndexByte(s, '\\') < 0 {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) &&
			s[i+1] >= '0' && s[i+1] <= '3' &&
			s[i+2] >= '0' && s[i+2] <= '7' &&
			s[i+3] >= '0' && s[i+3] <= '7' {
			b := ((s[i+1] - '0') << 6) | ((s[i+2] - '0') << 3) | (s[i+3] - '0')
			sb.WriteByte(b)
			i += 3
			continue
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// findMountForPath busca en el archivo mountinfo el punto de montaje más específico (prefijo más largo)
// que contenga absPath.
func findMountForPath(absPath, mountinfoFile string) (bestMount, bestSource string) {
	if !strings.HasPrefix(absPath, "/") {
		return "", ""
	}
	absPath = filepath.Clean(absPath)

	f, err := os.Open(mountinfoFile)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	longestLen := -1

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mountPoint := unescapeMountinfoField(fields[4])
		if !strings.HasPrefix(mountPoint, "/") {
			continue
		}
		mountPoint = filepath.Clean(mountPoint)

		if !pathIsUnderMount(absPath, mountPoint) {
			continue
		}

		if len(mountPoint) >= longestLen {
			sepIdx := -1
			for i := 6; i < len(fields); i++ {
				if fields[i] == "-" {
					sepIdx = i
					break
				}
			}
			if sepIdx != -1 && len(fields) > sepIdx+2 {
				longestLen = len(mountPoint)
				bestMount = mountPoint
				bestSource = unescapeMountinfoField(fields[sepIdx+2])
			}
		}
	}

	return bestMount, bestSource
}

func pathIsUnderMount(path, mountPoint string) bool {
	if !strings.HasPrefix(path, "/") || !strings.HasPrefix(mountPoint, "/") {
		return false
	}
	path = filepath.Clean(path)
	mountPoint = filepath.Clean(mountPoint)
	if mountPoint == "/" {
		return true
	}
	if path == mountPoint {
		return true
	}
	return strings.HasPrefix(path, mountPoint) && len(path) > len(mountPoint) && path[len(mountPoint)] == '/'
}

func parentBlockDeviceName(devName string) string {
	i := len(devName)
	for i > 0 && devName[i-1] >= '0' && devName[i-1] <= '9' {
		i--
	}
	if i == 0 || i == len(devName) {
		return ""
	}
	if i >= 2 && devName[i-1] == 'p' && devName[i-2] >= '0' && devName[i-2] <= '9' {
		return devName[:i-1]
	}
	base := devName[:i]
	for _, pfx := range []string{"xvd", "dasd", "sd", "vd", "hd"} {
		if strings.HasPrefix(base, pfx) && len(base) > len(pfx) {
			letters := base[len(pfx):]
			allLower := true
			for j := 0; j < len(letters); j++ {
				if letters[j] < 'a' || letters[j] > 'z' {
					allLower = false
					break
				}
			}
			if allLower {
				return base
			}
		}
	}
	return ""
}

// checkSysfsRotational consulta el archivo 'rotational' en sysfs para un nombre de dispositivo de bloques.
func checkSysfsRotational(devName, sysfsDir string) bool {
	return checkSysfsRotationalDepth(devName, sysfsDir, 0)
}

func checkSysfsRotationalDepth(devName, sysfsDir string, depth int) bool {
	if devName == "" || depth > 4 {
		return false
	}
	devPath := filepath.Join(sysfsDir, devName)
	realPath, err := filepath.EvalSymlinks(devPath)
	if err != nil {
		realPath = devPath
	}

	directQueue := filepath.Join(realPath, "queue", "rotational")
	if data, err := os.ReadFile(directQueue); err == nil {
		return bytes.HasPrefix(bytes.TrimSpace(data), []byte("1"))
	}

	parentDir := filepath.Dir(realPath)
	if filepath.Clean(parentDir) != filepath.Clean(sysfsDir) {
		parentQueue := filepath.Join(parentDir, "queue", "rotational")
		if data, err := os.ReadFile(parentQueue); err == nil {
			return bytes.HasPrefix(bytes.TrimSpace(data), []byte("1"))
		}
	}

	if entries, err := os.ReadDir(filepath.Join(realPath, "slaves")); err == nil && len(entries) > 0 {
		for _, entry := range entries {
			if checkSysfsRotationalDepth(entry.Name(), sysfsDir, depth+1) {
				return true
			}
		}
		return false
	}

	if parentDev := parentBlockDeviceName(filepath.Base(realPath)); parentDev != "" && parentDev != devName {
		return checkSysfsRotationalDepth(parentDev, sysfsDir, depth+1)
	}

	return false
}
