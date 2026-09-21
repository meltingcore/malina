# Changelog

## [Unreleased]

### Security and reliability

- Reserve restore targets atomically and revalidate physical media after unmounting.
- Preserve unknown partial backup directories and allocate collision-free concurrent backup paths.
- Prefer stable hardware identifiers, exclude fixed MMC/eMMC restore targets, and serialize SSH
  host-key pinning.
- Gate releases on tests and publish only from matching semantic-version tags.

### Quality

- Add race-focused Go regressions, frontend unit tests, architecture documentation, responsive window
  controls, keyboard tab navigation, modal focus trapping, and more readable operational text.

## [0.1.0]

### Features

- Native desktop application for macOS, Windows, and Linux, plus a command-line interface.
- Live, compressed whole-device Raspberry Pi backups over the bundled SSH client.
- Password and key SSH authentication.
- Linux source-disk discovery through `/proc` and `/sys`.
- POSIX-compatible remote `dd` streaming.
- SHA-256 backup manifests, backup verification, and optional full-device read-back verification.
- Removable-device discovery and guarded restore workflows.
- Background jobs with progress reporting, pause, resume, cancellation, and automatic cleanup.
