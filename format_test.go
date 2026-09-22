package main

import (
	"testing"
)

func TestParseFormat(t *testing.T) {
	tests := []struct {
		input string
		want  Format
		err   bool
	}{
		{"gz", Gz, false},
		{"GZ", Gz, false},
		{"xz", Xz, false},
		{"bz2", Bz2, false},
		{"bz3", Bz3, false},
		{"zst", Zst, false},
		{"lz", Lz, false},
		{"lrz", Lrz, false},
		{"zip", Zip, false},
		{"7z", SevenZ, false},
		{"tar", Tar, false},
		{"rar", Rar, false},
		{"lz4", Lz4, false},
		{"br", Br, false},
		{"unknown", 0, true},
		{"", 0, true},
	}
	for _, tt := range tests {
		got, err := ParseFormat(tt.input)
		if (err != nil) != tt.err {
			t.Errorf("ParseFormat(%q) error = %v, wantErr = %v", tt.input, err, tt.err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseFormat(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestExtForFormat(t *testing.T) {
	tests := []struct {
		format Format
		want   string
	}{
		{Gz, "tar.gz"},
		{Xz, "tar.xz"},
		{Bz2, "tar.bz2"},
		{Bz3, "tar.bz3"},
		{Zst, "tar.zst"},
		{Lz, "tar.lz"},
		{Lrz, "tar.lrz"},
		{Zip, "zip"},
		{SevenZ, "7z"},
		{Tar, "tar"},
		{Rar, "rar"},
		{Lz4, "tar.lz4"},
		{Br, "tar.br"},
	}
	for _, tt := range tests {
		got := ExtForFormat(tt.format)
		if got != tt.want {
			t.Errorf("ExtForFormat(%v) = %q, want %q", tt.format, got, tt.want)
		}
	}
}

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		filename string
		wantErr  bool
		checkIs  bool
	}{
		{"file.tar.gz", false, true},
		{"file.tgz", false, true},
		{"file.tar.xz", false, true},
		{"file.txz", false, true},
		{"file.tar.bz2", false, true},
		{"file.tbz2", false, true},
		{"file.tar.bz3", false, true},
		{"file.tar.zst", false, true},
		{"file.tzst", false, true},
		{"file.tar.lz", false, true},
		{"file.tlz", false, true},
		{"file.tar.lrz", false, true},
		{"file.tar.lz4", false, true},
		{"file.tar.br", false, true},
		{"file.tar", false, false},
		{"file.gz", false, false},
		{"file.xz", false, false},
		{"file.bz2", false, false},
		{"file.bz3", false, false},
		{"file.zst", false, false},
		{"file.lz", false, false},
		{"file.lrz", false, false},
		{"file.zip", false, false},
		{"file.7z", false, false},
		{"file.rar", false, false},
		{"file.lz4", false, false},
		{"file.br", false, false},
		{"file.unknown", true, false},
	}
	for _, tt := range tests {
		got, err := DetectFormat(tt.filename)
		if (err != nil) != tt.wantErr {
			t.Errorf("DetectFormat(%q) error = %v, wantErr = %v", tt.filename, err, tt.wantErr)
			continue
		}
		if err == nil && tt.filename != "" {
			if got.IsTar != tt.checkIs {
				t.Errorf("DetectFormat(%q) IsTar = %v, want %v", tt.filename, got.IsTar, tt.checkIs)
			}
			if got.Tool == "" {
				t.Errorf("DetectFormat(%q) Tool is empty", tt.filename)
			}
		}
	}
}

func TestFormatString(t *testing.T) {
	tests := []Format{Gz, Xz, Bz2, Bz3, Zst, Lz, Lrz, Zip, SevenZ, Tar, Rar, Lz4, Br}
	for _, f := range tests {
		if f.String() == "" {
			t.Errorf("Format %d has empty string", f)
		}
	}
}

func TestSevenZ(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Format
		err   bool
	}{
		{"sevenZ parse", "7z", SevenZ, false},
		{"sevenZ upper", "7Z", SevenZ, false},
		{"sevenZ format name", SevenZ.String(), SevenZ, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFormat(tt.input)
			if (err != nil) != tt.err {
				t.Errorf("ParseFormat(%q) error = %v, wantErr = %v", tt.input, err, tt.err)
				return
			}
			if got != tt.want {
				t.Errorf("ParseFormat(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatInfo(t *testing.T) {
	tests := []struct {
		format Format
		ext    string
	}{
		{Gz, "gz"},
		{Xz, "xz"},
		{Bz2, "bz2"},
		{Bz3, "bz3"},
		{Zst, "zst"},
		{Lz, "lz"},
		{Lrz, "lrz"},
		{Zip, "zip"},
		{SevenZ, "7z"},
		{Tar, "tar"},
		{Rar, "rar"},
		{Lz4, "lz4"},
		{Br, "br"},
	}
	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			if tt.format.String() != tt.ext {
				t.Errorf("Format(%d).String() = %q, want %q", tt.format, tt.format.String(), tt.ext)
			}
			got, err := ParseFormat(tt.ext)
			if err != nil {
				t.Errorf("ParseFormat(%q) error = %v", tt.ext, err)
				return
			}
			if got != tt.format {
				t.Errorf("ParseFormat(%q) = %v, want %v", tt.ext, got, tt.format)
			}
		})
	}
}

func TestFormatIsContainerAndIsStream(t *testing.T) {
	tests := []struct {
		format      Format
		isContainer bool
		isStream    bool
	}{
		{Gz, false, true},
		{Xz, false, true},
		{Bz2, false, true},
		{Bz3, false, true},
		{Zst, false, true},
		{Lz, false, true},
		{Lrz, false, true},
		{Zip, true, false},
		{SevenZ, true, false},
		{Tar, true, false},
		{Rar, true, false},
		{Lz4, false, true},
		{Br, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.format.String(), func(t *testing.T) {
			if got := tt.format.IsContainer(); got != tt.isContainer {
				t.Errorf("Format(%s).IsContainer() = %v, want %v", tt.format, got, tt.isContainer)
			}
			if got := tt.format.IsStream(); got != tt.isStream {
				t.Errorf("Format(%s).IsStream() = %v, want %v", tt.format, got, tt.isStream)
			}

			info := FormatInfoFromFormat(tt.format)
			if got := info.IsContainer(); got != tt.isContainer {
				t.Errorf("FormatInfo(%s).IsContainer() = %v, want %v", tt.format, got, tt.isContainer)
			}
			if got := info.IsStream(); got != tt.isStream {
				t.Errorf("FormatInfo(%s).IsStream() = %v, want %v", tt.format, got, tt.isStream)
			}
		})
	}
}

func TestArchiveBaseName(t *testing.T) {
	tests := []struct {
		name      string
		format    Format
		inputPath string
		want      string
	}{
		// Stream formats: return filepath.Base(inputPath)
		{"Gz regular file", Gz, "/path/to/archive.tar.gz", "archive.tar.gz"},
		{"Gz relative path", Gz, "data.txt", "data.txt"},
		{"Xz path", Xz, "/var/log/syslog.log", "syslog.log"},
		{"Zst path", Zst, "../folder/document.pdf", "document.pdf"},
		{"Lz4 path", Lz4, "myfile.dat", "myfile.dat"},
		{"Br path", Br, "/tmp/style.css", "style.css"},
		// Container formats: return strings.TrimSuffix(filepath.Base(inputPath), filepath.Ext(inputPath))
		{"Zip path", Zip, "/path/to/archive.zip", "archive"},
		{"Zip relative", Zip, "mydata.zip", "mydata"},
		{"SevenZ path", SevenZ, "/home/user/backup.7z", "backup"},
		{"Tar path", Tar, "/etc/config.tar", "config"},
		{"Tar with dots", Tar, "data.2024.tar", "data.2024"},
		{"Rar path", Rar, "/downloads/pack.rar", "pack"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.format.ArchiveBaseName(tt.inputPath); got != tt.want {
				t.Errorf("Format(%s).ArchiveBaseName(%q) = %q, want %q", tt.format, tt.inputPath, got, tt.want)
			}

			info := FormatInfoFromFormat(tt.format)
			if got := info.ArchiveBaseName(tt.inputPath); got != tt.want {
				t.Errorf("FormatInfo(%s).ArchiveBaseName(%q) = %q, want %q", tt.format, tt.inputPath, got, tt.want)
			}
		})
	}
}

func TestDecompressMultithreadFlags(t *testing.T) {
	// Gz: pigz PipeFlags should include -p + ncpuStr()
	gzInfo := FormatInfoFromFormat(Gz)
	if gzInfo.Tool == "pigz" {
		wantPipe := "-dc -p " + ncpuStr()
		if gzInfo.PipeFlags != wantPipe {
			t.Errorf("FormatInfoFromFormat(Gz).PipeFlags = %q, want %q", gzInfo.PipeFlags, wantPipe)
		}
	}

	// Bz2: when tool is lbzip2, PipeFlags and DirectFlags should include -n + ncpuStr()
	bz2Info := FormatInfoFromFormat(Bz2)
	if bz2Info.Tool == "lbzip2" {
		wantPipe := "-dc -n " + ncpuStr()
		wantDirect := "-dk -n " + ncpuStr()
		if bz2Info.PipeFlags != wantPipe {
			t.Errorf("FormatInfoFromFormat(Bz2).PipeFlags = %q, want %q", bz2Info.PipeFlags, wantPipe)
		}
		if bz2Info.DirectFlags != wantDirect {
			t.Errorf("FormatInfoFromFormat(Bz2).DirectFlags = %q, want %q", bz2Info.DirectFlags, wantDirect)
		}
	}
}

func TestHasTarSuffix(t *testing.T) {
	tests := []struct {
		filename string
		want     bool
	}{
		{"archive.tar.gz", true},
		{"backup.tgz", true},
		{"data.tar.xz", true},
		{"PHOTO.TAR.XZ", true},
		{"package.txz", true},
		{"archive.tar.bz2", true},
		{"backup.tbz2", true},
		{"bundle.tar.bz3", true},
		{"mydata.tar.zst", true},
		{"image.tzst", true},
		{"doc.tar.lz", true},
		{"doc.tlz", true},
		{"file.tar.lrz", true},
		{"pack.tar.lz4", true},
		{"web.tar.br", true},
		{"Project.Tgz", true},
		{"/path/to/archive.TAR.GZ", true},
		// Non-tar files
		{"file.tar", false},
		{"file.gz", false},
		{"file.xz", false},
		{"file.bz2", false},
		{"file.bz3", false},
		{"file.zst", false},
		{"file.lz", false},
		{"file.lrz", false},
		{"file.lz4", false},
		{"file.br", false},
		{"file.zip", false},
		{"file.7z", false},
		{"file.rar", false},
		{"file.txt", false},
		{"archive", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := HasTarSuffix(tt.filename)
			if got != tt.want {
				t.Errorf("HasTarSuffix(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestStripTarSuffix(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"archive.tar.gz", "archive"},
		{"backup.tgz", "backup"},
		{"data.tar.xz", "data"},
		{"PHOTO.TAR.XZ", "PHOTO"},
		{"package.txz", "package"},
		{"archive.tar.bz2", "archive"},
		{"backup.tbz2", "backup"},
		{"bundle.tar.bz3", "bundle"},
		{"mydata.tar.zst", "mydata"},
		{"image.tzst", "image"},
		{"doc.tar.lz", "doc"},
		{"doc.tlz", "doc"},
		{"file.tar.lrz", "file"},
		{"pack.tar.lz4", "pack"},
		{"web.tar.br", "web"},
		{"Project.Tgz", "Project"},
		{"/path/to/archive.TAR.GZ", "/path/to/archive"},
		// Non-tar files should remain unchanged
		{"file.tar", "file.tar"},
		{"file.gz", "file.gz"},
		{"file.xz", "file.xz"},
		{"file.bz2", "file.bz2"},
		{"file.bz3", "file.bz3"},
		{"file.zst", "file.zst"},
		{"file.lz", "file.lz"},
		{"file.lrz", "file.lrz"},
		{"file.lz4", "file.lz4"},
		{"file.br", "file.br"},
		{"file.zip", "file.zip"},
		{"file.7z", "file.7z"},
		{"file.rar", "file.rar"},
		{"file.txt", "file.txt"},
		{"archive", "archive"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := StripTarSuffix(tt.filename)
			if got != tt.want {
				t.Errorf("StripTarSuffix(%q) = %q, want %q", tt.filename, got, tt.want)
			}
		})
	}
}



