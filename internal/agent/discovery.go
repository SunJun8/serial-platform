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
	Error        error `json:"-"`
}

type UdevRunner interface {
	Info(devName string) (string, error)
}

type PermissionChecker func(path string) error

type ExecUdevRunner struct{}

func (ExecUdevRunner) Info(devName string) (string, error) {
	out, err := exec.Command("udevadm", "info", "-q", "property", "-n", devName).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("udevadm info %s: %w: %s", devName, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

type DiscoveryConfig struct {
	DevDir            string
	Udev              UdevRunner
	User              string
	PermissionChecker PermissionChecker
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
	checkPermission := config.PermissionChecker
	if checkPermission == nil {
		checkPermission = checkDevicePermission
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
		permissionErr := normalizePermissionError(checkPermission(path))
		permissionOK := permissionErr == nil
		props, err := udev.Info(path)
		if err != nil {
			device := DiscoveredDevice{
				DevName:      path,
				PermissionOK: permissionOK,
				ErrorMessage: err.Error(),
				Error:        err,
			}
			if !permissionOK {
				device.Error = errors.Join(err, permissionErr)
			}
			devices = append(devices, device)
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
			PermissionOK: permissionOK,
		}
		if !permissionOK {
			device.Error = permissionErr
		}
		if errors.Is(permissionErr, ErrDevicePermission) {
			device.ErrorMessage = PermissionAdvice(path, config.User)
		} else if permissionErr != nil {
			device.ErrorMessage = permissionErr.Error()
		}
		devices = append(devices, device)
	}
	return devices, nil
}

func checkDevicePermission(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	const readWriteAccess = 0x6 // R_OK | W_OK for access(2).
	return syscall.Access(path, readWriteAccess)
}

func normalizePermissionError(err error) error {
	if err == nil || errors.Is(err, ErrDevicePermission) {
		return err
	}
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		return fmt.Errorf("%w: %s", ErrDevicePermission, err)
	}
	return err
}

func PermissionAdvice(devName, user string) string {
	if strings.TrimSpace(user) == "" {
		user = "$USER"
	}
	return fmt.Sprintf("serial device %s is not accessible by current user. Recommended: sudo usermod -aG dialout %s; newgrp dialout; or log out and log in again.", devName, user)
}

var ErrDevicePermission = errors.New("serial device permission denied")
