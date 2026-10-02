package desktop

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/meltingcore/malina/internal/core"
	"github.com/zalando/go-keyring"
)

const deviceConfigVersion = 1
const credentialService = "Malina SSH"

// SavedDevice contains persistent settings, never passwords or key contents.
type SavedDevice struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Username         string `json:"username"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	AuthMethod       string `json:"authMethod"`
	Identity         string `json:"identity,omitempty"`
	OutputDirectory  string `json:"outputDirectory"`
	RememberPassword bool   `json:"rememberPassword"`
	CredentialRef    string `json:"credentialRef,omitempty"`
}

type DeviceConfig struct {
	Version          int           `json:"version"`
	Devices          []SavedDevice `json:"devices"`
	SelectedDeviceID string        `json:"selectedDeviceId,omitempty"`
}

// SaveDeviceRequest deliberately separates ephemeral secrets from durable data.
type SaveDeviceRequest struct {
	Device             SavedDevice `json:"device"`
	Password           string      `json:"password,omitempty"`
	CopyPasswordFromID string      `json:"copyPasswordFromId,omitempty"`
}

type credentialStore interface {
	Set(string, string) error
	Get(string) (string, error)
	Delete(string) error
}

type systemCredentials struct{}

// Bind a credential to its device/account inside the credential store as well
// as in the form. Editing the JSON by hand cannot redirect a saved password.
type storedDevicePassword struct {
	DeviceID string `json:"deviceId"`
	Username string `json:"username"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Password string `json:"password"`
}

func credentialValue(device SavedDevice, password string) string {
	data, _ := json.Marshal(storedDevicePassword{
		DeviceID: device.ID, Username: device.Username, Host: device.Host, Port: device.Port, Password: password,
	})
	return string(data)
}

func (s *deviceStore) password(device SavedDevice) (string, error) {
	value, err := s.credentials.Get(device.CredentialRef)
	if err != nil {
		return "", err
	}
	var stored storedDevicePassword
	if err := json.Unmarshal([]byte(value), &stored); err != nil || stored.DeviceID != device.ID ||
		stored.Username != device.Username || !strings.EqualFold(stored.Host, device.Host) || stored.Port != device.Port || stored.Password == "" {
		return "", errors.New("saved credential does not match the selected account")
	}
	return stored.Password, nil
}

func (systemCredentials) Set(ref, password string) error {
	return keyring.Set(credentialService, ref, password)
}
func (systemCredentials) Get(ref string) (string, error) {
	return keyring.Get(credentialService, ref)
}
func (systemCredentials) Delete(ref string) error {
	err := keyring.Delete(credentialService, ref)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

type deviceStore struct {
	mu          sync.Mutex
	path        string // Tests override this; production always uses the user's home.
	credentials credentialStore
}

func (s *deviceStore) configPath() (string, error) {
	if s.path != "" {
		return s.path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", core.WrapError("CONFIG_HOME_UNAVAILABLE", "Cannot locate your home folder for .malina.json.", err)
	}
	return filepath.Join(home, ".malina.json"), nil
}

var profileIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
var hostnamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*$`)

func normalizeDevice(device SavedDevice) (SavedDevice, error) {
	device.Name = strings.TrimSpace(device.Name)
	device.Username = strings.TrimSpace(device.Username)
	device.Host = strings.Trim(strings.TrimSpace(device.Host), "[]")
	device.Identity = strings.TrimSpace(device.Identity)
	device.OutputDirectory = strings.TrimSpace(device.OutputDirectory)
	if device.Name == "" || len([]rune(device.Name)) > 100 || strings.IndexFunc(device.Name, unicode.IsControl) >= 0 {
		return SavedDevice{}, core.NewError("INVALID_DEVICE_NAME", "Enter a device name of 1–100 characters without control characters.")
	}
	if !usernamePattern.MatchString(device.Username) {
		return SavedDevice{}, core.NewError("INVALID_DEVICE_USER", "Enter a valid SSH username.")
	}
	if !hostnamePattern.MatchString(device.Host) && net.ParseIP(device.Host) == nil {
		return SavedDevice{}, core.NewError("INVALID_DEVICE_HOST", "Enter a hostname or IP address, without the username or port.")
	}
	if device.Port < 1 || device.Port > 65535 {
		return SavedDevice{}, core.NewError("INVALID_DEVICE_PORT", "SSH port must be between 1 and 65535.")
	}
	if device.AuthMethod != "key" && device.AuthMethod != "password" {
		return SavedDevice{}, core.NewError("INVALID_DEVICE_AUTH", "Choose SSH key or password authentication.")
	}
	if !filepath.IsAbs(device.OutputDirectory) || (device.Identity != "" && !filepath.IsAbs(device.Identity)) {
		return SavedDevice{}, core.NewError("INVALID_DEVICE_PATH", "Choose an absolute backup folder and SSH key path.")
	}
	device.OutputDirectory = filepath.Clean(device.OutputDirectory)
	if device.AuthMethod == "key" {
		device.RememberPassword = false
	} else {
		device.Identity = ""
	}
	return device, nil
}

func configReadError(err error) error {
	return core.WrapError("CONFIG_READ_FAILED", "Could not read .malina.json. The file has been preserved; repair it or move it aside before saving devices.", err)
}

// read validates the entire file before any mutation. Unknown versions/fields,
// invalid records, and truncated files cannot be silently overwritten.
func (s *deviceStore) read() (DeviceConfig, error) {
	empty := DeviceConfig{Version: deviceConfigVersion, Devices: []SavedDevice{}}
	path, err := s.configPath()
	if err != nil {
		return empty, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return empty, configReadError(err)
	}
	if !info.Mode().IsRegular() || info.Size() > 4<<20 {
		return empty, configReadError(errors.New("config must be a regular file smaller than 4 MiB"))
	}
	file, err := os.Open(path)
	if err != nil {
		return empty, configReadError(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 4<<20))
	decoder.DisallowUnknownFields()
	var config DeviceConfig
	if err := decoder.Decode(&config); err != nil {
		return empty, configReadError(err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return empty, configReadError(errors.New("unexpected data after configuration"))
	}
	if config.Version != deviceConfigVersion || config.Devices == nil {
		return empty, configReadError(errors.New("unsupported config version or missing devices"))
	}
	ids, names := make(map[string]bool), make(map[string]bool)
	for i, device := range config.Devices {
		device, err = normalizeDevice(device)
		if err != nil || !profileIDPattern.MatchString(device.ID) || ids[device.ID] || names[strings.ToLower(device.Name)] ||
			(device.RememberPassword != (device.CredentialRef != "")) ||
			(device.CredentialRef != "" && !profileIDPattern.MatchString(device.CredentialRef)) {
			return empty, configReadError(errors.New("invalid or duplicate saved device"))
		}
		config.Devices[i] = device
		ids[device.ID], names[strings.ToLower(device.Name)] = true, true
	}
	if config.SelectedDeviceID != "" && !ids[config.SelectedDeviceID] {
		return empty, configReadError(errors.New("selected device does not exist"))
	}
	return config, nil
}

func (s *deviceStore) write(config DeviceConfig) error {
	path, err := s.configPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if len(data)+1 > 4<<20 {
		return core.NewError("CONFIG_TOO_LARGE", "The device configuration is too large to save. Shorten its paths or remove unused devices.")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".malina-*.tmp")
	if err != nil {
		return core.WrapError("CONFIG_SAVE_FAILED", "Could not save .malina.json. Your existing configuration has been preserved.", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(append(data, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		return core.WrapError("CONFIG_SAVE_FAILED", "Could not save .malina.json. Your existing configuration has been preserved.", err)
	}
	return nil
}

func newProfileID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func findSavedDevice(config DeviceConfig, id string) (SavedDevice, bool) {
	for _, device := range config.Devices {
		if device.ID == id {
			return device, true
		}
	}
	return SavedDevice{}, false
}

func sameAccount(a, b SavedDevice) bool {
	return a.Username == b.Username && strings.EqualFold(a.Host, b.Host) && a.Port == b.Port && a.AuthMethod == b.AuthMethod
}

func savedConnectionHost(device SavedDevice) string {
	host := device.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return device.Username + "@" + host
}

func credentialError(err error) error {
	return core.WrapError("CREDENTIAL_STORE_UNAVAILABLE", "Could not access the system credential store. Unlock it and retry, or turn off Remember password securely and enter the password when needed.", err)
}

func (s *Service) LoadDeviceConfig() (DeviceConfig, error) {
	s.profiles.mu.Lock()
	defer s.profiles.mu.Unlock()
	return s.profiles.read()
}

func (s *Service) SaveDevice(request SaveDeviceRequest) (DeviceConfig, error) {
	s.profiles.mu.Lock()
	defer s.profiles.mu.Unlock()
	config, err := s.profiles.read()
	if err != nil {
		return config, err
	}
	device, err := normalizeDevice(request.Device)
	if err != nil {
		return config, err
	}
	previous, exists := findSavedDevice(config, device.ID)
	if device.ID != "" && !exists {
		return config, core.NewError("DEVICE_PROFILE_NOT_FOUND", "This saved device no longer exists. Save it as a new device.")
	}
	for _, other := range config.Devices {
		if other.ID != device.ID && strings.EqualFold(other.Name, device.Name) {
			return config, core.NewError("DEVICE_NAME_EXISTS", "A device with this name already exists. Choose a different name.")
		}
	}
	if device.ID == "" {
		device.ID, err = newProfileID()
		if err != nil {
			return config, err
		}
	}
	device.CredentialRef = "" // Never accept a client-supplied credential reference.
	password := request.Password
	if device.RememberPassword {
		if password == "" && previous.RememberPassword && sameAccount(previous, device) {
			device.CredentialRef = previous.CredentialRef
		} else {
			if password == "" && request.CopyPasswordFromID != "" {
				source, found := findSavedDevice(config, request.CopyPasswordFromID)
				if found && source.RememberPassword && sameAccount(source, device) {
					password, err = s.profiles.password(source)
					if err != nil {
						return config, credentialError(err)
					}
				}
			}
			if password == "" {
				return config, core.NewError("PASSWORD_REQUIRED", "Enter the SSH password to remember it for this account, or turn off Remember password securely.")
			}
			device.CredentialRef, err = newProfileID()
			if err != nil {
				return config, err
			}
			if err := s.profiles.credentials.Set(device.CredentialRef, credentialValue(device, password)); err != nil {
				return config, credentialError(err)
			}
		}
	}
	// Clear old credentials before removing their reference. If deletion fails,
	// leave the profile intact so the user can unlock the store and retry.
	var previousPassword string
	if previous.CredentialRef != "" && previous.CredentialRef != device.CredentialRef {
		previousPassword, err = s.profiles.credentials.Get(previous.CredentialRef)
		if err != nil && !errors.Is(err, keyring.ErrNotFound) {
			if device.CredentialRef != "" {
				_ = s.profiles.credentials.Delete(device.CredentialRef)
			}
			return config, credentialError(err)
		}
		if err := s.profiles.credentials.Delete(previous.CredentialRef); err != nil {
			if device.CredentialRef != "" {
				_ = s.profiles.credentials.Delete(device.CredentialRef)
			}
			return config, credentialError(err)
		}
	}
	if exists {
		for i := range config.Devices {
			if config.Devices[i].ID == device.ID {
				config.Devices[i] = device
			}
		}
	} else {
		config.Devices = append(config.Devices, device)
	}
	config.SelectedDeviceID = device.ID
	if err := s.profiles.write(config); err != nil {
		if previousPassword != "" {
			_ = s.profiles.credentials.Set(previous.CredentialRef, previousPassword)
		}
		if device.CredentialRef != "" && device.CredentialRef != previous.CredentialRef {
			_ = s.profiles.credentials.Delete(device.CredentialRef)
		}
		return DeviceConfig{}, err
	}
	return config, nil
}

func (s *Service) SelectSavedDevice(id string) (DeviceConfig, error) {
	s.profiles.mu.Lock()
	defer s.profiles.mu.Unlock()
	config, err := s.profiles.read()
	if err != nil {
		return config, err
	}
	if _, found := findSavedDevice(config, id); id != "" && !found {
		return config, core.NewError("DEVICE_PROFILE_NOT_FOUND", "This saved device no longer exists.")
	}
	if config.SelectedDeviceID == id {
		return config, nil
	}
	config.SelectedDeviceID = id
	if err := s.profiles.write(config); err != nil {
		return DeviceConfig{}, err
	}
	return config, nil
}

func (s *Service) DeleteSavedDevice(id string) (DeviceConfig, error) {
	s.profiles.mu.Lock()
	defer s.profiles.mu.Unlock()
	config, err := s.profiles.read()
	if err != nil {
		return config, err
	}
	device, found := findSavedDevice(config, id)
	if !found {
		return config, core.NewError("DEVICE_PROFILE_NOT_FOUND", "This saved device no longer exists.")
	}
	var previousPassword string
	if device.CredentialRef != "" {
		previousPassword, err = s.profiles.credentials.Get(device.CredentialRef)
		if err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return config, credentialError(err)
		}
		if err := s.profiles.credentials.Delete(device.CredentialRef); err != nil {
			return config, credentialError(err)
		}
	}
	for i, saved := range config.Devices {
		if saved.ID == id {
			config.Devices = append(config.Devices[:i], config.Devices[i+1:]...)
			break
		}
	}
	if config.SelectedDeviceID == id {
		config.SelectedDeviceID = ""
	}
	if err := s.profiles.write(config); err != nil {
		if previousPassword != "" {
			_ = s.profiles.credentials.Set(device.CredentialRef, previousPassword)
		}
		return DeviceConfig{}, err
	}
	return config, nil
}

// SavedDevicePassword is only called when starting an operation, never when
// listing/loading profiles. Bind retrieval to the current account to prevent
// sending a saved password to an edited host, username, or port.
func (s *Service) SavedDevicePassword(id string, connection core.Connection) (string, error) {
	s.profiles.mu.Lock()
	defer s.profiles.mu.Unlock()
	config, err := s.profiles.read()
	if err != nil {
		return "", err
	}
	device, found := findSavedDevice(config, id)
	username, host, hasUser := strings.Cut(connection.Host, "@")
	host = strings.Trim(host, "[]")
	if !found || !device.RememberPassword || device.AuthMethod != "password" ||
		!hasUser || username != device.Username || !strings.EqualFold(host, device.Host) || connection.Port != device.Port || connection.Identity != "" {
		return "", core.NewError("PASSWORD_REQUIRED", "Enter the SSH password for the selected account.")
	}
	password, err := s.profiles.password(device)
	if err != nil || password == "" {
		return "", core.WrapError("SAVED_PASSWORD_UNAVAILABLE", "The saved password is unavailable or the credential store is locked. Enter the SSH password or unlock the store and retry.", err)
	}
	return password, nil
}

// ValidateBackupDestination checks local resources before starting network work.
// Missing custom destinations are never recreated (e.g. an unplugged drive).
func (s *Service) ValidateBackupDestination(request core.BackupRequest) error {
	if request.Connection.Identity != "" {
		info, err := os.Stat(request.Connection.Identity)
		if err != nil || !info.Mode().IsRegular() {
			return core.NewError("SSH_KEY_UNAVAILABLE", "The selected SSH key is unavailable. Choose the key again.")
		}
	}
	path := request.OutputDirectory
	if path == s.DefaultBackupDirectory() {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return core.NewError("OUTPUT_UNAVAILABLE", "The backup folder is unavailable or cannot be created. Choose a folder again.")
		}
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return core.NewError("OUTPUT_UNAVAILABLE", "The backup folder is unavailable. Reconnect the drive or choose a folder again.")
	}
	file, err := os.CreateTemp(path, ".malina-write-check-*")
	if err != nil {
		return core.NewError("OUTPUT_NOT_WRITABLE", "The backup folder is not writable. Choose another folder or check its permissions.")
	}
	closeErr := file.Close()
	removeErr := os.Remove(file.Name())
	if closeErr != nil || removeErr != nil {
		return core.NewError("OUTPUT_NOT_WRITABLE", "Could not check write access to the backup folder.")
	}
	return nil
}

// StartDeviceBackup freezes the submitted settings and display name. It does
// not consult the profile again while the background job runs.
func (s *Service) StartDeviceBackup(request core.BackupRequest, profileName string) (core.Job, error) {
	if err := s.ValidateBackupDestination(request); err != nil {
		return core.Job{}, err
	}
	return s.startBackup(request, strings.TrimSpace(profileName))
}
