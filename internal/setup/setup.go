// Package setup installs and removes razerctl for the current user. The
// udev rule and the systemd unit are embedded, so the binary is all a user
// needs.
package setup

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/giftpilz0/razerctl/internal/config"
)

var (
	//go:embed 99-razer-deathadder.rules
	udevRule []byte
	//go:embed razerctl.service
	unitFile []byte
)

const (
	rulePath = "/etc/udev/rules.d/99-razer-deathadder.rules"
	unitName = "razerctl.service"
)

// binPath is where the unit's ExecStart (%h/.local/bin/razerctl) expects us.
func binPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "bin", "razerctl")
}

func unitPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "systemd", "user", unitName)
}

func step(format string, a ...any) { fmt.Printf("· "+format+"\n", a...) }

// run executes a command with the terminal attached (sudo may ask for a
// password).
func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func systemctl(args ...string) error { return run("systemctl", append([]string{"--user"}, args...)...) }

func notRoot() error {
	if os.Geteuid() == 0 {
		return errors.New("run this as your normal user, not with sudo: the service is per-user and sudo is asked for where needed")
	}
	return nil
}

func sameContent(path string, want []byte) bool {
	have, err := os.ReadFile(path)
	return err == nil && bytes.Equal(have, want)
}

// writeAtomic replaces path without ever leaving a partial file, which also
// works while the old binary is running.
func writeAtomic(path string, data []byte, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Chmod(tmp, perm); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Install copies this binary to ~/.local/bin, installs the udev rule (via
// sudo, only if it changed) and enables the user service. It is safe to
// run again, e.g. to upgrade.
func Install() error {
	if err := notRoot(); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	dst := binPath()
	if a, b := statOrNil(self), statOrNil(dst); a != nil && b != nil && os.SameFile(a, b) {
		step("binary already at %s", dst)
	} else {
		data, err := os.ReadFile(self)
		if err != nil {
			return err
		}
		if err := writeAtomic(dst, data, 0o755); err != nil {
			return err
		}
		step("installed binary to %s", dst)
	}

	if sameContent(rulePath, udevRule) {
		step("udev rule up to date")
	} else if err := installRule(); err != nil {
		return err
	}

	if sameContent(unitPath(), unitFile) {
		step("service unit up to date")
	} else if err := writeAtomic(unitPath(), unitFile, 0o644); err != nil {
		return err
	} else {
		step("installed %s", unitPath())
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", unitName}, {"restart", unitName}} {
		if err := systemctl(args...); err != nil {
			return err
		}
	}
	step("service enabled and (re)started")

	if !slices.Contains(filepath.SplitList(os.Getenv("PATH")), filepath.Dir(dst)) {
		fmt.Printf("note: %s is not in your PATH; add it to run 'razerctl' directly\n", filepath.Dir(dst))
	}
	fmt.Println("done: run 'razerctl' for the TUI, 'journalctl --user -u razerctl -f' for the service log")
	return nil
}

func statOrNil(path string) fs.FileInfo {
	st, _ := os.Stat(path)
	return st
}

func installRule() error {
	tmp, err := os.CreateTemp("", "razerctl-udev-*.rules")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(udevRule); err != nil {
		_ = tmp.Close()
		return err
	}
	_ = tmp.Close()
	step("installing udev rule %s (needs sudo)", rulePath)
	if err := run("sudo", "install", "-m", "0644", tmp.Name(), rulePath); err != nil {
		return err
	}
	return reloadUdev()
}

func reloadUdev() error {
	if err := run("sudo", "udevadm", "control", "--reload"); err != nil {
		return err
	}
	return run("sudo", "udevadm", "trigger", "--subsystem-match=hidraw", "--action=change")
}

// Uninstall stops and removes the service, the udev rule and the binary.
// The config is kept unless purge is set.
func Uninstall(purge bool) error {
	if err := notRoot(); err != nil {
		return err
	}
	var errs []error
	// Fails harmlessly if the unit is not installed.
	_ = exec.Command("systemctl", "--user", "disable", "--now", unitName).Run()
	if err := os.Remove(unitPath()); err == nil {
		step("removed %s", unitPath())
	} else if !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, err)
	}
	errs = append(errs, systemctl("daemon-reload"))

	if statOrNil(rulePath) != nil {
		step("removing udev rule %s (needs sudo)", rulePath)
		if err := run("sudo", "rm", "-f", rulePath); err != nil {
			errs = append(errs, err)
		} else {
			errs = append(errs, reloadUdev())
		}
	}

	if err := os.Remove(binPath()); err == nil {
		step("removed %s", binPath())
	} else if !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, err)
	}

	cfgDir := filepath.Dir(config.Path())
	if purge {
		if err := os.RemoveAll(cfgDir); err != nil {
			errs = append(errs, err)
		} else {
			step("removed %s", cfgDir)
		}
	} else if statOrNil(cfgDir) != nil {
		step("kept your settings in %s (use 'uninstall --purge' to remove them)", cfgDir)
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	fmt.Println("razerctl uninstalled")
	return nil
}
