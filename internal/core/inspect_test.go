package core

import (
	"strings"
	"testing"
)

func inspectionOutput(rootDisk, bootDisk, sudo string) string {
	return strings.Join([]string{
		"hostname\tpi-lab",
		"model\tRaspberry Pi 5 Model B Rev 1.0",
		"os\tRaspberry Pi OS",
		"architecture\taarch64",
		"rootSource\t" + rootDisk + "p2",
		"rootDisk\t" + rootDisk,
		"rootFilesystem\text4",
		"bootSource\t" + bootDisk + "p1",
		"bootDisk\t" + bootDisk,
		"diskSize\t68719476736",
		"logicalSectorSize\t512",
		"sudoAvailable\t" + sudo,
	}, "\n")
}

func TestParseInspectionSupportsSingleDiskLayout(t *testing.T) {
	info, err := parseInspection(inspectionOutput("/dev/mmcblk0", "/dev/mmcblk0", "true"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Supported || info.RootDisk != "/dev/mmcblk0" || info.DiskSize != 68719476736 {
		t.Fatalf("unexpected inspection: %#v", info)
	}
	if len(info.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", info.Warnings)
	}
}

func TestParseInspectionRejectsHybridBootLayout(t *testing.T) {
	info, err := parseInspection(inspectionOutput("/dev/nvme0n1", "/dev/mmcblk0", "true"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Supported || len(info.Warnings) != 1 {
		t.Fatalf("expected one hybrid warning: %#v", info)
	}
	err = assertBackupSupported(info)
	assertErrorCode(t, err, "HYBRID_BOOT_UNSUPPORTED")
}

func TestParseInspectionRequiresPasswordlessSudo(t *testing.T) {
	info, err := parseInspection(inspectionOutput("/dev/sda", "/dev/sda", "false"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Supported {
		t.Fatal("expected inspection to be unsupported")
	}
	assertErrorCode(t, assertBackupSupported(info), "SUDO_REQUIRED")
}

func TestConnectionRejectsShellMetacharacters(t *testing.T) {
	_, err := sshArgs(Connection{Host: "pi@host;touch-pwned"}, "true")
	assertErrorCode(t, err, "INVALID_HOST")
}

func TestPasswordSSHAddress(t *testing.T) {
	username, address, err := passwordSSHAddress(Connection{Host: "pi@[2001:db8::10]", Port: 2222, Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if username != "pi" || address != "[2001:db8::10]:2222" {
		t.Fatalf("unexpected SSH target: %q %q", username, address)
	}
}

func TestConnectionRejectsConflictingAuthentication(t *testing.T) {
	err := validateConnection(Connection{Host: "pi@raspberrypi.local", Identity: "/tmp/key", Password: "secret"})
	assertErrorCode(t, err, "AUTH_CONFLICT")
}

func assertErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s error", code)
	}
	typed, ok := err.(*Error)
	if !ok || typed.Code != code {
		t.Fatalf("expected %s, got %#v", code, err)
	}
}
