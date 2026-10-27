package razer

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/giftpilz0/razerctl/internal/hidraw"
)

const (
	VendorRazer           = 0x1532
	PIDMouseWired         = 0x007C
	PIDMouseWireless      = 0x007D
	PIDDock               = 0x007E
	receiverWait          = 60 * time.Millisecond
	dockWait              = 31 * time.Millisecond
	tid3F            byte = 0x3F
	tidFF            byte = 0xFF // dock serial
)

// Mouse is a DeathAdder V2 Pro, wired or through its receiver.
type Mouse struct {
	*Conn
	Wired bool
}

// Dock is the Razer Mouse Dock.
type Dock struct{ *Conn }

// Found is the result of a scan.
type Found struct {
	Mouse *Mouse // nil if absent
	Dock  *Dock  // nil if absent
}

func (f Found) Close() {
	if f.Mouse != nil {
		_ = f.Mouse.Close()
	}
	if f.Dock != nil {
		_ = f.Dock.Close()
	}
}

// Scan locates the interfaces that answer Razer feature reports. A wired
// connection is preferred over the receiver when both are present.
func Scan() (Found, error) {
	nodes, err := hidraw.Enumerate(VendorRazer)
	if err != nil {
		return Found{}, err
	}
	var f Found
	for _, pid := range []uint16{PIDMouseWired, PIDMouseWireless} {
		if c := probe(nodes, pid, receiverWait, tid3F); c != nil {
			f.Mouse = &Mouse{Conn: c, Wired: pid == PIDMouseWired}
			break
		}
	}
	if c := probe(nodes, PIDDock, dockWait, tid3F); c != nil {
		f.Dock = &Dock{Conn: c}
	}
	return f, nil
}

// probe tries the interfaces of pid (mouse-protocol interface first) and
// returns the first that answers a firmware-version read. Any decoded
// reply counts, including "timeout" from a receiver whose mouse sleeps.
func probe(nodes []hidraw.Node, pid uint16, wait time.Duration, tid byte) *Conn {
	var cands []hidraw.Node
	for _, n := range nodes {
		if n.Product == pid && n.Protocol == 2 {
			cands = append(cands, n)
		}
	}
	for _, n := range nodes {
		if n.Product == pid && n.Protocol != 2 {
			cands = append(cands, n)
		}
	}
	for _, n := range cands {
		c, err := Dial(n, wait)
		if err != nil {
			continue
		}
		_, err = c.FirmwareVersion(tid)
		var se *StatusError
		if err == nil || errors.As(err, &se) {
			return c
		}
		_ = c.Close()
	}
	return nil
}

// ---- mouse ----

func (m *Mouse) Firmware() (string, error)     { return m.FirmwareVersion(tid3F) }
func (m *Mouse) SerialNumber() (string, error) { return m.Serial(tid3F) }

// Battery returns the charge in percent.
func (m *Mouse) Battery() (int, error) {
	r, err := m.Do(Request{TID: tid3F, Class: 0x07, Cmd: 0x80, Size: 0x02})
	if err != nil {
		return 0, err
	}
	return Percent(r.Args[1]), nil
}

func (m *Mouse) Charging() (bool, error) {
	r, err := m.Do(Request{TID: tid3F, Class: 0x07, Cmd: 0x84, Size: 0x02})
	if err != nil {
		return false, err
	}
	return r.Args[1] == 1, nil
}

// Every setter below reads the device first and writes only when the value
// differs, and only to the storage slots that differ. Slot 1 (VarStore) is
// persistent memory, so an unchanged setting must never be sent again.
// Slot 0 (NoStore) is the live, volatile state: the mouse resets it to its
// defaults on reconnect while slot 1 keeps its value. Each setter reports
// whether it wrote anything.

const (
	MinDPI = 100
	MaxDPI = 20000
)

func (m *Mouse) DPI() (x, y int, err error) { return m.dpiAt(NoStore) }

func (m *Mouse) dpiAt(storage byte) (x, y int, err error) {
	r, err := m.Do(Request{TID: tid3F, Class: 0x04, Cmd: 0x85, Size: 0x07, Args: []byte{storage}})
	if err != nil {
		return 0, 0, err
	}
	return int(r.Args[1])<<8 | int(r.Args[2]), int(r.Args[3])<<8 | int(r.Args[4]), nil
}

// SetDPI sets the DPI in each storage slot given. Only NoStore changes what
// the sensor uses; VarStore alone is accepted but changes nothing live.
func (m *Mouse) SetDPI(x, y int, storages ...byte) (bool, error) {
	if x < MinDPI || x > MaxDPI || y < MinDPI || y > MaxDPI {
		return false, fmt.Errorf("DPI must be %d..%d", MinDPI, MaxDPI)
	}
	wrote := false
	for _, st := range storages {
		hx, hy, err := m.dpiAt(st)
		if err != nil {
			return wrote, err
		}
		if hx == x && hy == y {
			continue
		}
		if _, err := m.Do(Request{TID: tid3F, Class: 0x04, Cmd: 0x05, Size: 0x07,
			Args: []byte{st, byte(x >> 8), byte(x), byte(y >> 8), byte(y), 0, 0}}); err != nil {
			return wrote, err
		}
		wrote = true
	}
	return wrote, nil
}

// Stage is one DPI-button stage. ID is the device's own stage number.
type Stage struct {
	ID   byte
	X, Y int
}

const MaxStages = 5

// DPIStages returns the live active stage id and all stages.
func (m *Mouse) DPIStages() (active byte, stages []Stage, err error) {
	return m.stagesAt(NoStore)
}

func (m *Mouse) stagesAt(storage byte) (active byte, stages []Stage, err error) {
	r, err := m.Do(Request{TID: tid3F, Class: 0x04, Cmd: 0x86, Size: 0x26, Args: []byte{storage}})
	if err != nil {
		return 0, nil, err
	}
	n := min(int(r.Args[2]), MaxStages)
	for i := 0; i < n; i++ {
		a := r.Args[3+7*i:]
		stages = append(stages, Stage{ID: a[0], X: int(a[1])<<8 | int(a[2]), Y: int(a[3])<<8 | int(a[4])})
	}
	return r.Args[1], stages, nil
}

// SetDPIStages sets the stages in each storage slot given; active must be
// one of the stage ids.
func (m *Mouse) SetDPIStages(active byte, stages []Stage, storages ...byte) (bool, error) {
	if len(stages) == 0 || len(stages) > MaxStages {
		return false, fmt.Errorf("need 1..%d stages", MaxStages)
	}
	args := []byte{0, active, byte(len(stages))}
	found := false
	for _, s := range stages {
		if s.X < MinDPI || s.X > MaxDPI || s.Y < MinDPI || s.Y > MaxDPI {
			return false, fmt.Errorf("stage DPI must be %d..%d", MinDPI, MaxDPI)
		}
		found = found || s.ID == active
		args = append(args, s.ID, byte(s.X>>8), byte(s.X), byte(s.Y>>8), byte(s.Y), 0, 0)
	}
	if !found {
		return false, fmt.Errorf("active stage %d does not exist", active)
	}
	wrote := false
	for _, st := range storages {
		hActive, have, err := m.stagesAt(st)
		if err != nil {
			return wrote, err
		}
		if hActive == active && slices.Equal(have, stages) {
			continue
		}
		args[0] = st
		if _, err := m.Do(Request{TID: tid3F, Class: 0x04, Cmd: 0x06, Size: 0x26, Args: args}); err != nil {
			return wrote, err
		}
		wrote = true
	}
	return wrote, nil
}

var PollRates = []int{125, 500, 1000}

var pollCodes = map[int]byte{1000: 0x01, 500: 0x02, 125: 0x08}

func (m *Mouse) PollRate() (int, error) {
	r, err := m.Do(Request{TID: tid3F, Class: 0x00, Cmd: 0x85, Size: 0x01})
	if err != nil {
		return 0, err
	}
	for hz, code := range pollCodes {
		if code == r.Args[0] {
			return hz, nil
		}
	}
	return 0, fmt.Errorf("unknown poll rate code 0x%02x", r.Args[0])
}

func (m *Mouse) SetPollRate(hz int) (bool, error) {
	code := pollCodes[hz]
	if code == 0 {
		return false, fmt.Errorf("poll rate must be 125, 500 or 1000")
	}
	r, err := m.Do(Request{TID: tid3F, Class: 0x00, Cmd: 0x85, Size: 0x01})
	if err != nil || r.Args[0] == code {
		return false, err
	}
	_, err = m.Do(Request{TID: tid3F, Class: 0x00, Cmd: 0x05, Size: 0x01, Args: []byte{code}})
	return err == nil, err
}

const (
	MinIdle = 60
	MaxIdle = 900
)

// IdleTime is the seconds of inactivity before the mouse sleeps.
func (m *Mouse) IdleTime() (int, error) {
	r, err := m.Do(Request{TID: tid3F, Class: 0x07, Cmd: 0x83, Size: 0x02})
	if err != nil {
		return 0, err
	}
	return int(r.Args[0])<<8 | int(r.Args[1]), nil
}

func (m *Mouse) SetIdleTime(sec int) (bool, error) {
	if sec < MinIdle || sec > MaxIdle {
		return false, fmt.Errorf("idle time must be %d..%d seconds", MinIdle, MaxIdle)
	}
	have, err := m.IdleTime()
	if err != nil || have == sec {
		return false, err
	}
	_, err = m.Do(Request{TID: tid3F, Class: 0x07, Cmd: 0x03, Size: 0x02, Args: []byte{byte(sec >> 8), byte(sec)}})
	return err == nil, err
}

// The low-battery threshold is a raw 0x0C..0x3F value (~5%..25%).
const (
	MinLowBattery = 5
	MaxLowBattery = 25
)

func (m *Mouse) LowBattery() (int, error) {
	raw, err := m.lowBatteryRaw()
	return Percent(raw), err
}

func (m *Mouse) lowBatteryRaw() (byte, error) {
	r, err := m.Do(Request{TID: tid3F, Class: 0x07, Cmd: 0x81, Size: 0x01})
	return r.Args[0], err
}

func (m *Mouse) SetLowBattery(percent int) (bool, error) {
	raw := byte(max(0x0C, min(0x3F, int(FromPercent(percent)))))
	have, err := m.lowBatteryRaw()
	if err != nil || have == raw {
		return false, err
	}
	_, err = m.Do(Request{TID: tid3F, Class: 0x07, Cmd: 0x01, Size: 0x01, Args: []byte{raw}})
	return err == nil, err
}

// Lighting reads the logo state from one storage slot. Slot 0 (NoStore) is
// what the LED currently shows; slot 1 (VarStore) is the saved setting, which
// the firmware does not load back after a reconnect.
func (m *Mouse) Lighting(storage byte) (Lighting, error) {
	l, err := readEffect(m.Conn, storage, LEDLogo)
	if err != nil {
		return Lighting{}, err
	}
	b, err := m.Do(Request{TID: tid3F, Class: 0x0F, Cmd: 0x84, Size: 0x03, Args: []byte{storage, LEDLogo}})
	if err != nil {
		return Lighting{}, err
	}
	l.Brightness = b.Args[2]
	return l, nil
}

// SetLighting writes the effect and the brightness separately, each only to
// the storage slots where it differs.
func (m *Mouse) SetLighting(l Lighting, storages ...byte) (bool, error) {
	wrote := false
	for _, st := range storages {
		have, err := m.Lighting(st)
		if err != nil {
			return wrote, err
		}
		w, err := writeLighting(m.Conn, LEDLogo, st, l, !SameEffect(l, have), !SameBrightness(l, have))
		wrote = wrote || w
		if err != nil {
			return wrote, err
		}
	}
	return wrote, nil
}

func readEffect(c *Conn, storage, led byte) (Lighting, error) {
	r, err := c.Do(Request{TID: tid3F, Class: 0x0F, Cmd: 0x82, Size: 0x0C, Args: []byte{storage, led}})
	if err != nil {
		return Lighting{}, err
	}
	return decodeEffect(r.Args[:]), nil
}

func writeLighting(c *Conn, led, storage byte, l Lighting, effect, brightness bool) (bool, error) {
	wrote := false
	if effect {
		req, err := l.EffectRequest(tid3F, storage, led)
		if err != nil {
			return false, err
		}
		if _, err := c.Do(req); err != nil {
			return false, err
		}
		wrote = true
	}
	if brightness && l.Effect != EffectOff {
		if _, err := c.Do(BrightnessRequest(tid3F, storage, led, l.Brightness)); err != nil {
			return wrote, err
		}
		wrote = true
	}
	return wrote, nil
}

// ---- dock ----

func (d *Dock) Firmware() (string, error)     { return d.FirmwareVersion(tid3F) }
func (d *Dock) SerialNumber() (string, error) { return d.Serial(tidFF) }

// Lighting reads the dock effect from one storage slot; the slots behave
// like the mouse's. The dock cannot report brightness, so it is always zero.
func (d *Dock) Lighting(storage byte) (Lighting, error) {
	return readEffect(d.Conn, storage, LEDZero)
}

// SetLighting writes the effect to the storage slots where it differs. The
// dock cannot report brightness, so the caller decides: it is written only
// when brightness is set (and never for "off", where it does not matter).
func (d *Dock) SetLighting(l Lighting, brightness bool, storages ...byte) (bool, error) {
	wrote := false
	for _, st := range storages {
		have, err := d.Lighting(st)
		if err != nil {
			return wrote, err
		}
		w, err := writeLighting(d.Conn, LEDZero, st, l, !SameEffect(l, have), brightness)
		wrote = wrote || w
		if err != nil {
			return wrote, err
		}
	}
	return wrote, nil
}
