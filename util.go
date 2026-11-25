package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
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

	logFile      *os.File
	nullFile     *os.File
	nullFileOnce sync.Once

	logBuf          bytes.Buffer
	logBufTruncated bool
	logMu           sync.Mutex
	loggingActive   atomic.Bool

	activeProgressTracker atomic.Pointer[ProgressTracker]

	execCommand = exec.Command
)

type FileProgress struct {
	Name                string
	Size                int64
	Current             atomic.Int64
	uncompressedSize    atomic.Int64
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

func (fp *FileProgress) SetUncompressedSize(sz int64) {
	if fp != nil {
		fp.uncompressedSize.Store(sz)
	}
}

func (fp *FileProgress) GetUncompressedSize() int64 {
	if fp != nil {
		return fp.uncompressedSize.Load()
	}
	return 0
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
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err == nil {
				total += info.Size()
			}
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

const (
	cgroupUnlimitedBytes = int64(1) << 60
	defaultMemLimitMB    = 1024
)

var (
	getMemLimit = GetMemLimit

	procMeminfoPath  = "/proc/meminfo"
	cgroupV2MemMax   = "/sys/fs/cgroup/memory.max"
	cgroupV2MemCurr  = "/sys/fs/cgroup/memory.current"
	cgroupV1MemLimit = "/sys/fs/cgroup/memory/memory.limit_in_bytes"
	cgroupV1MemUsage = "/sys/fs/cgroup/memory/memory.usage_in_bytes"

	memCacheMu    sync.Mutex
	memCacheVal   int
	memCacheAt    time.Time
	memCacheValid bool
	memCacheTTL   = 250 * time.Millisecond
	memNow        = time.Now
)

func resetMemLimitCache() {
	memCacheMu.Lock()
	memCacheValid = false
	memCacheVal = 0
	memCacheAt = time.Time{}
	memCacheMu.Unlock()
}

func GetMemLimit() int {
	memCacheMu.Lock()
	defer memCacheMu.Unlock()

	now := memNow()
	if memCacheValid {
		if elapsed := now.Sub(memCacheAt); elapsed >= 0 && elapsed < memCacheTTL {
			return memCacheVal
		}
	}

	val := probeMemLimitMB()
	memCacheVal = val
	memCacheAt = now
	memCacheValid = true
	return val
}

func probeMemLimitMB() int {
	hostBytes, hostOK := readHostMemAvailBytes()
	cgBytes, cgOK := readCgroupMemAvailBytes()

	var availBytes int64
	switch {
	case hostOK && cgOK:
		availBytes = min(hostBytes, cgBytes)
	case hostOK:
		availBytes = hostBytes
	case cgOK:
		availBytes = cgBytes
	default:
		return defaultMemLimitMB
	}

	mb := int((availBytes / 1024) * 70 / 100 / 1024)
	if mb < 1 {
		return 1
	}
	return mb
}

func readHostMemAvailBytes() (int64, bool) {
	if data, err := os.ReadFile(procMeminfoPath); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "MemAvailable:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil && kb > 0 && kb <= math.MaxInt64/1024 {
						return kb * 1024, true
					}
				}
			}
		}
	}
	return getMemAvailBytesFromFree()
}

func readInt64File(path string) (int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(data))
	if s == "" || s == "max" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func readCgroupPair(limitPath, usagePath string) (int64, bool) {
	limit, ok := readInt64File(limitPath)
	if !ok || limit <= 0 || limit >= cgroupUnlimitedBytes {
		return 0, false
	}
	usage, ok := readInt64File(usagePath)
	if !ok || usage < 0 {
		return limit, true
	}
	rem := limit - usage
	if rem < 0 {
		rem = 0
	}
	return rem, true
}

func readCgroupMemAvailBytes() (int64, bool) {
	if rem, ok := readCgroupPair(cgroupV2MemMax, cgroupV2MemCurr); ok {
		return rem, true
	}
	return readCgroupPair(cgroupV1MemLimit, cgroupV1MemUsage)
}

func getMemAvailBytesFromFree() (int64, bool) {
	cmd := execCommand("free", "-k")
	out, err := cmd.Output()
	if err != nil {
		return 0, false
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Mem:") {
			fields := strings.Fields(line)
			if len(fields) >= 7 {
				if avail, err := strconv.ParseInt(fields[6], 10, 64); err == nil && avail > 0 && avail <= math.MaxInt64/1024 {
					return avail * 1024, true
				}
			}
		}
	}
	return 0, false
}

func getMemFromFree() int {
	if b, ok := getMemAvailBytesFromFree(); ok && b > 0 {
		return int((b / 1024) * 70 / 100 / 1024)
	}
	return 0
}

// --- Size formatting ---

func FormatSize(bytes int64) string {
	neg := bytes < 0
	var u uint64
	if neg {
		u = uint64(-bytes)
	} else {
		u = uint64(bytes)
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	unit := 0
	size := float64(u)
	for size >= 1024 && unit < 5 {
		size /= 1024
		unit++
	}
	prefix := ""
	if neg {
		prefix = "-"
	}
	if unit == 0 {
		return fmt.Sprintf("%s%d %s", prefix, u, units[unit])
	}
	return fmt.Sprintf("%s%.1f %s", prefix, size, units[unit])
}

func CalcPct(orig, final int64) string {
	if orig > 0 {
		pct := float64(orig-final) / float64(orig) * 100
		return fmt.Sprintf("%.2f", pct)
	}
	return "0.00"
}

// --- Disk space ---

var getDiskAvail = getDiskAvailOrUnknown

func getDiskAvailOrUnknown(dir string) int64 {
	avail, ok := probeDiskAvail(dir)
	if !ok {
		return -1
	}
	return avail
}

func probeDiskAvail(dir string) (int64, bool) {
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

	safeTarget := safeFlagArg(target)
	cmd := execCommand("df", "-B1", "--output=avail", "--", safeTarget)
	out, err := cmd.Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		if len(lines) >= 2 {
			s := strings.TrimSpace(lines[1])
			if avail, ok := parseDiskInt64Clamped(s, 1); ok {
				return avail, true
			}
		}
	}

	cmd = execCommand("df", "--", safeTarget)
	out, err = cmd.Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[1])
			if len(fields) >= 4 {
				if avail, ok := parseDiskInt64Clamped(fields[3], 1024); ok {
					return avail, true
				}
			}
		}
	}

	return 0, false
}

func parseDiskInt64Clamped(s string, scale int64) (int64, bool) {
	if s == "" {
		return 0, false
	}
	if strings.HasPrefix(s, "-") {
		if _, err := strconv.ParseInt(s, 10, 64); err == nil {
			return 0, true
		}
		return 0, false
	}
	u, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	if scale > 1 && u > uint64(math.MaxInt64)/uint64(scale) {
		return math.MaxInt64, true
	}
	prod := u * uint64(scale)
	if prod > uint64(math.MaxInt64) {
		return math.MaxInt64, true
	}
	return int64(prod), true
}

func GetAvailBytes(dir string) int64 {
	avail, ok := probeDiskAvail(dir)
	if !ok {
		return 0
	}
	return avail
}

func CheckDiskSpace(needed int64, dir string, op string) error {
	avail := getDiskAvail(dir)
	if avail < 0 {
		return nil
	}
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

func addClampedInt64(a, b int64) int64 {
	if b <= 0 {
		return a
	}
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func mulDivClampedInt64(v, num, den int64) int64 {
	if v <= 0 || num <= 0 || den <= 0 {
		return 0
	}
	q := v / den
	r := v % den
	if q > math.MaxInt64/num {
		return math.MaxInt64
	}
	return addClampedInt64(q*num, (r*num)/den)
}

func parsePositiveSizeClamped(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "-") {
		return 0, false
	}
	u, err := strconv.ParseUint(s, 10, 64)
	if err != nil || u == 0 {
		return 0, false
	}
	if u > uint64(math.MaxInt64) {
		return math.MaxInt64, true
	}
	return int64(u), true
}

func sum7zSltSizes(tool, safeFile string) int64 {
	cmd := execCommand(tool, "l", "-slt", "-p-", "--", safeFile)
	cmd.Stdin = getNullFile()
	out, _ := cmd.Output()
	var sum int64
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Size = ") {
			parts := strings.SplitN(line, "= ", 2)
			if len(parts) == 2 {
				if size, ok := parsePositiveSizeClamped(parts[1]); ok {
					sum = addClampedInt64(sum, size)
				}
			}
		}
	}
	return sum
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
			est := mulDivClampedInt64(totalSize, 95, 100)
			if est < 1<<20 {
				est = 1 << 20
			}
			return est
		}
	}

	var pct int64
	switch format {
	case Tar:
		pct = 102
	case Lz4:
		pct = 60
	case Zip:
		pct = 50
	case Gz:
		pct = 40
	case Zst:
		pct = 35
	case Bz2, Rar:
		pct = 30
	case Br:
		pct = 28
	case SevenZ, Xz, Bz3, Lz:
		pct = 25
	case Lrz:
		pct = 20
	default:
		pct = 35
	}

	estimated := mulDivClampedInt64(totalSize, pct, 100)
	if estimated < 1<<20 {
		estimated = 1 << 20
	}
	return estimated
}

func EstimateUncompressedSize(file string) int64 {
	f := strings.ToLower(file)
	safeFile := safeFlagArg(file)

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
		cmd := execCommand("gzip", "-l", "--", safeFile)
		cmd.Stdin = getNullFile()
		out, _ := cmd.Output()
		lines := strings.Split(string(out), "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[1])
			if len(fields) >= 2 {
				if size, ok := parsePositiveSizeClamped(fields[1]); ok {
					return size
				}
			}
		}

	case strings.HasSuffix(f, ".xz") || strings.HasSuffix(f, ".txz"):
		cmd := execCommand("xz", "-l", "--robot", "--", safeFile)
		cmd.Stdin = getNullFile()
		out, _ := cmd.Output()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "file\t") {
				fields := strings.Split(line, "\t")
				if len(fields) >= 5 {
					if size, ok := parsePositiveSizeClamped(fields[4]); ok {
						return size
					}
				}
			}
		}

	case strings.HasSuffix(f, ".zst") || strings.HasSuffix(f, ".tzst"):
		cmd := execCommand("zstd", "-lv", "--", safeFile)
		cmd.Stdin = getNullFile()
		out, _ := cmd.Output()
		var sum int64
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "Decompressed Size:") {
				if idx1 := strings.LastIndex(line, "("); idx1 != -1 {
					if idx2 := strings.LastIndex(line, " B)"); idx2 != -1 && idx2 > idx1 {
						numStr := strings.TrimSpace(line[idx1+1 : idx2])
						if size, ok := parsePositiveSizeClamped(numStr); ok {
							sum = addClampedInt64(sum, size)
						}
					}
				}
			}
		}
		if sum > 0 {
			return sum
		}
		cmdL := execCommand("zstd", "-l", "--", safeFile)
		cmdL.Stdin = getNullFile()
		outL, _ := cmdL.Output()
		for _, line := range strings.Split(string(outL), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 5 {
				if size, ok := parsePositiveSizeClamped(fields[4]); ok {
					sum = addClampedInt64(sum, size)
				} else if fval, err := strconv.ParseFloat(fields[4], 64); err == nil && fval > 0 && !math.IsNaN(fval) && len(fields) >= 6 {
					unit := strings.ToUpper(fields[5])
					mult := float64(1)
					switch unit {
					case "KIB", "KB", "K":
						mult = 1024
					case "MIB", "MB", "M":
						mult = 1024 * 1024
					case "GIB", "GB", "G":
						mult = 1024 * 1024 * 1024
					case "TIB", "TB", "T":
						mult = 1024 * 1024 * 1024 * 1024
					}
					prod := fval * mult
					if math.IsInf(prod, 1) || prod >= float64(math.MaxInt64) {
						sum = math.MaxInt64
					} else if prod > 0 {
						sum = addClampedInt64(sum, int64(prod))
					}
				}
			}
		}
		if sum > 0 {
			return sum
		}

	case strings.HasSuffix(f, ".zip"):
		if r, err := zip.OpenReader(file); err == nil {
			var sum uint64
			for _, zf := range r.File {
				if zf.UncompressedSize64 > uint64(math.MaxInt64)-sum {
					sum = uint64(math.MaxInt64)
					break
				}
				sum += zf.UncompressedSize64
			}
			r.Close()
			if sum > 0 {
				return int64(sum)
			}
		}
		cmd := execCommand("unzip", "-l", "--", safeFile)
		cmd.Stdin = getNullFile()
		out, _ := cmd.Output()
		lines := strings.Split(string(out), "\n")
		for len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
		if len(lines) >= 3 {
			fields := strings.Fields(lines[len(lines)-1])
			if len(fields) >= 1 {
				if size, ok := parsePositiveSizeClamped(fields[0]); ok {
					return size
				}
			}
		}

	case strings.HasSuffix(f, ".7z"):
		if sum := sum7zSltSizes(sevenzBin(), safeFile); sum > 0 {
			return sum
		}

	case strings.HasSuffix(f, ".rar"):
		tool := "7z"
		if !hasTool("7z") {
			tool = sevenzBin()
		}
		if sum := sum7zSltSizes(tool, safeFile); sum > 0 {
			return sum
		}
		if fi, err := os.Stat(file); err == nil {
			return mulDivClampedInt64(fi.Size(), 3, 1)
		}

	case strings.HasSuffix(f, ".lrz"):
		cmd := execCommand("lrzip", "-i", "--", safeFile)
		cmd.Stdin = getNullFile()
		out, _ := cmd.Output()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "Decompressed file size") {
				fields := strings.Fields(line)
				if len(fields) >= 1 {
					if size, ok := parsePositiveSizeClamped(fields[len(fields)-1]); ok {
						return size
					}
					if len(fields) >= 2 {
						if size, ok := parsePositiveSizeClamped(fields[len(fields)-2]); ok {
							return size
						}
					}
				}
			}
		}

	case strings.HasSuffix(f, ".lz") || strings.HasSuffix(f, ".tlz"):
		tool := "plzip"
		if !hasTool("plzip") {
			tool = "lzip"
		}
		if hasTool(tool) {
			cmd := execCommand(tool, "-l", "--", safeFile)
			cmd.Stdin = getNullFile()
			out, _ := cmd.Output()
			lines := strings.Split(string(out), "\n")
			if len(lines) >= 2 {
				fields := strings.Fields(lines[1])
				if len(fields) >= 2 {
					if size, ok := parsePositiveSizeClamped(fields[0]); ok {
						return size
					}
				}
			}
		}
		if fi, err := os.Stat(file); err == nil {
			return mulDivClampedInt64(fi.Size(), 4, 1)
		}

	case strings.HasSuffix(f, ".tar"):
		if fi, err := os.Stat(file); err == nil {
			return fi.Size()
		}

	case strings.HasSuffix(f, ".lz4"):
		if fi, err := os.Stat(file); err == nil {
			return mulDivClampedInt64(fi.Size(), 2, 1)
		}

	case strings.HasSuffix(f, ".br"):
		if fi, err := os.Stat(file); err == nil {
			return mulDivClampedInt64(fi.Size(), 7, 2)
		}

	case strings.HasSuffix(f, ".bz2") || strings.HasSuffix(f, ".tbz2") ||
		strings.HasSuffix(f, ".bz3"):
		if fi, err := os.Stat(file); err == nil {
			return mulDivClampedInt64(fi.Size(), 4, 1)
		}
	}

	// Fallback
	if fi, err := os.Stat(file); err == nil {
		return mulDivClampedInt64(fi.Size(), 3, 1)
	}
	return 0
}

// --- Filename ---

const maxReservedNames = 4096

var (
	uniqueNameMu  sync.Mutex
	reservedNames = make(map[string]uint64)
	reservedSeq   uint64
)

func safeFlagArg(p string) string {
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "-") || strings.HasPrefix(p, "@") {
		return "./" + p
	}
	if filepath.VolumeName(p) == "" {
		if colon := strings.IndexByte(p, ':'); colon >= 0 {
			slash := strings.IndexAny(p, `/\`)
			if slash < 0 || colon < slash {
				return "./" + p
			}
		}
	}
	return p
}

func resolveThreadCapForBudget(f Format, sizeBytes int64, memBudgetMB int) int {
	c := MaxEffectiveThreads(f, sizeBytes)
	c = MemorySafeMaxThreadsForBudget(f, c, memBudgetMB)
	if fmtMax := FormatMaxThreads(f); c > fmtMax {
		c = fmtMax
	}
	if c < 1 {
		c = 1
	}
	return c
}

func resolveThreadCap(f Format, sizeBytes int64) int {
	return resolveThreadCapForBudget(f, sizeBytes, getMemLimit())
}

func ReleaseReservedName(name string) {
	uniqueNameMu.Lock()
	delete(reservedNames, name)
	uniqueNameMu.Unlock()
}

func reserveNameLocked(name string) {
	reservedSeq++
	reservedNames[name] = reservedSeq
	if len(reservedNames) > maxReservedNames {
		pruneReservedNamesLocked()
	}
}

func pruneReservedNamesLocked() {
	for k := range reservedNames {
		if _, err := os.Lstat(k); err == nil {
			delete(reservedNames, k)
		}
	}
	if len(reservedNames) <= maxReservedNames {
		return
	}
	type entry struct {
		name string
		seq  uint64
	}
	entries := make([]entry, 0, len(reservedNames))
	for k, seq := range reservedNames {
		entries = append(entries, entry{name: k, seq: seq})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].seq < entries[j].seq
	})
	target := maxReservedNames / 2
	for i := 0; i < len(entries)-target; i++ {
		delete(reservedNames, entries[i].name)
	}
}

func isNameAvailable(name string) bool {
	if _, ok := reservedNames[name]; ok {
		return false
	}
	_, err := os.Lstat(name)
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
		reserveNameLocked(name)
		return name
	}
	for counter := 1; ; counter++ {
		name = fmt.Sprintf("%s_%d.%s", base, counter, ext)
		if isNameAvailable(name) {
			reserveNameLocked(name)
			return name
		}
	}
}

// --- Logging ---

func ensurePrivateLogDir(dir string) bool {
	if dir == "" || !filepath.IsAbs(dir) {
		return false
	}
	if fi, err := os.Lstat(dir); err == nil {
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || !isOwnedByCurrentUserOS(fi) {
			return false
		}
	} else if !os.IsNotExist(err) {
		return false
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return false
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 || !isOwnedByCurrentUserOS(fi) {
		return false
	}
	return true
}

func resolvePrivateLogDir() (string, error) {
	if os.Geteuid() == 0 {
		if ensurePrivateLogDir("/var/log/crush") {
			return "/var/log/crush", nil
		}
	}
	if state := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(state) {
		dir := filepath.Join(state, "crush")
		if ensurePrivateLogDir(dir) {
			return dir, nil
		}
	}
	if cache, err := os.UserCacheDir(); err == nil && filepath.IsAbs(cache) {
		dir := filepath.Join(cache, "crush", "logs")
		if ensurePrivateLogDir(dir) {
			return dir, nil
		}
	}
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("crush-log-%d", os.Getuid()))
	if ensurePrivateLogDir(dir) {
		return dir, nil
	}
	tmpDir, err := os.MkdirTemp(os.TempDir(), fmt.Sprintf("crush-log-%d-*", os.Getuid()))
	if err != nil {
		return "", err
	}
	if !ensurePrivateLogDir(tmpDir) {
		return "", fmt.Errorf("no se pudo asegurar directorio privado de logs: %s", tmpDir)
	}
	return tmpDir, nil
}

func openLogFileInRoot(root *os.Root, now string, pid int) (*os.File, error) {
	base := fmt.Sprintf("crush_%s_%d.log", now, pid)
	if f, err := root.OpenFile(base, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
		_ = f.Chmod(0o600)
		return f, nil
	}
	for seq := 1; seq <= 1024; seq++ {
		name := fmt.Sprintf("crush_%s_%d_%d.log", now, pid, seq)
		f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_ = f.Chmod(0o600)
			return f, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("no se pudo crear archivo de log único")
}

func SetupLogging() error {
	logDir, err := resolvePrivateLogDir()
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(logDir)
	if err != nil {
		return err
	}
	defer root.Close()

	now := time.Now().Format("20060102_150405")
	f, err := openLogFileInRoot(root, now, os.Getpid())
	if err != nil {
		return err
	}

	logMu.Lock()
	if logFile != nil {
		_ = logFile.Close()
	}
	logFile = f
	logMu.Unlock()

	return nil
}

func appendToLogFile(s string) {
	logMu.Lock()
	if logFile != nil {
		_, _ = logFile.WriteString(s)
	}
	logMu.Unlock()
}

const (
	maxLogBufBytes    = 1024 * 1024
	shrinkLogBufBytes = 64 * 1024
	logBufTruncNotice = "\n... [log buffer truncated]\n"
)

func appendToLogBufLocked(s string) {
	if logBuf.Len() == 0 {
		logBufTruncated = false
	}
	if logBufTruncated {
		return
	}
	limit := maxLogBufBytes - len(logBufTruncNotice)
	if logBuf.Len()+len(s) <= limit {
		logBuf.WriteString(s)
		return
	}
	if rem := limit - logBuf.Len(); rem > 0 {
		for rem > 0 && rem < len(s) && !utf8.RuneStart(s[rem]) {
			rem--
		}
		logBuf.WriteString(s[:rem])
	}
	logBuf.WriteString(logBufTruncNotice)
	logBufTruncated = true
}

func appendToLogBuf(s string) {
	logMu.Lock()
	appendToLogBufLocked(s)
	logMu.Unlock()
}

func setLoggingActive(active bool) {
	logMu.Lock()
	loggingActive.Store(active)
	logMu.Unlock()
}

func swapLoggingActiveAndFlush(active bool) bool {
	logMu.Lock()
	prev := loggingActive.Swap(active)
	var pending []byte
	if logBuf.Len() > 0 {
		pending = append([]byte(nil), logBuf.Bytes()...)
		logBuf.Reset()
	}
	if logBuf.Cap() > shrinkLogBufBytes {
		logBuf = bytes.Buffer{}
	}
	logBufTruncated = false
	logMu.Unlock()

	if len(pending) > 0 {
		_, _ = lockedStderr.Write(pending)
	}
	return prev
}

func flushLogBuf() {
	logMu.Lock()
	var pending []byte
	if logBuf.Len() > 0 {
		pending = append([]byte(nil), logBuf.Bytes()...)
		logBuf.Reset()
	}
	if logBuf.Cap() > shrinkLogBufBytes {
		logBuf = bytes.Buffer{}
	}
	logBufTruncated = false
	logMu.Unlock()

	if len(pending) > 0 {
		_, _ = lockedStderr.Write(pending)
	}
}

func writeStderrOrLogBuf(s string) {
	logMu.Lock()
	if loggingActive.Load() {
		appendToLogBufLocked(s)
		logMu.Unlock()
		return
	}
	logMu.Unlock()
	_, _ = lockedStderr.Write([]byte(s))
}

func WriteLog(s string) {
	writeStderrOrLogBuf(s)
	appendToLogFile(s)
}

func WriteLogf(format string, args ...interface{}) {
	WriteLog(fmt.Sprintf(format, args...))
}

func CloseLog() {
	logMu.Lock()
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
	logMu.Unlock()
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
	writeStderrOrLogBuf(out)
	appendToLogFile(out)
}

func writeStdoutFormatted(out string) {
	lockedStdout.Write([]byte(out))
	appendToLogFile(out)
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

const maxStderrBytes = 32 * 1024

type limitedBuffer struct {
	mu        sync.Mutex
	max       int
	buf       []byte
	truncated bool
}

func newLimitedBuffer(maxBytes int) *limitedBuffer {
	if maxBytes <= 0 {
		maxBytes = maxStderrBytes
	}
	return &limitedBuffer{max: maxBytes}
}

func (lb *limitedBuffer) Write(p []byte) (int, error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	if lb.max <= 0 {
		lb.max = maxStderrBytes
	}
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	if n >= lb.max {
		if n > lb.max || len(lb.buf) > 0 {
			lb.truncated = true
		}
		if cap(lb.buf) < lb.max {
			lb.buf = make([]byte, lb.max)
		} else {
			lb.buf = lb.buf[:lb.max]
		}
		copy(lb.buf, p[n-lb.max:])
		return n, nil
	}
	if len(lb.buf)+n > lb.max {
		excess := len(lb.buf) + n - lb.max
		copy(lb.buf, lb.buf[excess:])
		lb.buf = lb.buf[:len(lb.buf)-excess]
		lb.truncated = true
	}
	if cap(lb.buf) < len(lb.buf)+n {
		newCap := max(cap(lb.buf)*2, len(lb.buf)+n)
		if newCap > lb.max {
			newCap = lb.max
		}
		grown := make([]byte, len(lb.buf), newCap)
		copy(grown, lb.buf)
		lb.buf = grown
	}
	lb.buf = append(lb.buf, p...)
	return n, nil
}

func (lb *limitedBuffer) String() string {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	if len(lb.buf) == 0 {
		return ""
	}
	if lb.truncated {
		b := lb.buf
		skip := 0
		for skip < 3 && skip < len(b) && !utf8.RuneStart(b[skip]) {
			skip++
		}
		if skip > 0 && skip < len(b) {
			if r, _ := utf8.DecodeRune(b[skip:]); r != utf8.RuneError {
				b = b[skip:]
			}
		}
		return "... [truncated]\n" + string(b)
	}
	return string(lb.buf)
}

func (lb *limitedBuffer) Len() int {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	return len(lb.buf)
}

func stderrFor(pt *ProgressTracker) io.Writer {
	if pt == nil {
		return os.Stderr
	}
	return newLimitedBuffer(maxStderrBytes)
}

func augmentErr(cmd *exec.Cmd, err error) error {
	if err == nil {
		return nil
	}
	if cmd != nil && cmd.Stderr != nil {
		switch b := cmd.Stderr.(type) {
		case *limitedBuffer:
			if s := strings.TrimSpace(b.String()); s != "" {
				return fmt.Errorf("%w: %s", err, s)
			}
		case *bytes.Buffer:
			if s := strings.TrimSpace(b.String()); s != "" {
				return fmt.Errorf("%w: %s", err, s)
			}
		}
	}
	return err
}

func getNullFile() *os.File {
	nullFileOnce.Do(func() {
		if f, err := os.OpenFile(os.DevNull, os.O_RDWR, 0); err == nil {
			nullFile = f
		} else {
			nullFile = os.Stderr
		}
	})
	return nullFile
}

var lookPath = exec.LookPath

func hasTool(name string) bool {
	_, err := lookPath(name)
	return err == nil
}

func totalFileSize(files []string) int64 {
	var total int64
	for _, f := range files {
		fi, err := os.Stat(f)
		if err == nil {
			if fi.IsDir() {
				sz, _ := GetDirSize(f)
				total += sz
			} else {
				total += fi.Size()
			}
		}
	}
	return total
}

func parsePercent(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			neg := i > 0 && s[i-1] == '-'
			end := i
			for end < len(s) && s[end] >= '0' && s[end] <= '9' {
				end++
			}
			intEnd := end
			if end+1 < len(s) && s[end] == '.' && s[end+1] >= '0' && s[end+1] <= '9' {
				end++
				for end < len(s) && s[end] >= '0' && s[end] <= '9' {
					end++
				}
			}
			if !neg && end < len(s) && s[end] == '%' {
				if val, err := strconv.Atoi(s[i:intEnd]); err == nil && val >= 0 && val <= 100 {
					return val
				}
			}
			i = end - 1
		}
	}
	return -1
}

const maxProgressTokenBytes = 4096

func trackProgress(r io.Reader, pt *ProgressTracker, fileSize int64, fp *FileProgress) {
	if pt == nil || fileSize <= 0 {
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
			if token.Len() >= maxProgressTokenBytes {
				updateWithToken()
			}
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

func redactArgs(args []string) []string {
	masked := make([]string, len(args))
	copy(masked, args)
	for i := 0; i < len(masked); i++ {
		a := masked[i]
		if a == "--" {
			break
		}
		switch {
		case a == "-p" || a == "-P" || a == "-hp" || a == "-password" || a == "--password":
			if i+1 < len(masked) {
				masked[i+1] = "***"
				i++
			}
		case strings.HasPrefix(a, "--password="):
			masked[i] = "--password=***"
		case strings.HasPrefix(a, "-password="):
			masked[i] = "-password=***"
		case strings.HasPrefix(a, "-hp") && len(a) > 3:
			masked[i] = "-hp***"
		case strings.HasPrefix(a, "-P") && len(a) > 2:
			masked[i] = "-P***"
		case strings.HasPrefix(a, "-p") && len(a) > 2 && a != "-p-":
			masked[i] = "-p***"
		}
	}
	return masked
}

func runWithProgress(cmd *exec.Cmd, pt *ProgressTracker, fileSize int64, fp *FileProgress) error {
	if cmd.Stdin == nil {
		cmd.Stdin = getNullFile()
	}
	if pt == nil || fileSize <= 0 {
		cmd.Stdout = stdoutFor(pt)
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
	stoppedFlag   atomic.Bool
	lifecycleMu   sync.Mutex
	stopOnce      sync.Once
	files         []*FileProgress
	linesRendered int
	writer        io.Writer
	renderMu      sync.Mutex
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
	if pt == nil {
		return
	}
	pt.current.Add(n)
}

func (pt *ProgressTracker) FileDone(name string) {
	if pt == nil {
		return
	}
	pt.filesDone.Add(1)
	pt.currentFile.Store(name)
}

func (pt *ProgressTracker) SetCurrentFile(name string) {
	if pt == nil {
		return
	}
	pt.currentFile.Store(name)
}

func (pt *ProgressTracker) SetFiles(files []*FileProgress) {
	if pt == nil {
		return
	}
	cp := append([]*FileProgress(nil), files...)
	pt.renderMu.Lock()
	if pt.linesRendered > 0 {
		pt.eraseBlock()
	}
	pt.files = cp
	pt.renderMu.Unlock()
}

func (pt *ProgressTracker) Start() {
	if pt == nil || !pt.stderrIsTTY {
		return
	}
	pt.lifecycleMu.Lock()
	if pt.started.Load() || pt.stoppedFlag.Load() {
		pt.lifecycleMu.Unlock()
		return
	}
	if pt.done == nil {
		pt.done = make(chan struct{})
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	done := pt.done
	stopped := make(chan struct{})
	pt.ticker = ticker
	pt.stopped = stopped
	pt.started.Store(true)
	pt.lifecycleMu.Unlock()

	activeProgressTracker.Store(pt)
	setLoggingActive(true)

	pt.renderMu.Lock()
	_, _ = io.WriteString(pt.out(), "\033[?25l")
	pt.renderMu.Unlock()

	go func() {
		defer close(stopped)
		for {
			select {
			case <-ticker.C:
				if pt.renderMu.TryLock() {
					pt.render()
					pt.renderMu.Unlock()
				}
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()
}

func (pt *ProgressTracker) Stop() {
	if pt == nil {
		return
	}
	pt.stopOnce.Do(func() {
		pt.lifecycleMu.Lock()
		pt.stoppedFlag.Store(true)
		wasStarted := pt.started.Load()
		ticker := pt.ticker
		done := pt.done
		stopped := pt.stopped
		pt.lifecycleMu.Unlock()

		if wasStarted {
			ticker.Stop()
			close(done)
			<-stopped
		} else if done != nil {
			close(done)
		}

		activeProgressTracker.CompareAndSwap(pt, nil)
		pt.renderMu.Lock()
		pt.eraseBlock()
		if pt.stderrIsTTY {
			_, _ = io.WriteString(pt.out(), "\033[?25h")
		}
		pt.renderMu.Unlock()

		if activeProgressTracker.Load() == nil {
			swapLoggingActiveAndFlush(false)
		} else {
			flushLogBuf()
		}

		if pt.stderrIsTTY {
			pt.renderMu.Lock()
			pt.renderFinal()
			pt.renderMu.Unlock()
		}
	})
}

func (pt *ProgressTracker) Suspend(fn func()) {
	if pt == nil || !pt.started.Load() {
		fn()
		return
	}
	pt.renderMu.Lock()
	defer pt.renderMu.Unlock()

	pt.eraseBlock()
	if pt.stderrIsTTY {
		_, _ = io.WriteString(pt.out(), "\033[?25h")
	}

	prevLogging := swapLoggingActiveAndFlush(false)

	defer func() {
		setLoggingActive(prevLogging)
		if pt.stderrIsTTY {
			_, _ = io.WriteString(pt.out(), "\033[?25l")
		}
	}()

	fn()
}

func SuspendActiveProgress(fn func()) {
	if pt := activeProgressTracker.Load(); pt != nil {
		pt.Suspend(fn)
		return
	}
	fn()
}

func (pt *ProgressTracker) eraseBlock() {
	if !pt.stderrIsTTY || pt.linesRendered <= 0 {
		pt.linesRendered = 0
		return
	}
	w := pt.out()
	fmt.Fprintf(w, "\r\033[%dA", pt.linesRendered)
	for i := 0; i < pt.linesRendered; i++ {
		io.WriteString(w, "\r\033[K\n")
	}
	fmt.Fprintf(w, "\r\033[%dA", pt.linesRendered)
	pt.linesRendered = 0
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
	} else if current == 0 && done < int64(filesTotal) {
		line += fmt.Sprintf("  %siniciando...%s", Yellow, NC)
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
	runes := []rune(fp.Name)
	if len(runes) > pad-4 {
		runes = append(runes[:pad-7], '.', '.', '.')
	}
	display := string(runes)
	if padSpaces := (pad - 2) - utf8.RuneCountInString(display); padSpaces > 0 {
		display += strings.Repeat(" ", padSpaces)
	}
	name := "  " + display

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
		} else if current == 0 {
			etaStr = "iniciando"
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
	if remainingSecs > (24 * time.Hour).Seconds() {
		return ">24h"
	}
	remaining := time.Duration(remainingSecs * float64(time.Second))

	if remaining < time.Minute {
		return fmt.Sprintf("%ds", int(remaining.Seconds()))
	}
	if remaining < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(remaining.Minutes()), int(remaining.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm%02ds", int(remaining.Hours()), int(remaining.Minutes())%60, int(remaining.Seconds())%60)
}

func makeBar(pct float64, width int) string {
	if width <= 0 {
		return ""
	}
	if pct < 0 {
		pct = 0
	}
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
	if n > 0 {
		if cw.pt != nil {
			cw.pt.Add(int64(n))
		}
		if cw.fp != nil {
			cw.fp.Current.Add(int64(n))
		}
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

var spliceBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, defaultPipeCapacity)
		return &b
	},
}

func copyWithSplicePool(w io.Writer, r io.Reader) (int64, error) {
	bp := spliceBufPool.Get().(*[]byte)
	defer spliceBufPool.Put(bp)
	return io.CopyBuffer(w, r, *bp)
}

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
	pipes := make([]*os.File, 0, max(0, (len(cmds)-1)*2))
	closePipes := func() {
		for i, f := range pipes {
			if f != nil {
				_ = f.Close()
				pipes[i] = nil
			}
		}
	}

	for i := 0; i < len(cmds)-1; i++ {
		r, w, err := os.Pipe()
		if err != nil {
			closePipes()
			return fmt.Errorf("pipeline pipe %d: %w", i, err)
		}
		pipes = append(pipes, r, w)
		setPipeCapacity(r, w, defaultPipeCapacity)
		cmds[i].Stdout = w
		cmds[i+1].Stdin = r
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
			closePipes()
			for j := 0; j < i; j++ {
				if cmds[j].Process != nil {
					_ = cmds[j].Process.Kill()
					_ = cmds[j].Wait()
				}
			}
			return fmt.Errorf("pipeline start %d: %w", i, err)
		}
	}
	closePipes()

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

func waitPipePair(prod, cons *exec.Cmd, pr *io.PipeReader, pw *io.PipeWriter) (prodErr, consErr error) {
	done := make(chan struct{})
	go func() {
		if prod != nil {
			prodErr = augmentErr(prod, prod.Wait())
		}
		if pw != nil {
			_ = pw.CloseWithError(prodErr)
		}
		close(done)
	}()

	if cons != nil {
		consErr = augmentErr(cons, cons.Wait())
	}
	if pr != nil {
		_ = pr.CloseWithError(consErr)
	}
	select {
	case <-done:
	default:
		if consErr != nil && prod != nil && prod.Process != nil {
			_ = prod.Process.Kill()
		}
		<-done
	}
	return prodErr, consErr
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

func isValidSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < 64; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func escapeSHA256Filename(name string) (string, bool) {
	if !strings.ContainsAny(name, "\\\n\r") {
		return name, false
	}
	var sb strings.Builder
	sb.Grow(len(name) + 4)
	for i := 0; i < len(name); i++ {
		switch name[i] {
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		default:
			sb.WriteByte(name[i])
		}
	}
	return sb.String(), true
}

func unescapeSHA256Filename(s string) string {
	if strings.IndexByte(s, '\\') < 0 {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			switch s[i] {
			case '\\':
				sb.WriteByte('\\')
			case 'n':
				sb.WriteByte('\n')
			case 'r':
				sb.WriteByte('\r')
			default:
				sb.WriteByte('\\')
				sb.WriteByte(s[i])
			}
		} else {
			sb.WriteByte(s[i])
		}
	}
	return sb.String()
}

func parseSHA256Line(rawLine string) (hashHex string, filename string, hasName bool, ok bool) {
	line := strings.TrimRight(rawLine, "\r")
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false, false
	}
	escaped := false
	if strings.HasPrefix(trimmed, `\`) {
		escaped = true
		trimmed = trimmed[1:]
	}
	if strings.HasPrefix(trimmed, "SHA256 (") {
		if idx := strings.LastIndex(trimmed, ") = "); idx > len("SHA256 (") {
			namePart := trimmed[len("SHA256 ("):idx]
			hashPart := strings.TrimSpace(trimmed[idx+len(") = "):])
			if isValidSHA256Hex(hashPart) {
				if escaped {
					namePart = unescapeSHA256Filename(namePart)
				}
				return strings.ToLower(hashPart), namePart, true, true
			}
		}
		return "", "", false, false
	}
	if len(trimmed) < 64 || !isValidSHA256Hex(trimmed[:64]) {
		return "", "", false, false
	}
	hashPart := strings.ToLower(trimmed[:64])
	rest := trimmed[64:]
	if strings.TrimSpace(rest) == "" {
		if escaped {
			return "", "", false, false
		}
		return hashPart, "", false, true
	}
	var namePart string
	switch {
	case strings.HasPrefix(rest, "  ") || strings.HasPrefix(rest, " *"):
		namePart = rest[2:]
	case rest[0] == ' ' || rest[0] == '\t':
		namePart = strings.TrimPrefix(strings.TrimLeft(rest, " \t"), "*")
	default:
		return "", "", false, false
	}
	if escaped {
		namePart = unescapeSHA256Filename(namePart)
	}
	if namePart == "" {
		return "", "", false, false
	}
	return hashPart, namePart, true, true
}

func WriteSHA256File(archivePath string) (string, error) {
	hashHex, err := ComputeSHA256(archivePath)
	if err != nil {
		return "", err
	}
	shaPath := archivePath + ".sha256"
	dir := filepath.Dir(shaPath)
	shaName := filepath.Base(shaPath)

	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()

	if fi, err := root.Lstat(shaName); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("rechazando escritura sobre symlink existente: %s", shaPath)
	}

	escapedName, escaped := escapeSHA256Filename(filepath.Base(archivePath))
	prefix := ""
	if escaped {
		prefix = `\`
	}
	content := fmt.Sprintf("%s%s  %s\n", prefix, hashHex, escapedName)

	f, err := root.OpenFile(shaName, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	_, writeErr := f.WriteString(content)
	closeErr := f.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hashHex, nil
}

func ParseSHA256File(shaPath, targetFile string) (string, error) {
	data, err := os.ReadFile(shaPath)
	if err != nil {
		return "", err
	}
	base := filepath.Base(targetFile)
	type parsedEntry struct {
		hash string
		name string
	}
	var named []parsedEntry
	var rawHash string

	for _, line := range strings.Split(string(data), "\n") {
		h, name, hasName, ok := parseSHA256Line(line)
		if !ok {
			continue
		}
		if hasName {
			if name == base {
				return h, nil
			}
			named = append(named, parsedEntry{hash: h, name: name})
		} else if rawHash == "" {
			rawHash = h
		}
	}

	for _, e := range named {
		if !strings.ContainsAny(e.name, "\n\r") {
			normBase := filepath.Base(strings.ReplaceAll(e.name, `\`, "/"))
			if normBase == base {
				return e.hash, nil
			}
		}
	}

	if len(named) == 0 && rawHash != "" {
		return rawHash, nil
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

func MemorySafeMaxThreadsForBudget(f Format, threads int, memMB int) int {
	if threads <= 1 {
		return 1
	}
	if memMB <= 0 {
		return threads
	}
	var mbPerThread int
	switch f {
	case Xz:
		mbPerThread = 674
	case Lrz:
		mbPerThread = 512
	case SevenZ:
		mbPerThread = 300
	case Zst:
		mbPerThread = 160
	case Lz:
		mbPerThread = 128
	case Bz3:
		mbPerThread = 96
	case Br, Rar:
		mbPerThread = 64
	case Bz2:
		mbPerThread = 16
	default:
		mbPerThread = 8
	}
	maxSafe := memMB / mbPerThread
	if maxSafe < 1 {
		maxSafe = 1
	}
	if threads > maxSafe {
		return maxSafe
	}
	return threads
}

func MemorySafeMaxThreads(f Format, threads int) int {
	return MemorySafeMaxThreadsForBudget(f, threads, getMemLimit())
}

// AllocateThreadsProportional distributes totalCores among files based on their sizes.
// Files with larger sizes receive proportionally more threads, ensuring all files finish
// at approximately the same time and eliminating tail latency (idle CPU cores).
// Each file receives at least minThreads (default 1) and at most maxThreadsPerJob.
// If sizes are 0 or unknown, it distributes threads evenly.
func AllocateThreadsProportional(sizes []int64, totalCores int, minThreads int, maxThreadsPerJob int) []int {
	caps := make([]int, len(sizes))
	for i := range caps {
		caps[i] = maxThreadsPerJob
	}
	return AllocateThreadsProportionalWithCaps(sizes, totalCores, minThreads, caps)
}

// AllocateThreadsProportionalWithCaps distributes totalCores among files based on their sizes,
// respecting an individual maximum thread cap for each file.
func AllocateThreadsProportionalWithCaps(sizes []int64, totalCores int, minThreads int, maxCaps []int) []int {
	n := len(sizes)
	if n == 0 {
		return nil
	}
	if minThreads < 1 {
		minThreads = 1
	}

	caps := make([]int, n)
	sumCaps := 0
	for i := 0; i < n; i++ {
		c := totalCores
		if i < len(maxCaps) && maxCaps[i] > 0 {
			c = maxCaps[i]
		}
		if c < minThreads {
			c = minThreads
		}
		caps[i] = c
		sumCaps += c
	}

	result := make([]int, n)

	if n == 1 {
		t := totalCores
		if t > caps[0] {
			t = caps[0]
		}
		if t < minThreads {
			t = minThreads
		}
		result[0] = t
		return result
	}

	allOneCap := true
	for _, c := range caps {
		if c != 1 {
			allOneCap = false
			break
		}
	}
	if allOneCap {
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

	targetTotal := totalCores
	if targetTotal > sumCaps {
		targetTotal = sumCaps
	}

	if allZero || totalSize == 0 {
		allocated := 0
		for i := 0; i < n; i++ {
			result[i] = minThreads
			allocated += minThreads
		}
		for allocated < targetTotal {
			added := false
			for i := 0; i < n; i++ {
				if result[i] < caps[i] {
					result[i]++
					allocated++
					added = true
					if allocated == targetTotal {
						break
					}
				}
			}
			if !added {
				break
			}
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
		raw := float64(targetTotal) * float64(max(0, sz)) / float64(totalSize)
		count := int(raw)
		if count < minThreads {
			count = minThreads
		}
		if count > caps[i] {
			count = caps[i]
		}
		result[i] = count
		allocated += count
		remainders[i] = itemRemainder{index: i, remainder: raw - float64(int(raw))}
	}

	if allocated < targetTotal {
		sort.Slice(remainders, func(i, j int) bool {
			if remainders[i].remainder == remainders[j].remainder {
				return sizes[remainders[i].index] > sizes[remainders[j].index]
			}
			return remainders[i].remainder > remainders[j].remainder
		})
		for allocated < targetTotal {
			added := false
			for _, rem := range remainders {
				idx := rem.index
				if result[idx] < caps[idx] {
					result[idx]++
					allocated++
					added = true
					if allocated == targetTotal {
						break
					}
				}
			}
			if !added {
				break
			}
		}
	} else if allocated > targetTotal {
		sort.Slice(remainders, func(i, j int) bool {
			if remainders[i].remainder == remainders[j].remainder {
				return sizes[remainders[i].index] < sizes[remainders[j].index]
			}
			return remainders[i].remainder < remainders[j].remainder
		})
		for allocated > targetTotal {
			reduced := false
			for _, rem := range remainders {
				idx := rem.index
				if result[idx] > minThreads {
					result[idx]--
					allocated--
					reduced = true
					if allocated == targetTotal {
						break
					}
				}
			}
			if !reduced {
				break
			}
		}
	}

	return result
}

func PipeFlagsForThreads(info FormatInfo, threads int) []string {
	return PipeFlagsForThreadsAndMem(info, threads, getMemLimit())
}

func PipeFlagsForThreadsAndMem(info FormatInfo, threads int, memBudgetMB int) []string {
	if threads <= 0 {
		threads = NCPU()
	}
	thStr := strconv.Itoa(threads)
	switch info.Tool {
	case "pigz":
		return []string{"-dc", "-p", thStr}
	case "xz":
		args := []string{"-dc", "-T" + thStr}
		if memBudgetMB > 0 {
			args = append(args, fmt.Sprintf("--memlimit-mt-decompress=%dMiB", max(65, memBudgetMB)))
		}
		return args
	case "zstd":
		return []string{"-dc", "-T" + thStr}
	case "bzip3":
		return []string{"-dc", "-j", thStr}
	case "plzip":
		return []string{"-dc", "--threads=" + thStr}
	case "lbzip2":
		return []string{"-dc", "-n", thStr}
	case "lrzip":
		args := []string{"-d", "-p", thStr, "-o", "-"}
		if memBudgetMB > 0 {
			args = append(args, "-m", strconv.Itoa(max(1, memBudgetMB/100)))
		}
		return args
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
	maxWorkers      int
	activeWorkers   int
}

// NewDynamicThreadPool creates a new thread pool with the specified limits.
func NewDynamicThreadPool(totalCores, minThreads, maxThreads, totalFiles int, totalBytes int64, maxWorkers ...int) *DynamicThreadPool {
	if totalCores < 1 {
		totalCores = NCPU()
	}
	if minThreads < 1 {
		minThreads = 1
	}
	if maxThreads < minThreads {
		maxThreads = totalCores
	}
	mw := 8
	if len(maxWorkers) > 0 && maxWorkers[0] > 0 {
		mw = maxWorkers[0]
	}
	if totalFiles > 0 && mw > totalFiles {
		mw = totalFiles
	}
	if mw < 1 {
		mw = 1
	}
	return &DynamicThreadPool{
		totalTokens:     totalCores,
		availableTokens: totalCores,
		minThreads:      minThreads,
		maxThreads:      maxThreads,
		remainingFiles:  totalFiles,
		remainingBytes:  totalBytes,
		maxWorkers:      mw,
	}
}

// Acquire requests thread tokens for a file of fileSize bytes.
func (p *DynamicThreadPool) Acquire(fileSize int64, maxFileThreads ...int) int {
	p.mu.Lock()
	defer p.mu.Unlock()

	capLimit := p.maxThreads
	if len(maxFileThreads) > 0 && maxFileThreads[0] > 0 && maxFileThreads[0] < capLimit {
		capLimit = maxFileThreads[0]
	}

	p.activeWorkers++
	slotsRemaining := p.maxWorkers - (p.activeWorkers - 1)
	if slotsRemaining < 1 {
		slotsRemaining = 1
	}

	if p.remainingFiles <= 1 || p.remainingBytes <= 0 || fileSize >= p.remainingBytes || slotsRemaining <= 1 {
		tokens := p.availableTokens
		if tokens > capLimit {
			tokens = capLimit
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

	avgRemaining := p.remainingBytes / int64(p.remainingFiles)
	if avgRemaining <= 0 {
		avgRemaining = 1
	}

	ratio := float64(fileSize) / float64(avgRemaining)
	base := float64(p.availableTokens) / float64(slotsRemaining)
	target := int(math.Round(base * ratio))

	maxAllowed := p.availableTokens - (slotsRemaining-1)*p.minThreads
	if maxAllowed < p.minThreads {
		maxAllowed = p.minThreads
	}
	if target > maxAllowed {
		target = maxAllowed
	}

	if target < p.minThreads {
		target = p.minThreads
	}
	if target > capLimit {
		target = capLimit
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
	p.activeWorkers--
	if p.activeWorkers < 0 {
		p.activeWorkers = 0
	}
	p.availableTokens += tokens
	if p.availableTokens > p.totalTokens {
		p.availableTokens = p.totalTokens
	}
}

// ParseCutoffTime parses a cutoff time from either a date/duration string or a reference file.
// If both are provided, it returns an error. If neither is provided, it returns a zero time.
func ParseCutoffTime(newerStr, newerThanFile string, now time.Time) (time.Time, error) {
	if newerStr != "" && newerThanFile != "" {
		return time.Time{}, fmt.Errorf("--newer y --newer-than son mutuamente excluyentes")
	}
	if newerThanFile != "" {
		fi, err := os.Stat(newerThanFile)
		if err != nil {
			return time.Time{}, fmt.Errorf("archivo de referencia para --newer-than no encontrado: %w", err)
		}
		return fi.ModTime(), nil
	}
	if newerStr == "" {
		return time.Time{}, nil
	}

	trimmed := strings.TrimSpace(newerStr)

	// Check if it's a day duration like "7d", "1d"
	if strings.HasSuffix(trimmed, "d") || strings.HasSuffix(trimmed, "D") {
		daysStr := trimmed[:len(trimmed)-1]
		if days, err := strconv.Atoi(daysStr); err == nil && days > 0 {
			dur := time.Duration(days) * 24 * time.Hour
			return now.Add(-dur), nil
		}
	}

	// Try standard Go duration (e.g. "24h", "30m", "1h30m")
	if dur, err := time.ParseDuration(trimmed); err == nil {
		if dur <= 0 {
			return time.Time{}, fmt.Errorf("la duración para --newer debe ser positiva: %q", newerStr)
		}
		return now.Add(-dur), nil
	}

	// Try date formats in local time location
	formats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"2006/01/02 15:04:05",
		"2006/01/02 15:04",
		"2006/01/02",
		time.RFC3339,
		"2006-01-02T15:04:05",
	}

	for _, layout := range formats {
		if t, err := time.ParseInLocation(layout, trimmed, time.Local); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("formato de fecha o duración inválido para --newer: %q (use YYYY-MM-DD o duraciones como 24h, 7d)", newerStr)
}




