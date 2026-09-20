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
	"golang.org/x/crypto/ssh/knownhosts"
)

var knownHostsMu sync.Mutex

func passwordSSHAddress(connection Connection) (username, address string, err error) {
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
			return "", "", NewError("SSH_USER_REQUIRED", "Include the SSH user in the address, for example pi@raspberrypi.local.")
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

func dialPasswordSSH(ctx context.Context, connection Connection) (*ssh.Client, error) {
	username, address, err := passwordSSHAddress(connection)
	if err != nil {
		return nil, err
	}
	hostKeyCallback, err := malinaHostKeyCallback()
	if err != nil {
		return nil, err
	}
	config := &ssh.ClientConfig{
		User:            username,
		Auth:            []ssh.AuthMethod{ssh.Password(connection.Password)},
		HostKeyCallback: hostKeyCallback,
		Timeout:         15 * time.Second,
	}
	connectionSocket, err := (&net.Dialer{Timeout: config.Timeout}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, WrapError("SSH_CONNECTION_FAILED", "Cannot connect to the Raspberry Pi: "+err.Error(), err)
	}
	clientConnection, channels, requests, err := ssh.NewClientConn(connectionSocket, address, config)
	if err != nil {
		_ = connectionSocket.Close()
		message := "SSH authentication failed. Check the address, user, and password."
		return nil, WrapError("SSH_AUTH_FAILED", message, err)
	}
	return ssh.NewClient(clientConnection, channels, requests), nil
}

func runPasswordSSH(ctx context.Context, connection Connection, command string, input io.Reader) (CommandResult, error) {
	client, err := dialPasswordSSH(ctx, connection)
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

type passwordDiskStream struct {
	stdout  io.Reader
	session *ssh.Session
	client  *ssh.Client
	done    chan struct{}
	once    sync.Once
	stderr  *bytes.Buffer
}

func (s *passwordDiskStream) Read(buffer []byte) (int, error) { return s.stdout.Read(buffer) }

func (s *passwordDiskStream) finish() {
	s.once.Do(func() {
		close(s.done)
		_ = s.session.Close()
		_ = s.client.Close()
	})
}

func (s *passwordDiskStream) Close() error {
	s.finish()
	return nil
}

func (s *passwordDiskStream) Wait() error {
	err := s.session.Wait()
	s.finish()
	if err == nil {
		return nil
	}
	message := "SSH disk stream failed: " + err.Error()
	if detail := strings.TrimSpace(s.stderr.String()); detail != "" {
		message += ": " + detail
	}
	return WrapError("COMMAND_FAILED", message, err)
}

func openPasswordDisk(ctx context.Context, connection Connection, command string) (DiskStream, error) {
	client, err := dialPasswordSSH(ctx, connection)
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
	session.Stderr = stderr
	if err := session.Start(command); err != nil {
		_ = session.Close()
		_ = client.Close()
		return nil, WrapError("SSH_COMMAND_FAILED", "Cannot start the SSH disk stream.", err)
	}
	stream := &passwordDiskStream{stdout: stdout, session: session, client: client, done: make(chan struct{}), stderr: stderr}
	go func() {
		select {
		case <-ctx.Done():
			stream.finish()
		case <-stream.done:
		}
	}()
	return stream, nil
}
