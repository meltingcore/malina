package desktop

import (
	"errors"
	"testing"
)

func TestIsDialogCancelled(t *testing.T) {
	err := errors.New("cancelled by user")
	if !isDialogCancelled(err) {
		t.Fatalf("isDialogCancelled(%v) = false, want true", err)
	}
}

func TestIsDialogCancelledRejectsOtherErrors(t *testing.T) {
	for _, err := range []error{nil, errors.New("access denied"), errors.New("cancelled by application")} {
		if isDialogCancelled(err) {
			t.Fatalf("isDialogCancelled(%v) = true, want false", err)
		}
	}
}
