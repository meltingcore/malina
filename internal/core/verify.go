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
	"regexp"
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// LoadBackup validates manifest structure without reading the image payload.
func LoadBackup(backupPath string) (Backup, error) {
	absolutePath, err := filepath.Abs(backupPath)
	if err != nil {
		return Backup{}, WrapError("INVALID_MANIFEST", "Cannot resolve backup path: "+err.Error(), err)
	}
	var manifest Manifest
	if err := readJSON(filepath.Join(absolutePath, "manifest.json"), &manifest); err != nil {
		return Backup{}, WrapError("INVALID_MANIFEST", "Cannot read backup manifest: "+err.Error(), err)
	}
	if manifest.Format != BackupFormat || manifest.FormatVersion != BackupFormatVersion {
		return Backup{}, NewError("UNSUPPORTED_BACKUP", "This is not a supported Malina backup.")
	}
	if manifest.Status != "complete" {
		return Backup{}, NewError("INCOMPLETE_BACKUP", "The selected backup is not marked complete.")
	}
	if manifest.Consistency != "live" || manifest.Image.Compression != "gzip" || manifest.Image.File != ImageFilename {
		return Backup{}, NewError("UNSUPPORTED_BACKUP", "Only gzip whole-disk backups are supported.")
	}
	if manifest.Image.RawBytes <= 0 || manifest.Image.CompressedBytes <= 0 || manifest.Source.Bytes != manifest.Image.RawBytes {
		return Backup{}, NewError("INVALID_MANIFEST", "The backup manifest contains invalid image sizes.")
	}
	if !sha256Pattern.MatchString(manifest.Image.SHA256) || !sha256Pattern.MatchString(manifest.Image.CompressedSHA256) {
		return Backup{}, NewError("INVALID_MANIFEST", "The backup manifest contains invalid checksums.")
	}
	return Backup{Path: absolutePath, Manifest: manifest}, nil
}

// Verify checks both compressed and raw image hashes and the exact raw byte count.
func (e *Engine) Verify(ctx context.Context, backupPath string, onProgress ProgressFunc) (VerifyResult, error) {
	backup, err := LoadBackup(backupPath)
	if err != nil {
		return VerifyResult{}, err
	}
	imagePath := filepath.Join(backup.Path, backup.Manifest.Image.File)
	imageFile, err := os.Open(imagePath)
	if err != nil {
		return VerifyResult{}, WrapError("VERIFY_FAILED", "Cannot open backup image: "+err.Error(), err)
	}
	defer imageFile.Close()
	return verifyBackupFile(ctx, backup, imageFile, onProgress)
}

func verifyBackupFile(ctx context.Context, backup Backup, imageFile *os.File, onProgress ProgressFunc) (VerifyResult, error) {
	if _, err := imageFile.Seek(0, io.SeekStart); err != nil {
		return VerifyResult{}, WrapError("VERIFY_FAILED", "Cannot seek in backup image: "+err.Error(), err)
	}
	compressedHasher := sha256.New()
	compressedReader := &countingReader{
		ctx: ctx, reader: imageFile, hash: compressedHasher,
		total: backup.Manifest.Image.CompressedBytes, phase: "verify",
		message: "Verifying compressed backup", onProgress: onProgress,
	}
	if _, err := io.Copy(io.Discard, compressedReader); err != nil {
		return VerifyResult{}, WrapError("VERIFY_FAILED", "Cannot read backup image: "+err.Error(), err)
	}
	compressedReader.finish()
	compressedBytes := compressedReader.read
	compressedHash := hex.EncodeToString(compressedHasher.Sum(nil))
	if compressedBytes != backup.Manifest.Image.CompressedBytes {
		return VerifyResult{}, NewError("CHECKSUM_MISMATCH", fmt.Sprintf("Backup verification failed: expected %d compressed bytes, read %d.", backup.Manifest.Image.CompressedBytes, compressedBytes))
	}
	if compressedHash != backup.Manifest.Image.CompressedSHA256 {
		return VerifyResult{}, NewError("CHECKSUM_MISMATCH", "Backup verification failed: compressed checksum mismatch.")
	}
	if _, err := imageFile.Seek(0, io.SeekStart); err != nil {
		return VerifyResult{}, WrapError("VERIFY_FAILED", "Cannot seek in backup image: "+err.Error(), err)
	}
	zipReader, err := gzip.NewReader(imageFile)
	if err != nil {
		return VerifyResult{}, WrapError("VERIFY_FAILED", "Backup data is unreadable: "+err.Error(), err)
	}
	defer zipReader.Close()
	rawHash := sha256.New()
	reader := &countingReader{
		ctx:        ctx,
		reader:     zipReader,
		hash:       rawHash,
		total:      backup.Manifest.Image.RawBytes,
		phase:      "verify",
		message:    "Verifying backup",
		onProgress: onProgress,
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return VerifyResult{}, WrapError("VERIFY_FAILED", "Backup data is unreadable: "+err.Error(), err)
	}
	reader.finish()
	actualRawHash := hex.EncodeToString(rawHash.Sum(nil))
	if reader.read != backup.Manifest.Image.RawBytes {
		return VerifyResult{}, NewError("CHECKSUM_MISMATCH", fmt.Sprintf("Backup verification failed: expected %d bytes, read %d.", backup.Manifest.Image.RawBytes, reader.read))
	}
	if actualRawHash != backup.Manifest.Image.SHA256 {
		return VerifyResult{}, NewError("CHECKSUM_MISMATCH", "Backup verification failed: uncompressed checksum mismatch.")
	}
	emit(onProgress, Progress{Phase: "complete", Message: "Backup verified", Fraction: 1})
	return VerifyResult{
		Valid:            true,
		RawBytes:         reader.read,
		SHA256:           actualRawHash,
		CompressedSHA256: compressedHash,
		Manifest:         backup.Manifest,
	}, nil
}
