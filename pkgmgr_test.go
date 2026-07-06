package main

import (
	"os"
	"testing"
)

func TestDetectPkgManager(t *testing.T) {
	mgr := DetectPkgManager()
	if mgr == nil {
		t.Log("No package manager detected (expected in some environments)")
	} else {
		t.Logf("Detected package manager: %s", mgr.Name)
	}
}

func TestFindToolInfo(t *testing.T) {
	info := findToolInfo("pigz")
	if info == nil {
		t.Error("findToolInfo(pigz) = nil")
	}
	if info.Name != "pigz" {
		t.Errorf("findToolInfo(pigz).Name = %q, want pigz", info.Name)
	}

	info = findToolInfo("nonexistent")
	if info != nil {
		t.Errorf("findToolInfo(nonexistent) = %v, want nil", info)
	}
}

func TestToolPkgSlice(t *testing.T) {
	mgr := PkgManager{Name: "apt-get"}
	info := findToolInfo("pigz")
	if info == nil {
		t.Skip("tool info not found")
	}
	pkg := toolPkgSlice(*info, mgr)
	if pkg != "pigz" {
		t.Errorf("toolPkgSlice(pigz, apt-get) = %q, want pigz", pkg)
	}

	mgr.Name = "pacman"
	pkg = toolPkgSlice(*info, mgr)
	if pkg != "pigz" {
		t.Errorf("toolPkgSlice(pigz, pacman) = %q, want pigz", pkg)
	}
}

func TestContains(t *testing.T) {
	list := []string{"a", "b", "c"}
	if !contains(list, "a") {
		t.Error("contains should find 'a'")
	}
	if contains(list, "d") {
		t.Error("contains should not find 'd'")
	}
}

func TestIsToolInstalled(t *testing.T) {
	if isToolInstalled(nil, "pigz") {
		t.Error("isToolInstalled(nil, pigz) = true, want false")
	}
}

func TestListCompressed(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "*.unknown")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	err = ListCompressed(f)
	if err == nil {
		t.Error("ListCompressed(.unknown) = nil, want error")
	}
}
