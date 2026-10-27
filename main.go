// razerctl configures a Razer DeathAdder V2 Pro and its Mouse Dock from
// Linux and keeps the lighting preset applied across reconnects.
//
// It only sends runtime configuration commands; it cannot flash firmware.
package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/giftpilz0/razerctl/internal/config"
	"github.com/giftpilz0/razerctl/internal/daemon"
	"github.com/giftpilz0/razerctl/internal/razer"
	"github.com/giftpilz0/razerctl/internal/setup"
	"github.com/giftpilz0/razerctl/internal/tui"
)

const usage = `usage: razerctl [command]

commands:
  (none), tui   interactive terminal UI
  status        print device state (read-only)
  apply         apply the saved preset once
  daemon        keep the saved preset applied (run as a systemd user service)
  install       install this binary, its udev rule and the user service
                (asks for sudo once for the udev rule; safe to re-run to upgrade)
  uninstall     remove all of that again; add --purge to delete the settings too
`

func main() {
	cmd := "tui"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "tui":
		err = tui.Run()
	case "status":
		err = status()
	case "apply":
		err = apply()
	case "install":
		err = setup.Install()
	case "uninstall":
		purge := len(os.Args) > 2 && os.Args[2] == "--purge"
		err = setup.Uninstall(purge)
	case "daemon":
		log.SetFlags(0) // journald adds timestamps
		err = daemon.Run()
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "razerctl:", err)
		os.Exit(1)
	}
}

func status() error {
	f, err := razer.Scan()
	if err != nil {
		return err
	}
	defer f.Close()
	if f.Mouse == nil && f.Dock == nil {
		return errors.New("no DeathAdder V2 Pro or Mouse Dock found (check udev permissions)")
	}
	show := func(label string, v any, err error) {
		if err != nil {
			fmt.Printf("  %-18s error: %v\n", label, err)
			return
		}
		fmt.Printf("  %-18s %v\n", label, v)
	}
	if m := f.Mouse; m != nil {
		conn := "wireless"
		if m.Wired {
			conn = "wired"
		}
		fmt.Printf("DeathAdder V2 Pro (%s, %s)\n", conn, m.Node.Path)
		v, err := m.Firmware()
		show("firmware", v, err)
		if errors.Is(err, razer.ErrUnreachable) {
			fmt.Println("  mouse is asleep or off; move it and retry")
		} else {
			s, err := m.SerialNumber()
			show("serial", s, err)
			b, err := m.Battery()
			show("battery %", b, err)
			c, err := m.Charging()
			show("charging", c, err)
			x, y, err := m.DPI()
			show("dpi", fmt.Sprintf("%dx%d", x, y), err)
			act, st, err := m.DPIStages()
			show("dpi stages", fmt.Sprintf("active=%d %v", act, st), err)
			p, err := m.PollRate()
			show("poll rate Hz", p, err)
			it, err := m.IdleTime()
			show("idle time s", it, err)
			lb, err := m.LowBattery()
			show("low battery %", lb, err)
			for _, st := range []byte{razer.NoStore, razer.VarStore} {
				l, err := m.Lighting(st)
				show(fmt.Sprintf("logo (slot %d)", st), l, err)
			}
		}
	}
	if d := f.Dock; d != nil {
		fmt.Printf("Mouse Dock (%s)\n", d.Node.Path)
		v, err := d.Firmware()
		show("firmware", v, err)
		s, err := d.SerialNumber()
		show("serial", s, err)
		for _, st := range []byte{razer.NoStore, razer.VarStore} {
			l, err := d.Lighting(st)
			show(fmt.Sprintf("effect (slot %d)", st), l.Effect, err)
		}
	}
	return nil
}

func apply() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	f, err := razer.Scan()
	if err != nil {
		return err
	}
	defer f.Close()
	return daemon.ApplyAll(cfg, f)
}
