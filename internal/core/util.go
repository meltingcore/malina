package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var safeNamePattern = regexp.MustCompile(`[^a-z0-9._-]+`)

func safeName(value string) string {
	value = strings.Trim(safeNamePattern.ReplaceAllString(strings.ToLower(value), "-"), "-")
	if value == "" {
		return "raspberrypi"
	}
	return value
}

func backupName(hostname string, now time.Time) string {
	return fmt.Sprintf("%s-%s", safeName(hostname), now.UTC().Format("20060102T150405Z"))
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func readJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	count, err := io.Copy(hash, file)
	if err != nil {
		return "", count, err
	}
	return hex.EncodeToString(hash.Sum(nil)), count, nil
}

func prepareAtomicDirectory(parent, name string) (partialPath, finalPath string, err error) {
	if err = os.MkdirAll(parent, 0o700); err != nil {
		return "", "", err
	}
	finalPath = filepath.Join(parent, name)
	partialPath = finalPath + ".partial"
	if _, statErr := os.Stat(finalPath); statErr == nil {
		return "", "", NewError("BACKUP_EXISTS", "Backup already exists: "+finalPath)
	} else if !os.IsNotExist(statErr) {
		return "", "", statErr
	}
	if err = os.RemoveAll(partialPath); err != nil {
		return "", "", err
	}
	if err = os.Mkdir(partialPath, 0o700); err != nil {
		return "", "", err
	}
	return partialPath, finalPath, nil
}

type countingReader struct {
	ctx         context.Context
	reader      io.Reader
	hash        io.Writer
	read        int64
	total       int64
	phase       string
	message     string
	onProgress  ProgressFunc
	lastUpdated time.Time
}

func (r *countingReader) Read(buffer []byte) (int, error) {
	if control := streamControlFromContext(r.ctx); control != nil {
		if err := control.Wait(r.ctx); err != nil {
			return 0, err
		}
	}
	if r.ctx != nil {
		select {
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		default:
		}
	}
	n, err := r.reader.Read(buffer)
	if n > 0 {
		r.read += int64(n)
		if r.hash != nil {
			_, _ = r.hash.Write(buffer[:n])
		}
		if r.onProgress != nil && (time.Since(r.lastUpdated) >= 250*time.Millisecond || r.read == r.total) {
			r.lastUpdated = time.Now()
			fraction := 0.0
			if r.total > 0 {
				fraction = float64(r.read) / float64(r.total)
			}
			r.onProgress(Progress{Phase: r.phase, Message: r.message, Bytes: r.read, TotalBytes: r.total, Fraction: fraction})
		}
	}
	return n, err
}

func (r *countingReader) finish() {
	if r.onProgress == nil {
		return
	}
	fraction := 0.0
	if r.total > 0 {
		fraction = float64(r.read) / float64(r.total)
	}
	r.onProgress(Progress{Phase: r.phase, Message: r.message, Bytes: r.read, TotalBytes: r.total, Fraction: fraction})
}
