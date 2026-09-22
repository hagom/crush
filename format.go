package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

type Format int

const (
	Gz Format = iota
	Xz
	Bz2
	Bz3
	Zst
	Lz
	Lrz
	Zip
	SevenZ
	Tar
	Rar
	Lz4
	Br
)

var formatNames = map[Format]string{
	Gz:     "gz",
	Xz:     "xz",
	Bz2:    "bz2",
	Bz3:    "bz3",
	Zst:    "zst",
	Lz:     "lz",
	Lrz:    "lrz",
	Zip:    "zip",
	SevenZ: "7z",
	Tar:    "tar",
	Rar:    "rar",
	Lz4:    "lz4",
	Br:     "br",
}

var FormatsByCompression = []Format{
	Lrz, Bz3, Xz, SevenZ, Bz2, Br, Zst, Rar, Lz, Gz, Lz4, Zip, Tar,
}

func (f Format) String() string {
	if s, ok := formatNames[f]; ok {
		return s
	}
	return fmt.Sprintf("Format(%d)", f)
}

func (f Format) IsContainer() bool {
	switch f {
	case Zip, SevenZ, Tar, Rar:
		return true
	default:
		return false
	}
}

func (f Format) IsStream() bool {
	return !f.IsContainer()
}

func (f Format) ArchiveBaseName(inputPath string) string {
	base := filepath.Base(inputPath)
	if f.IsStream() {
		return base
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

type FormatInfo struct {
	Format      Format
	Tool        string
	PipeFlags   string
	DirectFlags string
	IsTar       bool
	TestFlag    string
}

func (fi FormatInfo) IsContainer() bool {
	if fi.Format != 0 {
		return fi.Format.IsContainer()
	}
	switch fi.Tool {
	case "unzip", "tar", "7z", "7za", "7zr", "rar", "unrar":
		return true
	}
	return fi.Format.IsContainer()
}

func (fi FormatInfo) IsStream() bool {
	return !fi.IsContainer()
}

func (fi FormatInfo) ArchiveBaseName(inputPath string) string {
	base := filepath.Base(inputPath)
	if fi.IsStream() {
		return base
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func ExtForFormat(f Format) string {
	if f.IsContainer() {
		return f.String()
	}
	return "tar." + f.String()
}

func ParseFormat(s string) (Format, error) {
	for f, name := range formatNames {
		if strings.EqualFold(s, name) {
			return f, nil
		}
	}
	return 0, fmt.Errorf("formato no soportado: %s", s)
}

func FormatInfoFromFormat(f Format) FormatInfo {
	switch f {
	case Gz:
		return FormatInfo{Format: Gz, Tool: "pigz", PipeFlags: "-dc", DirectFlags: "-dk", IsTar: false, TestFlag: "-t"}
	case Xz:
		return FormatInfo{Format: Xz, Tool: "xz", PipeFlags: "-dc -T0", DirectFlags: "-d -T0 -k", IsTar: false, TestFlag: "-t"}
	case Bz2:
		return FormatInfo{Format: Bz2, Tool: bzip2Bin(), PipeFlags: "-dc", DirectFlags: "-dk", IsTar: false, TestFlag: "-t"}
	case Bz3:
		return FormatInfo{Format: Bz3, Tool: "bzip3", PipeFlags: "-dc -j " + ncpuStr(), DirectFlags: "-d -kj " + ncpuStr(), IsTar: false, TestFlag: "-t"}
	case Zst:
		return FormatInfo{Format: Zst, Tool: "zstd", PipeFlags: "-dc -T0", DirectFlags: "-d -T0 -k", IsTar: false, TestFlag: "-t"}
	case Lz:
		return FormatInfo{Format: Lz, Tool: "plzip", PipeFlags: "-dc --threads=" + ncpuStr(), DirectFlags: "-dk --threads=" + ncpuStr(), IsTar: false, TestFlag: "-t"}
	case Lrz:
		return FormatInfo{Format: Lrz, Tool: "lrzip", PipeFlags: "-d -p " + ncpuStr() + " -o -", DirectFlags: "-d -p " + ncpuStr(), IsTar: false, TestFlag: "-t"}
	case Lz4:
		return FormatInfo{Format: Lz4, Tool: "lz4", PipeFlags: "-dc", DirectFlags: "-dk", IsTar: false, TestFlag: "-t"}
	case Br:
		return FormatInfo{Format: Br, Tool: "brotli", PipeFlags: "-dc", DirectFlags: "-dk", IsTar: false, TestFlag: "-t"}
	case Zip:
		return FormatInfo{Format: Zip, Tool: "unzip", PipeFlags: "-o", DirectFlags: "-o", TestFlag: "-t"}
	case SevenZ:
		return FormatInfo{Format: SevenZ, Tool: sevenzBin(), PipeFlags: "x -mmt=on", DirectFlags: "x -mmt=on", TestFlag: "t"}
	case Rar:
		return FormatInfo{Format: Rar, Tool: rarBin(), PipeFlags: "x -mt" + ncpuStr(), DirectFlags: "x -mt" + ncpuStr(), TestFlag: "t"}
	case Tar:
		return FormatInfo{Format: Tar, Tool: "tar", PipeFlags: "-xf", DirectFlags: "-xf", TestFlag: "-tf"}
	default:
		return FormatInfo{}
	}
}

var knownTarSuffixes = []struct {
	suffix string
	format Format
}{
	{".tar.gz", Gz}, {".tgz", Gz},
	{".tar.xz", Xz}, {".txz", Xz},
	{".tar.bz2", Bz2}, {".tbz2", Bz2},
	{".tar.bz3", Bz3},
	{".tar.zst", Zst}, {".tzst", Zst},
	{".tar.lz", Lz}, {".tlz", Lz},
	{".tar.lrz", Lrz}, {".tar.lz4", Lz4}, {".tar.br", Br},
}

func ParseFormatFromExt(filename string) (Format, bool) {
	lower := strings.ToLower(filename)
	for _, entry := range knownTarSuffixes {
		if strings.HasSuffix(lower, entry.suffix) {
			return entry.format, true
		}
	}
	ext := lower[strings.LastIndex(lower, ".")+1:]
	f, err := ParseFormat(ext)
	if err != nil {
		return 0, false
	}
	return f, true
}

func DetectFormat(filename string) (FormatInfo, error) {
	ext, ok := ParseFormatFromExt(filename)
	if !ok {
		return FormatInfo{}, fmt.Errorf("formato no reconocido: %s", filename)
	}
	fi := FormatInfoFromFormat(ext)
	lower := strings.ToLower(filename)
	fi.IsTar = strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") ||
		strings.HasSuffix(lower, ".tar.xz") || strings.HasSuffix(lower, ".txz") ||
		strings.HasSuffix(lower, ".tar.bz2") || strings.HasSuffix(lower, ".tbz2") ||
		strings.HasSuffix(lower, ".tar.bz3") || strings.HasSuffix(lower, ".tar.zst") ||
		strings.HasSuffix(lower, ".tzst") || strings.HasSuffix(lower, ".tar.lz") ||
		strings.HasSuffix(lower, ".tlz") || strings.HasSuffix(lower, ".tar.lrz") ||
		strings.HasSuffix(lower, ".tar.lz4") || strings.HasSuffix(lower, ".tar.br")
	return fi, nil
}
