package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	devices, err := DiscoverDevices(DiscoveryConfig{DevDir: devDir, Udev: runner})
	if err != nil {
		t.Fatalf("DiscoverDevices returned error: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("len(devices) = %d, want 2", len(devices))
	}
	if devices[0].IDPath == "" || devices[0].DevName == "" {
		t.Fatalf("device missing identity: %+v", devices[0])
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
}

func (r fakeUdevRunner) Info(devName string) (string, error) {
	props, ok := r.props[devName]
	if !ok {
		return "", fmt.Errorf("missing fake udev props for %s", devName)
	}
	return props, nil
}
