package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

func clearRotationalCache() {
	rotationalCache.Range(func(key, _ any) bool {
		rotationalCache.Delete(key)
		return true
	})
}

func countRotationalCacheEntries() (int, map[string]bool) {
	entries := make(map[string]bool)
	rotationalCache.Range(func(key, value any) bool {
		entries[key.(string)] = value.(bool)
		return true
	})
	return len(entries), entries
}

func TestIsRotationalLinux_CachesOnlyByMountPoint(t *testing.T) {
	tmpDir := t.TempDir()

	devicesDir := filepath.Join(tmpDir, "devices")
	sysfsDir := filepath.Join(tmpDir, "sys_class_block")
	if err := os.MkdirAll(sysfsDir, 0755); err != nil {
		t.Fatal(err)
	}

	makeBlockDev := func(disk, part, rot string) string {
		devDir := filepath.Join(devicesDir, disk)
		if err := os.MkdirAll(filepath.Join(devDir, "queue"), 0755); err != nil {
			t.Fatal(err)
		}
		rotFile := filepath.Join(devDir, "queue", "rotational")
		if err := os.WriteFile(rotFile, []byte(rot+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		target := devDir
		name := disk
		if part != "" {
			target = filepath.Join(devDir, part)
			if err := os.MkdirAll(target, 0755); err != nil {
				t.Fatal(err)
			}
			name = part
		}
		if err := os.Symlink(target, filepath.Join(sysfsDir, name)); err != nil {
			t.Fatal(err)
		}
		return rotFile
	}

	hddRotFile := makeBlockDev("sda", "sda1", "1")
	makeBlockDev("nvme0n1", "nvme0n1p1", "0")

	mountinfoPath := filepath.Join(tmpDir, "mountinfo")
	mountinfoContent := `31 35 0:27 / /run rw,nosuid,nodev,noexec,relatime shared:5 - tmpfs tmpfs rw
62 35 0:50 / /mnt/ssd rw,noatime shared:115 - ext4 /dev/nvme0n1p1 rw
179 35 0:48 / /mnt/hdd rw,noatime shared:352 - ext4 /dev/sda1 rw
`
	if err := os.WriteFile(mountinfoPath, []byte(mountinfoContent), 0644); err != nil {
		t.Fatal(err)
	}

	origMountinfo := defaultMountinfo
	origSysfs := defaultSysfsBlock
	defaultMountinfo = mountinfoPath
	defaultSysfsBlock = sysfsDir
	clearRotationalCache()
	t.Cleanup(func() {
		defaultMountinfo = origMountinfo
		defaultSysfsBlock = origSysfs
		clearRotationalCache()
	})

	tests := []struct {
		name      string
		paths     []string
		wantRot   bool
		wantMount string
	}{
		{
			name:      "many paths under HDD mount",
			paths:     []string{"/mnt/hdd/a.txt", "/mnt/hdd/sub/b.bin", "/mnt/hdd/sub/deep/c.tar", "/mnt/hdd/other/d.log"},
			wantRot:   true,
			wantMount: "/mnt/hdd",
		},
		{
			name:      "many paths under SSD mount",
			paths:     []string{"/mnt/ssd/x.txt", "/mnt/ssd/dir/y.go", "/mnt/ssd/dir/z.json"},
			wantRot:   false,
			wantMount: "/mnt/ssd",
		},
		{
			name:      "non-dev tmpfs mount",
			paths:     []string{"/run/a.sock", "/run/user/1000/b.pid"},
			wantRot:   false,
			wantMount: "/run",
		},
		{
			name:      "paths outside known mounts",
			paths:     []string{"/unmounted/file1", "/unmounted/dir/file2"},
			wantRot:   false,
			wantMount: "",
		},
	}

	wantCachedMounts := make(map[string]bool)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, p := range tc.paths {
				got := isRotationalLinux(p, defaultMountinfo, defaultSysfsBlock)
				if got != tc.wantRot {
					t.Errorf("isRotationalLinux(%q) = %v, want %v", p, got, tc.wantRot)
				}
				if _, ok := rotationalCache.Load(p); ok {
					t.Errorf("rotationalCache must not store individual path %q", p)
				}
			}
			if tc.wantMount != "" {
				wantCachedMounts[tc.wantMount] = tc.wantRot
			}
		})
	}

	gotCount, gotEntries := countRotationalCacheEntries()
	if gotCount != len(wantCachedMounts) {
		t.Fatalf("rotationalCache has %d entries (%v), want %d (%v)", gotCount, gotEntries, len(wantCachedMounts), wantCachedMounts)
	}
	for mount, wantVal := range wantCachedMounts {
		gotVal, ok := gotEntries[mount]
		if !ok || gotVal != wantVal {
			t.Errorf("rotationalCache[%q] = (%v, %v), want (%v, true)", mount, gotVal, ok, wantVal)
		}
	}

	if err := os.Remove(hddRotFile); err != nil {
		t.Fatal(err)
	}
	const numConcurrent = 32
	var wg sync.WaitGroup
	for i := 0; i < numConcurrent; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p := fmt.Sprintf("/mnt/hdd/batch/file_%d.dat", idx)
			if !isRotationalLinux(p, defaultMountinfo, defaultSysfsBlock) {
				t.Errorf("isRotationalLinux(%q) = false, want cached true", p)
			}
		}(i)
	}
	wg.Wait()

	afterCount, afterEntries := countRotationalCacheEntries()
	if afterCount != len(wantCachedMounts) {
		t.Errorf("after %d distinct paths, rotationalCache grew to %d entries (%v), want %d", numConcurrent, afterCount, afterEntries, len(wantCachedMounts))
	}
}

func TestIsRotationalLinux_EscapesMapperPartitionsAndPathEdgeCases(t *testing.T) {
	tmpDir := t.TempDir()
	sysfsDir := filepath.Join(tmpDir, "sys_class_block")
	devDir := filepath.Join(tmpDir, "dev")
	if err := os.MkdirAll(filepath.Join(devDir, "mapper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sysfsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. Flat sysfs layout (without parent device tree symlink): mmcblk0 (0) + mmcblk0p1, nvme0n1 (0) + nvme0n1p1, sdc (1) + sdc2
	writeQueueRot := func(dev, val string) {
		qDir := filepath.Join(sysfsDir, dev, "queue")
		if err := os.MkdirAll(qDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(qDir, "rotational"), []byte(val+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeQueueRot("mmcblk0", "0")
	if err := os.MkdirAll(filepath.Join(sysfsDir, "mmcblk0p1"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeQueueRot("sdc", "1")
	if err := os.MkdirAll(filepath.Join(sysfsDir, "sdc2"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 2. /dev/mapper/vg_hdd-lv_backup -> ../dm-0, where dm-0 has slaves/sdc2 -> ../../sdc2
	if err := os.WriteFile(filepath.Join(devDir, "dm-0"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../dm-0", filepath.Join(devDir, "mapper", "vg_hdd-lv_backup")); err != nil {
		t.Fatal(err)
	}
	dm0Slaves := filepath.Join(sysfsDir, "dm-0", "slaves")
	if err := os.MkdirAll(dm0Slaves, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sysfsDir, "sdc2"), filepath.Join(dm0Slaves, "sdc2")); err != nil {
		t.Fatal(err)
	}

	// 3. /dev/mapper/vg_ssd-lv_fast -> ../dm-1 with direct queue/rotational = 0
	if err := os.WriteFile(filepath.Join(devDir, "dm-1"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../dm-1", filepath.Join(devDir, "mapper", "vg_ssd-lv_fast")); err != nil {
		t.Fatal(err)
	}
	writeQueueRot("dm-1", "0")

	// 4. mountinfo with:
	// - initial rootfs on / shadowed by real HDD /dev/sdc2 on / (overmount at same length!)
	// - octal-escaped space \040 and backslash \134 in mount point
	// - private mount with 0 optional fields before "-"
	// - /dev/mapper/ symlinks
	mountinfoPath := filepath.Join(tmpDir, "mountinfo")
	mountinfoContent := fmt.Sprintf(
		"1 1 0:1 / / rw - rootfs rootfs rw\n"+
			"20 1 8:34 / / rw,relatime - ext4 %s/sdc2 rw\n"+
			"30 20 179:1 / /mnt/sd\\040card rw - ext4 %s/mmcblk0p1 rw\n"+
			"40 20 253:0 / /media/My\\040Backup\\134HDD rw,relatime shared:10 - xfs %s/mapper/vg_hdd-lv_backup rw\n"+
			"50 20 253:1 / /mnt/fast_lvm rw,relatime - ext4 %s/mapper/vg_ssd-lv_fast rw\n",
		devDir, devDir, devDir, devDir,
	)
	if err := os.WriteFile(mountinfoPath, []byte(mountinfoContent), 0o644); err != nil {
		t.Fatal(err)
	}

	origDev := defaultDevDir
	defaultDevDir = devDir
	t.Cleanup(func() {
		defaultDevDir = origDev
		clearRotationalCache()
	})

	tests := []struct {
		name    string
		path    string
		wantRot bool
	}{
		{"overmounted root / resolves later sdc2 HDD mount", "/var/log/syslog", true},
		{"octal escaped space in SD card mount (mmcblk0p1 -> mmcblk0 SSD)", "/mnt/sd card/photo.jpg", false},
		{"octal escaped space+backslash + mapper -> dm-0 -> slaves/sdc2 -> sdc HDD", `/media/My Backup\HDD/archive.tar`, true},
		{"mapper -> dm-1 direct queue/rotational=0 SSD", "/mnt/fast_lvm/db.zst", false},
		{"uncleaned .. traversal out of fast_lvm back to root HDD", "/mnt/fast_lvm/../var/data.bin", true},
		{"trailing slash on SSD mount point", "/mnt/fast_lvm/", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRotationalLinux(tc.path, mountinfoPath, sysfsDir); got != tc.wantRot {
				t.Errorf("isRotationalLinux(%q) = %v, want %v", tc.path, got, tc.wantRot)
			}
		})
	}

	if pathIsUnderMount("", "/") || pathIsUnderMount("relative/file", "/") {
		t.Errorf("pathIsUnderMount must reject empty or relative paths")
	}
}
