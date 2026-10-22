package razer

import (
	"bytes"
	"testing"
)

// From the OpenRazer comment on razer_chroma_extended_matrix_effect_static:
// 00 3f 0000 00 09 0f 02 010501000001ff0000
func TestEncodeStaticMatchesOpenRazer(t *testing.T) {
	l := Lighting{Effect: EffectStatic, Color: RGB{R: 0xff}}
	req, err := l.EffectRequest(0x3f, VarStore, 0x05)
	if err != nil {
		t.Fatal(err)
	}
	b, err := req.Encode()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x00, 0x3f, 0x00, 0x00, 0x00, 0x09, 0x0f, 0x02, 0x01, 0x05, 0x01, 0x00, 0x00, 0x01, 0xff, 0x00, 0x00}
	if !bytes.Equal(b[:len(want)], want) {
		t.Fatalf("got % x\nwant % x", b[:len(want)], want)
	}
	var c byte
	for _, v := range b[2:88] {
		c ^= v
	}
	if b[88] != c {
		t.Fatalf("crc %02x, want %02x", b[88], c)
	}
}

func TestEffectRoundTrip(t *testing.T) {
	cases := []Lighting{
		{Effect: EffectOff},
		{Effect: EffectStatic, Color: RGB{1, 2, 3}},
		{Effect: EffectBreath, Color: RGB{4, 5, 6}},
		{Effect: EffectBreathDual, Color: RGB{1, 2, 3}, Color2: RGB{7, 8, 9}},
		{Effect: EffectBreathRandom},
		{Effect: EffectSpectrum},
		{Effect: EffectReactive, Color: RGB{9, 9, 9}, Speed: 3},
	}
	for _, l := range cases {
		req, err := l.EffectRequest(0x3f, VarStore, LEDLogo)
		if err != nil {
			t.Fatal(err)
		}
		var args [maxArgs]byte
		copy(args[:], req.Args)
		if got := decodeEffect(args[:]); !SameEffect(l, got) {
			t.Errorf("%s: decoded as %s", l, got)
		}
	}
}

func TestDecodeRejectsBadCRC(t *testing.T) {
	b, _ := Request{TID: 0x3f, Class: 0x00, Cmd: 0x81, Size: 2}.Encode()
	if _, ok := decode(b[:]); !ok {
		t.Fatal("valid report rejected")
	}
	b[10] ^= 1
	if _, ok := decode(b[:]); ok {
		t.Fatal("corrupt report accepted")
	}
}

// Reply captured from the user's mouse: 2 stages of 1600 and 3200 DPI.
func TestStageLayout(t *testing.T) {
	args := []byte{0x01, 0x01, 0x02, 0x01, 0x06, 0x40, 0x06, 0x40, 0x00, 0x00, 0x02, 0x0c, 0x80, 0x0c, 0x80}
	n := int(args[2])
	var got []Stage
	for i := 0; i < n; i++ {
		a := args[3+7*i:]
		got = append(got, Stage{ID: a[0], X: int(a[1])<<8 | int(a[2]), Y: int(a[3])<<8 | int(a[4])})
	}
	want := []Stage{{1, 1600, 1600}, {2, 3200, 3200}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestPercent(t *testing.T) {
	for p := 0; p <= 100; p++ {
		if Percent(FromPercent(p)) != p {
			t.Fatalf("%d%% does not round-trip", p)
		}
	}
}
