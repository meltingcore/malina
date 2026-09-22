//go:build !windows

package core

import "os/exec"

func configureProcess(_ *exec.Cmd) {}
