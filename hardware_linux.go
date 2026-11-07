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
	useCache := (mountinfoFile == defaultMountinfo && sysfsDir == defaultSysfsBlock)
	if useCache {
		if val, ok := rotationalCache.Load(absPath); ok {
			return val.(bool)
		}
	}

	mountPoint, devSource := findMountForPath(absPath, mountinfoFile)
	if mountPoint != "" && useCache {
		if val, ok := rotationalCache.Load(mountPoint); ok {
			rotationalCache.Store(absPath, val)
			return val.(bool)
		}
	}

	if devSource == "" || !strings.HasPrefix(devSource, "/dev/") {
		if useCache {
			rotationalCache.Store(absPath, false)
			if mountPoint != "" {
				rotationalCache.Store(mountPoint, false)
			}
		}
		return false
	}

	devName := strings.TrimPrefix(devSource, "/dev/")
	isRot := checkSysfsRotational(devName, sysfsDir)

	if useCache {
		rotationalCache.Store(absPath, isRot)
		if mountPoint != "" {
			rotationalCache.Store(mountPoint, isRot)
		}
	}
	return isRot
}

// findMountForPath busca en el archivo mountinfo el punto de montaje más específico (prefijo más largo)
// que contenga absPath.
func findMountForPath(absPath, mountinfoFile string) (bestMount, bestSource string) {
	f, err := os.Open(mountinfoFile)
	if err != nil {
		return "", ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	longestLen := -1

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		mountPoint := fields[4]

		if !pathIsUnderMount(absPath, mountPoint) {
			continue
		}

		if len(mountPoint) > longestLen {
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
				bestSource = fields[sepIdx+2]
			}
		}
	}

	return bestMount, bestSource
}

func pathIsUnderMount(path, mountPoint string) bool {
	if mountPoint == "/" {
		return true
	}
	if path == mountPoint {
		return true
	}
	if strings.HasPrefix(path, mountPoint) {
		if len(path) > len(mountPoint) && path[len(mountPoint)] == '/' {
			return true
		}
	}
	return false
}

// checkSysfsRotational consulta el archivo 'rotational' en sysfs para un nombre de dispositivo de bloques.
func checkSysfsRotational(devName, sysfsDir string) bool {
	devPath := filepath.Join(sysfsDir, devName)

	// 1. Si devPath es un symlink (comportamiento estándar en /sys/class/block/), resolverlo
	realPath, err := filepath.EvalSymlinks(devPath)
	if err != nil {
		realPath = devPath
	}

	// 2. Probar si tiene 'queue/rotational' directamente (ej. disco completo /sys/.../block/sda/queue/rotational)
	directQueue := filepath.Join(realPath, "queue", "rotational")
	if data, err := os.ReadFile(directQueue); err == nil {
		return bytes.HasPrefix(bytes.TrimSpace(data), []byte("1"))
	}

	// 3. Probar en el directorio padre (ej. partición /sys/.../block/sda/sda1 -> padre sda/queue/rotational)
	parentQueue := filepath.Join(filepath.Dir(realPath), "queue", "rotational")
	if data, err := os.ReadFile(parentQueue); err == nil {
		return bytes.HasPrefix(bytes.TrimSpace(data), []byte("1"))
	}

	return false
}
