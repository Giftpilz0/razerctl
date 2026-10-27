package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/giftpilz0/razerctl/internal/razer"
)

// palette holds two rows of LED-friendly colours. LEDs render saturated
// colours well and pastels poorly, so most entries are fully saturated.
var palette = [2][10]razer.RGB{
	hexRow("ff0000", "ff4000", "ff8c00", "ffd200", "a0ff00", "00ff00", "00ff78", "00ffff", "008cff", "0000ff"),
	hexRow("5a00ff", "a000ff", "ff00ff", "ff008c", "ff003c", "44d62c", "ffffff", "ffbe78", "ff783c", "282828"),
}

func hexRow(hex ...string) (row [10]razer.RGB) {
	for i, h := range hex {
		c, err := razer.ParseRGB(h)
		if err != nil {
			panic(err)
		}
		row[i] = c
	}
	return row
}

const (
	rowPalette1 = iota
	rowPalette2
	rowHue
	rowSat
	rowVal
	pickerRows
)

const barWidth = 40

type pickerAction int

const (
	pickNone pickerAction = iota
	pickConfirm
	pickCancel
)

// picker edits one colour with a palette and HSV sliders. Hue, saturation
// and value are kept separately so hue survives passing through grey.
type picker struct {
	title   string
	col     razer.RGB
	h, s, v float64 // 0..360, 0..1, 0..1
	row     int
	pal     int
	hexMode bool
	input   textinput.Model
}

func newPicker(title string, c razer.RGB) *picker {
	p := &picker{title: title, input: textinput.New(), row: rowHue}
	p.input.CharLimit = 7
	p.input.Prompt = "#"
	p.setRGB(c)
	// Start on the palette entry that matches, if any.
	for r := range palette {
		for i, pc := range palette[r] {
			if pc == c {
				p.row, p.pal = r, i
			}
		}
	}
	return p
}

func (p *picker) setRGB(c razer.RGB) {
	p.col = c
	h, s, v := rgbToHSV(c)
	if s > 0 && v > 0 {
		p.h = h
	}
	if v > 0 {
		p.s = s
	}
	p.v = v
}

func (p *picker) setHSV() { p.col = hsvToRGB(p.h, p.s, p.v) }

// update handles a key. changed reports a new colour, for live preview.
func (p *picker) update(k tea.KeyMsg) (act pickerAction, changed bool) {
	if p.hexMode {
		switch k.String() {
		case "esc":
			p.hexMode = false
		case "enter":
			c, err := razer.ParseRGB(p.input.Value())
			if err != nil {
				return pickNone, false
			}
			p.hexMode = false
			p.setRGB(c)
			return pickNone, true
		default:
			p.input, _ = p.input.Update(k)
		}
		return pickNone, false
	}

	old := p.col
	key := k.String()
	switch key {
	case "esc", "q":
		return pickCancel, false
	case "enter", " ":
		return pickConfirm, false
	case "#", "x":
		p.hexMode = true
		p.input.SetValue(strings.TrimPrefix(p.col.String(), "#"))
		p.input.CursorEnd()
		p.input.Focus()
		return pickNone, false
	case "up", "k":
		p.row = (p.row + pickerRows - 1) % pickerRows
	case "down", "j":
		p.row = (p.row + 1) % pickerRows
	case "left", "h", "right", "l", "shift+left", "shift+right", "pgup", "pgdown", "H", "L":
		dir := 1.0
		if key == "left" || key == "h" || key == "shift+left" || key == "pgdown" || key == "H" {
			dir = -1
		}
		big := key == "shift+left" || key == "shift+right" || key == "pgup" || key == "pgdown" || key == "H" || key == "L"
		p.step(dir, big)
	case "home":
		p.step(-1000, false)
	case "end":
		p.step(1000, false)
	}
	return pickNone, p.col != old
}

func (p *picker) step(dir float64, big bool) {
	switch p.row {
	case rowPalette1, rowPalette2:
		n := len(palette[p.row])
		p.pal = max(0, min(n-1, p.pal+int(dir)))
		p.setRGB(palette[p.row][p.pal])
	case rowHue:
		d := 5.0
		if big {
			d = 30
		}
		p.h = math.Mod(p.h+dir*d+3600, 360)
		if math.Abs(dir) > 1 {
			p.h = map[bool]float64{true: 359, false: 0}[dir > 0]
		}
		p.setHSV()
	case rowSat, rowVal:
		d := 0.02
		if big {
			d = 0.1
		}
		x := &p.s
		if p.row == rowVal {
			x = &p.v
		}
		*x = math.Max(0, math.Min(1, *x+dir*d))
		p.setHSV()
	}
}

// ---- rendering ----

func cell(c razer.RGB, text string) string {
	fg := lipgloss.Color("#000000")
	if luminance(c) < 0.5 {
		fg = lipgloss.Color("#ffffff")
	}
	return lipgloss.NewStyle().Background(lipgloss.Color(c.String())).Foreground(fg).Render(text)
}

func luminance(c razer.RGB) float64 {
	return (0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)) / 255
}

// bar draws a gradient; at(x) gives the colour for x in [0,1], pos marks
// the current value.
func bar(at func(x float64) razer.RGB, pos float64) string {
	var b strings.Builder
	mark := int(math.Round(pos * (barWidth - 1)))
	for i := 0; i < barWidth; i++ {
		x := float64(i) / (barWidth - 1)
		t := " "
		if i == mark {
			t = "┃"
		}
		b.WriteString(cell(at(x), t))
	}
	return b.String()
}

func (p *picker) view() string {
	var b strings.Builder
	label := func(row int, s string) string {
		if row == p.row && !p.hexMode {
			return sCursor.Render("› " + fmt.Sprintf("%-6s", s))
		}
		return "  " + sDim.Render(fmt.Sprintf("%-6s", s))
	}

	for r := range palette {
		name := ""
		if r == 0 {
			name = "Preset"
		}
		b.WriteString(label(r, name))
		for i, c := range palette[r] {
			t := "   "
			if c == p.col {
				t = " ● "
			}
			if r == p.row && i == p.pal && !p.hexMode {
				t = "[" + strings.TrimSpace(t) + "]"
				if len(t) == 2 {
					t = "[ ]"
				}
			}
			b.WriteString(cell(c, t) + " ")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(label(rowHue, "Hue") + bar(func(x float64) razer.RGB { return hsvToRGB(x*360, 1, 1) }, p.h/360) +
		sDim.Render(fmt.Sprintf(" %3.0f°", p.h)) + "\n")
	b.WriteString(label(rowSat, "Sat") + bar(func(x float64) razer.RGB { return hsvToRGB(p.h, x, math.Max(p.v, 0.25)) }, p.s) +
		sDim.Render(fmt.Sprintf(" %3.0f%%", p.s*100)) + "\n")
	b.WriteString(label(rowVal, "Value") + bar(func(x float64) razer.RGB { return hsvToRGB(p.h, p.s, x) }, p.v) +
		sDim.Render(fmt.Sprintf(" %3.0f%%", p.v*100)) + "\n\n")

	sw := cell(p.col, strings.Repeat(" ", 12))
	hex := p.col.String()
	if p.hexMode {
		hex = p.input.View()
	}
	b.WriteString("  " + sw + "  " + hex + "\n")
	b.WriteString("  " + sw + "  " + sDim.Render(fmt.Sprintf("rgb(%d, %d, %d)", p.col.R, p.col.G, p.col.B)))
	return b.String()
}

func (p *picker) help() string {
	if p.hexMode {
		return "type a hex colour · enter set · esc back"
	}
	return "↑↓ row · ←→ adjust (shift+←→ bigger steps) · # hex · enter choose · esc cancel"
}

// ---- colour math ----

func hsvToRGB(h, s, v float64) razer.RGB {
	h = math.Mod(h, 360)
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	to := func(f float64) uint8 { return uint8(math.Round((f + m) * 255)) }
	return razer.RGB{R: to(r), G: to(g), B: to(b)}
}

func rgbToHSV(c razer.RGB) (h, s, v float64) {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	d := mx - mn
	v = mx
	if mx > 0 {
		s = d / mx
	}
	if d == 0 {
		return 0, s, v
	}
	switch mx {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h, s, v
}
