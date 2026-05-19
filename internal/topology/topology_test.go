package topology

import (
	"strings"
	"testing"
)

func TestParseUdevProperties(t *testing.T) {
	props := ParseUdevProperties(`ID_PATH=pci-0000:00:14.0-usb-0:1.2:1.0
ID_PATH_TAG=pci-0000_00_14_0-usb-0_1_2_1_0
DEVPATH=/devices/pci0000:00/0000:00:14.0/usb1/1-1/1-1.2/ttyUSB0
ID_VENDOR_ID=1a86
ID_MODEL_ID=7523
ID_USB_DRIVER=ch341
ID_VENDOR=QinHeng_Electronics
ID_MODEL=USB2.0-Serial
IGNORED=value
`)
	if props.IDPath != "pci-0000:00:14.0-usb-0:1.2:1.0" {
		t.Fatalf("IDPath = %q", props.IDPath)
	}
	if props.IDPathTag != "pci-0000_00_14_0-usb-0_1_2_1_0" {
		t.Fatalf("IDPathTag = %q", props.IDPathTag)
	}
	if props.SysfsDevpath != "/devices/pci0000:00/0000:00:14.0/usb1/1-1/1-1.2/ttyUSB0" {
		t.Fatalf("SysfsDevpath = %q", props.SysfsDevpath)
	}
	if props.VID != "1a86" || props.PID != "7523" {
		t.Fatalf("VID/PID = %q/%q", props.VID, props.PID)
	}
	if props.Driver != "ch341" {
		t.Fatalf("Driver = %q", props.Driver)
	}
	if props.Manufacturer != "QinHeng_Electronics" {
		t.Fatalf("Manufacturer = %q", props.Manufacturer)
	}
	if props.Product != "USB2.0-Serial" {
		t.Fatalf("Product = %q", props.Product)
	}
}

func TestRenderUdevRuleUsesIDPathTag(t *testing.T) {
	rule, err := RenderUdevRule(ChannelRule{
		IDPathTag: "pci-0000_00_14_0-usb-0_1_2_1_0",
		Symlink:   "lab/host01/hub01/port02/console",
	})
	if err != nil {
		t.Fatalf("RenderUdevRule returned error: %v", err)
	}
	if !strings.Contains(rule, `ENV{ID_PATH_TAG}=="pci-0000_00_14_0-usb-0_1_2_1_0"`) {
		t.Fatalf("rule missing ID_PATH_TAG: %s", rule)
	}
	if !strings.Contains(rule, `SYMLINK+="lab/host01/hub01/port02/console"`) {
		t.Fatalf("rule missing symlink: %s", rule)
	}
	if !strings.HasSuffix(rule, "\n") {
		t.Fatalf("rule missing trailing newline: %q", rule)
	}
}

func TestRenderUdevRuleRejectsEmptyInput(t *testing.T) {
	if _, err := RenderUdevRule(ChannelRule{Symlink: "lab/console"}); err == nil {
		t.Fatal("RenderUdevRule with empty IDPathTag returned nil error")
	}
	if _, err := RenderUdevRule(ChannelRule{IDPathTag: "pci-0000_00_14_0-usb-0_1_2_1_0"}); err == nil {
		t.Fatal("RenderUdevRule with empty Symlink returned nil error")
	}
}
