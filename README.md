# Malina

Malina makes compressed, whole-device backups of a running Raspberry Pi over SSH and restores them
to removable media. The native desktop GUI and CLI use the same Go engine.

The current release intentionally has one consistency mode: **live**. It runs `sync`, then reads the
physical disk while Raspberry Pi OS remains online. This is convenient, but it is not an atomic
snapshot—files can change during capture.

## What is included

- Native Wails desktop app for macOS, Windows, and Linux
- Go CLI for scripting and headless use
- Native file and folder pickers for keys, backup locations, and restore images
- Remote Pi inspection over OpenSSH
- SSH key/agent authentication or an in-memory password
- Background job queue with pause, resume, cancel, and live details for concurrent backup and
  restore work
- Full raw-device streaming with `dd`, gzip compression, and SHA-256 checksums
- Backup verification before any restore destination is opened
- Removable-device discovery and exact-path erase confirmation
- Optional full read-back verification after restore

A raw image includes the partition table, boot sectors, boot partition, root partition, and
unallocated gaps. Malina rejects a hybrid layout when boot and root live on different physical disks
because one image would not contain the whole bootable system. Raspberry Pi EEPROM bootloader
configuration is not stored on the disk and is therefore not included.

## Requirements

Development computer:

- Go 1.25 or newer
- Node.js 20.19 or newer and pnpm
- Wails v3 CLI (the project is pinned to `v3.0.0-beta.23`)
- Platform build tools required by Wails
- OpenSSH client available as `ssh`

Raspberry Pi:

- SSH enabled, with either key or password authentication
- `findmnt`, `lsblk`, `dd`, and `sync`
- Passwordless `sudo`, or direct root SSH access

Key authentication uses the system OpenSSH client in batch mode. Password authentication uses Go's
SSH client; the password remains in memory only for the active operation and is never written to
settings, manifests, or logs. Malina remembers first-seen host keys in its user configuration
directory and rejects changed keys.

## Develop and build

```sh
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.23
pnpm --dir frontend install
wails3 dev
```

Build the desktop app and CLI:

```sh
wails3 build
wails3 task cli:build
```

Outputs are written to `bin/`. Run all checks with:

```sh
wails3 task test
```

## CLI examples

```sh
# Inspect the Pi and its boot/root disk layout
malina inspect --host pi@raspberrypi.local

# Or prompt securely for an SSH password
malina inspect --host pi@raspberrypi.local --password

# Create a live raw-image backup
malina backup \
  --host pi@raspberrypi.local \
  --identity ~/.ssh/id_ed25519 \
  --output "$HOME/Malina Backups"

# List and verify backups
malina backups --output "$HOME/Malina Backups"
malina verify "$HOME/Malina Backups/pi-20260920T120000Z"

# Discover safe restore destinations
malina devices

# Restore (requires administrator/root access)
sudo malina restore "$HOME/Malina Backups/pi-20260920T120000Z" \
  --device /dev/disk4 \
  --confirm /dev/disk4
```

Linux device paths usually look like `/dev/sdb`; Windows paths look like `\\.\PhysicalDrive2`. Use
only paths returned by `malina devices` or the GUI. Read-back verification is enabled by default and
can be disabled with `--no-verify`.

## Backup layout

```text
pi-20260920T120000Z/
├── disk.img.gz
└── manifest.json
```

The manifest contains the source details, image sizes, timestamps, and both raw and compressed
SHA-256 checksums required to validate and restore the image.

The destination must be at least as large as the original device. Space beyond the original image
remains unallocated.

## Safety boundaries

- Only complete whole-disk images are accepted.
- Internal/system disks are excluded from destination discovery.
- An arbitrary path cannot bypass removable-device discovery.
- The backup is fully verified before the destination is opened for writing.
- A dedicated confirmation dialog shows the exact image and destination before erasure; the backend
  still requires a matching target identifier.
- Failed captures leave no partial backup directory.

Incremental backups, schedules, cloud storage, image shrinking, selective restore, filesystem
freezing, and multi-device boot layouts are outside the initial scope.
