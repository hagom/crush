package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	Name    string
	Size    int64
	Current atomic.Int64
	status  string
	start   time.Time
	outPath string
	mu      sync.Mutex
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

func pollFileProgress(fp *FileProgress) {
	if fp == nil || fp.Status() != "active" || fp.OutPath() == "" || fp.Size <= 0 {
		return
	}
	fi, err := os.Stat(fp.OutPath())
	if err != nil {
		return
	}
	if fi.Size() > 0 {
		fp.Current.Store(fi.Size())
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
	cmd := execCommand("df", "-B1", "--output=avail", dir)
	out, err := cmd.Output()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		if len(lines) >= 2 {
			if avail, err := strconv.ParseInt(strings.TrimSpace(lines[1]), 10, 64); err == nil {
				return avail
			}
		}
	}

	cmd = execCommand("df", dir)
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

	case strings.HasSuffix(f, ".bz2") || strings.HasSuffix(f, ".tbz2") ||
		strings.HasSuffix(f, ".bz3") || strings.HasSuffix(f, ".lz") ||
		strings.HasSuffix(f, ".tlz") || strings.HasSuffix(f, ".lz4") ||
		strings.HasSuffix(f, ".br"):
		if fi, err := os.Stat(file); err == nil {
			return fi.Size() * 6
		}
	}

	// Fallback: compressed size * 4
	if fi, err := os.Stat(file); err == nil {
		return fi.Size() * 4
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
		fmt.Fprint(os.Stderr, s)
	}
	if logFile != nil {
		logFile.WriteString(s)
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
	br := bufio.NewReader(r)
	lastPct := -1
	for {
		line, err := br.ReadString('\r')
		if err != nil && len(line) == 0 {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			if err != nil {
				break
			}
			continue
		}
		pct := parsePercent(line)
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
		if err != nil {
			break
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
	fmt.Fprintf(os.Stderr, "\033[%dA", pt.linesRendered)
	for i := 0; i < pt.linesRendered; i++ {
		os.Stderr.WriteString("\033[K\n")
	}
	fmt.Fprintf(os.Stderr, "\033[%dA", pt.linesRendered)
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

	if pt.linesRendered > 0 {
		fmt.Fprintf(os.Stderr, "\033[%dA", pt.linesRendered)
	}

	os.Stderr.WriteString(globalBarLine(globalPct, current, done, pt.filesTotal, pt.total, elapsed))
	os.Stderr.WriteString("\033[K\n")

	for _, fp := range pt.files {
		pollFileProgress(fp)
		os.Stderr.WriteString(fileLine(fp))
		os.Stderr.WriteString("\033[K\n")
	}

	pt.linesRendered = 1 + len(pt.files)
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
	os.Stderr.WriteString(line)
}

func globalBarLine(pct float64, current int64, done int64, filesTotal int, total int64, elapsed time.Duration) string {
	bar := makeBar(pct, 20)

	line := fmt.Sprintf("\r[%s] %5.1f%%  %d/%d", bar, pct, done, filesTotal)

	if current > 0 && elapsed.Seconds() > 0 {
		speed := float64(current) / elapsed.Seconds()
		line += fmt.Sprintf("  %s/s", FormatSize(int64(speed)))
	}

	if total > 0 && current > 0 {
		remaining := time.Duration(float64(elapsed) / float64(current) * float64(total-current))
		line += fmt.Sprintf("  %v restantes", remaining.Round(time.Second))
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
		return fmt.Sprintf("%s%sesperando...%s", name, Yellow, NC)
	case "active":
		current := fp.Current.Load()
		if current == 0 && fp.Size == 0 {
			return fmt.Sprintf("%s%sen proceso...%s", name, Bold, NC)
		}
		var pct float64
		if fp.Size > 0 {
			pct = float64(current) * 100 / float64(fp.Size)
			if pct > 100 {
				pct = 100
			}
		}
		bar := makeBar(pct, 10)
		line := fmt.Sprintf("%s [%s] %3d%%", name, bar, int(pct))
		if fp.Size > 0 {
			line += fmt.Sprintf("  %s/%s", fmtSizeDec(current), fmtSizeDec(fp.Size))
		}
		if current > 0 && fp.Size > 0 && current < fp.Size {
			elapsed := time.Since(fp.StartTime())
			if elapsed.Seconds() > 0 {
				remaining := time.Duration(float64(elapsed) / float64(current) * float64(fp.Size-current))
				line += fmt.Sprintf("  %v", remaining.Round(time.Second))
			}
		}
		return line
	case "done":
		return fmt.Sprintf("%s%s✓%s", name, Green, NC)
	case "error":
		return fmt.Sprintf("%s%s✗%s", name, Red, NC)
	default:
		return fmt.Sprintf("%s %s", name, fp.Status())
	}
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
	for sz > 1024 && unit < 5 {
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

// --- Pipeline: chain multiple commands ---

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (lw *lockedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Write(p)
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
