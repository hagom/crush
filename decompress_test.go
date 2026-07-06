package main

import (
	"testing"
)

func TestDecompressNoFiles(t *testing.T) {
	opts := DecompressOptions{}
	err := DoDecompress(nil, opts)
	if err == nil {
		t.Error("DoDecompress with nil files should error")
	}
}

func TestDecompressDryRun(t *testing.T) {
	opts := DecompressOptions{
		DryRun: true,
	}
	err := DoDecompress([]string{"test.tar.gz"}, opts)
	if err != nil {
		t.Errorf("DoDecompress dry-run = %v", err)
	}
}
