package desktop

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/meltingcore/malina/internal/core"
)

type testDiskStream struct {
	io.ReadCloser
}

func (s *testDiskStream) Wait() error { return nil }

type testRemote struct {
	data  []byte
	block bool
}

func (r *testRemote) Inspect(context.Context, core.Connection) (core.PiInfo, error) {
	return core.PiInfo{
		Hostname: "pi-test", Model: "Raspberry Pi", OS: "Raspberry Pi OS", Architecture: "aarch64",
		RootDisk: "/dev/mmcblk0", RootFilesystem: "ext4", DiskSize: int64(len(r.data)),
		LogicalSectorSize: 512, SudoAvailable: true, Supported: true,
	}, nil
}

func (r *testRemote) Sync(context.Context, core.Connection) error { return nil }

func (r *testRemote) OpenDisk(ctx context.Context, _ core.Connection, _ string) (core.DiskStream, error) {
	if r.block {
		return &testDiskStream{ReadCloser: &blockingReader{ctx: ctx}}, nil
	}
	return &testDiskStream{ReadCloser: io.NopCloser(bytes.NewReader(r.data))}, nil
}

type blockingReader struct {
	ctx context.Context
}

func (r *blockingReader) Read([]byte) (int, error) {
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func (r *blockingReader) Close() error { return nil }

type testDevices struct {
	device core.Device
}

func (d testDevices) List(context.Context) ([]core.Device, error) {
	return []core.Device{d.device}, nil
}
func (d testDevices) Unmount(context.Context, string) error      { return nil }
func (d testDevices) WritablePath(device string) (string, error) { return device, nil }
func (d testDevices) Eject(context.Context, string) bool         { return true }

type blockingRestoreDevices struct {
	device  core.Device
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (d *blockingRestoreDevices) List(context.Context) ([]core.Device, error) {
	return []core.Device{d.device}, nil
}
func (d *blockingRestoreDevices) Unmount(context.Context, string) error {
	d.once.Do(func() { close(d.entered) })
	<-d.release
	return nil
}
func (d *blockingRestoreDevices) WritablePath(string) (string, error) {
	return d.device.Path, nil
}
func (*blockingRestoreDevices) Eject(context.Context, string) bool { return true }

func waitForJob(t *testing.T, service *Service, id string, finished bool) core.Job {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, job := range service.ListJobs() {
			if job.ID == id && (!finished || !jobIsActive(job.Status)) {
				return job
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for job %s", id)
		case <-ticker.C:
		}
	}
}

func TestBackgroundJobsAllowRestoreWhileBackupRuns(t *testing.T) {
	raw := bytes.Repeat([]byte("malina-job-data"), 8192)
	backupEngine := &core.Engine{Remote: &testRemote{data: raw}}
	backup, err := backupEngine.Backup(context.Background(), core.BackupRequest{
		Connection: core.Connection{Host: "user@host"}, OutputDirectory: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(t.TempDir(), "target.img")
	if err := os.WriteFile(target, make([]byte, len(raw)), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewService(nil, &core.Engine{
		Remote:  &testRemote{data: raw, block: true},
		Devices: testDevices{device: core.Device{ID: target, Path: target, Name: "Test drive", Bytes: int64(len(raw)), Removable: true}},
	})

	backupJob, err := service.StartBackup(core.BackupRequest{
		Connection: core.Connection{Host: "user@host"}, OutputDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	restoreJob, err := service.StartRestore(core.RestoreRequest{
		BackupPath: backup.Path, Device: target, Confirm: target, Verify: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	finishedRestore := waitForJob(t, service, restoreJob.ID, true)
	if finishedRestore.Status != jobStatusCompleted {
		t.Fatalf("restore did not complete: %#v", finishedRestore)
	}
	runningBackup := waitForJob(t, service, backupJob.ID, false)
	if !jobIsActive(runningBackup.Status) {
		t.Fatalf("backup unexpectedly stopped while restore ran: %#v", runningBackup)
	}
	if !service.PauseJob(backupJob.ID) || !service.ResumeJob(backupJob.ID) || !service.CancelJob(backupJob.ID) {
		t.Fatal("pause, resume, and cancel should apply to the active backup")
	}
	cancelledBackup := waitForJob(t, service, backupJob.ID, true)
	if cancelledBackup.Status != jobStatusCancelled {
		t.Fatalf("backup was not cancelled: %#v", cancelledBackup)
	}
	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, raw) {
		t.Fatal("restore target did not match the backup")
	}
}

func TestStartRestoreAtomicallyReservesTarget(t *testing.T) {
	raw := bytes.Repeat([]byte("restore-reservation"), 4096)
	backupEngine := &core.Engine{Remote: &testRemote{data: raw}}
	backup, err := backupEngine.Backup(context.Background(), core.BackupRequest{
		Connection: core.Connection{Host: "user@host"}, OutputDirectory: t.TempDir(),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.img")
	if err := os.WriteFile(target, make([]byte, len(raw)), 0o600); err != nil {
		t.Fatal(err)
	}
	devices := &blockingRestoreDevices{
		device:  core.Device{ID: "test:target", Path: target, Name: "Test drive", Bytes: int64(len(raw)), Removable: true, Stable: true},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	service := NewService(nil, &core.Engine{Devices: devices})
	first, err := service.StartRestore(core.RestoreRequest{
		BackupPath: backup.Path, Device: devices.device.ID, Confirm: devices.device.ID, Verify: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	<-devices.entered
	_, err = service.StartRestore(core.RestoreRequest{
		BackupPath: backup.Path, Device: devices.device.Path, Confirm: devices.device.Path, Verify: false,
	})
	if err == nil {
		t.Fatal("second restore unexpectedly acquired the same physical target")
	}
	typed, ok := err.(*core.Error)
	if !ok || typed.Code != "DEVICE_BUSY" {
		t.Fatalf("expected DEVICE_BUSY, got %#v", err)
	}
	close(devices.release)
	finished := waitForJob(t, service, first.ID, true)
	if finished.Status != jobStatusCompleted {
		t.Fatalf("first restore did not complete: %#v", finished)
	}
}
