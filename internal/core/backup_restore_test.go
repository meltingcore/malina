package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type memoryDiskStream struct {
	io.ReadCloser
	waitErr error
}

func (s *memoryDiskStream) Wait() error { return s.waitErr }

type fakeRemote struct {
	info    PiInfo
	data    []byte
	waitErr error
	syncErr error
}

type changingDeviceOperations struct {
	before  Device
	after   Device
	lists   int
	ejected string
}

func (d *changingDeviceOperations) List(context.Context) ([]Device, error) {
	d.lists++
	if d.lists == 1 {
		return []Device{d.before}, nil
	}
	return []Device{d.after}, nil
}

func (*changingDeviceOperations) Unmount(context.Context, string) error { return nil }
func (*changingDeviceOperations) WritablePath(device string) (string, error) {
	return device, nil
}
func (d *changingDeviceOperations) Eject(_ context.Context, device string) bool {
	d.ejected = device
	return true
}

func (r *fakeRemote) Inspect(context.Context, Connection) (PiInfo, error) { return r.info, nil }
func (r *fakeRemote) Sync(context.Context, Connection) error              { return r.syncErr }
func (r *fakeRemote) OpenDisk(context.Context, Connection, string) (DiskStream, error) {
	return &memoryDiskStream{ReadCloser: io.NopCloser(bytes.NewReader(r.data)), waitErr: r.waitErr}, nil
}

func testEngine(data []byte) *Engine {
	return &Engine{
		Remote: &fakeRemote{
			data: data,
			info: PiInfo{
				Hostname: "pi-test", Model: "Raspberry Pi 5", OS: "Raspberry Pi OS",
				Architecture: "aarch64", RootDisk: "/dev/mmcblk0", RootFilesystem: "ext4",
				DiskSize: int64(len(data)), LogicalSectorSize: 512, SudoAvailable: true, Supported: true,
			},
		},
		Now: func() time.Time { return time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC) },
	}
}

func TestBackupVerifyAndRestoreRoundTrip(t *testing.T) {
	raw := bytes.Repeat([]byte("malina-raw-disk\x00"), 4096)
	engine := testEngine(raw)
	backup, err := engine.Backup(context.Background(), BackupRequest{
		Connection: Connection{Host: "user@host"}, OutputDirectory: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if backup.Manifest.Consistency != "live" || backup.Manifest.Image.RawBytes != int64(len(raw)) {
		t.Fatalf("unexpected manifest: %#v", backup.Manifest)
	}
	entries, err := os.ReadDir(backup.Path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("backup must contain only disk.img.gz and manifest.json; found %v", entries)
	}
	for _, name := range []string{"disk.img.gz", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(backup.Path, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}

	verified, err := engine.Verify(context.Background(), backup.Path, nil)
	if err != nil || !verified.Valid {
		t.Fatalf("verification failed: %#v, %v", verified, err)
	}

	destination := filepath.Join(t.TempDir(), "restored.img")
	restored, err := engine.restore(context.Background(), RestoreRequest{
		BackupPath: backup.Path, Device: destination, Confirm: destination, Verify: true,
	}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, raw) || !restored.Verified || restored.BytesWritten != int64(len(raw)) {
		t.Fatalf("round trip mismatch: %#v", restored)
	}
}

func TestIncompleteBackupRemovesPartialDirectory(t *testing.T) {
	engine := testEngine([]byte("too short"))
	engine.Remote.(*fakeRemote).info.DiskSize++
	parent := t.TempDir()
	_, err := engine.Backup(context.Background(), BackupRequest{
		Connection: Connection{Host: "user@host"}, OutputDirectory: parent,
	}, nil)
	assertErrorCode(t, err, "INCOMPLETE_IMAGE")
	entries, readErr := os.ReadDir(parent)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed backup left files behind: %v", entries)
	}
}

func TestCancelledBackupRemovesPartialDirectory(t *testing.T) {
	engine := testEngine(bytes.Repeat([]byte("cancelled image"), 1024))
	parent := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := engine.Backup(ctx, BackupRequest{
		Connection: Connection{Host: "user@host"}, OutputDirectory: parent,
	}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	entries, readErr := os.ReadDir(parent)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("cancelled backup left files behind: %v", entries)
	}
}

func TestRestoreVerifiesBeforeOpeningDestination(t *testing.T) {
	engine := testEngine([]byte("valid image content"))
	backup, err := engine.Backup(context.Background(), BackupRequest{
		Connection: Connection{Host: "user@host"}, OutputDirectory: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(backup.Path, ImageFilename)
	file, err := os.OpenFile(imagePath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("tampered"))
	_ = file.Close()

	destination := filepath.Join(t.TempDir(), "destination.img")
	const sentinel = "do not erase"
	if err := os.WriteFile(destination, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = engine.restore(context.Background(), RestoreRequest{
		BackupPath: backup.Path, Device: destination, Confirm: destination,
	}, nil, true)
	assertErrorCode(t, err, "CHECKSUM_MISMATCH")
	contents, readErr := os.ReadFile(destination)
	if readErr != nil || string(contents) != sentinel {
		t.Fatalf("destination changed before verification completed: %q, %v", contents, readErr)
	}
}

func TestBackupPropagatesRemoteWaitFailure(t *testing.T) {
	engine := testEngine([]byte("disk"))
	engine.Remote.(*fakeRemote).waitErr = errors.New("ssh stopped")
	_, err := engine.Backup(context.Background(), BackupRequest{
		Connection: Connection{Host: "user@host"}, OutputDirectory: t.TempDir(),
	}, nil)
	if err == nil || err.Error() != "ssh stopped" {
		t.Fatalf("expected wait error, got %v", err)
	}
}

func TestRestoreRequiresExactConfirmation(t *testing.T) {
	engine := testEngine([]byte("disk"))
	_, err := engine.restore(context.Background(), RestoreRequest{
		BackupPath: "unused", Device: "/dev/disk4", Confirm: "/dev/disk5",
	}, nil, true)
	assertErrorCode(t, err, "CONFIRMATION_REQUIRED")
}

func TestRestoreStopsWhenConfirmedDeviceChanges(t *testing.T) {
	engine := testEngine([]byte("valid image content"))
	backup, err := engine.Backup(context.Background(), BackupRequest{
		Connection: Connection{Host: "user@host"}, OutputDirectory: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	devices := &changingDeviceOperations{
		before: Device{ID: "linux:original", Path: "/dev/sdb", Name: "Original", Bytes: 1 << 20, Transport: "usb", Removable: true, Stable: true},
		after:  Device{ID: "linux:replacement", Path: "/dev/sdb", Name: "Replacement", Bytes: 1 << 20, Transport: "usb", Removable: true, Stable: true},
	}
	engine.Devices = devices
	_, err = engine.Restore(context.Background(), RestoreRequest{
		BackupPath: backup.Path, Device: devices.before.ID, Confirm: devices.before.ID,
	}, nil)
	assertErrorCode(t, err, "DEVICE_CHANGED")
}

func TestRestoreUsesPathForStableDeviceWriteAndEject(t *testing.T) {
	raw := []byte("valid image content")
	engine := testEngine(raw)
	backup, err := engine.Backup(context.Background(), BackupRequest{
		Connection: Connection{Host: "user@host"}, OutputDirectory: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(t.TempDir(), "target.img")
	if err := os.WriteFile(targetPath, make([]byte, len(raw)), 0o600); err != nil {
		t.Fatal(err)
	}
	target := Device{ID: "test:stable-device", Path: targetPath, Name: "Stable target", Bytes: int64(len(raw)), Transport: "usb", Removable: true, Stable: true}
	devices := &changingDeviceOperations{before: target, after: target}
	engine.Devices = devices
	result, err := engine.Restore(context.Background(), RestoreRequest{
		BackupPath: backup.Path, Device: target.ID, Confirm: target.ID, Verify: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Device != targetPath || devices.ejected != targetPath {
		t.Fatalf("stable ID leaked into path operation: result=%q eject=%q", result.Device, devices.ejected)
	}
}

func TestLoadBackupRejectsUnsafeImageMetadata(t *testing.T) {
	engine := testEngine([]byte("disk image"))
	backup, err := engine.Backup(context.Background(), BackupRequest{
		Connection: Connection{Host: "user@host"}, OutputDirectory: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	backup.Manifest.Image.File = "../outside.img.gz"
	if err := writeJSON(filepath.Join(backup.Path, "manifest.json"), backup.Manifest); err != nil {
		t.Fatal(err)
	}
	_, err = LoadBackup(backup.Path)
	assertErrorCode(t, err, "UNSUPPORTED_BACKUP")
}

func TestBackupNeverPersistsSSHPassword(t *testing.T) {
	engine := testEngine([]byte("disk image"))
	backup, err := engine.Backup(context.Background(), BackupRequest{
		Connection:      Connection{Host: "user@host", Password: "never-write-this"},
		OutputDirectory: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(backup.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(manifest, []byte("never-write-this")) {
		t.Fatal("SSH password was persisted in the backup manifest")
	}
	entries, err := os.ReadDir(backup.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, readErr := os.ReadFile(filepath.Join(backup.Path, entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if bytes.Contains(data, []byte("never-write-this")) {
			t.Fatalf("SSH password was persisted in %s", entry.Name())
		}
	}
}

func TestConcurrentBackupsClaimDifferentDirectories(t *testing.T) {
	raw := bytes.Repeat([]byte("concurrent backup"), 4096)
	engine := testEngine(raw)
	parent := t.TempDir()
	start := make(chan struct{})
	results := make(chan Backup, 2)
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			backup, err := engine.Backup(context.Background(), BackupRequest{
				Connection: Connection{Host: "user@host"}, OutputDirectory: parent,
			}, nil)
			results <- backup
			errors <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	paths := map[string]bool{}
	for backup := range results {
		paths[backup.Path] = true
		if _, err := engine.Verify(context.Background(), backup.Path, nil); err != nil {
			t.Fatalf("concurrent backup did not verify: %v", err)
		}
	}
	if len(paths) != 2 {
		t.Fatalf("concurrent backups shared a path: %#v", paths)
	}
}

func TestBackupDoesNotDeleteUnknownPartialDirectory(t *testing.T) {
	engine := testEngine([]byte("disk image"))
	parent := t.TempDir()
	base := backupName("pi-test", engine.clock())
	stale := filepath.Join(parent, base+".partial")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(stale, "keep-me")
	if err := os.WriteFile(marker, []byte("owned by another run"), 0o600); err != nil {
		t.Fatal(err)
	}

	backup, err := engine.Backup(context.Background(), BackupRequest{
		Connection: Connection{Host: "user@host"}, OutputDirectory: parent,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if backup.Path == filepath.Join(parent, base) {
		t.Fatal("backup reused a name with an existing partial directory")
	}
	if contents, err := os.ReadFile(marker); err != nil || string(contents) != "owned by another run" {
		t.Fatalf("existing partial data was changed: %q, %v", contents, err)
	}
}
