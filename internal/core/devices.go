package core

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
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

type DeviceOperations interface {
	List(ctx context.Context) ([]Device, error)
	Unmount(ctx context.Context, device string) error
	WritablePath(device string) (string, error)
	Eject(ctx context.Context, device string) bool
}

type DeviceManager struct {
	GOOS   string
	Runner CommandRunner
}

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

type linuxDevicesPayload struct {
	BlockDevices []linuxDevice `json:"blockdevices"`
}

type linuxDevice struct {
	Name        string        `json:"name"`
	Path        string        `json:"path"`
	Size        json.Number   `json:"size"`
	Model       string        `json:"model"`
	Transport   string        `json:"tran"`
	Removable   any           `json:"rm"`
	Type        string        `json:"type"`
	Mountpoints []string      `json:"mountpoints"`
	Children    []linuxDevice `json:"children"`
}

func linuxRemovable(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case float64:
		return typed == 1
	case json.Number:
		return typed.String() == "1"
	case string:
		return typed == "1" || typed == "true"
	default:
		return false
	}
}

func parseLinuxDevices(output string, excluded map[string]bool) ([]Device, error) {
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.UseNumber()
	var payload linuxDevicesPayload
	if err := decoder.Decode(&payload); err != nil {
		return nil, WrapError("DEVICE_LIST_FAILED", "Cannot parse lsblk output: "+err.Error(), err)
	}
	devices := make([]Device, 0)
	for _, disk := range payload.BlockDevices {
		if disk.Type != "disk" || excluded[disk.Path] {
			continue
		}
		if !linuxRemovable(disk.Removable) && disk.Transport != "usb" && disk.Transport != "mmc" {
			continue
		}
		bytes, err := strconv.ParseInt(disk.Size.String(), 10, 64)
		if err != nil || bytes <= 0 {
			continue
		}
		name := strings.TrimSpace(disk.Model)
		if name == "" {
			name = disk.Name
		}
		transport := disk.Transport
		if transport == "" {
			transport = "removable"
		}
		devices = append(devices, Device{ID: disk.Path, Path: disk.Path, Name: name, Bytes: bytes, Transport: transport, Removable: true})
	}
	return devices, nil
}

type windowsDisk struct {
	Number       int    `json:"Number"`
	FriendlyName string `json:"FriendlyName"`
	Size         int64  `json:"Size"`
	BusType      string `json:"BusType"`
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
		devices = append(devices, Device{ID: path, Path: path, Name: name, Bytes: disk.Size, Transport: strings.ToLower(bus), Removable: true})
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
	return Device{ID: path, Path: path, Name: name, Bytes: bytes, Transport: transport, Removable: true}, true
}

func (m *DeviceManager) List(ctx context.Context) ([]Device, error) {
	var devices []Device
	var err error
	switch m.GOOS {
	case "linux":
		root, runErr := m.run(ctx, "findmnt", "-n", "-o", "SOURCE", "--target", "/")
		if runErr != nil {
			return nil, runErr
		}
		ancestors, runErr := m.run(ctx, "lsblk", "--inverse", "--noheadings", "--output", "PATH", strings.TrimSpace(root.Stdout))
		if runErr != nil {
			return nil, runErr
		}
		lines := strings.Fields(ancestors.Stdout)
		excluded := map[string]bool{}
		if len(lines) > 0 {
			excluded[lines[len(lines)-1]] = true
		}
		listing, runErr := m.run(ctx, "lsblk", "--json", "--bytes", "--nodeps", "--output", "NAME,PATH,SIZE,MODEL,TRAN,RM,TYPE")
		if runErr != nil {
			return nil, runErr
		}
		devices, err = parseLinuxDevices(listing.Stdout, excluded)
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
		script := `Get-Disk | Select-Object Number,FriendlyName,Size,BusType,IsBoot,IsSystem | ConvertTo-Json -Compress`
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

func collectMountpoints(devices []linuxDevice, output *[]string) {
	for _, device := range devices {
		for _, mountpoint := range device.Mountpoints {
			if mountpoint != "" {
				*output = append(*output, mountpoint)
			}
		}
		collectMountpoints(device.Children, output)
	}
}

func (m *DeviceManager) Unmount(ctx context.Context, device string) error {
	if err := assertSafeDevicePath(device); err != nil {
		return err
	}
	switch m.GOOS {
	case "linux":
		listing, err := m.run(ctx, "lsblk", "--json", "--output", "PATH,MOUNTPOINTS", device)
		if err != nil {
			return err
		}
		var payload linuxDevicesPayload
		if err := json.Unmarshal([]byte(listing.Stdout), &payload); err != nil {
			return WrapError("DEVICE_UNMOUNT_FAILED", "Cannot parse mount information: "+err.Error(), err)
		}
		mountpoints := []string{}
		collectMountpoints(payload.BlockDevices, &mountpoints)
		sort.Slice(mountpoints, func(i, j int) bool { return len(mountpoints[i]) > len(mountpoints[j]) })
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
