package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"serial-platform/internal/topology"
)

type DiscoveredDevice struct {
	DevName      string
	IDPath       string
	IDPathTag    string
	SysfsDevpath string
	Interface    string
	VID          string
	PID          string
	Serial       string
	Driver       string
	Manufacturer string
	Product      string
	PermissionOK bool
	ErrorMessage string
}

type UdevRunner interface {
	Info(devName string) (string, error)
}

type ExecUdevRunner struct{}

func (ExecUdevRunner) Info(devName string) (string, error) {
	out, err := exec.Command("udevadm", "info", "-q", "property", "-n", devName).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("udevadm info %s: %w: %s", devName, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

type DiscoveryConfig struct {
	DevDir string
	Udev   UdevRunner
	User   string
}

func DiscoverDevices(config DiscoveryConfig) ([]DiscoveredDevice, error) {
	devDir := config.DevDir
	if devDir == "" {
		devDir = "/dev"
	}
	udev := config.Udev
	if udev == nil {
		udev = ExecUdevRunner{}
	}

	paths := make([]string, 0)
	for _, pattern := range []string{"ttyUSB*", "ttyACM*"} {
		matches, err := filepath.Glob(filepath.Join(devDir, pattern))
		if err != nil {
			return nil, err
		}
		paths = append(paths, matches...)
	}
	sort.Strings(paths)

	devices := make([]DiscoveredDevice, 0, len(paths))
	for _, path := range paths {
		props, err := udev.Info(path)
		if err != nil {
			devices = append(devices, DiscoveredDevice{
				DevName:      path,
				PermissionOK: canReadWrite(path),
				ErrorMessage: err.Error(),
			})
			continue
		}
		identity := topology.ParseUdevProperties(props)
		devName := identity.DevName
		if devName == "" {
			devName = path
		}
		device := DiscoveredDevice{
			DevName:      devName,
			IDPath:       identity.IDPath,
			IDPathTag:    identity.IDPathTag,
			SysfsDevpath: identity.SysfsDevpath,
			Interface:    identity.Interface,
			VID:          identity.VID,
			PID:          identity.PID,
			Serial:       identity.Serial,
			Driver:       identity.Driver,
			Manufacturer: identity.Manufacturer,
			Product:      identity.Product,
			PermissionOK: canReadWrite(path),
		}
		if !device.PermissionOK {
			device.ErrorMessage = PermissionAdvice(path, config.User)
		}
		devices = append(devices, device)
	}
	return devices, nil
}

func canReadWrite(path string) bool {
	file, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

func PermissionAdvice(devName, user string) string {
	if strings.TrimSpace(user) == "" {
		user = "$USER"
	}
	return fmt.Sprintf("serial device %s is not accessible by current user. Recommended: sudo usermod -aG dialout %s; newgrp dialout; or log out and log in again.", devName, user)
}

var ErrDevicePermission = errors.New("serial device permission denied")
