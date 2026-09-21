package core

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var hostPattern = regexp.MustCompile(`^(?:[a-zA-Z0-9._-]+@)?(?:[a-zA-Z0-9._-]+|\[[0-9a-fA-F:]+\])$`)

// DiskStream is a readable remote process whose exit status must also be checked.
type DiskStream interface {
	io.ReadCloser
	Wait() error
}

// Remote abstracts Raspberry Pi inspection and whole-disk streaming.
type Remote interface {
	Inspect(ctx context.Context, connection Connection) (PiInfo, error)
	Sync(ctx context.Context, connection Connection) error
	OpenDisk(ctx context.Context, connection Connection, device string) (DiskStream, error)
}

// SSHRemote implements Remote with Malina's bundled SSH client.
type SSHRemote struct{}

// NewSSHRemote returns an SSH-backed remote implementation.
func NewSSHRemote() *SSHRemote { return &SSHRemote{} }

func validateConnection(connection Connection) error {
	if !hostPattern.MatchString(connection.Host) {
		return NewError("INVALID_HOST", "Host must look like user@host or user@192.168.0.112")
	}
	if connection.Port < 0 || connection.Port > 65535 {
		return NewError("INVALID_PORT", "SSH port must be between 1 and 65535.")
	}
	return nil
}

func (r *SSHRemote) run(ctx context.Context, connection Connection, command string, input io.Reader) (CommandResult, error) {
	return runSSH(ctx, connection, command, input)
}

func (r *SSHRemote) Sync(ctx context.Context, connection Connection) error {
	_, err := r.run(ctx, connection, "sync", nil)
	return err
}

func linuxDiskReadCommand(device string, hasPassword bool) string {
	direct := fmt.Sprintf("dd if=%s bs=4194304", device)
	directProbe := fmt.Sprintf("dd if=%s of=/dev/null bs=1 count=0", device)
	passwordlessProbe := fmt.Sprintf("sudo -n -- dd if=%s of=/dev/null bs=1 count=0", device)
	passwordless := fmt.Sprintf("sudo -n -- dd if=%s bs=4194304", device)
	password := fmt.Sprintf("sudo -S -p '' -- dd if=%s bs=4194304", device)
	passwordBranch := "echo 'Reading the source disk requires a sudo password.' >&2; exit 77"
	if hasPassword {
		passwordBranch = "exec " + password
	}
	return fmt.Sprintf(
		`if [ "$(uname -s)" != Linux ]; then echo 'Malina remote backup requires Linux.' >&2; exit 64; elif %s 2>/dev/null; then exec %s; elif command -v sudo >/dev/null 2>&1 && %s 2>/dev/null; then exec %s; elif command -v sudo >/dev/null 2>&1; then %s; else echo 'Cannot read the source disk directly and sudo is unavailable.' >&2; exit 77; fi`,
		directProbe,
		direct,
		passwordlessProbe,
		passwordless,
		passwordBranch,
	)
}

func (r *SSHRemote) OpenDisk(ctx context.Context, connection Connection, device string) (DiskStream, error) {
	if err := assertSafeDevicePath(device); err != nil {
		return nil, err
	}
	// The path has passed a strict allow-list. dd's decimal block size is
	// POSIX-compatible; stdin remains available for sudo authentication.
	password := connection.SudoPassword
	if password == "" {
		password = connection.Password
	}
	command := linuxDiskReadCommand(device, password != "")
	var input io.Reader
	if password != "" {
		input = strings.NewReader(password + "\n")
	}
	return openSSHDisk(ctx, connection, command, input)
}
