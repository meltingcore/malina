//go:build windows

package core

import (
	"os/exec"
	"syscall"
)

// configureProcess prevents console applications such as powershell.exe from
// flashing a Command Prompt window when Malina is running as a GUI app.
func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
