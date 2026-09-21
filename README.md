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
- Remote Pi inspection over Malina's bundled SSH client
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

## Third-party work

Besides the whole tech stack, Malina uses the following additional third-party work:

* The app logo: [Fruit icons created by Magnific](https://www.flaticon.com/free-icons/fruit)
* The colour palette: [Enchanted Cherry Forest](https://coolors.co/palette/eaf2ef-912f56-521945-361f27-0d090a)

## Requirements

Development computer:

- Go 1.25 or newer
- Node.js 20.19 or newer and pnpm
- Wails v3 CLI (the project is pinned to `v3.0.0-beta.23`)
- Platform build tools required by Wails

Raspberry Pi:

- SSH enabled, with either key or password authentication
- Linux with `/proc` and `/sys` mounted
- `dd` supporting POSIX operands, plus `sync`
- Direct permission to read the source disk, or `sudo` access

Password, private-key, and SSH-agent authentication use Malina's bundled Go SSH client. Disk access
is attempted in this order: direct read, passwordless `sudo`, then password-backed `sudo`. With SSH
password authentication, the connection password is reused. With key or agent authentication, the
app asks for the Pi account password only if the first two access methods fail. If all three methods
fail, the backup stops with the remote error. Passwords remain in memory only for the active
operation and are never written to settings, manifests, or logs. Malina remembers first-seen host
keys in its user configuration directory and rejects changed keys.

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

Development builds use `bin/malina-dev` (or `bin/malina-dev.exe` on Windows), so running
`wails3 dev` does not replace or collide with a universal/release binary in `bin/malina`.

```sh
wails3 task test
```

The component boundaries, destructive-operation invariants, backup commit protocol, and background
job lifecycle are documented in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Releases

Changes are recorded in [CHANGELOG.md](CHANGELOG.md). Pull requests and updates to `main` run the
Go and frontend checks automatically. Pushing a stable semantic-version tag such as `v0.1.0`
builds Linux amd64, macOS universal, and Windows amd64 archives, generates SHA-256 checksums, and
publishes them to a GitHub Release.

CI verifies that the application metadata uses one consistent semantic version and that the
changelog contains its matching release section. The tag workflow assumes the tag is created from a
green commit already merged to `main`, so it only builds, packages, checksums, and publishes. The
current macOS app is ad-hoc signed and the Windows binary is unsigned. Platform signing and
notarisation steps still need to be added, along with their secrets, before either build will be
trusted automatically by the operating system.

## CLI examples

```sh
# Inspect the Pi and its boot/root disk layout
malina inspect --host user@host

# Or prompt securely for an SSH password
malina inspect --host user@host --password

# Create a live raw-image backup
malina backup \
  --host user@host \
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
- Stable hardware/media identifiers are preferred and revalidated immediately before a restore.
- Concurrent restores cannot reserve the same physical destination.
- An arbitrary path cannot bypass removable-device discovery.
- The backup is fully verified before the destination is opened for writing.
- A dedicated confirmation dialog shows the exact image and destination before erasure; the backend
  still requires a matching target identifier.
- Failed captures leave no partial backup directory.

Incremental backups, schedules, cloud storage, image shrinking, selective restore, filesystem
freezing, and multi-device boot layouts are outside the initial scope.
