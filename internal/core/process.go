package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

// CommandResult contains captured output from a platform command.
type CommandResult struct {
	Stdout string
	Stderr string
}

// CommandRunner abstracts operating-system commands for testing.
type CommandRunner interface {
	Run(ctx context.Context, name string, args []string, input io.Reader) (CommandResult, error)
}

// ExecRunner runs commands with os/exec and context cancellation.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args []string, input io.Reader) (CommandResult, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = input
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		if _, lookupErr := exec.LookPath(name); lookupErr != nil {
			return result, WrapError("COMMAND_NOT_FOUND", "Required command not found: "+name, lookupErr)
		}
		message := fmt.Sprintf("%s failed: %s", name, err)
		if stderr.Len() > 0 {
			message += ": " + stderr.String()
		}
		return result, WrapError("COMMAND_FAILED", message, err)
	}
	return result, nil
}
