package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

var knownHostsMu sync.Mutex

func sshAddress(connection Connection) (username, address string, err error) {
	if err := validateConnection(connection); err != nil {
		return "", "", err
	}
	host := connection.Host
	if separator := strings.LastIndex(host, "@"); separator >= 0 {
		username = host[:separator]
		host = host[separator+1:]
	} else {
		currentUser, userErr := user.Current()
		if userErr != nil || currentUser.Username == "" {
			return "", "", NewError("SSH_USER_REQUIRED", "Include the SSH user in the address, for example john@192.168.0.112")
		}
		username = currentUser.Username
	}
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	port := connection.Port
	if port == 0 {
		port = 22
	}
	return username, net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func malinaHostKeyCallback() (ssh.HostKeyCallback, error) {
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return nil, WrapError("SSH_HOST_KEY_FAILED", "Cannot locate the user configuration directory.", err)
	}
	directory := filepath.Join(configDirectory, "Malina")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, WrapError("SSH_HOST_KEY_FAILED", "Cannot create Malina's SSH configuration directory.", err)
	}
	path := filepath.Join(directory, "known_hosts")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, WrapError("SSH_HOST_KEY_FAILED", "Cannot open Malina's known-hosts file.", err)
	}
	if err := file.Close(); err != nil {
		return nil, WrapError("SSH_HOST_KEY_FAILED", "Cannot initialise Malina's known-hosts file.", err)
	}
	check, err := knownhosts.New(path)
	if err != nil {
		return nil, WrapError("SSH_HOST_KEY_FAILED", "Cannot read Malina's known-hosts file.", err)
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := check(hostname, remote, key)
		if err == nil {
			return nil
		}
		var keyError *knownhosts.KeyError
		if !errors.As(err, &keyError) || len(keyError.Want) != 0 {
			return WrapError("SSH_HOST_KEY_CHANGED", "The Raspberry Pi host key changed. Refusing to connect.", err)
		}

		knownHostsMu.Lock()
		defer knownHostsMu.Unlock()
		knownFile, openErr := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
		if openErr != nil {
			return WrapError("SSH_HOST_KEY_FAILED", "Cannot remember the Raspberry Pi host key.", openErr)
		}
		_, writeErr := fmt.Fprintln(knownFile, knownhosts.Line([]string{hostname}, key))
		closeErr := knownFile.Close()
		if writeErr != nil || closeErr != nil {
			return WrapError("SSH_HOST_KEY_FAILED", "Cannot remember the Raspberry Pi host key.", errors.Join(writeErr, closeErr))
		}
		return nil
	}, nil
}

func privateKeySigner(path, password string) (ssh.Signer, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.ParsePrivateKey(contents)
	if err == nil {
		return signer, nil
	}
	var passphraseMissing *ssh.PassphraseMissingError
	if password != "" && errors.As(err, &passphraseMissing) {
		return ssh.ParsePrivateKeyWithPassphrase(contents, []byte(password))
	}
	return nil, err
}

func defaultIdentityPaths() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(home, ".ssh", "id_ed25519"),
		filepath.Join(home, ".ssh", "id_ecdsa"),
		filepath.Join(home, ".ssh", "id_rsa"),
	}
}

func sshAuthMethods(connection Connection) ([]ssh.AuthMethod, []io.Closer, error) {
	methods := make([]ssh.AuthMethod, 0, 3)
	closers := make([]io.Closer, 0, 1)

	if connection.Identity != "" {
		signer, err := privateKeySigner(connection.Identity, connection.Password)
		if err != nil {
			return nil, nil, WrapError("SSH_KEY_FAILED", "Cannot use the selected SSH private key: "+err.Error(), err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	} else if connection.UseDefaultKeys || connection.Password == "" {
		if socket := os.Getenv("SSH_AUTH_SOCK"); socket != "" {
			if connectionSocket, err := net.Dial("unix", socket); err == nil {
				methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(connectionSocket).Signers))
				closers = append(closers, connectionSocket)
			}
		}
		for _, path := range defaultIdentityPaths() {
			signer, err := privateKeySigner(path, "")
			if err == nil {
				methods = append(methods, ssh.PublicKeys(signer))
			}
		}
	}
	if connection.Password != "" {
		methods = append(methods, ssh.Password(connection.Password))
	}
	if len(methods) == 0 {
		return nil, closers, NewError("SSH_AUTH_REQUIRED", "No usable SSH key or agent was found. Choose a private key or use password authentication.")
	}
	return methods, closers, nil
}

func dialSSH(ctx context.Context, connection Connection) (*ssh.Client, error) {
	username, address, err := sshAddress(connection)
	if err != nil {
		return nil, err
	}
	hostKeyCallback, err := malinaHostKeyCallback()
	if err != nil {
		return nil, err
	}
	auth, authClosers, err := sshAuthMethods(connection)
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, closer := range authClosers {
			_ = closer.Close()
		}
	}()
	config := &ssh.ClientConfig{
		User:            username,
		Auth:            auth,
		HostKeyCallback: hostKeyCallback,
		Timeout:         15 * time.Second,
	}
	connectionSocket, err := (&net.Dialer{Timeout: config.Timeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, WrapError("SSH_CONNECTION_FAILED", "Cannot connect to the Raspberry Pi: "+err.Error(), err)
	}
	handshakeDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connectionSocket.Close()
		case <-handshakeDone:
		}
	}()
	clientConnection, channels, requests, err := ssh.NewClientConn(connectionSocket, address, config)
	close(handshakeDone)
	if err != nil {
		_ = connectionSocket.Close()
		return nil, WrapError("SSH_AUTH_FAILED", "SSH authentication failed. Check the address and credentials.", err)
	}
	return ssh.NewClient(clientConnection, channels, requests), nil
}

func runSSH(ctx context.Context, connection Connection, command string, input io.Reader) (CommandResult, error) {
	client, err := dialSSH(ctx, connection)
	if err != nil {
		return CommandResult{}, err
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return CommandResult{}, WrapError("SSH_COMMAND_FAILED", "Cannot create the SSH session.", err)
	}
	defer session.Close()
	session.Stdin = input
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = client.Close()
		case <-done:
		}
	}()
	err = session.Run(command)
	close(done)
	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		message := "SSH command failed: " + err.Error()
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			message += ": " + detail
		}
		return result, WrapError("COMMAND_FAILED", message, err)
	}
	return result, nil
}

type sshClientDiskStream struct {
	stdout  io.Reader
	session *ssh.Session
	client  *ssh.Client
	done    chan struct{}
	once    sync.Once
	stderr  *bytes.Buffer
}

func (s *sshClientDiskStream) Read(buffer []byte) (int, error) { return s.stdout.Read(buffer) }

func (s *sshClientDiskStream) finish() {
	s.once.Do(func() {
		close(s.done)
		_ = s.session.Close()
		_ = s.client.Close()
	})
}

func (s *sshClientDiskStream) Close() error {
	s.finish()
	return nil
}

func (s *sshClientDiskStream) Wait() error {
	err := s.session.Wait()
	s.finish()
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(s.stderr.String())
	lowerDetail := strings.ToLower(detail)
	if strings.Contains(lowerDetail, "requires a sudo password") {
		return WrapError("SUDO_PASSWORD_REQUIRED", "Cannot read the source disk directly or with passwordless sudo. Enter the Raspberry Pi account password and try again.", err)
	}
	if strings.Contains(lowerDetail, "incorrect password") ||
		strings.Contains(lowerDetail, "a password is required") ||
		strings.Contains(lowerDetail, "no password was provided") ||
		strings.Contains(lowerDetail, "authentication failure") ||
		strings.Contains(lowerDetail, "sorry, try again") {
		return WrapError("SUDO_AUTH_FAILED", "Cannot read the source disk. The Raspberry Pi account password was not accepted by sudo: "+detail, err)
	}
	message := "SSH disk stream failed: " + err.Error()
	if detail != "" {
		message += ": " + detail
	}
	return WrapError("COMMAND_FAILED", message, err)
}

func openSSHDisk(ctx context.Context, connection Connection, command string, input io.Reader) (DiskStream, error) {
	client, err := dialSSH(ctx, connection)
	if err != nil {
		return nil, err
	}
	session, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return nil, WrapError("SSH_COMMAND_FAILED", "Cannot create the SSH disk session.", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, WrapError("SSH_COMMAND_FAILED", "Cannot open the SSH disk stream.", err)
	}
	stderr := &bytes.Buffer{}
	session.Stdin = input
	session.Stderr = stderr
	if err := session.Start(command); err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, WrapError("SSH_COMMAND_FAILED", "Cannot start the SSH disk stream.", err)
	}
	stream := &sshClientDiskStream{stdout: stdout, session: session, client: client, done: make(chan struct{}), stderr: stderr}
	go func() {
		select {
		case <-ctx.Done():
			stream.finish()
		case <-stream.done:
		}
	}()
	return stream, nil
}
