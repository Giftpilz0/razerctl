package razer

import (
	"fmt"
	"testing"
)

// fakeDevice stores what each set command writes and answers the matching
// get command with it, which is how the real device mirrors its settings.
type fakeDevice struct {
	state  map[string][]byte
	writes []string // "class:cmd slot" of every set command received
	reply  []byte
	status Status // answer with this instead of OK when set
}

func newFake(t *testing.T) (*fakeDevice, *Conn) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir()) // for hidraw.AcquireLock
	f := &fakeDevice{state: map[string][]byte{}}
	return f, &Conn{dev: f}
}

// key identifies a setting: the get command plus its storage/LED selector.
func key(class, cmd byte, args []byte) string {
	cmd |= 0x80
	switch class {
	case 0x0F:
		return fmt.Sprintf("%02x:%02x %v", class, cmd, args[:2])
	case 0x04:
		return fmt.Sprintf("%02x:%02x %v", class, cmd, args[:1])
	}
	return fmt.Sprintf("%02x:%02x", class, cmd)
}

// seed sets device state without counting it as a write.
func (f *fakeDevice) seed(r Request) { f.state[key(r.Class, r.Cmd, r.Args)] = r.Args }

func (f *fakeDevice) SetFeature(b []byte) error {
	r, _ := decode(b[1:])
	req := Request{TID: r.TID, Class: r.Class, Cmd: r.Cmd, Size: r.Size, Args: r.Args[:]}
	if r.Cmd&0x80 == 0 {
		f.seed(req)
		f.writes = append(f.writes, fmt.Sprintf("%02x:%02x slot %d", r.Class, r.Cmd, r.Args[0]))
	} else if v, ok := f.state[key(r.Class, r.Cmd, r.Args[:])]; ok {
		req.Args = v
	}
	enc, _ := req.Encode()
	enc[0] = byte(StatusOK)
	if f.status != 0 {
		enc[0] = byte(f.status)
	}
	f.reply = enc[:]
	return nil
}

func (f *fakeDevice) GetFeature(b []byte) error { copy(b[1:], f.reply); return nil }
func (f *fakeDevice) Close() error              { return nil }

func seedLighting(f *fakeDevice, led, storage byte, l Lighting) {
	req, _ := l.EffectRequest(tid3F, storage, led)
	f.seed(req)
	f.seed(BrightnessRequest(tid3F, storage, led, l.Brightness))
}

func expectWrites(t *testing.T, f *fakeDevice, want ...string) {
	t.Helper()
	if fmt.Sprint(f.writes) != fmt.Sprint(want) {
		t.Fatalf("writes %v, want %v", f.writes, want)
	}
	f.writes = nil
}

func TestUnchangedSettingsAreNotWritten(t *testing.T) {
	f, c := newFake(t)
	m := &Mouse{Conn: c}
	logo := Lighting{Effect: EffectStatic, Color: RGB{1, 2, 3}, Brightness: 100}
	stages := []Stage{{1, 1600, 1600}, {2, 3200, 3200}}
	for _, st := range []byte{NoStore, VarStore} {
		seedLighting(f, LEDLogo, st, logo)
		f.seed(Request{Class: 0x04, Cmd: 0x05, Args: []byte{st, 0x0c, 0x80, 0x0c, 0x80}})
		f.seed(Request{Class: 0x04, Cmd: 0x06, Args: []byte{st, 2, 2, 1, 0x06, 0x40, 0x06, 0x40, 0, 0, 2, 0x0c, 0x80, 0x0c, 0x80}})
	}
	f.seed(Request{Class: 0x00, Cmd: 0x05, Args: []byte{0x01}})
	f.seed(Request{Class: 0x07, Cmd: 0x03, Args: []byte{0x01, 0x2c}})
	f.seed(Request{Class: 0x07, Cmd: 0x01, Args: []byte{FromPercent(10)}})

	for name, set := range map[string]func() (bool, error){
		"lighting":    func() (bool, error) { return m.SetLighting(logo, NoStore, VarStore) },
		"dpi":         func() (bool, error) { return m.SetDPI(3200, 3200, NoStore, VarStore) },
		"stages":      func() (bool, error) { return m.SetDPIStages(2, stages, NoStore, VarStore) },
		"poll rate":   func() (bool, error) { return m.SetPollRate(1000) },
		"idle":        func() (bool, error) { return m.SetIdleTime(300) },
		"low battery": func() (bool, error) { return m.SetLowBattery(10) },
	} {
		wrote, err := set()
		if err != nil || wrote {
			t.Errorf("%s: wrote=%v err=%v", name, wrote, err)
		}
	}
	expectWrites(t, f)
}

func TestOnlyDifferingPartsAreWritten(t *testing.T) {
	f, c := newFake(t)
	m := &Mouse{Conn: c}
	saved := Lighting{Effect: EffectStatic, Color: RGB{1, 2, 3}, Brightness: 100}
	seedLighting(f, LEDLogo, NoStore, Lighting{Effect: EffectSpectrum, Brightness: 100}) // reset by a reconnect
	seedLighting(f, LEDLogo, VarStore, saved)

	// Only the live slot's effect differs.
	if _, err := m.SetLighting(saved, NoStore, VarStore); err != nil {
		t.Fatal(err)
	}
	expectWrites(t, f, "0f:02 slot 0")

	// Brightness alone changes: no effect writes.
	dimmer := saved
	dimmer.Brightness = 50
	if _, err := m.SetLighting(dimmer, NoStore, VarStore); err != nil {
		t.Fatal(err)
	}
	expectWrites(t, f, "0f:04 slot 0", "0f:04 slot 1")

	// Brightness does not matter while off.
	seedLighting(f, LEDLogo, NoStore, Lighting{Effect: EffectOff, Brightness: 10})
	if _, err := m.SetLighting(Lighting{Effect: EffectOff, Brightness: 200}, NoStore); err != nil {
		t.Fatal(err)
	}
	expectWrites(t, f)

	// Second identical call writes nothing.
	if _, err := m.SetDPI(800, 800, NoStore); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetDPI(800, 800, NoStore); err != nil {
		t.Fatal(err)
	}
	expectWrites(t, f, "04:05 slot 0")
}

func TestDockBrightnessOnlyWhenAsked(t *testing.T) {
	f, c := newFake(t)
	d := &Dock{Conn: c}
	l := Lighting{Effect: EffectStatic, Color: RGB{9, 9, 9}, Brightness: 255}
	seedLighting(f, LEDZero, NoStore, l)
	if wrote, _ := d.SetLighting(l, false, NoStore); wrote {
		t.Fatal("unchanged dock written")
	}
	if _, err := d.SetLighting(l, true, NoStore); err != nil {
		t.Fatal(err)
	}
	expectWrites(t, f, "0f:04 slot 0")
}

func TestFailedWriteIsNotResent(t *testing.T) {
	f, c := newFake(t)
	m := &Mouse{Conn: c}
	f.seed(Request{Class: 0x07, Cmd: 0x03, Args: []byte{0x01, 0x2c}})
	f.status = StatusFailure
	if _, err := m.SetIdleTime(600); err == nil {
		t.Fatal("failure not reported")
	}
	// The read is retried, but the write must have been sent once.
	if len(f.writes) != 0 {
		t.Fatalf("read failed, yet wrote %v", f.writes)
	}
	f.status = 0
	if _, err := m.Do(Request{Class: 0x07, Cmd: 0x83}); err != nil { // prime the read
		t.Fatal(err)
	}
	f.status = StatusFailure
	if _, err := c.Do(Request{TID: tid3F, Class: 0x07, Cmd: 0x03, Size: 2, Args: []byte{0x02, 0x58}}); err == nil {
		t.Fatal("failure not reported")
	}
	expectWrites(t, f, "07:03 slot 2")
}
