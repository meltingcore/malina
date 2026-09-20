package core

import (
	"context"
	"sync"
)

type streamControlContextKey struct{}

// StreamControl cooperatively pauses reads without closing the underlying SSH stream.
type StreamControl struct {
	mu      sync.Mutex
	paused  bool
	resumed chan struct{}
}

func NewStreamControl() *StreamControl {
	return &StreamControl{}
}

func WithStreamControl(ctx context.Context, control *StreamControl) context.Context {
	return context.WithValue(ctx, streamControlContextKey{}, control)
}

func streamControlFromContext(ctx context.Context) *StreamControl {
	if ctx == nil {
		return nil
	}
	control, _ := ctx.Value(streamControlContextKey{}).(*StreamControl)
	return control
}

func (c *StreamControl) Pause() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.paused {
		return false
	}
	c.paused = true
	c.resumed = make(chan struct{})
	return true
}

func (c *StreamControl) Resume() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.paused {
		return false
	}
	c.paused = false
	close(c.resumed)
	c.resumed = nil
	return true
}

func (c *StreamControl) Wait(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	if !c.paused {
		c.mu.Unlock()
		return nil
	}
	resumed := c.resumed
	c.mu.Unlock()
	select {
	case <-resumed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
