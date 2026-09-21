package core

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestPrivateKeySignerUsesConnectionPasswordAsPassphrase(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(privateKey, "malina-test", []byte("account-password"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := privateKeySigner(path, "account-password"); err != nil {
		t.Fatalf("expected the account password to unlock the key: %v", err)
	}
	if _, err := privateKeySigner(path, "wrong-password"); err == nil {
		t.Fatal("expected the wrong password to fail")
	}
}

func TestSSHAuthAllowsKeyAndPasswordTogether(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "malina-test")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	methods, closers, err := sshAuthMethods(Connection{Identity: path, Password: "account-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, closer := range closers {
			_ = closer.Close()
		}
	}()
	if len(methods) != 2 {
		t.Fatalf("expected key and password authentication methods, got %d", len(methods))
	}
}

func TestSudoPasswordIsNotUsedForSSHAuthentication(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "malina-test")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	methods, closers, err := sshAuthMethods(Connection{Identity: path, SudoPassword: "sudo-only"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, closer := range closers {
			_ = closer.Close()
		}
	}()
	if len(methods) != 1 {
		t.Fatalf("expected only key authentication, got %d methods", len(methods))
	}
}

func TestPasswordOnlyDoesNotImplicitlyUseDefaultKeys(t *testing.T) {
	methods, closers, err := sshAuthMethods(Connection{Password: "account-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, closer := range closers {
			_ = closer.Close()
		}
	}()
	if len(methods) != 1 {
		t.Fatalf("expected password-only authentication, got %d methods", len(methods))
	}
}

func TestConcurrentFirstConnectionsCannotPinDifferentHostKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	callback := hostKeyCallbackForPath(path)
	keys := make([]ssh.PublicKey, 2)
	for index := range keys {
		public, _, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		keys[index], err = ssh.NewPublicKey(public)
		if err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for _, key := range keys {
		workers.Add(1)
		go func(key ssh.PublicKey) {
			defer workers.Done()
			<-start
			errors <- callback("example.test:22", &net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 22}, key)
		}(key)
	}
	close(start)
	workers.Wait()
	close(errors)
	succeeded := 0
	rejected := 0
	for err := range errors {
		if err == nil {
			succeeded++
			continue
		}
		if typed, ok := err.(*Error); ok && typed.Code == "SSH_HOST_KEY_CHANGED" {
			rejected++
			continue
		}
		t.Fatalf("unexpected callback error: %v", err)
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("expected one pinned key and one rejection, got %d successes and %d rejections", succeeded, rejected)
	}
}
