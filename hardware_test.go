package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsRotationalLinux_Mocks(t *testing.T) {
	tmpDir := t.TempDir()

	devicesDir := filepath.Join(tmpDir, "devices")
	sysfsDir := filepath.Join(tmpDir, "sys_class_block")
	if err := os.MkdirAll(sysfsDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 1. nvme0n1p3 (SSD / NVMe -> rotational = 0)
	nvmeDevDir := filepath.Join(devicesDir, "nvme0n1")
	if err := os.MkdirAll(filepath.Join(nvmeDevDir, "queue"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nvmeDevDir, "queue", "rotational"), []byte("0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	nvmePartDir := filepath.Join(nvmeDevDir, "nvme0n1p3")
	if err := os.MkdirAll(nvmePartDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(nvmePartDir, filepath.Join(sysfsDir, "nvme0n1p3")); err != nil {
		t.Fatal(err)
	}

	// 2. sda1 (HDD magnético / rotacional -> rotational = 1)
	sdaDevDir := filepath.Join(devicesDir, "sda")
	if err := os.MkdirAll(filepath.Join(sdaDevDir, "queue"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdaDevDir, "queue", "rotational"), []byte("1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	sdaPartDir := filepath.Join(sdaDevDir, "sda1")
	if err := os.MkdirAll(sdaPartDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sdaPartDir, filepath.Join(sysfsDir, "sda1")); err != nil {
		t.Fatal(err)
	}

	// 3. sdb (Disco entero rotacional -> rotational = 1)
	sdbDevDir := filepath.Join(devicesDir, "sdb")
	if err := os.MkdirAll(filepath.Join(sdbDevDir, "queue"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdbDevDir, "queue", "rotational"), []byte("1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sdbDevDir, filepath.Join(sysfsDir, "sdb")); err != nil {
		t.Fatal(err)
	}

	// 4. Crear mountinfo simulado con btrfs subvolúmenes y montajes estándar
	mountinfoPath := filepath.Join(tmpDir, "mountinfo")
	mountinfoContent := `27 35 0:24 / /sys rw,nosuid,nodev,noexec,relatime shared:6 - sysfs sysfs rw
31 35 0:27 / /run rw,nosuid,nodev,noexec,relatime shared:5 - tmpfs tmpfs rw,size=9646572k,mode=755,inode64
35 1 259:2 / / rw,noatime shared:1 - btrfs /dev/nvme0n1p2 rw,ssd
62 35 0:50 / /home rw,noatime shared:115 - btrfs /dev/nvme0n1p3 rw,ssd,discard=async
179 35 0:48 / /mnt/storage rw,noatime shared:352 - btrfs /dev/sda1 rw,space_cache=v2
200 35 8:16 / /media/hdd rw,relatime shared:400 - ext4 /dev/sdb rw
`
	if err := os.WriteFile(mountinfoPath, []byte(mountinfoContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 5. Casos de prueba table-driven
	tests := []struct {
		path     string
		wantRot  bool
		scenario string
	}{
		{"/home/user/documentos/test.txt", false, "NVMe subvolumen btrfs (/home)"},
		{"/mnt/storage/data/archive.tar", true, "HDD mecánico subvolumen btrfs (/mnt/storage)"},
		{"/media/hdd/backups/dump.tar", true, "HDD mecánico ext4 (/media/hdd)"},
		{"/run/user/1000/app.sock", false, "tmpfs en memoria RAM (/run)"},
		{"/unknown/mount/path", false, "Ruta en raíz sin dispositivo sysfs conocido"},
	}

	for _, tc := range tests {
		t.Run(tc.scenario, func(t *testing.T) {
			got := isRotationalLinux(tc.path, mountinfoPath, sysfsDir)
			if got != tc.wantRot {
				t.Errorf("isRotationalLinux(%q) = %v, want %v (%s)", tc.path, got, tc.wantRot, tc.scenario)
			}
		})
	}
}

func TestIsRotational_LiveHost(t *testing.T) {
	// Verificar que IsRotational se ejecute de forma robusta y consistente en el host real
	pathsToTest := []string{
		t.TempDir(),
		os.TempDir(),
		"/",
		filepath.Join(t.TempDir(), "sub", "nonexistent"),
	}

	for _, p := range pathsToTest {
		// Primera llamada (resolución en vivo)
		r1 := IsRotational(p)
		// Segunda llamada (consulta en caché)
		r2 := IsRotational(p)
		if r1 != r2 {
			t.Errorf("IsRotational(%q) resultado inconsistente entre llamadas: %v vs %v", p, r1, r2)
		}
	}
}
