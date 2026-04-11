// Package mobilebridge implements a CDP (Chrome DevTools Protocol) bridge for
// Android Chrome over ADB. See the repo README for an overview.
package mobilebridge

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// Device describes a single Android device as reported by `adb devices -l`.
type Device struct {
	Serial  string
	State   string // "device", "offline", "unauthorized", ...
	Model   string
	Product string
}

// commandRunner runs an external command and returns its combined output.
// It is a package-level variable so tests can stub it out without a real adb
// binary being present.
var commandRunner = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// adbPath is the executable used for ADB calls. Tests may override it.
var adbPath = "adb"

func runADB(args ...string) ([]byte, error) {
	return commandRunner(context.Background(), adbPath, args...)
}

// ListDevices runs `adb devices -l` and parses the result.
func ListDevices() ([]Device, error) {
	out, err := runADB("devices", "-l")
	if err != nil {
		return nil, fmt.Errorf("adb devices: %w: %s", err, string(out))
	}
	return parseDevices(string(out)), nil
}

// parseDevices parses the textual output of `adb devices -l`.
//
// Example input:
//
//	List of devices attached
//	R58N12ABCDE    device usb:336592896X product:starqltesq model:SM_G960U device:starqltesq transport_id:1
//	emulator-5554  offline
func parseDevices(out string) []Device {
	var devices []Device
	lines := strings.Split(out, "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "List of devices") {
			continue
		}
		if strings.HasPrefix(line, "*") {
			// daemon log lines like "* daemon not running; starting now"
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		d := Device{Serial: fields[0], State: fields[1]}
		for _, kv := range fields[2:] {
			parts := strings.SplitN(kv, ":", 2)
			if len(parts) != 2 {
				continue
			}
			switch parts[0] {
			case "model":
				d.Model = parts[1]
			case "product":
				d.Product = parts[1]
			}
		}
		devices = append(devices, d)
	}
	return devices
}

// Forward runs `adb -s <serial> forward tcp:<localPort> localabstract:<remoteAbstract>`.
func Forward(serial string, localPort int, remoteAbstract string) error {
	if serial == "" {
		return errors.New("mobilebridge: empty serial")
	}
	out, err := runADB("-s", serial, "forward",
		fmt.Sprintf("tcp:%d", localPort),
		"localabstract:"+remoteAbstract,
	)
	if err != nil {
		return fmt.Errorf("adb forward: %w: %s", err, string(out))
	}
	return nil
}

// Unforward runs `adb -s <serial> forward --remove tcp:<localPort>`.
func Unforward(serial string, localPort int) error {
	if serial == "" {
		return errors.New("mobilebridge: empty serial")
	}
	out, err := runADB("-s", serial, "forward", "--remove", fmt.Sprintf("tcp:%d", localPort))
	if err != nil {
		return fmt.Errorf("adb forward --remove: %w: %s", err, string(out))
	}
	return nil
}

// devtoolsSocketRe matches abstract socket names for Chrome's devtools socket.
// Typical forms: "chrome_devtools_remote" and "webview_devtools_remote_<pid>".
var devtoolsSocketRe = regexp.MustCompile(`@((?:chrome|webview)_devtools_remote[_A-Za-z0-9]*)`)

// ChromeDevtoolsSocket queries /proc/net/unix on the device and returns the
// name of the abstract socket that Chrome (or a WebView host) is listening on.
func ChromeDevtoolsSocket(serial string) (string, error) {
	if serial == "" {
		return "", errors.New("mobilebridge: empty serial")
	}
	out, err := runADB("-s", serial, "shell", "cat", "/proc/net/unix")
	if err != nil {
		return "", fmt.Errorf("adb shell cat /proc/net/unix: %w: %s", err, string(out))
	}
	name, ok := parseDevtoolsSocket(string(out))
	if !ok {
		return "", errors.New("mobilebridge: no chrome devtools socket found on device")
	}
	return name, nil
}

// parseDevtoolsSocket scans /proc/net/unix output for a devtools abstract
// socket. Abstract sockets in that file are prefixed with '@'.
//
// Preference order: chrome_devtools_remote (regular Chrome tabs) wins over
// webview_devtools_remote_<pid> (a WebView host process), since Chrome itself
// is almost always what a caller wants.
func parseDevtoolsSocket(procNetUnix string) (string, bool) {
	var webview string
	for _, line := range strings.Split(procNetUnix, "\n") {
		m := devtoolsSocketRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		if name == "chrome_devtools_remote" || name == "chrome_devtools_remote_0" {
			return name, true
		}
		if strings.HasPrefix(name, "chrome_devtools_remote") {
			return name, true
		}
		if webview == "" && strings.HasPrefix(name, "webview_devtools_remote") {
			webview = name
		}
	}
	if webview != "" {
		return webview, true
	}
	return "", false
}
