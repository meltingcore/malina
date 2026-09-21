package core

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const inspectScript = `set -eu
clean() { printf '%s' "$1" | tr '\t\r\n' '   '; }
mount_field() {
  target="$1"
  requested="$2"
  awk -v target="$target" -v requested="$requested" '
    $5 == target {
      for (field_index = 7; field_index <= NF; field_index++) {
        if ($field_index == "-") {
          if (requested == "device") print $3
          if (requested == "filesystem") print $(field_index + 1)
          if (requested == "source") print $(field_index + 2)
          exit
        }
      }
    }
  ' /proc/self/mountinfo
}

disk_for_device_number() {
  current="$(readlink -f "/sys/dev/block/$1" 2>/dev/null)" || return 1
  [ -n "$current" ] || return 1
  while :; do
    if [ -f "$current/partition" ]; then
      current="$(dirname "$current")"
      continue
    fi
    slave=''
    for candidate in "$current"/slaves/*; do
      [ -e "$candidate" ] || continue
      [ -z "$slave" ] || return 1
      slave="$candidate"
    done
    if [ -n "$slave" ]; then
      current="$(readlink -f "$slave" 2>/dev/null)" || return 1
      continue
    fi
    printf '/dev/%s' "$(basename "$current")"
    return 0
  done
}

root_device_number="$(mount_field / device)"
root_source="$(mount_field / source)"
root_filesystem="$(mount_field / filesystem)"
[ -n "$root_device_number" ] || {
  echo 'Cannot find the root filesystem in /proc/self/mountinfo.' >&2
  exit 20
}
root_disk="$(disk_for_device_number "$root_device_number")" || {
  echo 'Cannot resolve the root filesystem to a physical disk.' >&2
  exit 21
}

boot_source=''
boot_disk=''
for mountpoint in /boot/firmware /boot; do
  candidate_device_number="$(mount_field "$mountpoint" device)"
  if [ -n "$candidate_device_number" ]; then
    boot_source="$(mount_field "$mountpoint" source)"
    boot_disk="$(disk_for_device_number "$candidate_device_number" || true)"
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
root_name="${root_disk#/dev/}"
sectors="$(cat "/sys/class/block/$root_name/size" 2>/dev/null || true)"
disk_size="$(awk -v sectors="$sectors" 'BEGIN { if (sectors != "") printf "%.0f", sectors * 512 }')"
logical_sector="$(cat "/sys/class/block/$root_name/queue/logical_block_size" 2>/dev/null || true)"
sudo_ok='false'
if command -v sudo >/dev/null 2>&1; then sudo_ok='true'; fi
direct_access='false'
passwordless_sudo='false'
raw_access='false'
sudo_password_needed='false'
if dd if="$root_disk" of=/dev/null bs=1 count=0 2>/dev/null; then
  direct_access='true'
  raw_access='true'
elif [ "$sudo_ok" = 'true' ] && sudo -n -- dd if="$root_disk" of=/dev/null bs=1 count=0 2>/dev/null; then
  passwordless_sudo='true'
  raw_access='true'
elif [ "$sudo_ok" = 'true' ]; then
  sudo_password_needed='true'
  raw_access='true'
fi

printf 'hostname\t%s\n' "$(clean "$hostname_value")"
printf 'model\t%s\n' "$(clean "$model")"
printf 'os\t%s\n' "$(clean "$os_name")"
printf 'architecture\t%s\n' "$(clean "$(uname -m)")"
printf 'rootSource\t%s\n' "$(clean "$root_source")"
printf 'rootDisk\t%s\n' "$(clean "$root_disk")"
printf 'rootFilesystem\t%s\n' "$(clean "$root_filesystem")"
printf 'bootSource\t%s\n' "$(clean "$boot_source")"
printf 'bootDisk\t%s\n' "$(clean "$boot_disk")"
printf 'diskSize\t%s\n' "$(clean "$disk_size")"
printf 'logicalSectorSize\t%s\n' "$(clean "$logical_sector")"
printf 'sudoAvailable\t%s\n' "$sudo_ok"
printf 'directDiskAccess\t%s\n' "$direct_access"
printf 'passwordlessSudo\t%s\n' "$passwordless_sudo"
printf 'sudoPasswordNeeded\t%s\n' "$sudo_password_needed"
printf 'rawAccess\t%s\n' "$raw_access"
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
		Hostname:           values["hostname"],
		Model:              values["model"],
		OS:                 values["os"],
		Architecture:       values["architecture"],
		RootSource:         values["rootSource"],
		RootDisk:           values["rootDisk"],
		RootFilesystem:     values["rootFilesystem"],
		BootSource:         values["bootSource"],
		BootDisk:           values["bootDisk"],
		DiskSize:           diskSize,
		LogicalSectorSize:  sectorSize,
		SudoAvailable:      values["sudoAvailable"] == "true",
		DirectDiskAccess:   values["directDiskAccess"] == "true",
		PasswordlessSudo:   values["passwordlessSudo"] == "true",
		SudoPasswordNeeded: values["sudoPasswordNeeded"] == "true",
		Warnings:           []string{},
	}
	if info.BootDisk != "" && info.BootDisk != info.RootDisk {
		info.Warnings = append(info.Warnings, fmt.Sprintf("Hybrid boot detected: boot is on %s, root is on %s.", info.BootDisk, info.RootDisk))
	}
	if values["rawAccess"] != "true" {
		info.Warnings = append(info.Warnings, "The SSH account cannot read the source disk and sudo is unavailable.")
	}
	info.Supported = len(info.Warnings) == 0
	return info, nil
}

func (r *SSHRemote) Inspect(ctx context.Context, connection Connection) (PiInfo, error) {
	kernel, err := r.run(ctx, connection, "uname -s", nil)
	if err != nil {
		return PiInfo{}, err
	}
	if strings.TrimSpace(kernel.Stdout) != "Linux" {
		return PiInfo{}, NewError("UNSUPPORTED_REMOTE_OS", "Malina remote backup currently supports Linux; the connected system reported "+strings.TrimSpace(kernel.Stdout)+".")
	}
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
	if !info.Supported {
		return NewError("RAW_ACCESS_UNAVAILABLE", "The remote account needs permission to read the source disk or access to sudo.")
	}
	return nil
}
