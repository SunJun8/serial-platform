package topology

import "strings"

type USBIdentity struct {
	IDPath       string
	IDPathTag    string
	SysfsDevpath string
	VID          string
	PID          string
	Driver       string
	Manufacturer string
	Product      string
}

func ParseUdevProperties(text string) USBIdentity {
	var identity USBIdentity
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "ID_PATH":
			identity.IDPath = value
		case "ID_PATH_TAG":
			identity.IDPathTag = value
		case "DEVPATH":
			identity.SysfsDevpath = value
		case "ID_VENDOR_ID":
			identity.VID = value
		case "ID_MODEL_ID":
			identity.PID = value
		case "ID_USB_DRIVER":
			identity.Driver = value
		case "ID_VENDOR":
			identity.Manufacturer = value
		case "ID_MODEL":
			identity.Product = value
		}
	}
	return identity
}
