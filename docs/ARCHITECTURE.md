# Malina architecture and safety model

Malina has two entry points—a Wails desktop application and a Go CLI—over one `internal/core`
engine. The desktop layer owns native dialogs and background-job presentation; it must not duplicate
backup, verification, device-discovery, or restore safety rules.

## Components

- `internal/core`: SSH inspection and streaming, manifest validation, checksums, device discovery,
  backup creation, and restore execution.
- `internal/desktop`: Wails service, background jobs, pause/resume/cancel state, and restore-target
  reservations.
- `cmd/malina`: non-interactive CLI adapter over the core engine.
- `frontend`: native-webview interface. Generated Wails bindings live under `frontend/bindings` and
  are regenerated with `wails3 generate bindings -ts ./...`.

## Safety invariants

1. A restore image is fully verified before a destination is opened for writing.
2. A destination must come from current platform device discovery; arbitrary files and partitions
   are rejected by production restore paths.
3. The user confirmation value must exactly match the selected device identifier.
4. Only one active restore may reserve a physical device identifier.
5. Device identity, capacity, and path are rechecked after unmounting and immediately before open.
6. The system/root disk is excluded, fixed MMC/eMMC is not treated as removable, and ambiguous
   discovery failures stop the operation.
7. Backup directories are claimed atomically. A process only removes the partial directory it
   successfully created; stale or concurrently owned partials are preserved.
8. Passwords must not appear in manifests, configuration files, events, logs, or error text.
   Operation credentials remain temporary; desktop profiles may explicitly remember SSH passwords
   in the OS credential store. Separately requested sudo passwords are never persisted.
9. First-seen SSH host keys are pinned atomically. A changed or concurrent conflicting key is
   rejected.

Hardware serials, WWNs, and media UUIDs are preferred for device identity. Some media does not
expose a stable identifier; in that case Malina uses the device path and requires its size, model,
and transport to remain unchanged during the final revalidation window.

## Desktop device profiles

The desktop service owns profiles in `~/.malina.json` (`%USERPROFILE%\.malina.json` on Windows).
The versioned file stores stable device IDs, display names, explicit SSH users/hosts/ports,
authentication methods, key paths, local backup folders, and the last selected device ID.
Private-key contents and passwords never enter this file. The core engine and CLI do not read it.

Profile operations are serialized, reload and validate the entire file before changing it, and
write via a user-only temporary file, flush, and atomic rename. Malformed, unknown-version, or
unsupported configurations are preserved. Loading does not read credentials or contact a host.

Optional SSH passwords live in the OS credential store under opaque references. Retrieval is
bound to the profile's username, host, port, and password authentication method. An edited account
cannot reuse the old password. Credential replacements and profile copies use independent entries;
failed file writes remove new entries and attempt to restore any replaced credential. Disabling
remembering or deleting a profile removes its credential; a locked store fails the change so the
reference remains available for retry. Errors shown to the frontend omit provider error details.

The UI retains a saved baseline to identify unsaved edits and guards profile switching. Jobs receive
a copy of their settings and display name when submitted and never consult the profile afterward.
Restore-image and physical-target selections are independent of device-profile selection.

## Backup commit protocol

A backup streams into an atomically claimed `<name>.partial` directory. The compressed and raw
hashes are calculated during the stream, the file is flushed, the remote command must exit
successfully, the byte count must equal the inspected disk size, and the manifest is written last.
Only then is the directory renamed to its final name. Failure removes only that operation's claimed
partial directory.

## Job lifecycle

Desktop jobs use these states:

```text
running <-> paused
   |
   +------> cancelling -> cancelled
   |
   +-------------------> completed
   |
   +-------------------> failed
```

Pause is cooperative and takes effect at stream read boundaries. Cancellation of a restore can
leave incomplete media; the UI must continue to communicate that explicitly.

## Verification

The standard local quality command is:

```sh
wails3 task test
```

CI additionally runs Go race detection, `go vet`, a coverage floor, frontend unit tests, TypeScript
checking, and a production frontend build. Release publication is tag-driven and repeats the
release-candidate checks before building artifacts.
