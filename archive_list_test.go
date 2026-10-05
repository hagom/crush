package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestListArchiveMembersTar(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "sample.tar")
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	hdr1 := &tar.Header{Name: "dir/", Mode: 0755, Typeflag: tar.TypeDir}
	tw.WriteHeader(hdr1)
	hdr2 := &tar.Header{Name: "dir/hello.txt", Mode: 0644, Size: 12, Typeflag: tar.TypeReg}
	tw.WriteHeader(hdr2)
	tw.Write([]byte("hello world\n"))
	tw.Close()
	f.Close()

	members, err := ListArchiveMembers(tarPath, "")
	if err != nil {
		t.Fatalf("ListArchiveMembers failed: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("got %d members, want 2", len(members))
	}
	if members[0].Path != "dir/" || !members[0].IsDir {
		t.Errorf("member[0] mismatch: %+v", members[0])
	}
	if members[1].Path != "dir/hello.txt" || members[1].Size != 12 || members[1].IsDir {
		t.Errorf("member[1] mismatch: %+v", members[1])
	}
}

func TestListArchiveMembersZip(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "sample.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("doc.txt")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("documentation"))
	zw.Close()
	f.Close()

	members, err := ListArchiveMembers(zipPath, "")
	if err != nil {
		t.Fatalf("ListArchiveMembers failed: %v", err)
	}
	if len(members) != 1 || members[0].Path != "doc.txt" || members[0].Size != 13 {
		t.Errorf("zip member mismatch: %+v", members)
	}
}

func TestListArchiveMembersTarGz(t *testing.T) {
	tmpDir := t.TempDir()
	tarGzPath := filepath.Join(tmpDir, "sample.tar.gz")
	f, err := os.Create(tarGzPath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	tw.WriteHeader(&tar.Header{Name: "file.bin", Mode: 0644, Size: 100, Typeflag: tar.TypeReg})
	tw.Write(make([]byte, 100))
	tw.Close()
	gw.Close()
	f.Close()

	members, err := ListArchiveMembers(tarGzPath, "")
	if err != nil {
		t.Fatalf("ListArchiveMembers tar.gz failed: %v", err)
	}
	if len(members) != 1 || members[0].Path != "file.bin" || members[0].Size != 100 {
		t.Errorf("tar.gz member mismatch: %+v", members)
	}
}

func TestListArchiveMembersStream(t *testing.T) {
	tmpDir := t.TempDir()
	gzPath := filepath.Join(tmpDir, "single.txt.gz")
	f, err := os.Create(gzPath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	gw.Write([]byte("stream data content"))
	gw.Close()
	f.Close()

	members, err := ListArchiveMembers(gzPath, "")
	if err != nil {
		t.Fatalf("ListArchiveMembers single stream failed: %v", err)
	}
	if len(members) != 1 || members[0].Path != "single.txt" {
		t.Errorf("stream member mismatch: %+v", members)
	}
}
