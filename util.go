package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

	logFile  *os.File
	nullFile *os.File

	logBuf         bytes.Buffer
	logMu          sync.Mutex
	loggingActive  atomic.Bool

	execCommand = exec.Command
)

type FileProgress struct {
	Name                string
	Size                int64
	Current             atomic.Int64
	hasExternalProgress atomic.Bool
	status              string
	start               time.Time
	outPath             string
	partsDone           int
	partsTotal          int
	mu                  sync.Mutex
}

func (fp *FileProgress) HasExternalProgress() bool {
	return fp.hasExternalProgress.Load()
}

func (fp *FileProgress) SetHasExternalProgress(b bool) {
	fp.hasExternalProgress.Store(b)
}

func (fp *FileProgress) Status() string {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return fp.status
}

func (fp *FileProgress) SetStatus(s string) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.status = s
}

func (fp *FileProgress) StartTime() time.Time {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return fp.start
}

func (fp *FileProgress) SetStart(t time.Time) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.start = t
}

func (fp *FileProgress) OutPath() string {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return fp.outPath
}

func (fp *FileProgress) SetOutPath(p string) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.outPath = p
}

func (fp *FileProgress) Parts() (int, int) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return fp.partsDone, fp.partsTotal
}

func (fp *FileProgress) SetParts(done, total int) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.partsDone = done
	fp.partsTotal = total
}

func globSplitParts(file string) []string {
	pattern := file + ".part*"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool {
		idxI := strings.LastIndex(matches[i], ".part")
		idxJ := strings.LastIndex(matches[j], ".part")
		if idxI != -1 && idxJ != -1 {
			baseI := matches[i][:idxI]
			baseJ := matches[j][:idxJ]
			if baseI == baseJ {
				numI, errI := strconv.Atoi(matches[i][idxI+len(".part"):])
				numJ, errJ := strconv.Atoi(matches[j][idxJ+len(".part"):])
				if errI == nil && errJ == nil {
					return numI < numJ
				}
			}
		}
		return matches[i] < matches[j]
	})
	return matches
}

func findSplitParts(file string) []string {
	file = resolveSplitBase(file)
	matches := globSplitParts(file)
	if len(matches) == 0 {
		return []string{file}
	}
	return append([]string{file}, matches...)
}

func resolveSplitBase(file string) string {
	if idx := strings.Index(file, ".part"); idx != -1 {
		base := file[:idx]
		if _, err := os.Stat(base); err == nil {
			return base
		}
	}
	return file
}

func SortByLPT(items []string, sizeFn func(string) int64) map[string]int64 {
	sizes := make(map[string]int64, len(items))
	if len(items) == 0 {
		return sizes
	}
	for _, item := range items {
		sizes[item] = sizeFn(item)
	}
	if len(items) > 1 {
		sort.SliceStable(items, func(i, j int) bool {
			return sizes[items[i]] > sizes[items[j]]
		})
	}
	return sizes
}

func PartsTotalSize(parts []string) int64 {
	var total int64
	for _, p := range parts {
		if fi, err := os.Stat(p); err == nil {
			total += fi.Size()
		}
	}
	return total
}

func TotalArchiveSize(archivePath string) int64 {
	return PartsTotalSize(findSplitParts(archivePath))
}

func GetDirSize(dir string) (int64, error) {
	var total int64
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func IsSplitPartsDir(dir string) bool {
	return strings.HasSuffix(dir, "_parts") || strings.HasSuffix(dir, "_split")
}

func ResolveDecompressDir(archivePath string, configuredOutputDir string) string {
	if configuredOutputDir != "" {
		return configuredOutputDir
	}
	dir := filepath.Dir(archivePath)
	if IsSplitPartsDir(dir) {
		return filepath.Dir(dir)
	}
	return dir
}


func pollFileProgress(fp *FileProgress) {
	if fp == nil || fp.Status() != "active" || fp.OutPath() == "" || fp.Size <= 0 {
		return
	}
	if fp.HasExternalProgress() {
		return
	}
	fi, err := os.Stat(fp.OutPath())
	if err != nil {
		return
	}
	newSize := fi.Size()
	for _, p := range globSplitParts(fp.OutPath()) {
		if pfi, err := os.Stat(p); err == nil {
			newSize += pfi.Size()
		}
	}
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

func ncpuStr() string {
	return strconv.Itoa(NCPU())
}

func threadStr(limit int) string {
	if limit > 0 {
		return strconv.Itoa(limit)
	}
	return ncpuStr()
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
		if hasTool("lbzip2") {
			bzip2Cache = "lbzip2"
		} else if hasTool("pbzip2") {
			bzip2Cache = "pbzip2"
		} else {
			bzip2Cache = "bzip2"
		}
	})
	return bzip2Cache
}

func sevenzBin() string {
	sevenzOnce.Do(func() {
		if hasTool("7zz") {
			sevenzCache = "7zz"
		} else if hasTool("7z") {
			sevenzCache = "7z"
		} else if hasTool("7za") {
			sevenzCache = "7za"
		} else {
			sevenzCache = "7z"
		}
	})
	return sevenzCache
}

func rarBin() string {
	rarOnce.Do(func() {
		if hasTool("rar") {
			rarCache = "rar"
		} else if hasTool("unrar") {
			rarCache = "unrar"
		} else {
			rarCache = "rar"
		}
	})
	return rarCache
}

var (
	lz4ThreadOnce  sync.Once
	lz4ThreadCache bool
)

func lz4SupportsThreads() bool {
	lz4ThreadOnce.Do(func() {
		if !hasTool("lz4") {
			return
		}
		cmd := execCommand("lz4", "-T1", "--version")
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		lz4ThreadCache = (cmd.Run() == nil)
	})
	return lz4ThreadCache
}

// --- Memory ---

var getMemLimit = GetMemLimit

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

	if mb := getMemFromFree(); mb > 0 {
		return mb
	}

	return 1024
}

func getMemFromFree() int {
	cmd := execCommand("free", "-k")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
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
	return 0
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
	for size >= 1024 && unit < 5 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", bytes, units[unit])
	}
	return fmt.Sprintf("%.1f %s", size, units[unit])
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
	target := dir
	for {
		if _, err := os.Stat(target); err == nil {
			break
		}
		parent := filepath.Dir(target)
		if parent == target || parent == "" || parent == "." {
			target = "."
			break
		}
		target = parent
	}

	cmd := execCommand("df", "-B1", "--output=avail", target)
	out, err := cmd.Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		if len(lines) >= 2 {
			if avail, err := strconv.ParseInt(strings.TrimSpace(lines[1]), 10, 64); err == nil {
				return avail
			}
		}
	}

	cmd = execCommand("df", target)
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

func CheckDiskSpace(needed int64, dir string, op string) error {
	avail := GetAvailBytes(dir)
	if avail < needed {
		delta := needed - avail
		return fmt.Errorf("Espacio insuficiente para %s: libera %s. Necesario: %s, Disponible: %s",
			op, FormatSize(delta), FormatSize(needed), FormatSize(avail))
	}
	return nil
}

func isPrecompressedFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".zip", ".gz", ".tgz", ".xz", ".txz", ".bz2", ".tbz2", ".bz3",
		".zst", ".tzst", ".7z", ".rar", ".lz", ".tlz", ".lrz", ".lz4", ".br",
		".mp4", ".mkv", ".avi", ".mov", ".wmv", ".webm",
		".mp3", ".ogg", ".aac", ".m4a", ".opus",
		".jpg", ".jpeg", ".png", ".webp", ".avif", ".gif",
		".iso", ".jar", ".apk", ".deb", ".rpm":
		return true
	}
	return false
}

func EstimateCompressedSize(totalSize int64, format Format, files []string) int64 {
	if len(files) > 0 {
		allPre := true
		for _, f := range files {
			if !isPrecompressedFile(f) {
				allPre = false
				break
			}
		}
		if allPre {
			est := totalSize * 95 / 100
			if est < 1<<20 {
				est = 1 << 20
			}
			return est
		}
	}

	var estimated int64
	switch format {
	case Tar:
		estimated = totalSize * 102 / 100
	case Lz4:
		estimated = totalSize * 60 / 100
	case Zip:
		estimated = totalSize * 50 / 100
	case Gz:
		estimated = totalSize * 40 / 100
	case Zst:
		estimated = totalSize * 35 / 100
	case Bz2:
		estimated = totalSize * 30 / 100
	case Rar:
		estimated = totalSize * 30 / 100
	case Br:
		estimated = totalSize * 28 / 100
	case SevenZ, Xz, Bz3, Lz:
		estimated = totalSize * 25 / 100
	case Lrz:
		estimated = totalSize * 20 / 100
	default:
		estimated = totalSize * 35 / 100
	}

	if estimated < 1<<20 {
		estimated = 1 << 20
	}
	return estimated
}

func EstimateUncompressedSize(file string) int64 {
	f := strings.ToLower(file)

	switch {
	case strings.HasSuffix(f, ".gz") || strings.HasSuffix(f, ".tgz"):
		if fi, err := os.Stat(file); err == nil && fi.Size() >= 8 {
			fHandle, err := os.Open(file)
			if err == nil {
				buf := make([]byte, 4)
				if _, err := fHandle.ReadAt(buf, fi.Size()-4); err == nil {
					isize := int64(buf[0]) | int64(buf[1])<<8 | int64(buf[2])<<16 | int64(buf[3])<<24
					if isize > 0 && fi.Size() <= int64(1<<32) {
						fHandle.Close()
						return isize
					}
				}
				fHandle.Close()
			}
		}
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
		if r, err := zip.OpenReader(file); err == nil {
			var sum int64
			for _, zf := range r.File {
				sum += int64(zf.UncompressedSize64)
			}
			r.Close()
			if sum > 0 {
				return sum
			}
		}
		cmd := exec.Command("unzip", "-l", "--", file)
		out, _ := cmd.Output()
		lines := strings.Split(string(out), "\n")
		for len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
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

	case strings.HasSuffix(f, ".rar"):
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
		if fi, err := os.Stat(file); err == nil {
			return fi.Size() * 3
		}

	case strings.HasSuffix(f, ".lrz"):
		cmd := exec.Command("lrzip", "-i", "--", file)
		out, _ := cmd.Output()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "Decompressed file size") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if size, err := strconv.ParseInt(fields[len(fields)-2], 10, 64); err == nil {
						return size
					}
				}
			}
		}

	case strings.HasSuffix(f, ".tar"):
		if fi, err := os.Stat(file); err == nil {
			return fi.Size()
		}

	case strings.HasSuffix(f, ".lz4"):
		if fi, err := os.Stat(file); err == nil {
			return fi.Size() * 2
		}

	case strings.HasSuffix(f, ".br"):
		if fi, err := os.Stat(file); err == nil {
			return fi.Size() * 7 / 2
		}

	case strings.HasSuffix(f, ".bz2") || strings.HasSuffix(f, ".tbz2") ||
		strings.HasSuffix(f, ".bz3") || strings.HasSuffix(f, ".lz") ||
		strings.HasSuffix(f, ".tlz"):
		if fi, err := os.Stat(file); err == nil {
			return fi.Size() * 4
		}
	}

	// Fallback
	if fi, err := os.Stat(file); err == nil {
		return fi.Size() * 3
	}
	return 0
}

// --- Filename ---

var (
	uniqueNameMu  sync.Mutex
	reservedNames = make(map[string]bool)
)

func isNameAvailable(name string) bool {
	if reservedNames[name] {
		return false
	}
	_, err := os.Stat(name)
	return os.IsNotExist(err)
}

func GetUniqueName(base, ext string) string {
	uniqueNameMu.Lock()
	defer uniqueNameMu.Unlock()

	if strings.HasSuffix(base, "."+ext) {
		base = strings.TrimSuffix(base, "."+ext)
	}
	name := base + "." + ext
	if isNameAvailable(name) {
		reservedNames[name] = true
		return name
	}
	for counter := 1; counter < 1000; counter++ {
		name = fmt.Sprintf("%s_%d.%s", base, counter, ext)
		if isNameAvailable(name) {
			reservedNames[name] = true
			return name
		}
	}
	name = fmt.Sprintf("%s_%d.%s", base, 999, ext)
	reservedNames[name] = true
	return name
}

func ResetReservedNames() {
	uniqueNameMu.Lock()
	defer uniqueNameMu.Unlock()
	reservedNames = make(map[string]bool)
}

// --- Logging ---

func SetupLogging() error {
	logDir := "/var/log/crush"
	os.MkdirAll(logDir, 0755)

	now := time.Now().Format("20060102_150405")
	logPath := filepath.Join(logDir, "crush_"+now+".log")

	f, err := os.Create(logPath)
	if err != nil {
		logDir = "/tmp/crush"
		os.MkdirAll(logDir, 0755)
		logPath = filepath.Join(logDir, "crush_"+now+".log")
		f, err = os.Create(logPath)
		if err != nil {
			return err
		}
	}
	logFile = f

	return nil
}

func WriteLog(s string) {
	if loggingActive.Load() {
		logMu.Lock()
		logBuf.WriteString(s)
		logMu.Unlock()
	} else {
		lockedStderr.Write([]byte(s))
	}
	if logFile != nil {
		logMu.Lock()
		logFile.WriteString(s)
		logMu.Unlock()
	}
}

func WriteLogf(format string, args ...interface{}) {
	WriteLog(fmt.Sprintf(format, args...))
}

func CloseLog() {
	if logFile != nil {
		logFile.Close()
	}
}

// --- Colors ---

const (
	Green    = "\033[0;32m"
	Red      = "\033[0;31m"
	Yellow   = "\033[1;33m"
	Blue     = "\033[0;34m"
	Bold     = "\033[1m"
	BoldBlue = "\033[1;34m"
	NC       = "\033[0m"
)

var (
	lockedStderr = &lockedWriter{}
	lockedStdout = &lockedWriter{}
)

func setOutputWriters(stdout, stderr io.Writer) func() {
	lockedStdout.mu.Lock()
	lockedStderr.mu.Lock()
	oldStdout := lockedStdout.w
	oldStderr := lockedStderr.w
	if stdout != nil {
		lockedStdout.w = stdout
	}
	if stderr != nil {
		lockedStderr.w = stderr
	}
	lockedStderr.mu.Unlock()
	lockedStdout.mu.Unlock()

	return func() {
		lockedStdout.mu.Lock()
		lockedStderr.mu.Lock()
		lockedStdout.w = oldStdout
		lockedStderr.w = oldStderr
		lockedStderr.mu.Unlock()
		lockedStdout.mu.Unlock()
	}
}

func cleanConsoleMsg(msg string, prefixes ...string) string {
	msg = strings.TrimSpace(msg)
	for _, code := range []string{Red, Yellow, Green, Blue, Bold, BoldBlue, NC} {
		msg = strings.TrimPrefix(msg, code)
		msg = strings.TrimSuffix(msg, code)
	}
	msg = strings.TrimSpace(msg)
	for _, prefix := range prefixes {
		msg = strings.TrimPrefix(msg, prefix)
	}
	return strings.TrimSpace(msg)
}

func formatConsoleMsg(format string, a []any, prefixes ...string) string {
	var msg string
	if len(a) > 0 {
		msg = fmt.Sprintf(format, a...)
	} else {
		msg = format
	}
	return cleanConsoleMsg(msg, prefixes...)
}

func writeStderrFormatted(out string) {
	if loggingActive.Load() {
		logMu.Lock()
		logBuf.WriteString(out)
		logMu.Unlock()
	} else {
		lockedStderr.Write([]byte(out))
	}
	if logFile != nil {
		logMu.Lock()
		logFile.WriteString(out)
		logMu.Unlock()
	}
}

func writeStdoutFormatted(out string) {
	lockedStdout.Write([]byte(out))
	if logFile != nil {
		logMu.Lock()
		logFile.WriteString(out)
		logMu.Unlock()
	}
}

func WriteWarning(format string, a ...any) {
	msg := formatConsoleMsg(format, a, "⚠ Advertencia: ", "Warning: ", "Advertencia: ")
	writeStderrFormatted(Yellow + "⚠ Advertencia: " + msg + NC + "\n")
}

func WriteError(format string, a ...any) {
	msg := formatConsoleMsg(format, a, "✗ Error: ", "Error: ", "error: ")
	writeStderrFormatted(Red + "✗ Error: " + msg + NC + "\n")
}

func WriteInfo(format string, a ...any) {
	msg := formatConsoleMsg(format, a, "ℹ ", "Info: ", "Información: ")
	writeStderrFormatted(Blue + "ℹ " + msg + NC + "\n")
}

func WriteSuccess(format string, a ...any) {
	msg := formatConsoleMsg(format, a, "✓ ", "Success: ", "Éxito: ")
	writeStdoutFormatted(Green + "✓ " + msg + NC + "\n")
}


// --- Helpers ---

func effectiveThreads(extOrFile string, threadLimit int) int {
	limit := threadLimit
	if limit <= 0 {
		limit = NCPU()
	}

	ext := strings.ToLower(extOrFile)
	if ext == "lz4" || ext == "br" || ext == "tar" ||
		strings.HasSuffix(ext, ".lz4") || strings.HasSuffix(ext, ".br") ||
		strings.HasSuffix(ext, ".tar.lz4") || strings.HasSuffix(ext, ".tar.br") ||
		strings.HasSuffix(ext, ".tar") {
		return 1
	}

	if ext == "zip" || strings.HasSuffix(ext, ".zip") {
		if hasTool(sevenzBin()) {
			return limit
		}
		return 1
	}

	if ext == "gz" || strings.HasSuffix(ext, ".gz") || strings.HasSuffix(ext, ".tgz") || strings.HasSuffix(ext, ".tar.gz") {
		if hasTool("pigz") {
			return limit
		}
		return 1
	}

	if ext == "bz2" || strings.HasSuffix(ext, ".bz2") || strings.HasSuffix(ext, ".tbz2") || strings.HasSuffix(ext, ".tar.bz2") {
		bin := bzip2Bin()
		if bin == "lbzip2" || bin == "pbzip2" {
			return limit
		}
		return 1
	}

	if ext == "lz" || strings.HasSuffix(ext, ".lz") || strings.HasSuffix(ext, ".tlz") || strings.HasSuffix(ext, ".tar.lz") {
		if hasTool("plzip") {
			return limit
		}
		return 1
	}

	return limit
}

func stdoutFor(pt *ProgressTracker) io.Writer {
	if pt == nil {
		return os.Stdout
	}
	return getNullFile()
}

func stderrFor(pt *ProgressTracker) io.Writer {
	if pt == nil {
		return os.Stderr
	}
	return &bytes.Buffer{}
}

func augmentErr(cmd *exec.Cmd, err error) error {
	if err == nil {
		return nil
	}
	if b, ok := cmd.Stderr.(*bytes.Buffer); ok {
		if s := strings.TrimSpace(b.String()); s != "" {
			return fmt.Errorf("%w: %s", err, s)
		}
	}
	return err
}

func getNullFile() *os.File {
	if nullFile == nil {
		f, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			return os.Stderr
		}
		nullFile = f
	}
	return nullFile
}

func hasTool(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func totalFileSize(files []string) int64 {
	var total int64
	for _, f := range files {
		fi, err := os.Stat(f)
		if err == nil {
			total += fi.Size()
		}
	}
	return total
}

func parsePercent(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			end := i
			for end < len(s) && s[end] >= '0' && s[end] <= '9' {
				end++
			}
			if end < len(s) && s[end] == '%' {
				val, _ := strconv.Atoi(s[i:end])
				if val >= 0 && val <= 100 {
					return val
				}
			}
		}
	}
	return -1
}

func trackProgress(r io.Reader, pt *ProgressTracker, fileSize int64, fp *FileProgress) {
	if pt == nil || fileSize == 0 {
		return
	}
	if fp != nil {
		fp.SetHasExternalProgress(true)
	}
	br := bufio.NewReader(r)
	lastPct := 0
	var token strings.Builder

	updateWithToken := func() {
		if token.Len() == 0 {
			return
		}
		s := token.String()
		token.Reset()
		pct := parsePercent(s)
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

	for {
		b, err := br.ReadByte()
		if err != nil {
			updateWithToken()
			break
		}
		if b == '\r' || b == '\n' || b == '\x08' {
			updateWithToken()
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

func runWithProgress(cmd *exec.Cmd, pt *ProgressTracker, fileSize int64, fp *FileProgress) error {
	if pt == nil || fileSize == 0 {
		cmd.Stdout = os.Stdout
		cmd.Stderr = stderrFor(pt)
		return augmentErr(cmd, cmd.Run())
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("Error creando pipe: %w", err)
	}
	cmd.Stderr = stderrFor(pt)
	if err := cmd.Start(); err != nil {
		return err
	}
	trackProgress(stdout, pt, fileSize, fp)
	return augmentErr(cmd, cmd.Wait())
}

type ProgressTracker struct {
	total         int64
	current       atomic.Int64
	filesTotal    int
	filesDone     atomic.Int64
	currentFile   atomic.Value
	startTime     time.Time
	ticker        *time.Ticker
	done          chan struct{}
	stopped       chan struct{}
	stderrIsTTY   bool
	started       atomic.Bool
	files         []*FileProgress
	linesRendered int
	writer        io.Writer
}

func (pt *ProgressTracker) out() io.Writer {
	if pt.writer != nil {
		return pt.writer
	}
	return os.Stderr
}

func NewProgressTracker(total int64, filesTotal int) *ProgressTracker {
	pt := &ProgressTracker{
		total:       total,
		filesTotal:  filesTotal,
		startTime:   time.Now(),
		done:        make(chan struct{}),
		stderrIsTTY: isTerminal(),
	}
	pt.currentFile.Store("")
	return pt
}

func (pt *ProgressTracker) Add(n int64) {
	pt.current.Add(n)
}

func (pt *ProgressTracker) FileDone(name string) {
	pt.filesDone.Add(1)
	pt.currentFile.Store(name)
}

func (pt *ProgressTracker) SetCurrentFile(name string) {
	pt.currentFile.Store(name)
}

func (pt *ProgressTracker) SetFiles(files []*FileProgress) {
	pt.files = files
}

func (pt *ProgressTracker) Start() {
	if !pt.stderrIsTTY || pt.started.Swap(true) {
		return
	}
	loggingActive.Store(true)
	io.WriteString(pt.out(), "\033[?25l")
	pt.ticker = time.NewTicker(100 * time.Millisecond)
	pt.stopped = make(chan struct{})
	go func() {
		defer close(pt.stopped)
		for {
			select {
			case <-pt.ticker.C:
				pt.render()
			case <-pt.done:
				pt.ticker.Stop()
				return
			}
		}
	}()
}

func (pt *ProgressTracker) Stop() {
	if pt.started.Load() {
		pt.ticker.Stop()
		close(pt.done)
		<-pt.stopped
	} else {
		close(pt.done)
	}
	loggingActive.Store(false)
	pt.eraseBlock()
	if pt.stderrIsTTY {
		io.WriteString(pt.out(), "\033[?25h")
	}
	logMu.Lock()
	if logBuf.Len() > 0 {
		os.Stderr.Write(logBuf.Bytes())
		logBuf.Reset()
	}
	logMu.Unlock()
	if pt.stderrIsTTY {
		pt.renderFinal()
	}
}

func (pt *ProgressTracker) eraseBlock() {
	if !pt.stderrIsTTY || pt.linesRendered <= 0 {
		return
	}
	w := pt.out()
	fmt.Fprintf(w, "\r\033[%dA", pt.linesRendered)
	for i := 0; i < pt.linesRendered; i++ {
		io.WriteString(w, "\r\033[K\n")
	}
	fmt.Fprintf(w, "\r\033[%dA", pt.linesRendered)
}

func (pt *ProgressTracker) render() {
	if !pt.stderrIsTTY {
		return
	}
	current := pt.current.Load()
	done := pt.filesDone.Load()
	elapsed := time.Since(pt.startTime)

	var globalPct float64
	if pt.total > 0 {
		globalPct = float64(current) * 100 / float64(pt.total)
		if globalPct > 100 {
			globalPct = 100
		}
	}
	if pt.filesTotal > 0 {
		filePct := float64(done) * 100 / float64(pt.filesTotal)
		if filePct > globalPct {
			globalPct = filePct
		}
	}

	w := pt.out()
	if pt.linesRendered > 0 {
		fmt.Fprintf(w, "\r\033[%dA", pt.linesRendered)
	}

	io.WriteString(w, globalBarLine(globalPct, current, done, pt.filesTotal, pt.total, elapsed))
	io.WriteString(w, "\033[K\n")

	if len(pt.files) > 0 {
		io.WriteString(w, "\r\033[K\n")
		for _, fp := range pt.files {
			pollFileProgress(fp)
			io.WriteString(w, "\r"+fileLine(fp)+"\033[K\n")
		}
		pt.linesRendered = 2 + len(pt.files)
	} else {
		pt.linesRendered = 1
	}
}

func (pt *ProgressTracker) renderFinal() {
	current := pt.current.Load()
	done := pt.filesDone.Load()
	elapsed := time.Since(pt.startTime)

	line := fmt.Sprintf("%s✓%s 100%%  %d/%d", Green, NC, done, pt.filesTotal)
	if current > 0 && elapsed.Seconds() > 0 {
		speed := float64(current) / elapsed.Seconds()
		line += fmt.Sprintf("  %s/s", FormatSize(int64(speed)))
	}
	line += fmt.Sprintf("  %v  completado%s\n", elapsed.Round(time.Second), NC)
	io.WriteString(pt.out(), line)
}

func globalBarLine(pct float64, current int64, done int64, filesTotal int, total int64, elapsed time.Duration) string {
	bar := makeBar(pct, 20)

	line := fmt.Sprintf("\r[%s] %5.1f%%  %d/%d", bar, pct, done, filesTotal)

	if current > 0 && elapsed.Seconds() > 0 {
		speed := float64(current) / elapsed.Seconds()
		line += fmt.Sprintf("  %s/s", FormatSize(int64(speed)))
	}

	if total > 0 && current > 0 && current < total {
		if eta := formatETA(elapsed, current, total); eta != "" {
			line += fmt.Sprintf("  %s restantes", eta)
		}
	}

	return line
}

func fileLine(fp *FileProgress) string {
	pad := 26
	name := fp.Name
	if len(name) > pad-4 {
		name = name[:pad-7] + "..."
	}
	name = fmt.Sprintf("  %-"+fmt.Sprintf("%d", pad-2)+"s", name)

	switch fp.Status() {
	case "waiting":
		bar := makeBar(0, 10)
		sizeStr := fmt.Sprintf("%8s / %-8s", "0B", fmtSizeDec(fp.Size))
		return fmt.Sprintf("\033[0;39m%s [%s]   0%%  %s   %sesperando...%s", name, bar, sizeStr, Yellow, NC)
	case "active":
		current := fp.Current.Load()
		if current == 0 && fp.Size == 0 {
			return fmt.Sprintf("%s%sen proceso...%s", Yellow, name, NC)
		}
		var pct float64
		if fp.Size > 0 {
			pct = float64(current) * 100 / float64(fp.Size)
			if pct > 100 {
				pct = 100
			}
		}
		bar := makeBar(pct, 10)
		sizeStr := fmt.Sprintf("%8s / %-8s", fmtSizeDec(current), fmtSizeDec(fp.Size))
		etaStr := "         "
		doneParts, totalParts := fp.Parts()
		if totalParts > 1 {
			etaStr = fmt.Sprintf("%-9s", fmt.Sprintf("%d/%d", doneParts, totalParts))
		} else if current > 0 && fp.Size > 0 && current < fp.Size {
			start := fp.StartTime()
			if !start.IsZero() {
				elapsed := time.Since(start)
				if eta := formatETA(elapsed, current, fp.Size); eta != "" {
					etaStr = fmt.Sprintf("%9s", eta)
				}
			}
		}
		line := fmt.Sprintf("%s [%s] %3d%%  %s   %s", name, bar, int(pct), sizeStr, etaStr)
		return fmt.Sprintf("%s%s%s", Yellow, line, NC)
	case "done":
		bar := makeBar(100, 10)
		sizeStr := fmt.Sprintf("%8s / %-8s", fmtSizeDec(fp.Size), fmtSizeDec(fp.Size))
		doneParts, totalParts := fp.Parts()
		statusMark := "        ✓"
		if totalParts > 1 {
			statusMark = fmt.Sprintf(" (%d/%d) ✓", doneParts, totalParts)
		}
		line := fmt.Sprintf("%s [%s] 100%%  %s%s", name, bar, sizeStr, statusMark)
		return fmt.Sprintf("%s%s%s", Green, line, NC)
	case "error":
		current := fp.Current.Load()
		var pct float64
		if fp.Size > 0 {
			pct = float64(current) * 100 / float64(fp.Size)
			if pct > 100 {
				pct = 100
			}
		}
		bar := makeBar(pct, 10)
		sizeStr := fmt.Sprintf("%8s / %-8s", fmtSizeDec(current), fmtSizeDec(fp.Size))
		line := fmt.Sprintf("%s [%s] %3d%%  %s        ✗", name, bar, int(pct), sizeStr)
		return fmt.Sprintf("%s%s%s", Red, line, NC)
	default:
		return fmt.Sprintf("%s %s", name, fp.Status())
	}
}

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

func makeBar(pct float64, width int) string {
	filled := int(pct / 100 * float64(width))
	if filled > width {
		filled = width
	}
	if filled >= width {
		return strings.Repeat("=", width)
	} else if filled > 0 {
		return strings.Repeat("=", filled-1) + ">" + strings.Repeat(" ", width-filled)
	} else {
		return strings.Repeat(" ", width)
	}
}

func fmtSizeDec(bytes int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	unit := 0
	sz := float64(bytes)
	for sz >= 1024 && unit < 5 {
		sz /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%.0f%s", sz, units[unit])
	}
	return fmt.Sprintf("%.1f%s", sz, units[unit])
}



func isTerminal() bool {
	fi, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

type countingWriter struct {
	w  io.Writer
	pt *ProgressTracker
	fp *FileProgress
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	cw.pt.Add(int64(n))
	if cw.fp != nil {
		cw.fp.Current.Add(int64(n))
	}
	return n, err
}

func (cw *countingWriter) Close() error {
	if closer, ok := cw.w.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

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

func (cr *countingReader) Close() error {
	if closer, ok := cr.r.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// --- Pipeline: chain multiple commands ---

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (lw *lockedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	w := lw.w
	if w == nil {
		if lw == lockedStdout {
			w = os.Stdout
		} else {
			w = os.Stderr
		}
	}
	return w.Write(p)
}

const defaultPipeCapacity = 1048576 // 1 MiB

func setPipeCapacity(r io.Reader, w io.Writer, size int) {
	if size <= 0 {
		return
	}
	setPipeCapacityOS(r, w, size)
}

func getPipeCapacity(r io.Reader, w io.Writer) int {
	return getPipeCapacityOS(r, w)
}

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
		setPipeCapacity(cmds[i+1].Stdin, cmds[i].Stdout, defaultPipeCapacity)
	}
	cmds[len(cmds)-1].Stdout = stdout
	if stderr != nil {
		lw := &lockedWriter{w: stderr}
		for i := range cmds {
			cmds[i].Stderr = lw
		}
	}
	for i := range cmds {
		if err := cmds[i].Start(); err != nil {
			for j := 0; j < i; j++ {
				if cmds[j].Process != nil {
					_ = cmds[j].Process.Kill()
					_ = cmds[j].Wait()
				}
			}
			return fmt.Errorf("pipeline start %d: %w", i, err)
		}
	}

	type cmdResult struct {
		idx int
		err error
	}
	resCh := make(chan cmdResult, len(cmds))
	for i, cmd := range cmds {
		go func(index int, c *exec.Cmd) {
			resCh <- cmdResult{idx: index, err: c.Wait()}
		}(i, cmd)
	}

	var firstErr error
	for received := 0; received < len(cmds); received++ {
		res := <-resCh
		if res.err != nil && firstErr == nil {
			firstErr = fmt.Errorf("pipeline cmd %d: %w", res.idx, res.err)
			for j, cmd := range cmds {
				if j != res.idx && cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			}
		}
	}
	return firstErr
}

func ComputeSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func WriteSHA256File(archivePath string) (string, error) {
	hashHex, err := ComputeSHA256(archivePath)
	if err != nil {
		return "", err
	}
	shaPath := archivePath + ".sha256"
	content := fmt.Sprintf("%s  %s\n", hashHex, filepath.Base(archivePath))
	if err := os.WriteFile(shaPath, []byte(content), 0644); err != nil {
		return "", err
	}
	return hashHex, nil
}

func ParseSHA256File(shaPath, targetFile string) (string, error) {
	data, err := os.ReadFile(shaPath)
	if err != nil {
		return "", err
	}
	base := filepath.Base(targetFile)
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			filename := strings.TrimPrefix(fields[1], "*")
			if filename == base {
				return strings.ToLower(fields[0]), nil
			}
		}
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && len(fields[0]) == 64 {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no se encontró hash válido para %s en %s", base, shaPath)
}

func readPasswordTerminal(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	var fd uintptr
	var f *os.File
	if err == nil {
		defer tty.Close()
		fd = tty.Fd()
		f = tty
	} else {
		fd = os.Stdin.Fd()
		f = os.Stdin
	}

	restore, err := disableTerminalEchoOS(fd)
	if err == nil && restore != nil {
		defer func() {
			restore()
			fmt.Fprintln(os.Stderr)
		}()
	}

	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", io.EOF
}

var readPasswordFunc = readPasswordTerminal

// AllocateThreadsProportional distributes totalCores among files based on their sizes.
// Files with larger sizes receive proportionally more threads, ensuring all files finish
// at approximately the same time and eliminating tail latency (idle CPU cores).
// Each file receives at least minThreads (default 1) and at most maxThreadsPerJob.
// If sizes are 0 or unknown, it distributes threads evenly.
func AllocateThreadsProportional(sizes []int64, totalCores int, minThreads int, maxThreadsPerJob int) []int {
	n := len(sizes)
	if n == 0 {
		return nil
	}
	if minThreads < 1 {
		minThreads = 1
	}
	if maxThreadsPerJob < minThreads {
		maxThreadsPerJob = minThreads
	}
	if totalCores < n*minThreads {
		totalCores = n * minThreads
	}

	result := make([]int, n)

	if n == 1 {
		t := totalCores
		if t > maxThreadsPerJob {
			t = maxThreadsPerJob
		}
		if t < minThreads {
			t = minThreads
		}
		result[0] = t
		return result
	}

	if maxThreadsPerJob == 1 {
		for i := range result {
			result[i] = 1
		}
		return result
	}

	var totalSize int64
	allZero := true
	for _, sz := range sizes {
		if sz > 0 {
			totalSize += sz
			allZero = false
		}
	}

	if allZero || totalSize == 0 {
		base := totalCores / n
		rem := totalCores % n
		for i := 0; i < n; i++ {
			t := base
			if i < rem {
				t++
			}
			if t > maxThreadsPerJob {
				t = maxThreadsPerJob
			}
			if t < minThreads {
				t = minThreads
			}
			result[i] = t
		}
		return result
	}

	// Calculate proportional threads using Largest Remainder Method (Hamilton-Hare)
	type itemRemainder struct {
		index     int
		remainder float64
	}

	allocated := 0
	remainders := make([]itemRemainder, n)

	for i, sz := range sizes {
		raw := float64(totalCores) * float64(max(0, sz)) / float64(totalSize)
		count := int(raw)
		if count < minThreads {
			count = minThreads
		}
		if count > maxThreadsPerJob {
			count = maxThreadsPerJob
		}
		result[i] = count
		allocated += count
		remainders[i] = itemRemainder{index: i, remainder: raw - float64(int(raw))}
	}

	if allocated < totalCores {
		diff := totalCores - allocated
		sort.Slice(remainders, func(i, j int) bool {
			if remainders[i].remainder == remainders[j].remainder {
				return sizes[remainders[i].index] > sizes[remainders[j].index]
			}
			return remainders[i].remainder > remainders[j].remainder
		})
		for i := 0; i < diff && i < n; i++ {
			idx := remainders[i%n].index
			if result[idx] < maxThreadsPerJob {
				result[idx]++
				allocated++
			}
		}
	} else if allocated > totalCores {
		diff := allocated - totalCores
		sort.Slice(remainders, func(i, j int) bool {
			if remainders[i].remainder == remainders[j].remainder {
				return sizes[remainders[i].index] < sizes[remainders[j].index]
			}
			return remainders[i].remainder < remainders[j].remainder
		})
		for i := 0; i < diff && i < n; i++ {
			idx := remainders[i%n].index
			if result[idx] > minThreads {
				result[idx]--
				allocated--
			}
		}
	}

	return result
}

// PipeFlagsForThreads returns appropriate decompression CLI flags injecting the given thread limit.
func PipeFlagsForThreads(info FormatInfo, threads int) []string {
	if threads <= 0 {
		threads = NCPU()
	}
	thStr := fmt.Sprintf("%d", threads)
	switch info.Tool {
	case "pigz":
		return []string{"-dc", "-p", thStr}
	case "xz":
		return []string{"-dc", "-T" + thStr}
	case "zstd":
		return []string{"-dc", "-T" + thStr}
	case "bzip3":
		return []string{"-dc", "-j", thStr}
	case "plzip":
		return []string{"-dc", "--threads=" + thStr}
	case "lbzip2":
		return []string{"-dc", "-n", thStr}
	case "lrzip":
		return []string{"-d", "-p", thStr, "-o", "-"}
	case "lz4":
		if lz4SupportsThreads() {
			return []string{"-dc", "-T" + thStr}
		}
		return []string{"-dc"}
	default:
		return strings.Fields(info.PipeFlags)
	}
}

// DynamicThreadPool coordinates CPU thread tokens among concurrent workers.
type DynamicThreadPool struct {
	mu              sync.Mutex
	totalTokens     int
	availableTokens int
	minThreads      int
	maxThreads      int
	remainingFiles  int
	remainingBytes  int64
}

// NewDynamicThreadPool creates a new thread pool with the specified limits.
func NewDynamicThreadPool(totalCores, minThreads, maxThreads, totalFiles int, totalBytes int64) *DynamicThreadPool {
	if totalCores < 1 {
		totalCores = NCPU()
	}
	if minThreads < 1 {
		minThreads = 1
	}
	if maxThreads < minThreads {
		maxThreads = totalCores
	}
	return &DynamicThreadPool{
		totalTokens:     totalCores,
		availableTokens: totalCores,
		minThreads:      minThreads,
		maxThreads:      maxThreads,
		remainingFiles:  totalFiles,
		remainingBytes:  totalBytes,
	}
}

// Acquire requests thread tokens for a file of fileSize bytes.
func (p *DynamicThreadPool) Acquire(fileSize int64) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.remainingFiles <= 1 || p.remainingBytes <= 0 || fileSize >= p.remainingBytes {
		tokens := p.availableTokens
		if tokens > p.maxThreads {
			tokens = p.maxThreads
		}
		if tokens < p.minThreads {
			tokens = p.minThreads
		}
		p.availableTokens -= tokens
		if p.availableTokens < 0 {
			p.availableTokens = 0
		}
		p.remainingFiles--
		p.remainingBytes -= fileSize
		return tokens
	}

	ratio := float64(fileSize) / float64(p.remainingBytes)
	target := int(float64(p.availableTokens) * ratio)
	if target < p.minThreads {
		target = p.minThreads
	}
	if target > p.maxThreads {
		target = p.maxThreads
	}
	if target > p.availableTokens && p.availableTokens >= p.minThreads {
		target = p.availableTokens
	}

	p.availableTokens -= target
	if p.availableTokens < 0 {
		p.availableTokens = 0
	}
	p.remainingFiles--
	p.remainingBytes -= fileSize
	return target
}

// Release returns the allocated tokens back to the pool.
func (p *DynamicThreadPool) Release(tokens int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.availableTokens += tokens
	if p.availableTokens > p.totalTokens {
		p.availableTokens = p.totalTokens
	}
}



