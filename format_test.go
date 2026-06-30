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
