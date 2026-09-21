package main

import (
	"path/filepath"
	"testing"

	"github.com/meltingcore/malina/internal/core"
)

func TestConnectionNormalisesIdentityPath(t *testing.T) {
	connection, err := connection("pi@example.test", "relative/id_ed25519", 2222, false)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(connection.Identity) {
		t.Fatalf("identity path was not made absolute: %q", connection.Identity)
	}
	if connection.Host != "pi@example.test" || connection.Port != 2222 {
		t.Fatalf("unexpected connection: %#v", connection)
	}
}

func TestExecuteRejectsUnknownCommand(t *testing.T) {
	err := execute([]string{"not-a-command"})
	typed, ok := err.(*core.Error)
	if !ok || typed.Code != "UNKNOWN_COMMAND" {
		t.Fatalf("expected UNKNOWN_COMMAND, got %#v", err)
	}
}

func TestExecuteRequiresVerifyBackup(t *testing.T) {
	err := execute([]string{"verify"})
	typed, ok := err.(*core.Error)
	if !ok || typed.Code != "BACKUP_REQUIRED" {
		t.Fatalf("expected BACKUP_REQUIRED, got %#v", err)
	}
}
