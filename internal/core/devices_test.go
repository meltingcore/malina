package core

import "testing"

func TestParseLinuxDevicesReturnsOnlyExternalWholeDisks(t *testing.T) {
	payload := `{"blockdevices":[
    {"name":"nvme0n1","path":"/dev/nvme0n1","size":1000,"model":"System","tran":"nvme","rm":false,"type":"disk"},
    {"name":"sda","path":"/dev/sda","size":2000,"model":"USB Reader","tran":"usb","rm":false,"type":"disk"},
    {"name":"sdb","path":"/dev/sdb","size":3000,"model":"Removable","tran":"","rm":1,"type":"disk"},
    {"name":"sda1","path":"/dev/sda1","size":1500,"model":"","tran":"usb","rm":true,"type":"part"}
  ]}`
	devices, err := parseLinuxDevices(payload, map[string]bool{"/dev/nvme0n1": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 || devices[0].Path != "/dev/sda" || devices[1].Path != "/dev/sdb" {
		t.Fatalf("unexpected devices: %#v", devices)
	}
}

func TestParseWindowsDevicesExcludesSystemAndNonExternalDisks(t *testing.T) {
	payload := `[
    {"Number":0,"FriendlyName":"System","Size":1000,"BusType":"NVMe","IsBoot":true,"IsSystem":true},
    {"Number":2,"FriendlyName":"SD card","Size":2000,"BusType":"USB","IsBoot":false,"IsSystem":false},
    {"Number":3,"FriendlyName":"Internal","Size":3000,"BusType":"SATA","IsBoot":false,"IsSystem":false}
  ]`
	devices, err := parseWindowsDevices(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Path != `\\.\PhysicalDrive2` {
		t.Fatalf("unexpected devices: %#v", devices)
	}
}

func TestParseMacDeviceInfoExcludesInternalDisk(t *testing.T) {
	external := `<?xml version="1.0"?><plist><dict>
    <key>DeviceIdentifier</key><string>disk4</string>
    <key>TotalSize</key><integer>64000000000</integer>
    <key>Internal</key><false/><key>Whole</key><true/>
    <key>MediaName</key><string>SD Card</string><key>BusProtocol</key><string>USB</string>
  </dict></plist>`
	device, ok := parseMacDeviceInfo(external)
	if !ok || device.Path != "/dev/disk4" || device.Bytes != 64000000000 {
		t.Fatalf("unexpected device: %#v, %v", device, ok)
	}
	internal := `<?xml version="1.0"?><plist><dict>
    <key>DeviceIdentifier</key><string>disk0</string><key>TotalSize</key><integer>1000</integer>
    <key>Internal</key><true/><key>Whole</key><true/>
  </dict></plist>`
	if _, ok := parseMacDeviceInfo(internal); ok {
		t.Fatal("internal disk must not be returned")
	}
}

func TestSafeDevicePathsRejectPartitionsAndArguments(t *testing.T) {
	for _, path := range []string{"/dev/sda1", "/dev/disk4s1", "/dev/sda --help", `\\.\PhysicalDrive2\extra`} {
		if err := assertSafeDevicePath(path); err == nil {
			t.Fatalf("expected %q to be rejected", path)
		}
	}
	for _, path := range []string{"/dev/sda", "/dev/mmcblk0", "/dev/nvme0n1", "/dev/disk4", `\\.\PhysicalDrive2`} {
		if err := assertSafeDevicePath(path); err != nil {
			t.Fatalf("expected %q to be accepted: %v", path, err)
		}
	}
}
