package core

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

func (e *Engine) Restore(ctx context.Context, request RestoreRequest, onProgress ProgressFunc) (RestoreResult, error) {
	return e.restore(ctx, request, onProgress, false)
}

func (e *Engine) restore(ctx context.Context, request RestoreRequest, onProgress ProgressFunc, allowFileTarget bool) (RestoreResult, error) {
	if request.Device == "" {
		return RestoreResult{}, NewError("DEVICE_REQUIRED", "Choose a destination device.")
	}
	if request.Confirm != request.Device {
		return RestoreResult{}, NewError("CONFIRMATION_REQUIRED", fmt.Sprintf("Type the exact destination device (%s) to confirm erasure.", request.Device))
	}

	emit(onProgress, Progress{Phase: "preflight", Message: "Verifying backup before erasing the destination"})
	_, err := e.Verify(ctx, request.BackupPath, func(progress Progress) {
		progress.Phase = "preflight"
		emit(onProgress, progress)
	})
	if err != nil {
		return RestoreResult{}, err
	}
	backup, err := LoadBackup(request.BackupPath)
	if err != nil {
		return RestoreResult{}, err
	}
	emit(onProgress, Progress{
		Phase:       "prepare",
		Message:     "Preparing the restore pipeline",
		TotalBytes:  backup.Manifest.Image.RawBytes,
		Source:      filepath.Join(backup.Path, backup.Manifest.Image.File),
		Destination: request.Device,
	})

	writePath := request.Device
	devices := e.Devices
	if devices == nil {
		devices = NewDeviceManager(runtime.GOOS)
	}
	if !allowFileTarget {
		available, err := devices.List(ctx)
		if err != nil {
			return RestoreResult{}, err
		}
		var target *Device
		for index := range available {
			if available[index].ID == request.Device || available[index].Path == request.Device {
				target = &available[index]
				break
			}
		}
		if target == nil {
			return RestoreResult{}, NewError("UNSAFE_DESTINATION", "The destination is not in the current removable-device list. Refresh devices and try again.")
		}
		if target.Bytes < backup.Manifest.Image.RawBytes {
			return RestoreResult{}, NewError("DESTINATION_TOO_SMALL", fmt.Sprintf("The destination has %d bytes but the image needs %d.", target.Bytes, backup.Manifest.Image.RawBytes))
		}
		emit(onProgress, Progress{Phase: "unmount", Message: "Unmounting " + request.Device})
		if err := devices.Unmount(ctx, request.Device); err != nil {
			return RestoreResult{}, err
		}
		writePath, err = devices.WritablePath(target.Path)
		if err != nil {
			return RestoreResult{}, err
		}
	}

	imageFile, err := os.Open(filepath.Join(backup.Path, backup.Manifest.Image.File))
	if err != nil {
		return RestoreResult{}, WrapError("RESTORE_FAILED", "Cannot open backup image: "+err.Error(), err)
	}
	defer imageFile.Close()
	zipReader, err := gzip.NewReader(imageFile)
	if err != nil {
		return RestoreResult{}, WrapError("RESTORE_FAILED", "Cannot decompress backup image: "+err.Error(), err)
	}
	defer zipReader.Close()

	flags := os.O_RDWR
	if allowFileTarget {
		flags |= os.O_CREATE | os.O_TRUNC
	}
	targetFile, err := os.OpenFile(writePath, flags, 0o600)
	if err != nil {
		return RestoreResult{}, WrapError("DEVICE_OPEN_FAILED", fmt.Sprintf("Cannot open %s for writing: %s. Run Malina with administrator privileges.", writePath, err), err)
	}

	rawHash := sha256.New()
	reader := &countingReader{
		ctx:        ctx,
		reader:     zipReader,
		hash:       rawHash,
		total:      backup.Manifest.Image.RawBytes,
		phase:      "restore",
		message:    "Writing " + request.Device,
		onProgress: onProgress,
	}
	_, copyErr := io.Copy(targetFile, reader)
	reader.finish()
	syncErr := targetFile.Sync()
	closeErr := targetFile.Close()
	if copyErr != nil {
		return RestoreResult{}, WrapError("RESTORE_FAILED", "Writing the destination failed: "+copyErr.Error(), copyErr)
	}
	if syncErr != nil || closeErr != nil {
		return RestoreResult{}, WrapError("RESTORE_FAILED", "Finalising the destination failed", fmt.Errorf("sync: %v; close: %v", syncErr, closeErr))
	}
	actualHash := hex.EncodeToString(rawHash.Sum(nil))
	if reader.read != backup.Manifest.Image.RawBytes || actualHash != backup.Manifest.Image.SHA256 {
		return RestoreResult{}, NewError("RESTORE_FAILED", "The bytes written did not match the backup manifest.")
	}

	if request.Verify {
		readbackHash, bytesRead, err := hashDevice(ctx, writePath, backup.Manifest.Image.RawBytes, onProgress)
		if err != nil {
			return RestoreResult{}, err
		}
		if bytesRead != backup.Manifest.Image.RawBytes || readbackHash != backup.Manifest.Image.SHA256 {
			return RestoreResult{}, NewError("RESTORE_VERIFY_FAILED", "The destination checksum does not match the backup.")
		}
	}

	ejected := false
	if !allowFileTarget {
		ejected = devices.Eject(ctx, request.Device)
	}
	emit(onProgress, Progress{Phase: "complete", Message: "Restore complete", Fraction: 1})
	return RestoreResult{
		Device:       request.Device,
		BytesWritten: reader.read,
		Verified:     request.Verify,
		Ejected:      ejected,
		Manifest:     backup.Manifest,
	}, nil
}

func hashDevice(ctx context.Context, path string, expectedBytes int64, onProgress ProgressFunc) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, WrapError("RESTORE_VERIFY_FAILED", "Cannot reopen destination: "+err.Error(), err)
	}
	defer file.Close()
	hash := sha256.New()
	reader := &countingReader{
		ctx:        ctx,
		reader:     io.LimitReader(file, expectedBytes),
		hash:       hash,
		total:      expectedBytes,
		phase:      "readback",
		message:    "Verifying written data",
		onProgress: onProgress,
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return "", reader.read, WrapError("RESTORE_VERIFY_FAILED", "Reading destination failed: "+err.Error(), err)
	}
	reader.finish()
	return hex.EncodeToString(hash.Sum(nil)), reader.read, nil
}
