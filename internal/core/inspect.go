package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const inspectScript = `set -eu
clean() { printf '%s' "$1" | tr '\t\r\n' '   '; }
disk_for() {
  source_device="$1"
  case "$source_device" in /dev/*) ;; *) return 1 ;; esac
  device_type="$(lsblk -ndo TYPE "$source_device" 2>/dev/null | head -n 1)"
  if [ "$device_type" = "disk" ]; then
    printf '%s' "$source_device"
    return 0
  fi
  parent="$(lsblk -ndo PKNAME "$source_device" 2>/dev/null | head -n 1)"
  [ -n "$parent" ] || return 1
  printf '/dev/%s' "$parent"
}

root_source="$(findmnt -n -o SOURCE --target /)"
root_disk="$(disk_for "$root_source")" || {
  echo 'Cannot resolve the root filesystem to a physical disk.' >&2
  exit 21
}

boot_source=''
boot_disk=''
for mountpoint in /boot/firmware /boot; do
  candidate="$(findmnt -rn -o SOURCE --mountpoint "$mountpoint" 2>/dev/null || true)"
  if [ -n "$candidate" ]; then
    boot_source="$candidate"
    boot_disk="$(disk_for "$candidate" || true)"
    break
  fi
done

hostname_value="$(hostname 2>/dev/null || printf raspberrypi)"
model="$(tr -d '\000' </proc/device-tree/model 2>/dev/null || printf 'Raspberry Pi')"
os_name='Linux'
if [ -r /etc/os-release ]; then
  detected_os="$(sed -n 's/^PRETTY_NAME=//p' /etc/os-release | head -n 1 | sed 's/^"//; s/"$//')"
  if [ -n "$detected_os" ]; then os_name="$detected_os"; fi
fi
disk_size="$(lsblk -bndo SIZE "$root_disk" 2>/dev/null | head -n 1)"
logical_sector="$(lsblk -bndo LOG-SEC "$root_disk" 2>/dev/null | head -n 1)"
sudo_ok='false'
if [ "$(id -u)" = '0' ] || sudo -n true >/dev/null 2>&1; then sudo_ok='true'; fi

printf 'hostname\t%s\n' "$(clean "$hostname_value")"
printf 'model\t%s\n' "$(clean "$model")"
printf 'os\t%s\n' "$(clean "$os_name")"
printf 'architecture\t%s\n' "$(clean "$(uname -m)")"
printf 'rootSource\t%s\n' "$(clean "$root_source")"
printf 'rootDisk\t%s\n' "$(clean "$root_disk")"
printf 'rootFilesystem\t%s\n' "$(clean "$(findmnt -n -o FSTYPE --target /)")"
printf 'bootSource\t%s\n' "$(clean "$boot_source")"
printf 'bootDisk\t%s\n' "$(clean "$boot_disk")"
printf 'diskSize\t%s\n' "$(clean "$disk_size")"
printf 'logicalSectorSize\t%s\n' "$(clean "$logical_sector")"
printf 'sudoAvailable\t%s\n' "$sudo_ok"
`

func parseInspection(output string) (PiInfo, error) {
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 {
			values[parts[0]] = parts[1]
		}
	}
	for _, required := range []string{"hostname", "rootDisk", "diskSize"} {
		if values[required] == "" {
			return PiInfo{}, NewError("INVALID_INSPECTION", "Pi inspection did not return "+required+".")
		}
	}
	if err := assertSafeDevicePath(values["rootDisk"]); err != nil {
		return PiInfo{}, err
	}
	if values["bootDisk"] != "" {
		if err := assertSafeDevicePath(values["bootDisk"]); err != nil {
			return PiInfo{}, err
		}
	}
	diskSize, err := strconv.ParseInt(values["diskSize"], 10, 64)
	if err != nil || diskSize <= 0 {
		return PiInfo{}, NewError("INVALID_DISK_SIZE", "Invalid source disk size: "+values["diskSize"])
	}
	sectorSize, _ := strconv.ParseInt(values["logicalSectorSize"], 10, 64)
	info := PiInfo{
		Hostname:          values["hostname"],
		Model:             values["model"],
		OS:                values["os"],
		Architecture:      values["architecture"],
		RootSource:        values["rootSource"],
		RootDisk:          values["rootDisk"],
		RootFilesystem:    values["rootFilesystem"],
		BootSource:        values["bootSource"],
		BootDisk:          values["bootDisk"],
		DiskSize:          diskSize,
		LogicalSectorSize: sectorSize,
		SudoAvailable:     values["sudoAvailable"] == "true",
		Warnings:          []string{},
	}
	if info.BootDisk != "" && info.BootDisk != info.RootDisk {
		info.Warnings = append(info.Warnings, fmt.Sprintf("Hybrid boot detected: boot is on %s, root is on %s.", info.BootDisk, info.RootDisk))
	}
	if !info.SudoAvailable {
		info.Warnings = append(info.Warnings, "Passwordless sudo is unavailable; raw disk backup cannot start.")
	}
	info.Supported = len(info.Warnings) == 0
	return info, nil
}

func (r *SSHRemote) Inspect(ctx context.Context, connection Connection) (PiInfo, error) {
	result, err := r.run(ctx, connection, "sh -s", strings.NewReader(inspectScript))
	if err != nil {
		return PiInfo{}, err
	}
	return parseInspection(result.Stdout)
}

func assertBackupSupported(info PiInfo) error {
	if info.BootDisk != "" && info.BootDisk != info.RootDisk {
		return NewError(
			"HYBRID_BOOT_UNSUPPORTED",
			fmt.Sprintf("This Pi uses %s for boot and %s for root. A single raw image would be incomplete.", info.BootDisk, info.RootDisk),
		)
	}
	if !info.SudoAvailable {
		return NewError("SUDO_REQUIRED", "The remote account needs passwordless sudo, or must be root, to read the whole disk.")
	}
	return nil
}
