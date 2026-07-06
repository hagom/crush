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
	// Handle "7z" special case
	if s == "7z" {
		return SevenZ, nil
	}
	return 0, fmt.Errorf("formato no soportado: %s", s)
}

func DetectFormat(filename string) (FormatInfo, error) {
	ext := strings.ToLower(filename)

	var info FormatInfo
	switch {
	case strings.HasSuffix(ext, ".tar.gz") || strings.HasSuffix(ext, ".tgz"):
		info = FormatInfo{Tool: "pigz", PipeFlags: "-dc", DirectFlags: "-dk", IsTar: true, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".tar.xz") || strings.HasSuffix(ext, ".txz"):
		info = FormatInfo{Tool: "xz", PipeFlags: "-dc -T0", DirectFlags: "-d -T0 -k", IsTar: true, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".tar.bz2") || strings.HasSuffix(ext, ".tbz2"):
		info = FormatInfo{Tool: bzip2Bin(), PipeFlags: "-dc", DirectFlags: "-dk", IsTar: true, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".tar.bz3"):
		info = FormatInfo{Tool: "bzip3", PipeFlags: "-dc -j " + ncpuStr(), DirectFlags: "-d -j " + ncpuStr(), IsTar: true, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".tar.zst") || strings.HasSuffix(ext, ".tzst"):
		info = FormatInfo{Tool: "zstd", PipeFlags: "-dc -T0", DirectFlags: "-d -T0", IsTar: true, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".tar.lz") || strings.HasSuffix(ext, ".tlz"):
		info = FormatInfo{Tool: "plzip", PipeFlags: "-dc --threads=" + ncpuStr(), DirectFlags: "-dk --threads=" + ncpuStr(), IsTar: true, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".tar.lrz"):
		info = FormatInfo{Tool: "lrzip", PipeFlags: "-d -p " + ncpuStr() + " -o -", DirectFlags: "-d -k -p " + ncpuStr(), IsTar: true, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".tar.lz4"):
		info = FormatInfo{Tool: "lz4", PipeFlags: "-dc", DirectFlags: "-dk", IsTar: true, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".tar.br"):
		info = FormatInfo{Tool: "brotli", PipeFlags: "-dc", DirectFlags: "-dk", IsTar: true, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".lrz"):
		info = FormatInfo{Tool: "lrzip", PipeFlags: "-d -p " + ncpuStr() + " -o -", DirectFlags: "-d -k -p " + ncpuStr(), IsTar: false, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".lz4"):
		info = FormatInfo{Tool: "lz4", PipeFlags: "-dc", DirectFlags: "-dk", IsTar: false, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".zst"):
		info = FormatInfo{Tool: "zstd", PipeFlags: "-dc -T0", DirectFlags: "-d -T0", IsTar: false, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".xz"):
		info = FormatInfo{Tool: "xz", PipeFlags: "-dc -T0", DirectFlags: "-d -T0 -k", IsTar: false, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".gz"):
		info = FormatInfo{Tool: "pigz", PipeFlags: "-dc", DirectFlags: "-dk", IsTar: false, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".bz2"):
		info = FormatInfo{Tool: bzip2Bin(), PipeFlags: "-dc", DirectFlags: "-dk", IsTar: false, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".bz3"):
		info = FormatInfo{Tool: "bzip3", PipeFlags: "-dc -j " + ncpuStr(), DirectFlags: "-d -j " + ncpuStr(), IsTar: false, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".lz"):
		info = FormatInfo{Tool: "plzip", PipeFlags: "-dc --threads=" + ncpuStr(), DirectFlags: "-dk --threads=" + ncpuStr(), IsTar: false, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".zip"):
		info = FormatInfo{Tool: "unzip", PipeFlags: "-o", DirectFlags: "-o", IsTar: false, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".7z"):
		info = FormatInfo{Tool: sevenzBin(), PipeFlags: "x", DirectFlags: "x", IsTar: false, TestFlag: "t"}
	case strings.HasSuffix(ext, ".rar"):
		info = FormatInfo{Tool: rarBin(), PipeFlags: "x", DirectFlags: "x", IsTar: false, TestFlag: "t"}
	case strings.HasSuffix(ext, ".br"):
		info = FormatInfo{Tool: "brotli", PipeFlags: "-dc", DirectFlags: "-dk", IsTar: false, TestFlag: "-t"}
	case strings.HasSuffix(ext, ".tar"):
		info = FormatInfo{Tool: "tar", PipeFlags: "-xf", DirectFlags: "-xf", IsTar: false, TestFlag: "-tf"}
	default:
		return info, fmt.Errorf("formato no reconocido: %s", filename)
	}
	return info, nil
}
