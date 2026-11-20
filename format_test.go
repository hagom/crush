package main

import (
	"os"
	"path/filepath"
	"sync"
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
		{"auto", Auto, false},
		{"AUTO", Auto, false},
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

func TestParseFormatList(t *testing.T) {
	tests := []struct {
		input string
		want  []Format
		err   bool
	}{
		{"gz,xz,zst", []Format{Gz, Xz, Zst}, false},
		{" gz ,  7z , bz3 ", []Format{Gz, SevenZ, Bz3}, false},
		{"gz,xz,gz", []Format{Gz, Xz}, false},
		{"tar", []Format{Tar}, false},
		{"", nil, true},
		{"   ", nil, true},
		{",,", nil, true},
		{"gz,invalid", nil, true},
	}
	for _, tt := range tests {
		got, err := ParseFormatList(tt.input)
		if (err != nil) != tt.err {
			t.Errorf("ParseFormatList(%q) error = %v, wantErr = %v", tt.input, err, tt.err)
			continue
		}
		if !tt.err {
			if len(got) != len(tt.want) {
				t.Fatalf("ParseFormatList(%q) len = %d, want %d", tt.input, len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("ParseFormatList(%q)[%d] = %v, want %v", tt.input, i, got[i], tt.want[i])
				}
			}
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

func TestFormatMaxThreads(t *testing.T) {
	if got := FormatMaxThreads(Tar); got != 1 {
		t.Errorf("FormatMaxThreads(Tar) = %d, want 1", got)
	}
	if got := FormatMaxThreads(Br); got != 1 {
		t.Errorf("FormatMaxThreads(Br) = %d, want 1", got)
	}
	if got := FormatMaxThreads(Zst); got < 1 {
		t.Errorf("FormatMaxThreads(Zst) = %d, want >= 1", got)
	}
	if got := FormatMaxThreads(SevenZ); got < 1 {
		t.Errorf("FormatMaxThreads(SevenZ) = %d, want >= 1", got)
	}
	if lz4SupportsThreads() {
		if got := FormatMaxThreads(Lz4); got < 1 {
			t.Errorf("FormatMaxThreads(Lz4) multithread = %d, want >= 1", got)
		}
	} else {
		if got := FormatMaxThreads(Lz4); got != 1 {
			t.Errorf("FormatMaxThreads(Lz4) singlethread = %d, want 1", got)
		}
	}
}

func TestMaxEffectiveThreads(t *testing.T) {
	// Mono-hilo siempre devuelve 1 independientemente del tamaño
	if got := MaxEffectiveThreads(Tar, 10*1024*1024*1024); got != 1 {
		t.Errorf("MaxEffectiveThreads(Tar, 10GB) = %d, want 1", got)
	}
	if got := MaxEffectiveThreads(Br, 10*1024*1024*1024); got != 1 {
		t.Errorf("MaxEffectiveThreads(Br, 10GB) = %d, want 1", got)
	}

	// Archivo pequeño en Xz (5MB) solo puede usar 1 bloque/hilo
	if got := MaxEffectiveThreads(Xz, 5*1024*1024); got != 1 {
		t.Errorf("MaxEffectiveThreads(Xz, 5MB) = %d, want 1", got)
	}

	// Archivo grande en Xz (2GB) aprovecha NCPU
	if got := MaxEffectiveThreads(Xz, 2*1024*1024*1024); got < 2 {
		t.Errorf("MaxEffectiveThreads(Xz, 2GB) = %d, want >= 2", got)
	}

	// Archivo pequeño en SevenZ (8MB) no debe recibir NCPU hilos
	if got := MaxEffectiveThreads(SevenZ, 8*1024*1024); got != 1 {
		t.Errorf("MaxEffectiveThreads(SevenZ, 8MB) = %d, want 1", got)
	}

	// Archivo de 100MB en SevenZ puede usar múltiples bloques
	if got := MaxEffectiveThreads(SevenZ, 100*1024*1024); got < 2 {
		t.Errorf("MaxEffectiveThreads(SevenZ, 100MB) = %d, want >= 2", got)
	}
}


func TestExtForItem(t *testing.T) {
	tests := []struct {
		format Format
		isDir  bool
		want   string
	}{
		{Gz, false, "gz"},
		{Gz, true, "tar.gz"},
		{Xz, false, "xz"},
		{Xz, true, "tar.xz"},
		{Bz2, false, "bz2"},
		{Bz2, true, "tar.bz2"},
		{Bz3, false, "bz3"},
		{Bz3, true, "tar.bz3"},
		{Zst, false, "zst"},
		{Zst, true, "tar.zst"},
		{Lz, false, "lz"},
		{Lz, true, "tar.lz"},
		{Lrz, false, "lrz"},
		{Lrz, true, "tar.lrz"},
		{Lz4, false, "lz4"},
		{Lz4, true, "tar.lz4"},
		{Br, false, "br"},
		{Br, true, "tar.br"},
		{Zip, false, "zip"},
		{Zip, true, "zip"},
		{SevenZ, false, "7z"},
		{SevenZ, true, "7z"},
		{Tar, false, "tar"},
		{Tar, true, "tar"},
		{Rar, false, "rar"},
		{Rar, true, "rar"},
	}
	for _, tt := range tests {
		got := ExtForItem(tt.format, tt.isDir)
		if got != tt.want {
			t.Errorf("ExtForItem(%v, isDir=%v) = %q, want %q", tt.format, tt.isDir, got, tt.want)
		}
	}
}

func TestDetectAutoFormat(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Directory
	dirFmt := DetectAutoFormat(tmpDir)
	if hasTool(sevenzBin()) {
		if dirFmt != SevenZ {
			t.Errorf("DetectAutoFormat(dir) = %v, want SevenZ", dirFmt)
		}
	} else if hasTool("zstd") {
		if dirFmt != Zst {
			t.Errorf("DetectAutoFormat(dir) = %v, want Zst", dirFmt)
		}
	} else {
		if dirFmt != Gz {
			t.Errorf("DetectAutoFormat(dir) = %v, want Gz", dirFmt)
		}
	}

	// 2. Multimedia / pre-compressed
	for _, ext := range []string{"movie.mp4", "song.flac", "photo.jpg", "archive.zip", "data.tar.gz", "video.mkv"} {
		f := DetectAutoFormat(ext)
		if f != Zst {
			t.Errorf("DetectAutoFormat(%q) = %v, want Zst", ext, f)
		}
	}

	// 3. Text / code / structured data
	for _, ext := range []string{"main.go", "app.py", "notes.txt", "doc.json", "query.sql", "archive.tar"} {
		f := DetectAutoFormat(ext)
		if f != Xz {
			t.Errorf("DetectAutoFormat(%q) = %v, want Xz", ext, f)
		}
	}

	// 4. File without extension - plain text
	txtFile := filepath.Join(tmpDir, "sample_text")
	if err := os.WriteFile(txtFile, []byte("This is plain text with lines\nof readable ASCII code\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if f := DetectAutoFormat(txtFile); f != Xz {
		t.Errorf("DetectAutoFormat(sample_text) = %v, want Xz", f)
	}

	// 5. File without extension - binary nulls
	binFile := filepath.Join(tmpDir, "sample_bin")
	binData := make([]byte, 512)
	binData[0] = 0x7f
	binData[1] = 'E'
	binData[2] = 'L'
	binData[3] = 'F'
	if err := os.WriteFile(binFile, binData, 0644); err != nil {
		t.Fatal(err)
	}
	if f := DetectAutoFormat(binFile); f != Zst {
		t.Errorf("DetectAutoFormat(sample_bin) = %v, want Zst", f)
	}

	// 6. Unknown non-existent file without extension
	if f := DetectAutoFormat("no_ext_file_nonexistent"); f != Zst {
		t.Errorf("DetectAutoFormat(nonexistent) = %v, want Zst", f)
	}

	// 7. Unknown file without extension - high entropy (pre-compressed or random)
	randomFile := filepath.Join(tmpDir, "sample_random")
	rndData := make([]byte, 65536)
	for i := range rndData {
		rndData[i] = byte((i * 1103515245 + 12345) & 0xFF) // LCG generator con distribución uniforme
	}
	if err := os.WriteFile(randomFile, rndData, 0644); err != nil {
		t.Fatal(err)
	}
	wantFmt := Lz4
	if !hasTool("lz4") {
		wantFmt = Zst
	}
	if f := DetectAutoFormat(randomFile); f != wantFmt {
		t.Errorf("DetectAutoFormat(sample_random) = %v, want %v", f, wantFmt)
	}
}

func TestCalculateShannonEntropy(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		wantMin   float64
		wantMax   float64
		exactZero bool
	}{
		{
			name:      "datos vacíos",
			data:      nil,
			exactZero: true,
		},
		{
			name:      "todos bytes idénticos (ceros)",
			data:      make([]byte, 1024),
			exactZero: true,
		},
		{
			name: "uniforme exacto 256 bytes",
			data: func() []byte {
				b := make([]byte, 256)
				for i := 0; i < 256; i++ {
					b[i] = byte(i)
				}
				return b
			}(),
			wantMin: 7.9999,
			wantMax: 8.0001,
		},
		{
			name: "texto plano repetitivo",
			data: []byte("este es un texto de prueba que se repite una y otra vez para verificar entropia baja"),
			wantMin: 3.0,
			wantMax: 4.8,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := CalculateShannonEntropy(tc.data)
			if tc.exactZero {
				if h != 0.0 {
					t.Errorf("esperado 0.0, obtenido %f", h)
				}
			} else {
				if h < tc.wantMin || h > tc.wantMax {
					t.Errorf("entropía %f fuera de rango [%f, %f]", h, tc.wantMin, tc.wantMax)
				}
			}
		})
	}
}

func TestIsHighEntropyFile(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Archivo pequeño (<4096 bytes) -> debe retornar false
	smallFile := filepath.Join(tmpDir, "small.bin")
	os.WriteFile(smallFile, []byte{1, 2, 3, 4}, 0644)
	if IsHighEntropyFile(smallFile) {
		t.Errorf("archivo pequeño no debe ser detectado como alta entropía")
	}

	// 2. Archivo de texto plano (64 KiB) -> false
	txtFile := filepath.Join(tmpDir, "text.txt")
	txtData := make([]byte, 65536)
	copy(txtData, "Texto legible y estructurado repetido. ")
	for i := 38; i < len(txtData); i++ {
		txtData[i] = txtData[i%38]
	}
	os.WriteFile(txtFile, txtData, 0644)
	if IsHighEntropyFile(txtFile) {
		t.Errorf("archivo de texto no debe ser detectado como alta entropía")
	}

	// 3. Archivo de alta entropía (64 KiB uniforme) -> true
	rndFile := filepath.Join(tmpDir, "random.dat")
	rndData := make([]byte, 65536)
	for i := range rndData {
		rndData[i] = byte((i * 1664525 + 1013904223) & 0xFF)
	}
	os.WriteFile(rndFile, rndData, 0644)
	if !IsHighEntropyFile(rndFile) {
		t.Errorf("archivo pseudo-aleatorio uniforme debe ser detectado como alta entropía")
	}
}

func TestParseFormatFromExt_StrictDots(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		wantFmt  Format
		wantOK   bool
	}{
		{"bare zip", "zip", 0, false},
		{"bare tar", "tar", 0, false},
		{"bare 7z", "7z", 0, false},
		{"bare gz", "gz", 0, false},
		{"empty", "", 0, false},
		{"dir dot bare base", "dir.zip/plain", 0, false},
		{"valid zip", "a.zip", Zip, true},
		{"valid tar.gz", "a.tar.gz", Gz, true},
		{"valid nested tar.xz", "dir.zip/file.tar.xz", Xz, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotFmt, gotOK := ParseFormatFromExt(tc.filename)
			if gotOK != tc.wantOK || gotFmt != tc.wantFmt {
				t.Errorf("ParseFormatFromExt(%q) = (%v, %v), want (%v, %v)",
					tc.filename, gotFmt, gotOK, tc.wantFmt, tc.wantOK)
			}
		})
	}
}

func TestInspectAutoFormat(t *testing.T) {
	tmpDir := t.TempDir()

	writeFile := func(name string, data []byte) string {
		p := filepath.Join(tmpDir, name)
		if err := os.WriteFile(p, data, 0644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	makeText := func(n int) []byte {
		pattern := []byte("Structured plain text log line for entropy testing.\n")
		b := make([]byte, n)
		for i := range b {
			b[i] = pattern[i%len(pattern)]
		}
		return b
	}

	makeLowEntropyBin := func(n int) []byte {
		b := make([]byte, n)
		copy(b, []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00})
		return b
	}

	makeHighEntropy := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte((i*1664525 + 1013904223) & 0xFF)
		}
		return b
	}

	wantDirFmt := Gz
	if hasTool(sevenzBin()) {
		wantDirFmt = SevenZ
	} else if hasTool("zstd") {
		wantDirFmt = Zst
	}

	wantHighEntropyFmt := Gz
	if hasTool("lz4") {
		wantHighEntropyFmt = Lz4
	} else if hasTool("zstd") {
		wantHighEntropyFmt = Zst
	}

	emptyFile := writeFile("empty_noext", nil)
	smallText := writeFile("small_text", []byte("hello world\nsecond line\n"))
	smallBin := writeFile("small_bin", makeLowEntropyBin(512))
	smallMp4 := writeFile("small.mp4", makeHighEntropy(1024))
	largeText := writeFile("large_text", makeText(65536))
	largeBin := writeFile("large_bin", makeLowEntropyBin(65536))
	largeRandom := writeFile("large_random", makeHighEntropy(65536))
	highEntropyMp4 := writeFile("movie.mp4", makeHighEntropy(65536))
	lowEntropyMp4 := writeFile("zeros.mp4", makeLowEntropyBin(65536))
	highEntropyZip := writeFile("pack.zip", makeHighEntropy(65536))
	lowEntropyTxt := writeFile("notes.txt", makeText(65536))
	highEntropyTxt := writeFile("encrypted.txt", makeHighEntropy(65536))
	highEntropyTarGz := writeFile("bundle.tar.gz", makeHighEntropy(65536))

	ctrlData := make([]byte, 512)
	for i := range ctrlData {
		if i%5 == 0 {
			ctrlData[i] = 0x07
		} else {
			ctrlData[i] = 'a'
		}
	}
	ctrlBin := writeFile("ctrl_noext", ctrlData)

	symlinkRandom := filepath.Join(tmpDir, "link_random")
	if err := os.Symlink(largeRandom, symlinkRandom); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name            string
		path            string
		wantFmt         Format
		wantHighEntropy bool
	}{
		{"directory", tmpDir, wantDirFmt, false},
		{"nonexistent media ext", filepath.Join(tmpDir, "missing.mp4"), Zst, false},
		{"nonexistent tar.gz ext", filepath.Join(tmpDir, "missing.tar.gz"), Zst, false},
		{"nonexistent text ext", filepath.Join(tmpDir, "missing.json"), Xz, false},
		{"nonexistent unknown ext", filepath.Join(tmpDir, "missing_noext"), Zst, false},
		{"empty file without ext", emptyFile, Zst, false},
		{"small text under 4KiB", smallText, Xz, false},
		{"small binary under 4KiB", smallBin, Zst, false},
		{"control-heavy binary without nulls", ctrlBin, Zst, false},
		{"small mp4 under 4KiB", smallMp4, Zst, false},
		{"large plain text without ext", largeText, Xz, false},
		{"large low-entropy binary without ext", largeBin, Zst, false},
		{"large high-entropy without ext", largeRandom, wantHighEntropyFmt, true},
		{"symlink to high-entropy file", symlinkRandom, wantHighEntropyFmt, true},
		{"large high-entropy mp4", highEntropyMp4, Zst, true},
		{"large low-entropy mp4", lowEntropyMp4, Zst, false},
		{"large high-entropy zip", highEntropyZip, Zst, true},
		{"large low-entropy txt", lowEntropyTxt, Xz, false},
		{"large high-entropy txt", highEntropyTxt, Xz, true},
		{"large high-entropy tar.gz", highEntropyTarGz, Zst, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotFmt, gotHigh := InspectAutoFormat(tc.path)
			if gotFmt != tc.wantFmt || gotHigh != tc.wantHighEntropy {
				t.Errorf("InspectAutoFormat(%q) = (%v, %v), want (%v, %v)",
					tc.path, gotFmt, gotHigh, tc.wantFmt, tc.wantHighEntropy)
			}
			if wantDetect := DetectAutoFormat(tc.path); gotFmt != wantDetect {
				t.Errorf("InspectAutoFormat(%q) format = %v, DetectAutoFormat = %v", tc.path, gotFmt, wantDetect)
			}
			if wantEntropy := IsHighEntropyFile(tc.path); gotHigh != wantEntropy {
				t.Errorf("InspectAutoFormat(%q) highEntropy = %v, IsHighEntropyFile = %v", tc.path, gotHigh, wantEntropy)
			}
		})
	}
}

func TestInspectAutoFormat_PooledBufferSafetyAndConcurrency(t *testing.T) {
	bufPtr, ok := headerSamplePool.Get().(*[]byte)
	if !ok || bufPtr == nil || len(*bufPtr) != headerSampleSize {
		t.Fatalf("headerSamplePool yielded invalid buffer: ok=%v ptr=%v", ok, bufPtr)
	}
	headerSamplePool.Put(bufPtr)

	tmpDir := t.TempDir()

	rndData := make([]byte, 65536)
	for i := range rndData {
		rndData[i] = byte((i*1664525 + 1013904223) & 0xFF)
	}
	rndFile := filepath.Join(tmpDir, "rnd_noext")
	if err := os.WriteFile(rndFile, rndData, 0644); err != nil {
		t.Fatal(err)
	}

	tinyText := filepath.Join(tmpDir, "tiny_noext")
	if err := os.WriteFile(tinyText, []byte("plain ascii text\n"), 0644); err != nil {
		t.Fatal(err)
	}

	emptyFile := filepath.Join(tmpDir, "empty_noext")
	if err := os.WriteFile(emptyFile, nil, 0644); err != nil {
		t.Fatal(err)
	}

	wantRndFmt := Gz
	if hasTool("lz4") {
		wantRndFmt = Lz4
	} else if hasTool("zstd") {
		wantRndFmt = Zst
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if f, high := InspectAutoFormat(rndFile); f != wantRndFmt || !high {
					t.Errorf("rndFile: got (%v, %v), want (%v, true)", f, high, wantRndFmt)
				}
				if f, high := InspectAutoFormat(tinyText); f != Xz || high {
					t.Errorf("tinyText after rndFile: got (%v, %v), want (Xz, false)", f, high)
				}
				if f, high := InspectAutoFormat(emptyFile); f != Zst || high {
					t.Errorf("emptyFile after tinyText: got (%v, %v), want (Zst, false)", f, high)
				}
				if f := DetectAutoFormat(tinyText); f != Xz {
					t.Errorf("DetectAutoFormat(tinyText) = %v, want Xz", f)
				}
				if !IsHighEntropyFile(rndFile) {
					t.Errorf("IsHighEntropyFile(rndFile) = false, want true")
				}
			}
		}()
	}
	wg.Wait()
}

