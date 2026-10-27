package razer

import (
	"fmt"
	"strconv"
	"strings"
)

// RGB is a colour.
type RGB struct{ R, G, B uint8 }

func (c RGB) String() string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

func (c RGB) MarshalText() ([]byte, error) { return []byte(c.String()), nil }

func (c *RGB) UnmarshalText(b []byte) error {
	v, err := ParseRGB(string(b))
	if err != nil {
		return err
	}
	*c = v
	return nil
}

// ParseRGB accepts "#rrggbb" or "rrggbb".
func ParseRGB(s string) (RGB, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return RGB{}, fmt.Errorf("colour %q: want #rrggbb", s)
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return RGB{}, fmt.Errorf("colour %q: %w", s, err)
	}
	return RGB{uint8(v >> 16), uint8(v >> 8), uint8(v)}, nil
}

// Effect names an extended-matrix lighting effect.
type Effect string

const (
	EffectOff          Effect = "off"
	EffectStatic       Effect = "static"
	EffectBreath       Effect = "breath"
	EffectBreathDual   Effect = "breath-dual"
	EffectBreathRandom Effect = "breath-random"
	EffectSpectrum     Effect = "spectrum"
	EffectReactive     Effect = "reactive"
)

// Effect ids on the wire.
const (
	fxNone     = 0x00
	fxStatic   = 0x01
	fxBreath   = 0x02
	fxSpectrum = 0x03
	fxReactive = 0x05
)

// MouseEffects and DockEffects list what each device accepts (per OpenRazer).
var (
	MouseEffects = []Effect{EffectOff, EffectStatic, EffectBreath, EffectBreathDual, EffectBreathRandom, EffectSpectrum, EffectReactive}
	DockEffects  = []Effect{EffectOff, EffectStatic, EffectBreath, EffectBreathDual, EffectBreathRandom, EffectSpectrum}
)

// Lighting is a complete lighting state for one LED zone.
type Lighting struct {
	Effect     Effect `json:"effect"`
	Color      RGB    `json:"color"`
	Color2     RGB    `json:"color2"`
	Speed      uint8  `json:"speed,omitempty"` // reactive: 1 (short) .. 4 (long)
	Brightness uint8  `json:"brightness"`      // 0..255
}

// UsesColor/UsesColor2/UsesSpeed say which fields matter for the effect.
func (l Lighting) UsesColor() bool {
	switch l.Effect {
	case EffectStatic, EffectBreath, EffectBreathDual, EffectReactive:
		return true
	}
	return false
}
func (l Lighting) UsesColor2() bool { return l.Effect == EffectBreathDual }
func (l Lighting) UsesSpeed() bool  { return l.Effect == EffectReactive }

func (l Lighting) String() string {
	s := string(l.Effect)
	if l.UsesColor() {
		s += " " + l.Color.String()
	}
	if l.UsesColor2() {
		s += "/" + l.Color2.String()
	}
	if l.UsesSpeed() {
		s += fmt.Sprintf(" speed %d", l.Speed)
	}
	return s + fmt.Sprintf(", brightness %d%%", Percent(l.Brightness))
}

// Percent converts a 0..255 brightness into 0..100.
func Percent(v uint8) int { return (int(v)*100 + 127) / 255 }

// FromPercent converts 0..100 into 0..255.
func FromPercent(p int) uint8 {
	p = max(0, min(100, p))
	return uint8((p*255 + 50) / 100)
}

// effectArgs returns the 0x0F:0x02 arguments after storage and LED id.
func (l Lighting) effectArgs() (size byte, args []byte, err error) {
	c, c2 := l.Color, l.Color2
	switch l.Effect {
	case EffectOff:
		return 0x06, []byte{fxNone}, nil
	case EffectStatic:
		return 0x09, []byte{fxStatic, 0, 0, 1, c.R, c.G, c.B}, nil
	case EffectBreath:
		return 0x09, []byte{fxBreath, 1, 0, 1, c.R, c.G, c.B}, nil
	case EffectBreathDual:
		return 0x0C, []byte{fxBreath, 2, 0, 2, c.R, c.G, c.B, c2.R, c2.G, c2.B}, nil
	case EffectBreathRandom:
		return 0x06, []byte{fxBreath}, nil
	case EffectSpectrum:
		return 0x06, []byte{fxSpectrum}, nil
	case EffectReactive:
		speed := max(1, min(4, l.Speed))
		return 0x09, []byte{fxReactive, 0, speed, 1, c.R, c.G, c.B}, nil
	}
	return 0, nil, fmt.Errorf("unknown effect %q", l.Effect)
}

// EffectRequest builds the "set effect" command for storage/led.
func (l Lighting) EffectRequest(tid, storage, led byte) (Request, error) {
	size, rest, err := l.effectArgs()
	if err != nil {
		return Request{}, err
	}
	return Request{TID: tid, Class: 0x0F, Cmd: 0x02, Size: size,
		Args: append([]byte{storage, led}, rest...)}, nil
}

// BrightnessRequest builds the "set brightness" command for storage/led.
func BrightnessRequest(tid, storage, led, value byte) Request {
	return Request{TID: tid, Class: 0x0F, Cmd: 0x04, Size: 0x03, Args: []byte{storage, led, value}}
}

// decodeEffect interprets a 0x0F:0x82 reply. The reply mirrors the set
// command: args[2] effect id, args[3] mode/count, args[4] speed,
// args[5] colour count, args[6..] colours.
func decodeEffect(a []byte) Lighting {
	l := Lighting{Color: RGB{a[6], a[7], a[8]}, Color2: RGB{a[9], a[10], a[11]}}
	switch a[2] {
	case fxNone:
		l.Effect = EffectOff
	case fxStatic:
		l.Effect = EffectStatic
	case fxBreath:
		switch a[3] {
		case 0:
			l.Effect = EffectBreathRandom
		case 2:
			l.Effect = EffectBreathDual
		default:
			l.Effect = EffectBreath
		}
	case fxSpectrum:
		l.Effect = EffectSpectrum
	case fxReactive:
		l.Effect = EffectReactive
		l.Speed = a[4]
	default:
		l.Effect = Effect(fmt.Sprintf("unknown(0x%02x)", a[2]))
	}
	return l
}

// SameEffect compares only the fields that matter for the effect, so a
// device that leaves unused colour bytes non-zero still matches.
func SameEffect(a, b Lighting) bool {
	if a.Effect != b.Effect {
		return false
	}
	if a.UsesColor() && a.Color != b.Color {
		return false
	}
	if a.UsesColor2() && a.Color2 != b.Color2 {
		return false
	}
	if a.UsesSpeed() && max(1, min(4, a.Speed)) != b.Speed {
		return false
	}
	return true
}

// SameBrightness says whether have already shows l's brightness. It does
// not matter while the LED is off.
func SameBrightness(l, have Lighting) bool {
	return l.Effect == EffectOff || l.Brightness == have.Brightness
}

// SameLighting compares everything that is visible on the LED.
func SameLighting(l, have Lighting) bool { return SameEffect(l, have) && SameBrightness(l, have) }
