package core

import (
	"context"
	"errors"
	"testing"
)

func TestStreamControlPauseAndResume(t *testing.T) {
	control := NewStreamControl()
	if !control.Pause() || control.Pause() {
		t.Fatal("pause should change state exactly once")
	}
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		done <- control.Wait(context.Background())
	}()
	<-started
	select {
	case <-done:
		t.Fatal("paused stream was released before resume")
	default:
	}
	if !control.Resume() || control.Resume() {
		t.Fatal("resume should change state exactly once")
	}
	if err := <-done; err != nil {
		t.Fatalf("resume returned an error: %v", err)
	}
}

func TestStreamControlCancellationReleasesPause(t *testing.T) {
	control := NewStreamControl()
	control.Pause()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := control.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
