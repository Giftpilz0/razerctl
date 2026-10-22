// Package hidraw finds Razer hidraw nodes through sysfs and exchanges HID
// feature reports with them. It never touches anything but feature reports.
package hidraw

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Node is one hidraw interface of a USB device.
type Node struct {
	Path     string // /dev/hidrawN
	Vendor   uint16
	Product  uint16
	Protocol int    // USB bInterfaceProtocol (1 keyboard, 2 mouse), -1 if unknown
	SysPath  string // resolved sysfs path; changes when the device re-enumerates
}

// Enumerate lists hidraw nodes that belong to vendor, sorted by node number.
func Enumerate(vendor uint16) ([]Node, error) {
	entries, err := os.ReadDir("/sys/class/hidraw")
	if err != nil {
		return nil, err
	}
	var nodes []Node
	for _, e := range entries {
		base := filepath.Join("/sys/class/hidraw", e.Name(), "device")
		vid, pid, ok := readHIDID(filepath.Join(base, "uevent"))
		if !ok || vid != vendor {
			continue
		}
		sys, _ := filepath.EvalSymlinks(base)
		nodes = append(nodes, Node{
			Path:     "/dev/" + e.Name(),
			Vendor:   vid,
			Product:  pid,
			Protocol: readProtocol(sys),
			SysPath:  sys,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodeNum(nodes[i].Path) < nodeNum(nodes[j].Path) })
	return nodes, nil
}

func nodeNum(path string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(filepath.Base(path), "hidraw"))
	return n
}

// readHIDID parses "HID_ID=0003:00001532:0000007D".
func readHIDID(uevent string) (vid, pid uint16, ok bool) {
	data, err := os.ReadFile(uevent)
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		v, found := strings.CutPrefix(line, "HID_ID=")
		if !found {
			continue
		}
		parts := strings.Split(v, ":")
		if len(parts) != 3 {
			return 0, 0, false
		}
		a, err1 := strconv.ParseUint(parts[1], 16, 32)
		b, err2 := strconv.ParseUint(parts[2], 16, 32)
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		return uint16(a), uint16(b), true
	}
	return 0, 0, false
}

// readProtocol reads bInterfaceProtocol from the USB interface that owns the
// HID device (the parent directory of the HID device in sysfs).
func readProtocol(hidSys string) int {
	if hidSys == "" {
		return -1
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(hidSys), "bInterfaceProtocol"))
	if err != nil {
		return -1
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(data)), 16, 32)
	if err != nil {
		return -1
	}
	return int(n)
}

// Device is an open hidraw node.
type Device struct {
	f *os.File
}

func Open(path string) (*Device, error) {
	f, err := os.OpenFile(path, os.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return &Device{f: f}, nil
}

func (d *Device) Close() error { return d.f.Close() }

// HIDIOCSFEATURE(len) / HIDIOCGFEATURE(len): _IOC(READ|WRITE, 'H', nr, len).
func featureIoctl(nr, length uintptr) uintptr {
	return 3<<30 | length<<16 | 'H'<<8 | nr
}

func (d *Device) ioctl(nr uintptr, buf []byte) (int, error) {
	n, _, errno := unix.Syscall(unix.SYS_IOCTL, d.f.Fd(),
		featureIoctl(nr, uintptr(len(buf))), uintptr(unsafe.Pointer(&buf[0])))
	if errno != 0 {
		return 0, errno
	}
	return int(n), nil
}

// SetFeature sends buf, whose first byte is the report ID.
func (d *Device) SetFeature(buf []byte) error {
	n, err := d.ioctl(0x06, buf)
	if err != nil {
		return fmt.Errorf("set feature: %w", err)
	}
	if n != len(buf) {
		return fmt.Errorf("set feature: short write %d/%d", n, len(buf))
	}
	return nil
}

// GetFeature fills buf; buf[0] must hold the report ID on entry.
func (d *Device) GetFeature(buf []byte) error {
	if _, err := d.ioctl(0x07, buf); err != nil {
		return fmt.Errorf("get feature: %w", err)
	}
	return nil
}

// Lock serialises access to Razer devices across processes (the TUI and the
// background service), so request/response pairs never interleave.
type Lock struct{ f *os.File }

func LockPath() string {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "razerctl.lock")
}

func AcquireLock() (*Lock, error) {
	f, err := os.OpenFile(LockPath(), os.O_RDWR|os.O_CREATE|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
}

func (l *Lock) Release() {
	unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	l.f.Close()
}
