package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseWindowsDevicesExcludesSystemAndNonExternalDisks(t *testing.T) {
	payload := `[
    {"Number":0,"FriendlyName":"System","Size":1000,"BusType":"NVMe","IsBoot":true,"IsSystem":true},
    {"Number":2,"FriendlyName":"SD card","Size":2000,"BusType":"USB","UniqueId":"usb-card-123","IsBoot":false,"IsSystem":false},
    {"Number":3,"FriendlyName":"Internal","Size":3000,"BusType":"SATA","IsBoot":false,"IsSystem":false}
  ]`
	devices, err := parseWindowsDevices(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Path != `\\.\PhysicalDrive2` {
		t.Fatalf("unexpected devices: %#v", devices)
	}
	if !devices[0].Stable || devices[0].ID == devices[0].Path {
		t.Fatalf("expected a stable hardware identity: %#v", devices[0])
	}
}

func TestParseMacDeviceInfoExcludesInternalDisk(t *testing.T) {
	external := `<?xml version="1.0"?><plist><dict>
    <key>DeviceIdentifier</key><string>disk4</string>
    <key>TotalSize</key><integer>64000000000</integer>
    <key>Internal</key><false/><key>Whole</key><true/>
    <key>MediaName</key><string>SD Card</string><key>BusProtocol</key><string>USB</string>
  </dict></plist>`
	device, ok := parseMacDeviceInfo(external)
	if !ok || device.Path != "/dev/disk4" || device.Bytes != 64000000000 {
		t.Fatalf("unexpected device: %#v, %v", device, ok)
	}
	internal := `<?xml version="1.0"?><plist><dict>
    <key>DeviceIdentifier</key><string>disk0</string><key>TotalSize</key><integer>1000</integer>
    <key>Internal</key><true/><key>Whole</key><true/>
  </dict></plist>`
	if _, ok := parseMacDeviceInfo(internal); ok {
		t.Fatal("internal disk must not be returned")
	}
}

func TestSafeDevicePathsRejectPartitionsAndArguments(t *testing.T) {
	for _, path := range []string{"/dev/sda1", "/dev/disk4s1", "/dev/sda --help", `\\.\PhysicalDrive2\extra`} {
		if err := assertSafeDevicePath(path); err == nil {
			t.Fatalf("expected %q to be rejected", path)
		}
	}
	for _, path := range []string{"/dev/sda", "/dev/mmcblk0", "/dev/nvme0n1", "/dev/disk4", `\\.\PhysicalDrive2`} {
		if err := assertSafeDevicePath(path); err != nil {
			t.Fatalf("expected %q to be accepted: %v", path, err)
		}
	}
}

func TestParseLinuxMountInfo(t *testing.T) {
	mounts := parseLinuxMountInfo("36 25 8:2 / / rw - ext4 /dev/sda2 rw\n40 36 8:1 / /boot\\040files rw - vfat /dev/sda1 rw\n")
	if len(mounts) != 2 || mounts[0].DeviceNumber != "8:2" || mounts[0].Filesystem != "ext4" || mounts[0].Source != "/dev/sda2" || mounts[1].Mountpoint != "/boot files" {
		t.Fatalf("unexpected mounts: %#v", mounts)
	}
}

func TestListLinuxDevicesUsesProcAndSysfs(t *testing.T) {
	temporary := t.TempDir()
	sysRoot := filepath.Join(temporary, "sys")
	mountInfo := filepath.Join(temporary, "mountinfo")
	systemDisk := filepath.Join(sysRoot, "devices", "pci", "nvme", "nvme0n1")
	systemPartition := filepath.Join(systemDisk, "nvme0n1p2")
	usbDisk := filepath.Join(sysRoot, "devices", "pci", "usb1", "1-1", "block", "sda")
	fixedMMC := filepath.Join(sysRoot, "devices", "platform", "mmc", "block", "mmcblk1")
	for _, directory := range []string{
		filepath.Join(sysRoot, "dev", "block"), filepath.Join(sysRoot, "class", "block"),
		filepath.Join(systemDisk, "device"), systemPartition, filepath.Join(usbDisk, "device"), filepath.Join(fixedMMC, "device"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(systemPartition, "partition"), "2\n")
	write(filepath.Join(systemDisk, "size"), "2000\n")
	write(filepath.Join(systemDisk, "removable"), "0\n")
	write(filepath.Join(usbDisk, "size"), "4000\n")
	write(filepath.Join(usbDisk, "removable"), "0\n")
	write(filepath.Join(usbDisk, "device", "model"), "USB Card Reader\n")
	write(filepath.Join(fixedMMC, "size"), "8000\n")
	write(filepath.Join(fixedMMC, "removable"), "0\n")
	write(filepath.Join(fixedMMC, "device", "model"), "Soldered eMMC\n")
	write(mountInfo, "36 25 259:2 / / rw - ext4 /dev/nvme0n1p2 rw\n")
	for link, target := range map[string]string{
		filepath.Join(sysRoot, "dev", "block", "259:2"):     systemPartition,
		filepath.Join(sysRoot, "class", "block", "nvme0n1"): systemDisk,
		filepath.Join(sysRoot, "class", "block", "sda"):     usbDisk,
		filepath.Join(sysRoot, "class", "block", "mmcblk1"): fixedMMC,
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}

	devices, err := listLinuxDevices(mountInfo, sysRoot, filepath.Join(temporary, "dev"))
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Path != "/dev/sda" || devices[0].Bytes != 4000*512 || devices[0].Transport != "usb" {
		t.Fatalf("unexpected devices: %#v", devices)
	}
}

func TestListLinuxDevicesResolvesVirtualBtrfsRootAndExcludesAllBackingDisks(t *testing.T) {
	temporary := t.TempDir()
	sysRoot := filepath.Join(temporary, "sys")
	devRoot := filepath.Join(temporary, "dev")
	mountInfo := filepath.Join(temporary, "mountinfo")
	internalDisk := filepath.Join(sysRoot, "devices", "pci", "nvme0n1")
	internalPartition := filepath.Join(internalDisk, "nvme0n1p2")
	secondDisk := filepath.Join(sysRoot, "devices", "pci", "usb1", "block", "sda")
	secondPartition := filepath.Join(secondDisk, "sda2")
	restoreDisk := filepath.Join(sysRoot, "devices", "pci", "usb2", "block", "sdb")
	btrfsDevices := filepath.Join(sysRoot, "fs", "btrfs", "root-uuid", "devices")
	for _, directory := range []string{
		filepath.Join(sysRoot, "class", "block"), internalPartition, secondPartition,
		filepath.Join(restoreDisk, "device"), btrfsDevices, devRoot,
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, partition := range []string{internalPartition, secondPartition} {
		if err := os.WriteFile(filepath.Join(partition, "partition"), []byte("2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, disk := range []string{internalDisk, secondDisk, restoreDisk} {
		if err := os.WriteFile(filepath.Join(disk, "size"), []byte("4000\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(disk, "removable"), []byte("1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(mountInfo, []byte("36 25 0:37 /root / rw - btrfs /dev/nvme0n1p2[/root] rw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devRoot, "nvme0n1p2"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{
		filepath.Join(sysRoot, "class", "block", "nvme0n1p2"): internalPartition,
		filepath.Join(sysRoot, "class", "block", "nvme0n1"):   internalDisk,
		filepath.Join(sysRoot, "class", "block", "sda"):       secondDisk,
		filepath.Join(sysRoot, "class", "block", "sdb"):       restoreDisk,
		filepath.Join(btrfsDevices, "nvme0n1p2"):              internalPartition,
		filepath.Join(btrfsDevices, "sda2"):                   secondPartition,
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}

	devices, err := listLinuxDevices(mountInfo, sysRoot, devRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Path != "/dev/sdb" {
		t.Fatalf("expected only the separate restore device, got %#v", devices)
	}
}

func TestListLinuxDevicesRejectsUnresolvedVirtualRoot(t *testing.T) {
	temporary := t.TempDir()
	mountInfo := filepath.Join(temporary, "mountinfo")
	if err := os.WriteFile(mountInfo, []byte("36 25 0:37 / / rw - overlay overlay rw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := listLinuxDevices(mountInfo, filepath.Join(temporary, "sys"), filepath.Join(temporary, "dev")); err == nil {
		t.Fatal("must not offer restore devices when the root disk cannot be identified")
	}
}

func TestLinuxBackingDiskNamesTraversesDeviceMapperSlaves(t *testing.T) {
	temporary := t.TempDir()
	sysRoot := filepath.Join(temporary, "sys")
	dm := filepath.Join(sysRoot, "devices", "virtual", "block", "dm-0")
	firstDisk := filepath.Join(sysRoot, "devices", "pci", "nvme0n1")
	firstPartition := filepath.Join(firstDisk, "nvme0n1p2")
	secondDisk := filepath.Join(sysRoot, "devices", "pci", "usb1", "block", "sda")
	secondPartition := filepath.Join(secondDisk, "sda2")
	for _, directory := range []string{
		filepath.Join(sysRoot, "dev", "block"), filepath.Join(dm, "slaves"), firstPartition, secondPartition,
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, partition := range []string{firstPartition, secondPartition} {
		if err := os.WriteFile(filepath.Join(partition, "partition"), []byte("2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		filepath.Join(sysRoot, "dev", "block", "253:0"): dm,
		filepath.Join(dm, "slaves", "nvme0n1p2"):        firstPartition,
		filepath.Join(dm, "slaves", "sda2"):             secondPartition,
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}

	names, err := linuxBackingDiskNames(sysRoot, "253:0")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "nvme0n1" || names[1] != "sda" {
		t.Fatalf("unexpected backing disks: %#v", names)
	}
}
