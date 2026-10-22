// Package daemon keeps the saved lighting preset applied.
//
// It polls with read-only queries and writes only when the device state
// differs from the preset (mouse woke up, reconnected, dock re-plugged).
// It only ever writes the volatile live slot (NoStore), which is what the
// device resets on reconnect; the persistent slot (VarStore) is written
// solely by an explicit apply in the TUI. If a write does not stick, retries
// back off exponentially and stop after maxAttempts, so the device is never
// written to in a loop.
package daemon

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mschmidt/razerctl/internal/config"
	"github.com/mschmidt/razerctl/internal/hidraw"
	"github.com/mschmidt/razerctl/internal/razer"
)

const (
	maxBackoff = 10 * time.Minute
	// maxAttempts writes in a row that do not stick disable writing until
	// the config changes or the device reconnects.
	maxAttempts = 3
	// previewTTL bounds how long a TUI colour preview pauses enforcement, so
	// a crashed or forgotten TUI cannot disable the service for good.
	previewTTL = 2 * time.Minute
)

// PreviewPath is touched by the TUI while it shows an unsaved live preview.
func PreviewPath() string { return hidraw.LockPath() + ".preview" }

// PausePreview pauses enforcement for previewTTL; ResumePreview ends it.
func PausePreview() {
	now := time.Now()
	if os.Chtimes(PreviewPath(), now, now) != nil {
		os.WriteFile(PreviewPath(), nil, 0o600)
	}
}

func ResumePreview() { os.Remove(PreviewPath()) }

func previewActive(now time.Time) bool {
	st, err := os.Stat(PreviewPath())
	return err == nil && now.Sub(st.ModTime()) < previewTTL
}

// ApplyAll puts every enforced part of the preset on the live slot of the
// devices found, writing only what differs. The dock's brightness cannot be
// read back, so it is always sent (to the volatile slot only).
func ApplyAll(cfg config.Config, f razer.Found) error {
	var errs []error
	if f.Mouse != nil && cfg.EnforceMouse {
		_, err := f.Mouse.SetLighting(cfg.Mouse, razer.NoStore)
		errs = append(errs, err)
	}
	if f.Dock != nil && cfg.EnforceDock {
		_, err := f.Dock.SetLighting(cfg.Dock, true, razer.NoStore)
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// limiter tracks retries for one target.
type limiter struct {
	backoff  time.Duration
	next     time.Time
	attempts int // writes since the state last matched the preset
}

func (l *limiter) ready(now time.Time) bool { return l.attempts < maxAttempts && !now.Before(l.next) }

// wrote records a write. Two writes in a row mean the last one did not
// stick, so wait longer before the next; after maxAttempts, stop.
func (l *limiter) wrote(now time.Time, base time.Duration, what string) {
	l.attempts++
	if l.attempts >= maxAttempts {
		log.Printf("%s: %d writes did not stick; not writing again until the config changes or it reconnects", what, l.attempts)
		return
	}
	if l.attempts > 1 {
		l.backoff = min(max(2*l.backoff, base), maxBackoff)
		l.next = now.Add(l.backoff)
	}
}

func (l *limiter) ok() { *l = limiter{} }

type state struct {
	cfg      config.Config
	cfgMod   time.Time
	found    razer.Found
	nodesSig string

	mouseAwake bool
	paused     bool
	dockPath   string // sysfs path of the dock we last configured
	mouseLim   limiter
	dockLim    limiter
}

func Run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config %s: %w", config.Path(), err)
	}
	s := &state{cfg: cfg, cfgMod: config.ModTime()}
	defer func() { s.found.Close() }()
	log.Printf("razerctl daemon: config %s, checking every %s", config.Path(), cfg.Interval())
	log.Printf("mouse preset: %s (enforce=%v); dock preset: %s (enforce=%v)",
		cfg.Mouse, cfg.EnforceMouse, cfg.Dock, cfg.EnforceDock)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	timer := time.NewTimer(0)
	for {
		select {
		case sg := <-sig:
			if sg != syscall.SIGHUP {
				return nil
			}
			s.cfgMod = time.Time{} // force reload
		case <-timer.C:
		}
		s.tick(time.Now())
		timer.Reset(s.cfg.Interval())
	}
}

func (s *state) tick(now time.Time) {
	if mod := config.ModTime(); !mod.Equal(s.cfgMod) {
		cfg, err := config.Load()
		if err != nil {
			log.Printf("config reload failed, keeping previous: %v", err)
		} else {
			if cfg.Dock.Brightness != s.cfg.Dock.Brightness {
				s.dockPath = "" // brightness is unreadable: send it once
			}
			s.cfg = cfg
			log.Printf("config reloaded: mouse %s (enforce=%v); dock %s (enforce=%v)",
				cfg.Mouse, cfg.EnforceMouse, cfg.Dock, cfg.EnforceDock)
			// The preset changed: check immediately.
			s.mouseLim.ok()
			s.dockLim.ok()
		}
		s.cfgMod = mod
	}
	s.rescanIfChanged()
	if p := previewActive(now); p != s.paused {
		s.paused = p
		if p {
			log.Printf("paused: razerctl TUI is previewing lighting")
		} else {
			log.Printf("resumed")
			s.mouseLim.ok()
			s.dockLim.ok()
		}
	}
	if s.paused {
		return
	}
	if m := s.found.Mouse; m != nil && s.cfg.EnforceMouse && s.mouseLim.ready(now) {
		s.checkMouse(now, m)
	}
	if d := s.found.Dock; d != nil && s.cfg.EnforceDock && s.dockLim.ready(now) {
		s.checkDock(now, d)
	}
}

// rescanIfChanged reopens devices when hidraw nodes appear or disappear.
func (s *state) rescanIfChanged() {
	nodes, err := hidraw.Enumerate(razer.VendorRazer)
	if err != nil {
		log.Printf("enumerate: %v", err)
		return
	}
	var sig strings.Builder
	for _, n := range nodes {
		fmt.Fprintf(&sig, "%s=%s;", n.Path, n.SysPath)
	}
	if sig.String() == s.nodesSig {
		return
	}
	s.found.Close()
	f, err := razer.Scan()
	if err != nil {
		log.Printf("scan: %v", err)
		return
	}
	s.found, s.nodesSig = f, sig.String()
	s.mouseAwake = false
	s.mouseLim.ok()
	s.dockLim.ok()
	switch {
	case f.Mouse == nil:
		log.Printf("mouse: not connected")
	case f.Mouse.Wired:
		log.Printf("mouse: wired on %s", f.Mouse.Node.Path)
	default:
		log.Printf("mouse: receiver on %s", f.Mouse.Node.Path)
	}
	if f.Dock != nil {
		log.Printf("dock: %s", f.Dock.Node.Path)
	}
}

func (s *state) checkMouse(now time.Time, m *razer.Mouse) {
	want := s.cfg.Mouse
	have, err := m.Lighting(razer.NoStore)
	if errors.Is(err, razer.ErrUnreachable) {
		if s.mouseAwake {
			log.Printf("mouse: asleep or out of range")
		}
		s.mouseAwake = false
		return
	}
	if err != nil {
		log.Printf("mouse: read lighting: %v", err)
		s.nodesSig = "" // reopen on the next tick
		return
	}
	if !s.mouseAwake {
		log.Printf("mouse: reachable")
	}
	s.mouseAwake = true
	if razer.SameLighting(want, have) {
		s.mouseLim.ok()
		return
	}
	log.Printf("mouse: live slot is %s; want %s -> applying", have, want)
	if _, err := m.SetLighting(want, razer.NoStore); err != nil {
		log.Printf("mouse: apply failed: %v", err)
	}
	s.mouseLim.wrote(now, s.cfg.Interval(), "mouse")
}

func (s *state) checkDock(now time.Time, d *razer.Dock) {
	want := s.cfg.Dock
	have, err := d.Lighting(razer.NoStore)
	if err != nil {
		log.Printf("dock: read lighting: %v", err)
		s.nodesSig = ""
		return
	}
	// Brightness cannot be read back, so it is sent once per connection
	// (or brightness change); after that only the effect is compared.
	fresh := s.dockPath != d.Node.SysPath && want.Effect != razer.EffectOff
	if razer.SameEffect(want, have) && !fresh {
		s.dockPath = d.Node.SysPath
		s.dockLim.ok()
		return
	}
	log.Printf("dock: live slot is %s (new connection: %v); want %s -> applying", have.Effect, fresh, want)
	if _, err := d.SetLighting(want, fresh, razer.NoStore); err != nil {
		log.Printf("dock: apply failed: %v", err)
		return
	}
	s.dockPath = d.Node.SysPath
	s.dockLim.wrote(now, s.cfg.Interval(), "dock")
}
