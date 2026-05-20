package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestDiscoverDevicesParsesTTYUSBAndTTYACM(t *testing.T) {
	devDir := t.TempDir()
	ttyUSB := filepath.Join(devDir, "ttyUSB0")
	ttyACM := filepath.Join(devDir, "ttyACM0")
	if err := os.WriteFile(ttyUSB, []byte{}, 0o666); err != nil {
		t.Fatalf("WriteFile ttyUSB returned error: %v", err)
	}
	if err := os.WriteFile(ttyACM, []byte{}, 0o666); err != nil {
		t.Fatalf("WriteFile ttyACM returned error: %v", err)
	}

	runner := fakeUdevRunner{props: map[string]string{
		ttyUSB: "DEVNAME=" + ttyUSB + "\nID_PATH=pci-usb-0:1.1:1.0\nID_PATH_TAG=pci-usb-0_1_1_1_0\nID_USB_INTERFACE_NUM=00\nID_VENDOR_ID=1a86\nID_MODEL_ID=7523\n",
		ttyACM: "DEVNAME=" + ttyACM + "\nID_PATH=pci-usb-0:1.2:1.0\nID_PATH_TAG=pci-usb-0_1_2_1_0\nID_USB_INTERFACE_NUM=01\n",
	}}

	devices, err := DiscoverDevices(DiscoveryConfig{
		DevDir:            devDir,
		Udev:              runner,
		PermissionChecker: allowAllPermissions,
	})
	if err != nil {
		t.Fatalf("DiscoverDevices returned error: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("len(devices) = %d, want 2", len(devices))
	}
	for _, device := range devices {
		if device.IDPath == "" || device.DevName == "" {
			t.Fatalf("device missing identity: %+v", device)
		}
	}
}

func TestDiscoverDevicesReportsPermissionAdviceOnlyForPermissionDenied(t *testing.T) {
	devDir := t.TempDir()
	ttyUSB := filepath.Join(devDir, "ttyUSB0")
	if err := os.WriteFile(ttyUSB, []byte{}, 0o666); err != nil {
		t.Fatalf("WriteFile ttyUSB returned error: %v", err)
	}

	devices, err := DiscoverDevices(DiscoveryConfig{
		DevDir: devDir,
		Udev: fakeUdevRunner{props: map[string]string{
			ttyUSB: "DEVNAME=" + ttyUSB + "\nID_PATH=pci-usb-0:1.1:1.0\n",
		}},
		User: "miot",
		PermissionChecker: func(string) error {
			return syscall.EACCES
		},
	})
	if err != nil {
		t.Fatalf("DiscoverDevices returned error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("len(devices) = %d, want 1", len(devices))
	}
	device := devices[0]
	if device.PermissionOK {
		t.Fatalf("PermissionOK = true, want false")
	}
	if !strings.Contains(device.ErrorMessage, "usermod -aG dialout miot") {
		t.Fatalf("ErrorMessage missing dialout advice: %s", device.ErrorMessage)
	}
	if !errors.Is(device.Error, ErrDevicePermission) {
		t.Fatalf("Error does not wrap ErrDevicePermission: %v", device.Error)
	}
}

func TestDiscoverDevicesKeepsUdevErrorWhenUdevFails(t *testing.T) {
	devDir := t.TempDir()
	ttyUSB := filepath.Join(devDir, "ttyUSB0")
	if err := os.WriteFile(ttyUSB, []byte{}, 0o666); err != nil {
		t.Fatalf("WriteFile ttyUSB returned error: %v", err)
	}

	devices, err := DiscoverDevices(DiscoveryConfig{
		DevDir:            devDir,
		Udev:              fakeUdevRunner{err: errors.New("udev exploded")},
		PermissionChecker: allowAllPermissions,
	})
	if err != nil {
		t.Fatalf("DiscoverDevices returned error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("len(devices) = %d, want 1", len(devices))
	}
	device := devices[0]
	if !device.PermissionOK {
		t.Fatalf("PermissionOK = false, want true")
	}
	if !strings.Contains(device.ErrorMessage, "udev exploded") {
		t.Fatalf("ErrorMessage = %q, want udev error", device.ErrorMessage)
	}
	if strings.Contains(device.ErrorMessage, "dialout") {
		t.Fatalf("ErrorMessage should not contain dialout advice: %s", device.ErrorMessage)
	}
	if device.Error == nil || !strings.Contains(device.Error.Error(), "udev exploded") {
		t.Fatalf("Error = %v, want udev error", device.Error)
	}
}

func TestDiscoverDevicesKeepsNonPermissionAccessError(t *testing.T) {
	devDir := t.TempDir()
	ttyUSB := filepath.Join(devDir, "ttyUSB0")
	if err := os.WriteFile(ttyUSB, []byte{}, 0o666); err != nil {
		t.Fatalf("WriteFile ttyUSB returned error: %v", err)
	}

	devices, err := DiscoverDevices(DiscoveryConfig{
		DevDir: devDir,
		Udev: fakeUdevRunner{props: map[string]string{
			ttyUSB: "DEVNAME=" + ttyUSB + "\nID_PATH=pci-usb-0:1.1:1.0\n",
		}},
		PermissionChecker: func(string) error {
			return errors.New("stat vanished")
		},
	})
	if err != nil {
		t.Fatalf("DiscoverDevices returned error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("len(devices) = %d, want 1", len(devices))
	}
	device := devices[0]
	if device.PermissionOK {
		t.Fatalf("PermissionOK = true, want false")
	}
	if device.ErrorMessage != "stat vanished" {
		t.Fatalf("ErrorMessage = %q, want original access error", device.ErrorMessage)
	}
	if strings.Contains(device.ErrorMessage, "dialout") {
		t.Fatalf("ErrorMessage should not contain dialout advice: %s", device.ErrorMessage)
	}
}

func TestDiscoverDevicesFallsBackToScannedPathWhenDevnameMissing(t *testing.T) {
	devDir := t.TempDir()
	ttyUSB := filepath.Join(devDir, "ttyUSB0")
	if err := os.WriteFile(ttyUSB, []byte{}, 0o666); err != nil {
		t.Fatalf("WriteFile ttyUSB returned error: %v", err)
	}

	devices, err := DiscoverDevices(DiscoveryConfig{
		DevDir: devDir,
		Udev: fakeUdevRunner{props: map[string]string{
			ttyUSB: "ID_PATH=pci-usb-0:1.1:1.0\n",
		}},
		PermissionChecker: allowAllPermissions,
	})
	if err != nil {
		t.Fatalf("DiscoverDevices returned error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("len(devices) = %d, want 1", len(devices))
	}
	if devices[0].DevName != ttyUSB {
		t.Fatalf("DevName = %q, want scanned path %q", devices[0].DevName, ttyUSB)
	}
}

func TestDiscoverDevicesSortsMixedTTYUSBAndTTYACM(t *testing.T) {
	devDir := t.TempDir()
	paths := []string{
		filepath.Join(devDir, "ttyUSB1"),
		filepath.Join(devDir, "ttyACM0"),
		filepath.Join(devDir, "ttyUSB0"),
	}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte{}, 0o666); err != nil {
			t.Fatalf("WriteFile %s returned error: %v", path, err)
		}
	}

	runner := fakeUdevRunner{props: map[string]string{}}
	for _, path := range paths {
		runner.props[path] = "DEVNAME=" + path + "\nID_PATH=id-" + filepath.Base(path) + "\n"
	}

	devices, err := DiscoverDevices(DiscoveryConfig{
		DevDir:            devDir,
		Udev:              runner,
		PermissionChecker: allowAllPermissions,
	})
	if err != nil {
		t.Fatalf("DiscoverDevices returned error: %v", err)
	}

	want := []string{
		filepath.Join(devDir, "ttyACM0"),
		filepath.Join(devDir, "ttyUSB0"),
		filepath.Join(devDir, "ttyUSB1"),
	}
	if len(devices) != len(want) {
		t.Fatalf("len(devices) = %d, want %d", len(devices), len(want))
	}
	for i := range want {
		if devices[i].DevName != want[i] {
			t.Fatalf("devices[%d].DevName = %q, want %q", i, devices[i].DevName, want[i])
		}
	}
}

func TestPermissionAdviceForUnreadableDevice(t *testing.T) {
	advice := PermissionAdvice("/dev/ttyUSB0", "miot")
	if !strings.Contains(advice, "usermod -aG dialout miot") {
		t.Fatalf("advice missing dialout command: %s", advice)
	}
}

type fakeUdevRunner struct {
	props map[string]string
	err   error
}

func (r fakeUdevRunner) Info(devName string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	props, ok := r.props[devName]
	if !ok {
		return "", fmt.Errorf("missing fake udev props for %s", devName)
	}
	return props, nil
}

func allowAllPermissions(string) error {
	return nil
}
