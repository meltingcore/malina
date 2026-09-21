package core

import (
	"context"
	"runtime"
	"time"
)

// Engine coordinates backup, verification, restore, and platform dependencies.
// Remote, Devices, and Now are replaceable to support deterministic tests.
type Engine struct {
	Remote  Remote
	Devices DeviceOperations
	Now     func() time.Time
}

// NewEngine returns an engine backed by SSH and the current operating system.
func NewEngine() *Engine {
	return &Engine{
		Remote:  NewSSHRemote(),
		Devices: NewDeviceManager(runtime.GOOS),
		Now:     time.Now,
	}
}

func (e *Engine) clock() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Inspect discovers the remote Raspberry Pi disk layout and access capabilities.
func (e *Engine) Inspect(ctx context.Context, connection Connection) (PiInfo, error) {
	remote := e.Remote
	if remote == nil {
		remote = NewSSHRemote()
	}
	return remote.Inspect(ctx, connection)
}
