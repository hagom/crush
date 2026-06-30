package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ncpuOnce  sync.Once
	ncpuCache int

	bzip2Once  sync.Once
	bzip2Cache string

	sevenzOnce  sync.Once
	sevenzCache string

	rarOnce  sync.Once
	rarCache string

	logFile *os.File
)

func ncpuStr() string {
	return strconv.Itoa(NCPU())
}

func NCPU() int {
	ncpuOnce.Do(func() {
		// getconf _NPROCESSORS_ONLN
		cmd := exec.Command("getconf", "_NPROCESSORS_ONLN")
		out, err := cmd.Output()
		if err == nil {
			n, err := strconv.Atoi(strings.TrimSpace(string(out)))
			if err == nil && n > 0 {
				ncpuCache = n
				return
			}
		}

		// cgroup v2
		data, err := os.ReadFile("/sys/fs/cgroup/cpu.max")
		if err == nil {
			parts := strings.Fields(string(data))
			if len(parts) > 0 && parts[0] != "max" {
				quota, err := strconv.Atoi(parts[0])
				if err == nil && quota > 0 {
					period := 100000
					if len(parts) > 1 {
						if p, err := strconv.Atoi(parts[1]); err == nil && p > 0 {
							period = p
						}
					}
					n := (quota + period - 1) / period
					if n > 0 {
						ncpuCache = n
						return
					}
				}
			}
		}

		// cgroup v1
		quotaB, _ := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_quota_us")
		periodB, _ := os.ReadFile("/sys/fs/cgroup/cpu/cpu.cfs_period_us")
		if len(quotaB) > 0 && len(periodB) > 0 {
			quota, _ := strconv.Atoi(strings.TrimSpace(string(quotaB)))
			period, _ := strconv.Atoi(strings.TrimSpace(string(periodB)))
			if quota > 0 && period > 0 {
				n := (quota + period - 1) / period
				if n > 0 {
					ncpuCache = n
					return
				}
			}
		}

		ncpuCache = 1
	})
	return ncpuCache
}

func bzip2Bin() string {
	bzip2Once.Do(func() {
		if lookupPath("lbzip2") {
			bzip2Cache = "lbzip2"
		} else if lookupPath("pbzip2") {
			bzip2Cache = "pbzip2"
		} else {
			bzip2Cache = "lbzip2"
		}
	})
	return bzip2Cache
}

func sevenzBin() string {
	sevenzOnce.Do(func() {
		if lookupPath("7zz") {
			sevenzCache = "7zz"
		} else if lookupPath("7z") {
			sevenzCache = "7z"
		} else if lookupPath("7za") {
			sevenzCache = "7za"
		} else {
			sevenzCache = "7z"
		}
	})
	return sevenzCache
}

func rarBin() string {
	rarOnce.Do(func() {
		if lookupPath("rar") {
			rarCache = "rar"
		} else if lookupPath("unrar") {
			rarCache = "unrar"
		} else {
			rarCache = "rar"
		}
	})
	return rarCache
}

func lookupPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// --- Memory ---

func GetMemLimit() int {
	data, err := os.ReadFile("/proc/meminfo")
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "MemAvailable:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if kb, err := strconv.Atoi(fields[1]); err == nil && kb > 0 {
						return kb * 70 / 100 / 1024
					}
				}
			}
		}
	}

	cmd := exec.Command("free", "-k")
	out, err := cmd.Output()
	if err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(out)))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "Mem:") {
				fields := strings.Fields(line)
				if len(fields) >= 7 {
					if avail, err := strconv.Atoi(fields[6]); err == nil && avail > 0 {
						return avail * 70 / 100 / 1024
					}
				}
			}
		}
	}

	return 1024
}

// --- Size formatting ---

func FormatSize(bytes int64) string {
	if hasTool("numfmt") {
		cmd := exec.Command("numfmt", "--to=iec-i", "--suffix=B", strconv.FormatInt(bytes, 10))
		out, err := cmd.Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	}

	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	unit := 0
	size := float64(bytes)
	for size > 1024 && unit < 5 {
		size /= 1024
		unit++
	}
	return fmt.Sprintf("%.0f %s", size, units[unit])
}

func CalcPct(orig, final int64) string {
	if orig > 0 {
		pct := float64(orig-final) / float64(orig) * 100
		return fmt.Sprintf("%.2f", pct)
	}
	return "0.00"
}

// --- Disk space ---

func GetAvailBytes(dir string) int64 {
	if dir == "" {
		dir = "."
	}
	cmd := exec.Command("df", "-B1", "--output=avail", dir)
	out, err := cmd.Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		if len(lines) >= 2 {
			if avail, err := strconv.ParseInt(strings.TrimSpace(lines[1]), 10, 64); err == nil {
				return avail
			}
		}
	}

	cmd = exec.Command("df", dir)
	out, err = cmd.Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[1])
			if len(fields) >= 4 {
				if blocks, err := strconv.ParseInt(fields[3], 10, 64); err == nil {
					return blocks * 1024
				}
			}
		}
	}

	return 0
}

func CheckDiskSpace(needed int64, dir string) error {
	avail := GetAvailBytes(dir)
	if avail < needed {
		return fmt.Errorf("Espacio insuficiente. Necesario: %s, Disponible: %s",
			FormatSize(needed), FormatSize(avail))
	}
	return nil
}

func EstimateUncompressedSize(file string) int64 {
	f := strings.ToLower(file)

	switch {
	case strings.HasSuffix(f, ".gz") || strings.HasSuffix(f, ".tgz"):
		cmd := exec.Command("gzip", "-l", "--", file)
		out, _ := cmd.Output()
		lines := strings.Split(string(out), "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[1])
			if len(fields) >= 2 {
				if size, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					return size
				}
			}
		}

	case strings.HasSuffix(f, ".xz") || strings.HasSuffix(f, ".txz"):
		cmd := exec.Command("xz", "-l", "--robot", "--", file)
		out, _ := cmd.Output()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "file\t") {
				fields := strings.Split(line, "\t")
				if len(fields) >= 5 {
					if size, err := strconv.ParseInt(fields[4], 10, 64); err == nil {
						return size
					}
				}
			}
		}

	case strings.HasSuffix(f, ".zst") || strings.HasSuffix(f, ".tzst"):
		cmd := exec.Command("zstd", "-l", "--", file)
		out, _ := cmd.Output()
		var sum int64
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 5 {
				if size, err := strconv.ParseInt(fields[4], 10, 64); err == nil {
					sum += size
				}
			}
		}
		if sum > 0 {
			return sum
		}

	case strings.HasSuffix(f, ".zip"):
		cmd := exec.Command("unzip", "-l", "--", file)
		out, _ := cmd.Output()
		lines := strings.Split(string(out), "\n")
		if len(lines) >= 3 {
			fields := strings.Fields(lines[len(lines)-1])
			if len(fields) >= 1 {
				if size, err := strconv.ParseInt(fields[0], 10, 64); err == nil {
					return size
				}
			}
		}

	case strings.HasSuffix(f, ".7z"):
		cmd := exec.Command(sevenzBin(), "l", "-slt", "--", file)
		out, _ := cmd.Output()
		var sum int64
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "Size = ") {
				parts := strings.SplitN(line, "= ", 2)
				if len(parts) == 2 {
					if size, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64); err == nil {
						sum += size
					}
				}
			}
		}
		if sum > 0 {
			return sum
		}

	case strings.HasSuffix(f, ".lrz"):
		cmd := exec.Command("lrzip", "-i", "--", file)
		out, _ := cmd.Output()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "Decompressed file size") {
				fields := strings.Fields(line)
				if len(fields) > 0 {
					if size, err := strconv.ParseInt(fields[len(fields)-1], 10, 64); err == nil {
						return size
					}
				}
			}
		}

	case strings.HasSuffix(f, ".tar"):
		cmd := exec.Command("stat", "-c%s", "--", file)
		out, _ := cmd.Output()
		if size, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); err == nil {
			return size
		}

	case strings.HasSuffix(f, ".bz2") || strings.HasSuffix(f, ".tbz2") ||
		strings.HasSuffix(f, ".bz3") || strings.HasSuffix(f, ".lz") ||
		strings.HasSuffix(f, ".tlz"):
		cmd := exec.Command("stat", "-c%s", "--", file)
		out, _ := cmd.Output()
		if size, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); err == nil {
			return size * 6
		}
	}

	// Fallback: compressed size * 4
	cmd := exec.Command("stat", "-c%s", "--", file)
	out, _ := cmd.Output()
	if size, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); err == nil {
		return size * 4
	}
	return 0
}

// --- Filename ---

func GetUniqueName(base, ext string) string {
	if strings.HasSuffix(base, "."+ext) {
		base = strings.TrimSuffix(base, "."+ext)
	}
	name := base + "." + ext
	if _, err := os.Stat(name); err == nil {
		for counter := 1; ; counter++ {
			name = fmt.Sprintf("%s_%d.%s", base, counter, ext)
			if _, err := os.Stat(name); os.IsNotExist(err) {
				break
			}
		}
	}
	return name
}

// --- Logging ---

func SetupLogging() error {
	logDir := "/var/log/compresor"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		logDir = "/tmp/compresor"
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return err
		}
	}

	now := time.Now().Format("20060102_150405")
	logPath := filepath.Join(logDir, "compresor_"+now+".log")

	f, err := os.Create(logPath)
	if err != nil {
		return err
	}
	logFile = f

	return nil
}

func WriteLog(s string) {
	fmt.Print(s)
	if logFile != nil {
		logFile.WriteString(s)
	}
}

func WriteLogf(format string, args ...interface{}) {
	s := fmt.Sprintf(format, args...)
	fmt.Print(s)
	if logFile != nil {
		logFile.WriteString(s)
	}
}

func CloseLog() {
	if logFile != nil {
		logFile.Close()
	}
}

// --- Colors ---

const (
	Green  = "\033[0;32m"
	Red    = "\033[0;31m"
	Yellow = "\033[1;33m"
	Blue   = "\033[0;34m"
	Bold   = "\033[1m"
	NC     = "\033[0m"
)

// --- Helpers ---

func hasTool(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// --- Pipeline: connect multiple commands like bash pipes ---

func pipeline(stdout, stderr io.Writer, cmds ...*exec.Cmd) error {
	if len(cmds) == 0 {
		return nil
	}
	for i := 0; i < len(cmds)-1; i++ {
		var err error
		cmds[i+1].Stdin, err = cmds[i].StdoutPipe()
		if err != nil {
			return fmt.Errorf("pipeline pipe %d: %w", i, err)
		}
	}
	cmds[len(cmds)-1].Stdout = stdout
	if stderr != nil {
		for i := 0; i < len(cmds); i++ {
			cmds[i].Stderr = stderr
		}
	}
	for i := 0; i < len(cmds)-1; i++ {
		if err := cmds[i].Start(); err != nil {
			return fmt.Errorf("pipeline start %d: %w", i, err)
		}
	}
	if err := cmds[len(cmds)-1].Run(); err != nil {
		return fmt.Errorf("pipeline run: %w", err)
	}
	for i := 0; i < len(cmds)-1; i++ {
		if err := cmds[i].Wait(); err != nil {
			return fmt.Errorf("pipeline wait %d: %w", i, err)
		}
	}
	return nil
}
