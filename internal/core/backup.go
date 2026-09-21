package core

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Backup streams, compresses, hashes, and atomically commits one whole-disk backup.
func (e *Engine) Backup(ctx context.Context, request BackupRequest, onProgress ProgressFunc) (result Backup, resultErr error) {
	if request.OutputDirectory == "" {
		return Backup{}, NewError("OUTPUT_REQUIRED", "Choose a directory in which to store the backup.")
	}
	remote := e.Remote
	if remote == nil {
		remote = NewSSHRemote()
	}

	emit(onProgress, Progress{Phase: "inspect", Message: "Inspecting Raspberry Pi"})
	info, err := remote.Inspect(ctx, request.Connection)
	if err != nil {
		return Backup{}, err
	}
	if err := assertBackupSupported(info); err != nil {
		return Backup{}, err
	}

	startedAt := e.clock().UTC()
	partialPath, finalPath, err := prepareAtomicDirectory(request.OutputDirectory, backupName(info.Hostname, startedAt))
	if err != nil {
		return Backup{}, err
	}
	defer func() {
		if resultErr != nil {
			_ = os.RemoveAll(partialPath)
		}
	}()
	emit(onProgress, Progress{
		Phase:       "prepare",
		Message:     "Preparing the live image pipeline",
		TotalBytes:  info.DiskSize,
		Source:      info.RootDisk,
		Destination: filepath.Join(finalPath, ImageFilename),
	})

	emit(onProgress, Progress{Phase: "sync", Message: "Flushing filesystem writes on the Pi"})
	if err := remote.Sync(ctx, request.Connection); err != nil {
		return Backup{}, err
	}

	stream, err := remote.OpenDisk(ctx, request.Connection, info.RootDisk)
	if err != nil {
		return Backup{}, err
	}
	defer stream.Close()

	imagePath := filepath.Join(partialPath, ImageFilename)
	imageFile, err := os.OpenFile(imagePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Backup{}, WrapError("BACKUP_FAILED", "Cannot create backup image: "+err.Error(), err)
	}

	rawHash := sha256.New()
	compressedHash := sha256.New()
	reader := &countingReader{
		ctx:        ctx,
		reader:     stream,
		hash:       rawHash,
		total:      info.DiskSize,
		phase:      "backup",
		message:    "Reading and compressing the disk",
		onProgress: onProgress,
	}
	zipWriter, err := gzip.NewWriterLevel(io.MultiWriter(imageFile, compressedHash), gzip.BestSpeed)
	if err != nil {
		_ = imageFile.Close()
		return Backup{}, WrapError("BACKUP_FAILED", "Cannot initialise compression: "+err.Error(), err)
	}

	_, copyErr := io.Copy(zipWriter, reader)
	reader.finish()
	closeZipErr := zipWriter.Close()
	syncErr := imageFile.Sync()
	closeFileErr := imageFile.Close()
	if copyErr != nil {
		return Backup{}, WrapError("BACKUP_FAILED", "Reading the remote disk failed: "+copyErr.Error(), copyErr)
	}
	if closeZipErr != nil || syncErr != nil || closeFileErr != nil {
		return Backup{}, WrapError("BACKUP_FAILED", "Finalising the backup image failed", errors.Join(closeZipErr, syncErr, closeFileErr))
	}
	if err := stream.Wait(); err != nil {
		return Backup{}, err
	}
	if reader.read != info.DiskSize {
		return Backup{}, NewError("INCOMPLETE_IMAGE", fmt.Sprintf("Expected %d bytes from %s, but received %d.", info.DiskSize, info.RootDisk, reader.read))
	}
	emit(onProgress, Progress{
		Phase:       "finalize",
		Message:     "Finalising checksums and backup metadata",
		Bytes:       reader.read,
		TotalBytes:  info.DiskSize,
		Fraction:    1,
		Source:      info.RootDisk,
		Destination: filepath.Join(finalPath, ImageFilename),
	})

	imageStats, err := os.Stat(imagePath)
	if err != nil {
		return Backup{}, WrapError("BACKUP_FAILED", "Cannot inspect the backup image: "+err.Error(), err)
	}
	manifest := Manifest{
		Format:        BackupFormat,
		FormatVersion: BackupFormatVersion,
		Status:        "complete",
		Consistency:   "live",
		CreatedAt:     startedAt,
		CompletedAt:   e.clock().UTC(),
		Source: ManifestSource{
			Host:              request.Connection.Host,
			Hostname:          info.Hostname,
			Model:             info.Model,
			OS:                info.OS,
			Architecture:      info.Architecture,
			Device:            info.RootDisk,
			Bytes:             info.DiskSize,
			LogicalSectorSize: info.LogicalSectorSize,
			RootFilesystem:    info.RootFilesystem,
		},
		Image: ManifestImage{
			File:             ImageFilename,
			Compression:      "gzip",
			RawBytes:         reader.read,
			CompressedBytes:  imageStats.Size(),
			SHA256:           hex.EncodeToString(rawHash.Sum(nil)),
			CompressedSHA256: hex.EncodeToString(compressedHash.Sum(nil)),
		},
		Warning: "This image was captured from a running system and is not an atomic snapshot.",
	}
	if err := writeJSON(filepath.Join(partialPath, "manifest.json"), manifest); err != nil {
		return Backup{}, WrapError("BACKUP_FAILED", "Cannot write backup manifest: "+err.Error(), err)
	}
	if err := os.Rename(partialPath, finalPath); err != nil {
		return Backup{}, WrapError("BACKUP_FAILED", "Cannot commit backup: "+err.Error(), err)
	}

	emit(onProgress, Progress{Phase: "complete", Message: "Backup complete", Fraction: 1})
	return Backup{Path: finalPath, Manifest: manifest}, nil
}

func emit(callback ProgressFunc, progress Progress) {
	if callback != nil {
		callback(progress)
	}
}
