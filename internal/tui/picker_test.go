package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/giftpilz0/razerctl/internal/razer"
)

func TestHSVRoundTrip(t *testing.T) {
	for r := 0; r < 256; r += 15 {
		for g := 0; g < 256; g += 15 {
			for b := 0; b < 256; b += 15 {
				c := razer.RGB{R: uint8(r), G: uint8(g), B: uint8(b)}
				if got := hsvToRGB(rgbToHSV(c)); got != c {
					t.Fatalf("%s -> %s", c, got)
				}
			}
		}
	}
}

func TestPickerKeepsHueThroughGrey(t *testing.T) {
	p := newPicker("Colour", razer.RGB{R: 255})
	p.row = rowVal
	p.step(-1000, false) // value 0: black
	p.step(+1000, false) // back to full value
	if p.col != (razer.RGB{R: 255}) {
		t.Fatalf("hue lost: %s", p.col)
	}
}

func TestPickerKeys(t *testing.T) {
	p := newPicker("Colour", razer.RGB{G: 255}) // palette row 0, index 5
	if p.row != rowPalette1 || p.pal != 5 {
		t.Fatalf("start at row %d pal %d", p.row, p.pal)
	}
	if _, changed := p.update(tea.KeyMsg{Type: tea.KeyRight}); !changed || p.col != palette[0][6] {
		t.Fatalf("right: %s changed=%v", p.col, changed)
	}
	p.update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'#'}})
	p.input.SetValue("123456")
	p.update(tea.KeyMsg{Type: tea.KeyEnter})
	if p.col != (razer.RGB{R: 0x12, G: 0x34, B: 0x56}) || p.hexMode {
		t.Fatalf("hex entry: %s hexMode=%v", p.col, p.hexMode)
	}
	if act, _ := p.update(tea.KeyMsg{Type: tea.KeyEnter}); act != pickConfirm {
		t.Fatal("enter should confirm")
	}
}
