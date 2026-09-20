package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var hostPattern = regexp.MustCompile(`^(?:[a-zA-Z0-9._-]+@)?(?:[a-zA-Z0-9._-]+|\[[0-9a-fA-F:]+\])$`)

type DiskStream interface {
	io.ReadCloser
	Wait() error
}

type Remote interface {
	Inspect(ctx context.Context, connection Connection) (PiInfo, error)
	Sync(ctx context.Context, connection Connection) error
	OpenDisk(ctx context.Context, connection Connection, device string) (DiskStream, error)
}

type SSHRemote struct {
	Runner CommandRunner
}

func NewSSHRemote() *SSHRemote {
	return &SSHRemote{Runner: ExecRunner{}}
}

func validateConnection(connection Connection) error {
	if !hostPattern.MatchString(connection.Host) {
		return NewError("INVALID_HOST", "Host must look like pi@raspberrypi.local or 192.168.1.20.")
	}
	if connection.Port < 0 || connection.Port > 65535 {
		return NewError("INVALID_PORT", "SSH port must be between 1 and 65535.")
	}
	if connection.Password != "" && connection.Identity != "" {
		return NewError("AUTH_CONFLICT", "Choose either an SSH key or a password, not both.")
	}
	return nil
}

func sshArgs(connection Connection, command string) ([]string, error) {
	if err := validateConnection(connection); err != nil {
		return nil, err
	}
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "ConnectTimeout=15",
	}
	if connection.Identity != "" {
		args = append(args, "-i", connection.Identity)
	}
	if connection.Port > 0 {
		args = append(args, "-p", strconv.Itoa(connection.Port))
	}
	args = append(args, connection.Host, command)
	return args, nil
}

func privileged(command string) string {
	return `if [ "$(id -u)" = 0 ]; then exec ` + command + `; else exec sudo -n ` + command + `; fi`
}

func (r *SSHRemote) run(ctx context.Context, connection Connection, command string, input io.Reader) (CommandResult, error) {
	if connection.Password != "" {
		return runPasswordSSH(ctx, connection, command, input)
	}
	args, err := sshArgs(connection, command)
	if err != nil {
		return CommandResult{}, err
	}
	runner := r.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	return runner.Run(ctx, "ssh", args, input)
}

func (r *SSHRemote) Sync(ctx context.Context, connection Connection) error {
	_, err := r.run(ctx, connection, privileged("sync"), nil)
	return err
}

type sshDiskStream struct {
	stdout io.ReadCloser
	cmd    *exec.Cmd
	stderr *bytes.Buffer
}

func (s *sshDiskStream) Read(buffer []byte) (int, error) { return s.stdout.Read(buffer) }

func (s *sshDiskStream) Close() error {
	_ = s.stdout.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	return nil
}

func (s *sshDiskStream) Wait() error {
	err := s.cmd.Wait()
	if err == nil {
		return nil
	}
	message := fmt.Sprintf("ssh exited unsuccessfully: %s", err)
	if text := strings.TrimSpace(s.stderr.String()); text != "" {
		message += ": " + text
	}
	return WrapError("COMMAND_FAILED", message, err)
}

func (r *SSHRemote) OpenDisk(ctx context.Context, connection Connection, device string) (DiskStream, error) {
	if err := assertSafeDevicePath(device); err != nil {
		return nil, err
	}
	commandText := privileged(fmt.Sprintf("dd if=%s bs=4M iflag=fullblock status=none", device))
	if connection.Password != "" {
		return openPasswordDisk(ctx, connection, commandText)
	}
	args, err := sshArgs(connection, commandText)
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, "ssh", args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, WrapError("PROCESS_START_FAILED", "Cannot open ssh output: "+err.Error(), err)
	}
	stderr := &bytes.Buffer{}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		if _, lookupErr := exec.LookPath("ssh"); lookupErr != nil {
			return nil, WrapError("COMMAND_NOT_FOUND", "Required command not found: ssh", lookupErr)
		}
		return nil, WrapError("PROCESS_START_FAILED", "Cannot start ssh: "+err.Error(), err)
	}
	return &sshDiskStream{stdout: stdout, cmd: command, stderr: stderr}, nil
}
