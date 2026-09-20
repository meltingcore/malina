package core

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func ListBackups(directory string) ([]Backup, error) {
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return []Backup{}, nil
	}
	if err != nil {
		return nil, err
	}
	backups := make([]Backup, 0)
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasSuffix(entry.Name(), ".partial") {
			continue
		}
		backup, err := LoadBackup(filepath.Join(directory, entry.Name()))
		if err == nil {
			backups = append(backups, backup)
		}
	}
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Manifest.CreatedAt.After(backups[j].Manifest.CreatedAt)
	})
	return backups, nil
}
