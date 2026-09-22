package desktop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/meltingcore/malina/internal/core"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// isDialogCancelled identifies the cancellation error returned by Wails' Windows
// file-dialog implementation. Wails does not expose that sentinel publicly, so
// callers must distinguish its stable error text from actual dialog failures.
func isDialogCancelled(err error) bool {
	return err != nil && strings.EqualFold(strings.TrimSpace(err.Error()), "cancelled by user")
}

type Service struct {
	app            *application.App
	engine         *core.Engine
	jobsMu         sync.RWMutex
	jobs           map[string]*managedJob
	restoreTargets map[string]struct{}
	jobSequence    uint64
	appCtx         context.Context
}

func NewService(app *application.App, engine *core.Engine) *Service {
	return &Service{
		app: app, engine: engine, jobs: make(map[string]*managedJob),
		restoreTargets: make(map[string]struct{}), appCtx: context.Background(),
	}
}

func (s *Service) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.jobsMu.Lock()
	s.appCtx = ctx
	s.jobsMu.Unlock()
	return nil
}

func (s *Service) DefaultBackupDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "Malina Backups"
	}
	return filepath.Join(home, "Malina Backups")
}

func (s *Service) SelectIdentityFile(_ context.Context, currentPath string) (string, error) {
	dialog := s.app.Dialog.OpenFile().
		SetTitle("Choose SSH private key").
		CanChooseDirectories(false).
		CanChooseFiles(true).
		ShowHiddenFiles(true).
		AllowsOtherFileTypes(true)
	if currentPath != "" {
		dialog.SetDirectory(filepath.Dir(currentPath))
	}
	path, err := dialog.PromptForSingleSelection()
	if isDialogCancelled(err) {
		return "", nil
	}
	if err != nil {
		return "", core.WrapError("DIALOG_FAILED", "Could not open the key file picker.", err)
	}
	return path, nil
}

func (s *Service) SelectBackupDirectory(_ context.Context, currentPath string) (string, error) {
	dialog := s.app.Dialog.OpenFile().
		SetTitle("Choose backup location").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true)
	if currentPath != "" {
		dialog.SetDirectory(currentPath)
	}
	path, err := dialog.PromptForSingleSelection()
	if isDialogCancelled(err) {
		return "", nil
	}
	if err != nil {
		return "", core.WrapError("DIALOG_FAILED", "Could not open the folder picker.", err)
	}
	return path, nil
}

func (s *Service) SelectBackup(_ context.Context) (core.Backup, error) {
	path, err := s.app.Dialog.OpenFile().
		SetTitle("Choose a Malina backup").
		CanChooseDirectories(true).
		CanChooseFiles(false).
		PromptForSingleSelection()
	if isDialogCancelled(err) {
		return core.Backup{}, nil
	}
	if err != nil {
		return core.Backup{}, core.WrapError("DIALOG_FAILED", "Could not open the backup picker.", err)
	}
	if path == "" {
		return core.Backup{}, nil
	}
	return core.LoadBackup(path)
}

func (s *Service) Inspect(ctx context.Context, connection core.Connection) (core.PiInfo, error) {
	return s.engine.Inspect(ctx, connection)
}

func (s *Service) ListBackups(_ context.Context, directory string) ([]core.Backup, error) {
	return core.ListBackups(directory)
}

func (s *Service) ListDevices(ctx context.Context) ([]core.Device, error) {
	return s.engine.Devices.List(ctx)
}
