// Package config stores the lighting preset the background service enforces.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mschmidt/razerctl/internal/razer"
)

type Config struct {
	// Mouse logo preset; enforced when EnforceMouse is set.
	Mouse        razer.Lighting `json:"mouse_lighting"`
	EnforceMouse bool           `json:"enforce_mouse_lighting"`

	// Dock preset; the dock cannot report its brightness, so it is re-applied
	// whenever the dock (re)appears and whenever its effect differs.
	Dock        razer.Lighting `json:"dock_lighting"`
	EnforceDock bool           `json:"enforce_dock_lighting"`

	// How often the service checks the devices.
	IntervalSeconds int `json:"interval_seconds"`
}

func Default() Config {
	return Config{
		Mouse:           razer.Lighting{Effect: razer.EffectOff, Color: razer.RGB{G: 255}, Color2: razer.RGB{B: 255}, Speed: 2},
		EnforceMouse:    true,
		Dock:            razer.Lighting{Effect: razer.EffectSpectrum, Color: razer.RGB{G: 255}, Color2: razer.RGB{B: 255}, Brightness: 255},
		IntervalSeconds: 3,
	}
}

func (c Config) Interval() time.Duration {
	return time.Duration(max(1, c.IntervalSeconds)) * time.Second
}

func Path() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "razerctl", "config.json")
}

// Load returns the saved config, or the defaults if none exists yet.
func Load() (Config, error) {
	c := Default()
	data, err := os.ReadFile(Path())
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(data, &c)
	return c, err
}

// ModTime is used by the service to notice edits made by the TUI.
func ModTime() time.Time {
	st, err := os.Stat(Path())
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// Save writes atomically so the service never reads a half-written file.
func Save(c Config) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
