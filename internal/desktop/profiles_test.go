package desktop

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/meltingcore/malina/internal/core"
	"github.com/zalando/go-keyring"
)

type memoryCredentials struct {
	values         map[string]string
	setErr, getErr error
	deleteErr      error
	beforeSet      func()
}

func (m *memoryCredentials) Set(ref, password string) error {
	if m.setErr != nil {
		return m.setErr
	}
	m.values[ref] = password
	if m.beforeSet != nil {
		m.beforeSet()
	}
	return nil
}
func (m *memoryCredentials) Get(ref string) (string, error) {
	if m.getErr != nil {
		return "", m.getErr
	}
	if password, ok := m.values[ref]; ok {
		return password, nil
	}
	return "", keyring.ErrNotFound
}
func (m *memoryCredentials) Delete(ref string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	delete(m.values, ref)
	return nil
}

func profileService(t *testing.T) (*Service, *memoryCredentials) {
	t.Helper()
	credentials := &memoryCredentials{values: make(map[string]string)}
	service := NewService(nil, nil)
	service.profiles = &deviceStore{path: filepath.Join(t.TempDir(), ".malina.json"), credentials: credentials}
	return service, credentials
}

func testProfile(t *testing.T) SavedDevice {
	t.Helper()
	return SavedDevice{Name: "Living room", Username: "pi", Host: "raspberrypi.local", Port: 22,
		AuthMethod: "key", Identity: filepath.Join(t.TempDir(), "id_ed25519"), OutputDirectory: t.TempDir()}
}

func saveProfile(t *testing.T, service *Service, request SaveDeviceRequest) SavedDevice {
	t.Helper()
	config, err := service.SaveDevice(request)
	if err != nil {
		t.Fatal(err)
	}
	device, ok := findSavedDevice(config, config.SelectedDeviceID)
	if !ok {
		t.Fatal("saved device must become selected")
	}
	return device
}

func TestSavedDevicesSurviveRestartAndRename(t *testing.T) {
	service, credentials := profileService(t)
	empty, err := service.LoadDeviceConfig()
	if err != nil || len(empty.Devices) != 0 {
		t.Fatalf("missing file should load empty: %#v, %v", empty, err)
	}
	if _, err := os.Stat(service.profiles.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("loading must not create a config file")
	}
	device := saveProfile(t, service, SaveDeviceRequest{Device: testProfile(t)})
	device.Name = "Office Pi"
	updated := saveProfile(t, service, SaveDeviceRequest{Device: device})
	if updated.ID != device.ID {
		t.Fatal("rename changed the stable device ID")
	}
	restarted := NewService(nil, nil)
	restarted.profiles = &deviceStore{path: service.profiles.path, credentials: credentials}
	config, err := restarted.LoadDeviceConfig()
	if err != nil || len(config.Devices) != 1 || config.Devices[0].Name != "Office Pi" || config.SelectedDeviceID != device.ID {
		t.Fatalf("profile/selection not restored: %#v, %v", config, err)
	}
	info, _ := os.Stat(service.profiles.path)
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
	if _, err := service.SelectSavedDevice(""); err != nil {
		t.Fatal(err)
	}
	config, _ = restarted.LoadDeviceConfig()
	if config.SelectedDeviceID != "" || len(config.Devices) != 1 {
		t.Fatal("clearing selection removed profiles")
	}
}

func TestRememberedPasswordsAreSeparateAndAccountBound(t *testing.T) {
	service, credentials := profileService(t)
	profile := testProfile(t)
	profile.AuthMethod, profile.RememberPassword = "password", true
	device := saveProfile(t, service, SaveDeviceRequest{Device: profile, Password: "test-only-secret"})
	data, _ := os.ReadFile(service.profiles.path)
	if bytes.Contains(data, []byte("test-only-secret")) || bytes.Contains(data, []byte(`"password":`)) || device.Identity != "" {
		t.Fatal("config persisted a password or irrelevant key path")
	}
	connection := core.Connection{Host: "pi@raspberrypi.local", Port: 22}
	password, err := service.SavedDevicePassword(device.ID, connection)
	if err != nil || password != "test-only-secret" {
		t.Fatalf("saved password not retrieved: %v", err)
	}
	for _, changed := range []core.Connection{
		{Host: "PI@raspberrypi.local", Port: 22},
		{Host: "other@raspberrypi.local", Port: 22}, {Host: "pi@other.local", Port: 22},
		{Host: connection.Host, Port: 2222}, {Host: connection.Host, Port: 22, Identity: "/other/key"},
	} {
		if _, err := service.SavedDevicePassword(device.ID, changed); err == nil {
			t.Fatal("retrieved password for a changed SSH account")
		}
	}
	device.Name = "Renamed Pi"
	renamed := saveProfile(t, service, SaveDeviceRequest{Device: device})
	if renamed.CredentialRef != device.CredentialRef {
		t.Fatal("renaming should retain credentials")
	}
	replaced := saveProfile(t, service, SaveDeviceRequest{Device: renamed, Password: "replacement-test-secret"})
	if len(credentials.values) != 1 || credentials.values[replaced.CredentialRef] != credentialValue(replaced, "replacement-test-secret") {
		t.Fatal("replacement did not remove the old credential")
	}
	replaced.RememberPassword = false
	forgotten := saveProfile(t, service, SaveDeviceRequest{Device: replaced})
	if len(credentials.values) != 0 || forgotten.CredentialRef != "" {
		t.Fatal("turning off remembering did not erase the credential")
	}
}

func TestSaveCopyAndDeleteDoNotAffectOtherDeviceOrBackups(t *testing.T) {
	service, credentials := profileService(t)
	profile := testProfile(t)
	profile.AuthMethod, profile.RememberPassword = "password", true
	original := saveProfile(t, service, SaveDeviceRequest{Device: profile, Password: "copy-test-secret"})
	copy := original
	copy.ID, copy.Name = "", "Second destination"
	copy.OutputDirectory = t.TempDir()
	copied := saveProfile(t, service, SaveDeviceRequest{Device: copy, CopyPasswordFromID: original.ID})
	if copied.ID == original.ID || copied.CredentialRef == original.CredentialRef || len(credentials.values) != 2 {
		t.Fatal("copied profile must have independent identity and credentials")
	}
	backupPath := filepath.Join(copied.OutputDirectory, "existing-backup")
	if err := os.WriteFile(backupPath, []byte("backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := service.DeleteSavedDevice(copied.ID)
	if err != nil || len(config.Devices) != 1 || config.SelectedDeviceID != "" || len(credentials.values) != 1 {
		t.Fatalf("unexpected delete result: %#v, %v", config, err)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatal("deleting a profile affected backup data")
	}
	if credentials.values[original.CredentialRef] == "" {
		t.Fatal("deleting a copy removed original credentials")
	}
}

func TestConfigurationErrorsPreserveOriginalFile(t *testing.T) {
	for _, contents := range []string{
		`{`, `{"version":99,"devices":[]}`, `{"version":1,"devices":[]} {}`,
		`{"version":1,"devices":[],"password":"unexpected"}`, `{"version":1,"devices":[],"selectedDeviceId":"missing"}`,
	} {
		t.Run(contents, func(t *testing.T) {
			service, _ := profileService(t)
			if err := os.WriteFile(service.profiles.path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := service.LoadDeviceConfig(); err == nil {
				t.Fatal("invalid file unexpectedly loaded")
			}
			if _, err := service.SaveDevice(SaveDeviceRequest{Device: testProfile(t)}); err == nil {
				t.Fatal("invalid file unexpectedly overwritten")
			}
			data, _ := os.ReadFile(service.profiles.path)
			if string(data) != contents {
				t.Fatal("damaged configuration was modified")
			}
		})
	}
}

func TestCredentialStoreUnavailableDoesNotLeakOrBlockLoading(t *testing.T) {
	service, credentials := profileService(t)
	profile := testProfile(t)
	profile.AuthMethod, profile.RememberPassword = "password", true
	credentials.setErr = errors.New("locked: test-only-secret")
	_, err := service.SaveDevice(SaveDeviceRequest{Device: profile, Password: "test-only-secret"})
	if err == nil || strings.Contains(err.Error(), "test-only-secret") {
		t.Fatal("credential-store errors must be safe and actionable")
	}
	profile.RememberPassword = false
	device := saveProfile(t, service, SaveDeviceRequest{Device: profile})
	credentials.setErr = nil
	device.RememberPassword = true
	device = saveProfile(t, service, SaveDeviceRequest{Device: device, Password: "test-only-secret"})
	credentials.getErr = errors.New("locked: test-only-secret")
	if _, err := service.LoadDeviceConfig(); err != nil {
		t.Fatal("loading settings must not access the credential store")
	}
	_, err = service.SavedDevicePassword(device.ID, core.Connection{Host: "pi@raspberrypi.local", Port: 22})
	if err == nil || strings.Contains(err.Error(), "test-only-secret") {
		t.Fatal("password retrieval errors must not leak secrets")
	}
}

func TestSaveValidationAndConcurrentUpdates(t *testing.T) {
	service, _ := profileService(t)
	profile := testProfile(t)
	first := saveProfile(t, service, SaveDeviceRequest{Device: profile})
	profile.Name = "living ROOM"
	if _, err := service.SaveDevice(SaveDeviceRequest{Device: profile}); err == nil {
		t.Fatal("case-insensitive duplicate names should fail")
	}
	first.Host = "changed.local"
	first.AuthMethod, first.RememberPassword = "password", true
	if _, err := service.SaveDevice(SaveDeviceRequest{Device: first}); err == nil {
		t.Fatal("changed account must require its own password")
	}
	var wait sync.WaitGroup
	for _, name := range []string{"Pi A", "Pi B", "Pi C"} {
		wait.Add(1)
		go func(name string) {
			defer wait.Done()
			device := profile
			device.Name = name
			if _, err := service.SaveDevice(SaveDeviceRequest{Device: device}); err != nil {
				t.Error(err)
			}
		}(name)
	}
	wait.Wait()
	config, err := service.LoadDeviceConfig()
	if err != nil || len(config.Devices) != 4 {
		t.Fatalf("concurrent saves lost profiles: %#v, %v", config, err)
	}
}

func TestBackupDestinationAndKeyPreflight(t *testing.T) {
	service, _ := profileService(t)
	request := core.BackupRequest{Connection: core.Connection{Host: "pi@host"}, OutputDirectory: t.TempDir()}
	if err := service.ValidateBackupDestination(request); err != nil {
		t.Fatal(err)
	}
	request.OutputDirectory = filepath.Join(request.OutputDirectory, "missing-drive")
	if err := service.ValidateBackupDestination(request); err == nil {
		t.Fatal("missing custom destination should fail")
	}
	if _, err := os.Stat(request.OutputDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("preflight recreated a missing custom destination")
	}
	request.OutputDirectory = t.TempDir()
	request.Connection.Identity = filepath.Join(t.TempDir(), "missing-key")
	if err := service.ValidateBackupDestination(request); err == nil {
		t.Fatal("missing SSH key should fail")
	}
}

func TestDeviceJobKeepsSubmittedNameAndSettings(t *testing.T) {
	service, credentials := profileService(t)
	service.engine = &core.Engine{Remote: &testRemote{block: true}}
	profile := testProfile(t)
	profile.Identity = ""
	device := saveProfile(t, service, SaveDeviceRequest{Device: profile})
	job, err := service.StartDeviceBackup(core.BackupRequest{
		Connection: core.Connection{Host: savedConnectionHost(device)}, OutputDirectory: device.OutputDirectory,
	}, device.Name)
	if err != nil {
		t.Fatal(err)
	}
	originalDirectory := device.OutputDirectory
	device.Name, device.Host = "New name", "other.local"
	device.OutputDirectory = t.TempDir()
	saveProfile(t, service, SaveDeviceRequest{Device: device})
	if _, err := service.DeleteSavedDevice(device.ID); err != nil {
		t.Fatal(err)
	}
	current := waitForJob(t, service, job.ID, false)
	if !strings.Contains(current.Title, "Living room") || !strings.HasPrefix(current.Destination, originalDirectory) {
		t.Fatal("profile mutation changed the running job")
	}
	if len(credentials.values) != 0 || !service.CancelJob(job.ID) {
		t.Fatal("unexpected job or credential state")
	}
	waitForJob(t, service, job.ID, true)
}

func TestWriteFailureRollsBackNewCredential(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix folder permissions are used to simulate a write failure")
	}
	service, credentials := profileService(t)
	profile := testProfile(t)
	profile.AuthMethod, profile.RememberPassword = "password", true
	previous := saveProfile(t, service, SaveDeviceRequest{Device: profile, Password: "previous-test-secret"})
	before, _ := os.ReadFile(service.profiles.path)
	directory := filepath.Dir(service.profiles.path)
	credentials.beforeSet = func() { _ = os.Chmod(directory, 0o500) }
	t.Cleanup(func() { _ = os.Chmod(directory, 0o700) })
	_, err := service.SaveDevice(SaveDeviceRequest{Device: previous, Password: "new-test-secret"})
	if err == nil {
		t.Fatal("save should fail in unwritable directory")
	}
	if len(credentials.values) != 1 || credentials.values[previous.CredentialRef] != credentialValue(previous, "previous-test-secret") {
		t.Fatal("failed save did not restore the previous credential")
	}
	after, _ := os.ReadFile(service.profiles.path)
	if !bytes.Equal(before, after) {
		t.Fatal("failed save changed existing config")
	}
}

func TestEditingConfigurationCannotRedirectRememberedPassword(t *testing.T) {
	service, _ := profileService(t)
	profile := testProfile(t)
	profile.AuthMethod, profile.RememberPassword = "password", true
	device := saveProfile(t, service, SaveDeviceRequest{Device: profile, Password: "bound-test-secret"})
	config, err := service.LoadDeviceConfig()
	if err != nil {
		t.Fatal(err)
	}
	config.Devices[0].Host = "changed-by-hand.local"
	if err := service.profiles.write(config); err != nil {
		t.Fatal(err)
	}
	password, err := service.SavedDevicePassword(device.ID, core.Connection{Host: "pi@changed-by-hand.local", Port: 22})
	if err == nil || password != "" {
		t.Fatal("hand-edited account received the original password")
	}
}

func TestCredentialDeletionFailurePreservesProfileForRetry(t *testing.T) {
	service, credentials := profileService(t)
	profile := testProfile(t)
	profile.AuthMethod, profile.RememberPassword = "password", true
	device := saveProfile(t, service, SaveDeviceRequest{Device: profile, Password: "delete-test-secret"})
	before, _ := os.ReadFile(service.profiles.path)
	credentials.deleteErr = errors.New("credential store locked")
	if _, err := service.DeleteSavedDevice(device.ID); err == nil {
		t.Fatal("failed credential deletion must leave the profile available for retry")
	}
	device.RememberPassword = false
	if _, err := service.SaveDevice(SaveDeviceRequest{Device: device}); err == nil {
		t.Fatal("failed credential deletion must not silently disable remembering")
	}
	after, _ := os.ReadFile(service.profiles.path)
	if !bytes.Equal(before, after) || len(credentials.values) != 1 {
		t.Fatal("credential deletion failure changed the profile or password")
	}
}
