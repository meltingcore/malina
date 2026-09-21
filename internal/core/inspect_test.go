package core

import (
	"strings"
	"testing"
)

func inspectionOutput(rootDisk, bootDisk, sudo, direct, passwordless, passwordNeeded, rawAccess string) string {
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
		"directDiskAccess\t" + direct,
		"passwordlessSudo\t" + passwordless,
		"sudoPasswordNeeded\t" + passwordNeeded,
		"rawAccess\t" + rawAccess,
	}, "\n")
}

func TestParseInspectionSupportsSingleDiskLayout(t *testing.T) {
	info, err := parseInspection(inspectionOutput("/dev/mmcblk0", "/dev/mmcblk0", "true", "true", "false", "false", "true"))
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
	info, err := parseInspection(inspectionOutput("/dev/nvme0n1", "/dev/mmcblk0", "true", "false", "true", "false", "true"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Supported || len(info.Warnings) != 1 {
		t.Fatalf("expected one hybrid warning: %#v", info)
	}
	err = assertBackupSupported(info)
	assertErrorCode(t, err, "HYBRID_BOOT_UNSUPPORTED")
}

func TestParseInspectionAllowsDirectAccessWithoutSudo(t *testing.T) {
	info, err := parseInspection(inspectionOutput("/dev/sda", "/dev/sda", "false", "true", "false", "false", "true"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Supported {
		t.Fatalf("expected direct disk access to be supported: %#v", info)
	}
}

func TestParseInspectionReportsPasswordlessSudo(t *testing.T) {
	info, err := parseInspection(inspectionOutput("/dev/sda", "/dev/sda", "true", "false", "true", "false", "true"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Supported || info.DirectDiskAccess || !info.PasswordlessSudo || info.SudoPasswordNeeded {
		t.Fatalf("unexpected passwordless sudo access: %#v", info)
	}
}

func TestParseInspectionReportsSudoPasswordNeeded(t *testing.T) {
	info, err := parseInspection(inspectionOutput("/dev/sda", "/dev/sda", "true", "false", "false", "true", "true"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.Supported || !info.SudoPasswordNeeded {
		t.Fatalf("expected password-backed sudo access: %#v", info)
	}
}

func TestParseInspectionRequiresAPathToRawAccess(t *testing.T) {
	info, err := parseInspection(inspectionOutput("/dev/sda", "/dev/sda", "false", "false", "false", "false", "false"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Supported {
		t.Fatal("expected inspection to be unsupported")
	}
	assertErrorCode(t, assertBackupSupported(info), "RAW_ACCESS_UNAVAILABLE")
}

func TestConnectionRejectsShellMetacharacters(t *testing.T) {
	err := validateConnection(Connection{Host: "pi@host;touch-pwned"})
	assertErrorCode(t, err, "INVALID_HOST")
}

func TestSSHAddress(t *testing.T) {
	username, address, err := sshAddress(Connection{Host: "pi@[2001:db8::10]", Port: 2222, Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if username != "pi" || address != "[2001:db8::10]:2222" {
		t.Fatalf("unexpected SSH target: %q %q", username, address)
	}
}

func TestConnectionAllowsKeyAndPassword(t *testing.T) {
	err := validateConnection(Connection{Host: "user@host", Identity: "/tmp/key", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLinuxInspectionUsesProcAndSys(t *testing.T) {
	for _, forbidden := range []string{"findmnt", "lsblk"} {
		if strings.Contains(inspectScript, forbidden) {
			t.Fatalf("inspection still depends on %s", forbidden)
		}
	}
	for _, required := range []string{"/proc/self/mountinfo", "/sys/dev/block", "/sys/class/block"} {
		if !strings.Contains(inspectScript, required) {
			t.Fatalf("inspection does not use %s", required)
		}
	}
}

func TestLinuxDiskReadCommandUsesOrderedFallbacksAndPortableDD(t *testing.T) {
	command := linuxDiskReadCommand("/dev/mmcblk0", true)
	for _, forbidden := range []string{"iflag=", "status="} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("command contains non-portable option %q: %s", forbidden, command)
		}
	}
	for _, required := range []string{"dd if=/dev/mmcblk0 bs=4194304", "sudo -n -- dd", "sudo -S -p ''", "uname -s"} {
		if !strings.Contains(command, required) {
			t.Fatalf("command is missing %q: %s", required, command)
		}
	}
	direct := strings.Index(command, "elif dd if=/dev/mmcblk0")
	passwordless := strings.Index(command, "elif command -v sudo")
	password := strings.LastIndex(command, "exec sudo -S")
	if direct < 0 || passwordless <= direct || password <= passwordless {
		t.Fatalf("disk access fallbacks are out of order: %s", command)
	}
}

func TestLinuxDiskReadCommandWithoutPasswordFailsClearly(t *testing.T) {
	command := linuxDiskReadCommand("/dev/mmcblk0", false)
	if strings.Contains(command, "exec sudo -S") {
		t.Fatalf("command must not try password sudo without a password: %s", command)
	}
	if !strings.Contains(command, "requires a sudo password") {
		t.Fatalf("command does not explain why access failed: %s", command)
	}
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
