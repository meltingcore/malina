package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var safeDevicePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^/dev/(?:mmcblk\d+|sd[a-z]+|vd[a-z]+|xvd[a-z]+|nvme\d+n\d+)$`),
	regexp.MustCompile(`^/dev/disk\d+$`),
	regexp.MustCompile(`(?i)^\\\\\.\\PhysicalDrive\d+$`),
}

func assertSafeDevicePath(device string) error {
	for _, pattern := range safeDevicePatterns {
		if pattern.MatchString(device) {
			return nil
		}
	}
	return NewError("UNSAFE_DEVICE_PATH", "Unsupported or unsafe device path: "+device)
}

// DeviceOperations abstracts destructive platform device operations.
type DeviceOperations interface {
	List(ctx context.Context) ([]Device, error)
	Unmount(ctx context.Context, device string) error
	WritablePath(device string) (string, error)
	Eject(ctx context.Context, device string) bool
}

func hardwareDeviceID(platform string, values ...string) (string, bool) {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%s:%x", platform, sum[:12]), true
}

// DeviceManager discovers, unmounts, opens, and ejects physical devices.
type DeviceManager struct {
	GOOS   string
	Runner CommandRunner
}

// NewDeviceManager returns a device manager for a GOOS value.
func NewDeviceManager(goos string) *DeviceManager {
	return &DeviceManager{GOOS: goos, Runner: ExecRunner{}}
}

func (m *DeviceManager) run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	runner := m.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	return runner.Run(ctx, name, args, nil)
}

type linuxMount struct {
	DeviceNumber string
	Mountpoint   string
	Filesystem   string
	Source       string
}

func decodeMountInfoPath(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(value)
}

func parseLinuxMountInfo(contents string) []linuxMount {
	mounts := make([]linuxMount, 0)
	for _, line := range strings.Split(contents, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		mount := linuxMount{DeviceNumber: fields[2], Mountpoint: decodeMountInfoPath(fields[4])}
		for i := 6; i+2 < len(fields); i++ {
			if fields[i] == "-" {
				mount.Filesystem = fields[i+1]
				mount.Source = decodeMountInfoPath(fields[i+2])
				break
			}
		}
		mounts = append(mounts, mount)
	}
	return mounts
}

func linuxBackingDiskNames(sysRoot, deviceNumber string) ([]string, error) {
	start, err := filepath.EvalSymlinks(filepath.Join(sysRoot, "dev", "block", deviceNumber))
	if err != nil {
		return nil, err
	}
	return linuxBackingDiskNamesFromPath(start)
}

func linuxBackingDiskNamesFromPath(start string) ([]string, error) {
	disks := map[string]bool{}
	visiting := map[string]bool{}
	var visit func(string) error
	visit = func(path string) error {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		if visiting[resolved] {
			return fmt.Errorf("cycle in sysfs block-device graph at %s", resolved)
		}
		visiting[resolved] = true
		defer delete(visiting, resolved)

		if _, err := os.Stat(filepath.Join(resolved, "partition")); err == nil {
			return visit(filepath.Dir(resolved))
		}
		entries, err := os.ReadDir(filepath.Join(resolved, "slaves"))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if len(entries) > 0 {
			for _, entry := range entries {
				if err := visit(filepath.Join(resolved, "slaves", entry.Name())); err != nil {
					return err
				}
			}
			return nil
		}
		disks[filepath.Base(resolved)] = true
		return nil
	}
	if err := visit(start); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(disks))
	for name := range disks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

func linuxBackingDiskNamesForMount(sysRoot, devRoot string, mount linuxMount) ([]string, error) {
	names, err := linuxBackingDiskNames(sysRoot, mount.DeviceNumber)
	if err == nil {
		return names, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	sourceName := mount.Source
	if bracket := strings.LastIndex(sourceName, "["); bracket > 0 && strings.HasSuffix(sourceName, "]") {
		sourceName = sourceName[:bracket]
	}
	if !strings.HasPrefix(sourceName, "/dev/") {
		return nil, err
	}
	source, sourceErr := filepath.EvalSymlinks(filepath.Join(devRoot, strings.TrimPrefix(sourceName, "/dev/")))
	if sourceErr != nil {
		return nil, sourceErr
	}
	sourceSysfs, sourceErr := filepath.EvalSymlinks(filepath.Join(sysRoot, "class", "block", filepath.Base(source)))
	if sourceErr != nil {
		return nil, sourceErr
	}
	if mount.Filesystem != "btrfs" {
		return linuxBackingDiskNamesFromPath(sourceSysfs)
	}

	filesystems, sourceErr := os.ReadDir(filepath.Join(sysRoot, "fs", "btrfs"))
	if sourceErr != nil {
		return nil, sourceErr
	}
	for _, filesystem := range filesystems {
		devicesDir := filepath.Join(sysRoot, "fs", "btrfs", filesystem.Name(), "devices")
		devices, readErr := os.ReadDir(devicesDir)
		if readErr != nil {
			continue
		}
		foundSource := false
		paths := make([]string, 0, len(devices))
		for _, device := range devices {
			path, resolveErr := filepath.EvalSymlinks(filepath.Join(devicesDir, device.Name()))
			if resolveErr != nil {
				return nil, resolveErr
			}
			if path == sourceSysfs {
				foundSource = true
			}
			paths = append(paths, path)
		}
		if !foundSource {
			continue
		}
		all := map[string]bool{}
		for _, path := range paths {
			backing, resolveErr := linuxBackingDiskNamesFromPath(path)
			if resolveErr != nil {
				return nil, resolveErr
			}
			for _, name := range backing {
				all[name] = true
			}
		}
		names := make([]string, 0, len(all))
		for name := range all {
			names = append(names, name)
		}
		sort.Strings(names)
		return names, nil
	}
	return nil, fmt.Errorf("cannot identify all backing devices for the Btrfs root filesystem")
}

func readTrimmed(path string) string {
	contents, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(contents))
}

func linuxBlockTransport(sysRoot, name string) string {
	if strings.HasPrefix(name, "mmcblk") {
		return "mmc"
	}
	if strings.HasPrefix(name, "nvme") {
		return "nvme"
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(sysRoot, "class", "block", name))
	if err == nil {
		slashed := filepath.ToSlash(resolved)
		if strings.Contains(slashed, "/usb") {
			return "usb"
		}
		if strings.Contains(slashed, "/mmc") {
			return "mmc"
		}
	}
	return "removable"
}

func listLinuxDevices(mountInfoPath, sysRoot, devRoot string) ([]Device, error) {
	contents, err := os.ReadFile(mountInfoPath)
	if err != nil {
		return nil, WrapError("DEVICE_LIST_FAILED", "Cannot read Linux mount information: "+err.Error(), err)
	}
	mounts := parseLinuxMountInfo(string(contents))
	excluded := map[string]bool{}
	rootFound := false
	for _, mount := range mounts {
		if mount.Mountpoint != "/" {
			continue
		}
		rootFound = true
		names, err := linuxBackingDiskNamesForMount(sysRoot, devRoot, mount)
		if err != nil {
			return nil, WrapError("DEVICE_LIST_FAILED", "Cannot resolve the Linux system disk: "+err.Error(), err)
		}
		for _, name := range names {
			excluded[name] = true
		}
	}
	if !rootFound || len(excluded) == 0 {
		return nil, NewError("DEVICE_LIST_FAILED", "Cannot identify the Linux system disk safely.")
	}

	entries, err := os.ReadDir(filepath.Join(sysRoot, "class", "block"))
	if err != nil {
		return nil, WrapError("DEVICE_LIST_FAILED", "Cannot enumerate Linux block devices: "+err.Error(), err)
	}
	devices := make([]Device, 0)
	for _, entry := range entries {
		name := entry.Name()
		base := filepath.Join(sysRoot, "class", "block", name)
		if excluded[name] || readTrimmed(filepath.Join(base, "partition")) != "" {
			continue
		}
		path := "/dev/" + name
		if assertSafeDevicePath(path) != nil {
			continue
		}
		sectors, err := strconv.ParseInt(readTrimmed(filepath.Join(base, "size")), 10, 64)
		if err != nil || sectors <= 0 || sectors > (1<<63-1)/512 {
			continue
		}
		transport := linuxBlockTransport(sysRoot, name)
		removable := readTrimmed(filepath.Join(base, "removable")) == "1"
		// USB devices commonly report removable=0 even though the whole enclosure is
		// external. A fixed MMC device, however, is generally soldered eMMC and must
		// never be offered as a restore destination.
		if !removable && transport != "usb" {
			continue
		}
		model := readTrimmed(filepath.Join(base, "device", "model"))
		if model == "" {
			model = name
		}
		id, stable := hardwareDeviceID("linux",
			readTrimmed(filepath.Join(base, "wwid")),
			readTrimmed(filepath.Join(base, "device", "wwid")),
			readTrimmed(filepath.Join(base, "device", "serial")),
			readTrimmed(filepath.Join(base, "device", "cid")),
		)
		if !stable {
			id = path
		}
		devices = append(devices, Device{
			ID: id, Path: path, Name: model, Bytes: sectors * 512, Transport: transport,
			Removable: removable || transport == "usb", Stable: stable,
		})
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Path < devices[j].Path })
	return devices, nil
}

func linuxMountpointsForDisk(mountInfoPath, sysRoot, diskName string) ([]string, error) {
	contents, err := os.ReadFile(mountInfoPath)
	if err != nil {
		return nil, err
	}
	mountpoints := make([]string, 0)
	seen := map[string]bool{}
	for _, mount := range parseLinuxMountInfo(string(contents)) {
		names, err := linuxBackingDiskNames(sysRoot, mount.DeviceNumber)
		if err != nil {
			continue
		}
		for _, name := range names {
			if name == diskName && !seen[mount.Mountpoint] {
				seen[mount.Mountpoint] = true
				mountpoints = append(mountpoints, mount.Mountpoint)
			}
		}
	}
	sort.Slice(mountpoints, func(i, j int) bool { return len(mountpoints[i]) > len(mountpoints[j]) })
	return mountpoints, nil
}

type windowsDisk struct {
	Number       int    `json:"Number"`
	FriendlyName string `json:"FriendlyName"`
	Size         int64  `json:"Size"`
	BusType      string `json:"BusType"`
	UniqueID     string `json:"UniqueId"`
	SerialNumber string `json:"SerialNumber"`
	IsBoot       bool   `json:"IsBoot"`
	IsSystem     bool   `json:"IsSystem"`
}

func parseWindowsDevices(output string) ([]Device, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return []Device{}, nil
	}
	if strings.HasPrefix(trimmed, "{") {
		trimmed = "[" + trimmed + "]"
	}
	var disks []windowsDisk
	if err := json.Unmarshal([]byte(trimmed), &disks); err != nil {
		return nil, WrapError("DEVICE_LIST_FAILED", "Cannot parse PowerShell disk output: "+err.Error(), err)
	}
	devices := make([]Device, 0)
	for _, disk := range disks {
		bus := strings.ToUpper(disk.BusType)
		if disk.IsBoot || disk.IsSystem || (bus != "USB" && bus != "SD" && bus != "MMC") || disk.Size <= 0 {
			continue
		}
		path := fmt.Sprintf(`\\.\PhysicalDrive%d`, disk.Number)
		name := disk.FriendlyName
		if name == "" {
			name = fmt.Sprintf("Physical drive %d", disk.Number)
		}
		id, stable := hardwareDeviceID("windows", disk.UniqueID, disk.SerialNumber)
		if !stable {
			id = path
		}
		devices = append(devices, Device{ID: id, Path: path, Name: name, Bytes: disk.Size, Transport: strings.ToLower(bus), Removable: true, Stable: stable})
	}
	return devices, nil
}

func plistText(xml, key, element string) string {
	pattern := regexp.MustCompile(`(?s)<key>` + regexp.QuoteMeta(key) + `</key>\s*<` + element + `>(.*?)</` + element + `>`)
	match := pattern.FindStringSubmatch(xml)
	if len(match) != 2 {
		return ""
	}
	return html.UnescapeString(strings.TrimSpace(match[1]))
}

func plistBool(xml, key string) bool {
	pattern := regexp.MustCompile(`<key>` + regexp.QuoteMeta(key) + `</key>\s*<(true|false)\s*/>`)
	match := pattern.FindStringSubmatch(xml)
	return len(match) == 2 && match[1] == "true"
}

func parseMacDeviceInfo(xml string) (Device, bool) {
	identifier := plistText(xml, "DeviceIdentifier", "string")
	bytes, err := strconv.ParseInt(plistText(xml, "TotalSize", "integer"), 10, 64)
	if identifier == "" || err != nil || bytes <= 0 || plistBool(xml, "Internal") || !plistBool(xml, "Whole") {
		return Device{}, false
	}
	name := plistText(xml, "MediaName", "string")
	if name == "" {
		name = plistText(xml, "IORegistryEntryName", "string")
	}
	if name == "" {
		name = identifier
	}
	transport := strings.ToLower(plistText(xml, "BusProtocol", "string"))
	if transport == "" {
		transport = "external"
	}
	path := "/dev/" + identifier
	id, stable := hardwareDeviceID("darwin",
		plistText(xml, "MediaUUID", "string"),
		plistText(xml, "DiskUUID", "string"),
		plistText(xml, "VolumeUUID", "string"),
	)
	if !stable {
		id = path
	}
	return Device{ID: id, Path: path, Name: name, Bytes: bytes, Transport: transport, Removable: true, Stable: stable}, true
}

func (m *DeviceManager) List(ctx context.Context) ([]Device, error) {
	var devices []Device
	var err error
	switch m.GOOS {
	case "linux":
		devices, err = listLinuxDevices("/proc/self/mountinfo", "/sys", "/dev")
	case "darwin":
		listing, runErr := m.run(ctx, "diskutil", "list", "external", "physical")
		if runErr != nil {
			return nil, runErr
		}
		pathPattern := regexp.MustCompile(`(?m)^/dev/disk\d+`)
		seen := map[string]bool{}
		for _, devicePath := range pathPattern.FindAllString(listing.Stdout, -1) {
			if seen[devicePath] {
				continue
			}
			seen[devicePath] = true
			info, infoErr := m.run(ctx, "diskutil", "info", "-plist", devicePath)
			if infoErr != nil {
				return nil, infoErr
			}
			if device, ok := parseMacDeviceInfo(info.Stdout); ok {
				devices = append(devices, device)
			}
		}
	case "windows":
		script := `Get-Disk | Select-Object Number,FriendlyName,Size,BusType,UniqueId,SerialNumber,IsBoot,IsSystem | ConvertTo-Json -Compress`
		listing, runErr := m.run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
		if runErr != nil {
			return nil, runErr
		}
		devices, err = parseWindowsDevices(listing.Stdout)
	default:
		return nil, NewError("UNSUPPORTED_PLATFORM", "Device discovery is not supported on "+m.GOOS+".")
	}
	if err != nil {
		return nil, err
	}
	for _, device := range devices {
		if err := assertSafeDevicePath(device.Path); err != nil {
			return nil, err
		}
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Path < devices[j].Path })
	return devices, nil
}

func (m *DeviceManager) Unmount(ctx context.Context, device string) error {
	if err := assertSafeDevicePath(device); err != nil {
		return err
	}
	switch m.GOOS {
	case "linux":
		mountpoints, err := linuxMountpointsForDisk("/proc/self/mountinfo", "/sys", strings.TrimPrefix(device, "/dev/"))
		if err != nil {
			return WrapError("DEVICE_UNMOUNT_FAILED", "Cannot inspect mounted filesystems: "+err.Error(), err)
		}
		for _, mountpoint := range mountpoints {
			if _, err := m.run(ctx, "umount", mountpoint); err != nil {
				if _, sudoErr := m.run(ctx, "sudo", "-n", "umount", mountpoint); sudoErr != nil {
					return sudoErr
				}
			}
		}
		return nil
	case "darwin":
		_, err := m.run(ctx, "diskutil", "unmountDisk", device)
		return err
	case "windows":
		match := regexp.MustCompile(`(?i)PhysicalDrive(\d+)$`).FindStringSubmatch(device)
		if len(match) != 2 {
			return NewError("INVALID_DEVICE", "Invalid Windows device: "+device)
		}
		script := fmt.Sprintf(`$ErrorActionPreference='Stop'; $disk=Get-Disk -Number %s; if($disk.IsBoot -or $disk.IsSystem){throw 'Refusing to unmount a system disk'}; Get-Partition -DiskNumber %s | Where-Object DriveLetter | ForEach-Object { mountvol (('{0}:\' -f $_.DriveLetter)) /p }`, match[1], match[1])
		_, err := m.run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
		return err
	default:
		return NewError("UNSUPPORTED_PLATFORM", "Unmounting is not supported on "+m.GOOS+".")
	}
}

func (m *DeviceManager) WritablePath(device string) (string, error) {
	if err := assertSafeDevicePath(device); err != nil {
		return "", err
	}
	if m.GOOS == "darwin" {
		return strings.Replace(device, "/dev/disk", "/dev/rdisk", 1), nil
	}
	return device, nil
}

func (m *DeviceManager) Eject(ctx context.Context, device string) bool {
	var err error
	switch m.GOOS {
	case "darwin":
		_, err = m.run(ctx, "diskutil", "eject", device)
	case "linux":
		_, err = m.run(ctx, "udisksctl", "power-off", "--block-device", device)
	case "windows":
		match := regexp.MustCompile(`(?i)PhysicalDrive(\d+)$`).FindStringSubmatch(device)
		if len(match) != 2 {
			return false
		}
		_, err = m.run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Set-Disk -Number "+match[1]+" -IsOffline $true")
	default:
		return false
	}
	return err == nil
}
