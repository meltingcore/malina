package core

import (
	"context"
	"runtime"
	"time"
)

type Engine struct {
	Remote  Remote
	Devices DeviceOperations
	Now     func() time.Time
}

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

func (e *Engine) Inspect(ctx context.Context, connection Connection) (PiInfo, error) {
	remote := e.Remote
	if remote == nil {
		remote = NewSSHRemote()
	}
	return remote.Inspect(ctx, connection)
}
