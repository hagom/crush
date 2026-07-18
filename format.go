package main

import (
	"fmt"
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

type FormatInfo struct {
	Tool        string
	PipeFlags   string
	DirectFlags string
	IsTar       bool
	TestFlag    string
}

func ExtForFormat(f Format) string {
	switch f {
	case Zip, SevenZ, Rar, Tar:
		return f.String()
	default:
		return "tar." + f.String()
	}
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
		return FormatInfo{Tool: "pigz", PipeFlags: "-dc", DirectFlags: "-dk", IsTar: false, TestFlag: "-t"}
	case Xz:
		return FormatInfo{Tool: "xz", PipeFlags: "-dc -T0", DirectFlags: "-d -T0 -k", IsTar: false, TestFlag: "-t"}
	case Bz2:
		return FormatInfo{Tool: bzip2Bin(), PipeFlags: "-dc", DirectFlags: "-dk", IsTar: false, TestFlag: "-t"}
	case Bz3:
		return FormatInfo{Tool: "bzip3", PipeFlags: "-dc -j " + ncpuStr(), DirectFlags: "-d -kj " + ncpuStr(), IsTar: false, TestFlag: "-t"}
	case Zst:
		return FormatInfo{Tool: "zstd", PipeFlags: "-dc -T0", DirectFlags: "-d -T0 -k", IsTar: false, TestFlag: "-t"}
	case Lz:
		return FormatInfo{Tool: "plzip", PipeFlags: "-dc --threads=" + ncpuStr(), DirectFlags: "-dk --threads=" + ncpuStr(), IsTar: false, TestFlag: "-t"}
	case Lrz:
		return FormatInfo{Tool: "lrzip", PipeFlags: "-d -k -p " + ncpuStr() + " -o -", DirectFlags: "-d -k -p " + ncpuStr(), IsTar: false, TestFlag: "-t"}
	case Lz4:
		return FormatInfo{Tool: "lz4", PipeFlags: "-dc", DirectFlags: "-dk", IsTar: false, TestFlag: "-t"}
	case Br:
		return FormatInfo{Tool: "brotli", PipeFlags: "-dc", DirectFlags: "-dk", IsTar: false, TestFlag: "-t"}
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
	switch ext {
	case Zip:
		fi = FormatInfo{Tool: "unzip", PipeFlags: "-o", DirectFlags: "-o", TestFlag: "-t"}
	case SevenZ:
		fi = FormatInfo{Tool: sevenzBin(), PipeFlags: "x -mmt=on", DirectFlags: "x -mmt=on", TestFlag: "t"}
	case Rar:
		fi = FormatInfo{Tool: rarBin(), PipeFlags: "x -mt" + ncpuStr(), DirectFlags: "x -mt" + ncpuStr(), TestFlag: "t"}
	case Tar:
		fi = FormatInfo{Tool: "tar", PipeFlags: "-xf", DirectFlags: "-xf", TestFlag: "-tf"}
	}
	return fi, nil
}
