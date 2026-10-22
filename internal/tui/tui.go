// Package tui is the interactive Bubble Tea front end.
package tui

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/mschmidt/razerctl/internal/config"
	"github.com/mschmidt/razerctl/internal/razer"
)

func Run() error {
	_, err := tea.NewProgram(newModel(), tea.WithAltScreen()).Run()
	return err
}

// ---- device snapshot -------------------------------------------------------

type snapshot struct {
	mouse, dock  bool
	wired, awake bool
	mouseErr     error

	fw, serial         string
	battery            int
	charging           bool
	dpi                int
	activeStage        byte
	stages             []razer.Stage
	pollRate           int
	idle, lowBattery   int
	logo               razer.Lighting // live slot
	dockFW, dockSerial string
	dockLive           razer.Lighting // live slot; brightness unreadable

	service string
}

func load(f razer.Found) snapshot {
	var s snapshot
	s.service = serviceState()
	if m := f.Mouse; m != nil {
		s.mouse, s.wired = true, m.Wired
		s.fw, s.mouseErr = m.Firmware()
		s.awake = s.mouseErr == nil
		if s.awake {
			// Individual failures leave zero values; the first one is shown.
			var errs []error
			note := func(err error) { errs = append(errs, err) }
			var err error
			s.serial, err = m.SerialNumber()
			note(err)
			s.battery, err = m.Battery()
			note(err)
			s.charging, err = m.Charging()
			note(err)
			s.dpi, _, err = m.DPI()
			note(err)
			s.activeStage, s.stages, err = m.DPIStages()
			note(err)
			s.pollRate, err = m.PollRate()
			note(err)
			s.idle, err = m.IdleTime()
			note(err)
			s.lowBattery, err = m.LowBattery()
			note(err)
			s.logo, err = m.Lighting(razer.NoStore)
			note(err)
			s.mouseErr = errors.Join(errs...)
		}
	}
	if d := f.Dock; d != nil {
		s.dock = true
		s.dockFW, _ = d.Firmware()
		s.dockSerial, _ = d.SerialNumber()
		s.dockLive, _ = d.Lighting(razer.NoStore)
	}
	return s
}

func serviceState() string {
	out, _ := exec.Command("systemctl", "--user", "is-active", "razerctl.service").Output()
	st := strings.TrimSpace(string(out))
	if st == "" {
		st = "not installed"
	}
	return st
}

// ---- form fields -----------------------------------------------------------

type kind int

const (
	kChoice kind = iota
	kInt
	kColor
	kAction
)

type field struct {
	label   string
	kind    kind
	options []string // kChoice
	idx     int
	val     int // kInt
	lo, hi  int
	step    int
	unit    string
	col     razer.RGB // kColor
	show    func() bool

	// dev is the device value (val or idx) last shown by sync.
	dev    int
	synced bool
}

func (f *field) visible() bool { return f.show == nil || f.show() }

func (f *field) choice() string { return f.options[f.idx] }

func (f *field) adjust(dir int, big bool) {
	switch f.kind {
	case kChoice:
		f.idx = (f.idx + dir + len(f.options)) % len(f.options)
	case kInt:
		step := f.step
		if big {
			step *= 10
		}
		f.val = max(f.lo, min(f.hi, f.val+dir*step))
	}
}

// sync shows the device value v (val, or idx for a choice) unless the user
// has changed the field since the last sync; force overrides that.
func (f *field) sync(v int, force bool) {
	cur := &f.val
	if f.kind == kChoice {
		cur = &f.idx
	}
	if force || !f.synced || *cur == f.dev {
		*cur = v
	}
	f.dev, f.synced = v, true
}

func (f *field) setChoice(v string) {
	for i, o := range f.options {
		if o == v {
			f.idx = i
		}
	}
}

func choiceField(label string, opts ...string) *field {
	return &field{label: label, kind: kChoice, options: opts}
}

func intField(label string, lo, hi, step int, unit string) *field {
	return &field{label: label, kind: kInt, lo: lo, hi: hi, step: step, unit: unit, val: lo}
}

func effectNames(es []razer.Effect) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = string(e)
	}
	return out
}

// lightingForm edits one razer.Lighting plus its "keep applied" flag.
type lightingForm struct {
	effect, color, color2, speed, brightness, enforce *field
}

func newLightingForm(effects []razer.Effect, speed bool) *lightingForm {
	lf := &lightingForm{
		effect:     choiceField("Effect", effectNames(effects)...),
		color:      &field{label: "Colour", kind: kColor},
		color2:     &field{label: "Colour 2", kind: kColor},
		brightness: intField("Brightness", 0, 100, 5, "%"),
		enforce:    choiceField("Auto re-apply", "yes", "no"),
	}
	cur := func() razer.Lighting { return razer.Lighting{Effect: razer.Effect(lf.effect.choice())} }
	lf.color.show = func() bool { return cur().UsesColor() }
	lf.color2.show = func() bool { return cur().UsesColor2() }
	if speed {
		lf.speed = intField("Reactive duration", 1, 4, 1, "")
		lf.speed.show = func() bool { return cur().UsesSpeed() }
	}
	return lf
}

func (lf *lightingForm) fields() []*field {
	fs := []*field{lf.effect, lf.color, lf.color2}
	if lf.speed != nil {
		fs = append(fs, lf.speed)
	}
	return append(fs, lf.brightness, lf.enforce)
}

func (lf *lightingForm) set(l razer.Lighting, enforce bool) {
	lf.effect.setChoice(string(l.Effect))
	lf.color.col, lf.color2.col = l.Color, l.Color2
	if lf.speed != nil {
		lf.speed.val = max(1, int(l.Speed))
	}
	lf.brightness.val = razer.Percent(l.Brightness)
	lf.enforce.setChoice(map[bool]string{true: "yes", false: "no"}[enforce])
}

func (lf *lightingForm) get() (razer.Lighting, bool) {
	l := razer.Lighting{
		Effect:     razer.Effect(lf.effect.choice()),
		Color:      lf.color.col,
		Color2:     lf.color2.col,
		Brightness: razer.FromPercent(lf.brightness.val),
	}
	if lf.speed != nil {
		l.Speed = uint8(lf.speed.val)
	}
	return l, lf.enforce.choice() == "yes"
}

// ---- model -----------------------------------------------------------------

type tab struct {
	name   string
	fields []*field
	apply  func() tea.Cmd
	view   func() string // extra text above the fields
}

type model struct {
	found razer.Found
	snap  snapshot
	cfg   config.Config

	tabs   []*tab
	cur    int
	cursor int

	editing bool
	input   textinput.Model

	busy    bool
	spin    spinner.Model
	msg     string
	msgErr  bool
	width   int
	loaded  bool
	scanErr error

	logo, dock *lightingForm

	poll, dpi, stageCount, activeStage *field
	stageDPI                           [razer.MaxStages]*field

	idle, lowBattery *field

	// resync makes the next snapshot overwrite edited fields (after apply).
	resync bool

	// colour picker and live preview (preview.go)
	picker         *picker
	pickField      *field
	pickTarget     target
	pickWasDirty   bool
	dirty          [2]bool           // slot 0 shows an unsaved preview
	restore        [2]razer.Lighting // what to put back on cancel/quit
	previewSeq     int
	previewBusy    bool
	previewPending bool
	afterPreview   tea.Cmd
}

func newModel() *model {
	m := &model{spin: spinner.New(), input: textinput.New()}
	m.spin.Spinner = spinner.MiniDot
	m.input.CharLimit = 7
	m.cfg, _ = config.Load()

	m.logo = newLightingForm(razer.MouseEffects, true)
	m.dock = newLightingForm(razer.DockEffects, false)
	m.logo.set(m.cfg.Mouse, m.cfg.EnforceMouse)
	m.dock.set(m.cfg.Dock, m.cfg.EnforceDock)

	m.poll = choiceField("Polling rate", "125", "500", "1000")
	m.dpi = intField("Current DPI", razer.MinDPI, razer.MaxDPI, 50, "")
	m.stageCount = intField("DPI stages", 1, razer.MaxStages, 1, "")
	m.activeStage = intField("Active stage", 1, razer.MaxStages, 1, "")
	perf := []*field{m.poll, m.dpi, m.stageCount, m.activeStage}
	for i := range m.stageDPI {
		f := intField(fmt.Sprintf("  Stage %d", i+1), razer.MinDPI, razer.MaxDPI, 50, " DPI")
		f.show = func() bool { return i < m.stageCount.val }
		m.stageDPI[i] = f
		perf = append(perf, f)
	}

	m.idle = intField("Sleep after", razer.MinIdle, razer.MaxIdle, 30, "s")
	m.lowBattery = intField("Low-battery warning", razer.MinLowBattery, razer.MaxLowBattery, 1, "%")

	apply := &field{label: "[ Apply ]", kind: kAction}
	m.tabs = []*tab{
		{name: "Mouse lighting", fields: append(m.logo.fields(), apply), apply: m.applyLogo, view: m.logoView},
		{name: "Dock lighting", fields: append(m.dock.fields(), apply), apply: m.applyDock, view: m.dockView},
		{name: "Performance", fields: append(perf, apply), apply: m.applyPerf, view: m.perfView},
		{name: "Power", fields: []*field{m.idle, m.lowBattery, apply}, apply: m.applyPower, view: m.powerView},
		{name: "Info", view: m.infoView},
	}
	return m
}

// ---- messages and commands -------------------------------------------------

type scannedMsg struct {
	f   razer.Found
	err error
}
type snapMsg snapshot
type doneMsg struct {
	text string
	err  error
}
type tickMsg time.Time

func scan() tea.Msg {
	f, err := razer.Scan()
	return scannedMsg{f, err}
}

func (m *model) refresh() tea.Cmd {
	m.busy = true
	f := m.found
	return func() tea.Msg { return snapMsg(load(f)) }
}

func tick() tea.Cmd {
	return tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// run executes fn off the UI goroutine and refreshes afterwards.
func (m *model) run(text string, fn func() error) tea.Cmd {
	m.busy = true
	return func() tea.Msg { return doneMsg{text, fn()} }
}

func (m *model) Init() tea.Cmd {
	m.busy = true
	return tea.Batch(scan, m.spin.Tick, tick())
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case scannedMsg:
		m.found.Close()
		m.found, m.scanErr = msg.f, msg.err
		return m, m.refresh()
	case snapMsg:
		m.busy = false
		m.snap = snapshot(msg)
		m.fillFromDevice()
		m.loaded = true
	case previewTickMsg, previewDoneMsg:
		return m, m.updatePreview(msg)
	case tickMsg:
		if !m.busy && !m.editing && m.picker == nil {
			return m, tea.Batch(m.refresh(), tick())
		}
		return m, tick()
	case doneMsg:
		m.busy = false
		m.resync = true
		m.msg, m.msgErr = msg.text, msg.err != nil
		var same unchangedError
		if errors.As(msg.err, &same) {
			m.msg, m.msgErr = "no change: "+string(same), false
		} else if msg.err != nil {
			m.msg = msg.err.Error()
			if errors.Is(msg.err, razer.ErrUnreachable) {
				m.msg = "mouse is asleep: move it and try again"
			}
		}
		return m, m.refresh()
	case tea.KeyMsg:
		if m.picker != nil {
			if msg.String() == "ctrl+c" {
				m.restoreAll()
				return m, tea.Quit
			}
			return m, m.updatePicker(msg)
		}
		if m.editing {
			return m, m.updateEditing(msg)
		}
		return m, m.updateKeys(msg)
	}
	return m, nil
}

func (m *model) fillFromDevice() {
	s := m.snap
	if !s.awake {
		return
	}
	force := m.resync
	m.resync = false
	for i, o := range m.poll.options {
		if o == strconv.Itoa(s.pollRate) {
			m.poll.sync(i, force)
		}
	}
	m.dpi.sync(s.dpi, force)
	if n := len(s.stages); n > 0 {
		m.stageCount.sync(n, force)
		for i, st := range s.stages {
			m.stageDPI[i].sync(st.X, force)
			if st.ID == s.activeStage {
				m.activeStage.sync(i+1, force)
			}
		}
	}
	m.idle.sync(s.idle, force)
	m.lowBattery.sync(max(razer.MinLowBattery, min(razer.MaxLowBattery, s.lowBattery)), force)
}

func (m *model) visibleFields() []*field {
	var out []*field
	for _, f := range m.tabs[m.cur].fields {
		if f.visible() {
			out = append(out, f)
		}
	}
	return out
}

func (m *model) updateKeys(k tea.KeyMsg) tea.Cmd {
	fs := m.visibleFields()
	m.cursor = max(0, min(m.cursor, len(fs)-1))
	var f *field
	if len(fs) > 0 {
		f = fs[m.cursor]
	}
	switch k.String() {
	case "q", "ctrl+c":
		m.restoreAll()
		m.found.Close()
		return tea.Quit
	case "tab":
		m.cur, m.cursor = (m.cur+1)%len(m.tabs), 0
	case "shift+tab":
		m.cur, m.cursor = (m.cur+len(m.tabs)-1)%len(m.tabs), 0
	case "1", "2", "3", "4", "5":
		m.cur, m.cursor = int(k.String()[0]-'1'), 0
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = min(len(fs)-1, m.cursor+1)
	case "left", "h", "right", "l", "pgup", "pgdown":
		if f != nil {
			dir := 1
			if s := k.String(); s == "left" || s == "h" || s == "pgdown" {
				dir = -1
			}
			f.adjust(dir, strings.HasPrefix(k.String(), "pg"))
			if f == m.stageCount {
				m.activeStage.val = min(m.activeStage.val, m.stageCount.val)
			}
			if f == m.activeStage {
				m.activeStage.val = min(m.activeStage.val, m.stageCount.val)
			}
		}
	case "enter", " ":
		if f == nil {
			break
		}
		switch f.kind {
		case kAction:
			return m.applyTab()
		case kColor:
			m.openPicker(f)
		case kInt:
			m.startEdit(strconv.Itoa(f.val))
		case kChoice:
			f.adjust(1, false)
		}
	case "a":
		return m.applyTab()
	case "r":
		if !m.busy {
			m.msg = ""
			m.busy = true
			return scan // also picks up re-plugged devices
		}
	}
	return nil
}

func (m *model) applyTab() tea.Cmd {
	t := m.tabs[m.cur]
	if t.apply == nil || m.busy {
		return nil
	}
	return t.apply()
}

func (m *model) startEdit(v string) {
	m.editing = true
	m.input.SetValue(v)
	m.input.CursorEnd()
	m.input.Focus()
}

func (m *model) updateEditing(k tea.KeyMsg) tea.Cmd {
	f := m.visibleFields()[m.cursor]
	switch k.String() {
	case "esc":
		m.editing = false
		return nil
	case "enter":
		m.editing = false
		v := m.input.Value()
		switch f.kind {
		case kColor:
			c, err := razer.ParseRGB(v)
			if err != nil {
				m.msg, m.msgErr = err.Error(), true
				return nil
			}
			f.col = c
		case kInt:
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil || n < f.lo || n > f.hi {
				m.msg, m.msgErr = fmt.Sprintf("%s must be %d..%d", strings.TrimSpace(f.label), f.lo, f.hi), true
				return nil
			}
			f.val = n
		}
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return cmd
}

// ---- apply actions ---------------------------------------------------------

var errNoMouse = errors.New("mouse not connected")

// unchangedError reports that an apply had nothing to write; it is shown
// as information, not as a failure.
type unchangedError string

func (e unchangedError) Error() string { return string(e) }

func unchanged(wrote bool, text string) error {
	if wrote {
		return nil
	}
	return unchangedError(text)
}

// saveConfig re-reads the file so edits from other tabs are kept.
func saveConfig(edit func(*config.Config)) error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	edit(&c)
	return config.Save(c)
}

func (m *model) applyLogo() tea.Cmd {
	l, enforce := m.logo.get()
	mouse := m.found.Mouse
	m.saved(targetMouse)
	resume := m.resumeIfClean()
	return m.run("mouse lighting applied and saved as preset", func() error {
		if resume != nil {
			defer resume()
		}
		if mouse == nil {
			return errNoMouse
		}
		wrote, err := mouse.SetLighting(l, razer.NoStore, razer.VarStore)
		if err != nil {
			return err
		}
		if err := saveConfig(func(c *config.Config) { c.Mouse, c.EnforceMouse = l, enforce }); err != nil {
			return err
		}
		return unchanged(wrote, "mouse already showed this; preset saved")
	})
}

func (m *model) applyDock() tea.Cmd {
	l, enforce := m.dock.get()
	dock := m.found.Dock
	m.saved(targetDock)
	resume := m.resumeIfClean()
	return m.run("dock lighting applied and saved as preset", func() error {
		if resume != nil {
			defer resume()
		}
		if dock == nil {
			return errors.New("dock not connected")
		}
		// The dock cannot report brightness: send it only when it differs
		// from the preset saved last time.
		prev, err := config.Load()
		if err != nil {
			return err
		}
		wrote, err := dock.SetLighting(l, l.Brightness != prev.Dock.Brightness, razer.NoStore, razer.VarStore)
		if err != nil {
			return err
		}
		if err := saveConfig(func(c *config.Config) { c.Dock, c.EnforceDock = l, enforce }); err != nil {
			return err
		}
		return unchanged(wrote, "dock already showed this; preset saved")
	})
}

// applyPerf only touches settings the user changed since the last refresh,
// so an untouched DPI value does not override the stage that the stage write
// just selected; the setters then skip anything the device already has.
func (m *model) applyPerf() tea.Cmd {
	mouse, s := m.found.Mouse, m.snap
	poll, _ := strconv.Atoi(m.poll.choice())
	dpi := m.dpi.val
	firstID := byte(1)
	if len(s.stages) > 0 {
		firstID = s.stages[0].ID
	}
	var stages []razer.Stage
	for i := 0; i < m.stageCount.val; i++ {
		v := m.stageDPI[i].val
		stages = append(stages, razer.Stage{ID: firstID + byte(i), X: v, Y: v})
	}
	active := firstID + byte(m.activeStage.val-1)
	return m.run("performance settings applied", func() error {
		if mouse == nil {
			return errNoMouse
		}
		if !s.awake {
			return razer.ErrUnreachable
		}
		changed := false
		if poll != s.pollRate {
			w, err := mouse.SetPollRate(poll)
			if err != nil {
				return err
			}
			changed = changed || w
		}
		if active != s.activeStage || !slices.Equal(stages, s.stages) {
			w, err := mouse.SetDPIStages(active, stages, razer.NoStore, razer.VarStore)
			if err != nil {
				return err
			}
			changed = changed || w
		}
		if dpi != s.dpi {
			w, err := mouse.SetDPI(dpi, dpi, razer.NoStore, razer.VarStore)
			if err != nil {
				return err
			}
			changed = changed || w
		}
		return unchanged(changed, "nothing changed")
	})
}

func (m *model) applyPower() tea.Cmd {
	mouse, s := m.found.Mouse, m.snap
	idle, low := m.idle.val, m.lowBattery.val
	return m.run("power settings applied", func() error {
		if mouse == nil {
			return errNoMouse
		}
		if !s.awake {
			return razer.ErrUnreachable
		}
		changed := false
		if idle != s.idle {
			w, err := mouse.SetIdleTime(idle)
			if err != nil {
				return err
			}
			changed = changed || w
		}
		if low != s.lowBattery {
			w, err := mouse.SetLowBattery(low)
			if err != nil {
				return err
			}
			changed = changed || w
		}
		return unchanged(changed, "nothing changed")
	})
}

// ---- view ------------------------------------------------------------------

var (
	accent  = lipgloss.AdaptiveColor{Light: "#1a7f37", Dark: "#44d17a"}
	dim     = lipgloss.AdaptiveColor{Light: "#6e7781", Dark: "#8b949e"}
	warn    = lipgloss.AdaptiveColor{Light: "#bc4c00", Dark: "#f0883e"}
	bad     = lipgloss.AdaptiveColor{Light: "#cf222e", Dark: "#ff7b72"}
	sTitle  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	sDim    = lipgloss.NewStyle().Foreground(dim)
	sWarn   = lipgloss.NewStyle().Foreground(warn)
	sBad    = lipgloss.NewStyle().Foreground(bad)
	sTab    = lipgloss.NewStyle().Padding(0, 1).Foreground(dim)
	sTabOn  = lipgloss.NewStyle().Padding(0, 1).Bold(true).Foreground(accent).Underline(true)
	sCursor = lipgloss.NewStyle().Bold(true).Foreground(accent)
	sBox    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(dim).Padding(0, 1)
)

func swatch(c razer.RGB) string {
	return lipgloss.NewStyle().Background(lipgloss.Color(c.String())).Render("    ")
}

func (m *model) View() string {
	var b strings.Builder
	b.WriteString(sTitle.Render("razerctl") + sDim.Render(" · DeathAdder V2 Pro + Mouse Dock") + "\n")
	b.WriteString(m.statusLine() + "\n\n")

	var tabs []string
	for i, t := range m.tabs {
		st := sTab
		if i == m.cur {
			st = sTabOn
		}
		tabs = append(tabs, st.Render(fmt.Sprintf("%d %s", i+1, t.name)))
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, tabs...) + "\n")

	var body strings.Builder
	t := m.tabs[m.cur]
	if t.view != nil {
		if v := t.view(); v != "" {
			body.WriteString(v + "\n")
		}
	}
	if m.picker != nil {
		live := ""
		if m.pickTarget != targetNone {
			live = sDim.Render(" · live preview on the device")
		}
		body.Reset()
		body.WriteString(sTitle.Render(strings.TrimSpace(m.picker.title)) + live + "\n\n" + m.picker.view())
	} else {
		fs := m.visibleFields()
		for i, f := range fs {
			if f.kind == kAction {
				body.WriteString("\n")
			}
			body.WriteString(m.renderField(f, i == m.cursor) + "\n")
		}
	}
	box := sBox
	if m.width > 4 {
		box = box.Width(min(m.width-2, 78))
	}
	b.WriteString(box.Render(strings.TrimRight(body.String(), "\n")) + "\n")

	switch {
	case m.busy:
		b.WriteString(m.spin.View() + sDim.Render(" talking to device…") + "\n")
	case m.msg != "" && m.msgErr:
		b.WriteString(sBad.Render("✗ "+m.msg) + "\n")
	case m.msg != "":
		b.WriteString(sTitle.Render("✓ ") + m.msg + "\n")
	default:
		b.WriteString("\n")
	}
	help := "tab/1-5 switch · ↑↓ select · ←→ change (pgup/pgdn ×10) · enter edit · a apply · r rescan · q quit"
	if m.editing {
		help = "type a value · enter confirm · esc cancel"
	}
	if m.picker != nil {
		help = m.picker.help()
	}
	b.WriteString(sDim.Render(help))
	return b.String()
}

func (m *model) renderField(f *field, sel bool) string {
	label := fmt.Sprintf("%-22s", f.label)
	prefix := "  "
	if sel {
		prefix = sCursor.Render("› ")
		label = sCursor.Render(label)
	}
	var val string
	switch f.kind {
	case kChoice:
		val = "‹ " + f.choice() + " ›"
	case kInt:
		val = fmt.Sprintf("‹ %d%s ›", f.val, f.unit)
	case kColor:
		val = swatch(f.col) + " " + f.col.String()
	case kAction:
		if sel {
			return prefix + sCursor.Render(f.label)
		}
		return prefix + f.label
	}
	if sel && m.editing {
		val = m.input.View()
	}
	return prefix + label + val
}

func (m *model) statusLine() string {
	s := m.snap
	if !m.loaded {
		return sDim.Render("scanning…")
	}
	if m.scanErr != nil {
		return sBad.Render(m.scanErr.Error())
	}
	var parts []string
	switch {
	case !s.mouse:
		parts = append(parts, sBad.Render("mouse not found"))
	case !s.awake:
		parts = append(parts, sWarn.Render("mouse asleep (move it, press r)"))
	default:
		conn := "wireless"
		if s.wired {
			conn = "wired"
		}
		bat := fmt.Sprintf("battery %d%%", s.battery)
		if s.charging {
			bat += " ⚡"
		}
		batStyle := sTitle
		if s.battery <= 20 && !s.charging {
			batStyle = sWarn
		}
		parts = append(parts, "mouse "+conn, batStyle.Render(bat), fmt.Sprintf("%d DPI", s.dpi))
	}
	if s.dock {
		parts = append(parts, "dock ✓")
	} else {
		parts = append(parts, sDim.Render("no dock"))
	}
	svc := "service " + s.service
	if s.service == "active" {
		parts = append(parts, sTitle.Render(svc))
	} else {
		parts = append(parts, sWarn.Render(svc))
	}
	return strings.Join(parts, sDim.Render(" │ "))
}

func (m *model) logoView() string {
	if !m.snap.awake {
		return sDim.Render("Preset for the logo LED. The mouse is not reachable right now.")
	}
	return sDim.Render("Preset for the logo LED. Device now: ") + m.snap.logo.String()
}

func (m *model) dockView() string {
	if !m.snap.dock {
		return sWarn.Render("Dock not connected.")
	}
	return sDim.Render("Preset for the dock's underglow. The dock cannot report brightness;\nthe service re-applies this whenever the dock is plugged in.")
}

func (m *model) perfView() string {
	return sDim.Render("Stages are what the DPI button cycles through (X = Y).")
}

func (m *model) powerView() string {
	return sDim.Render("Stored on the mouse itself; not part of the service preset.")
}

func (m *model) infoView() string {
	s := m.snap
	row := func(k, v string) string { return fmt.Sprintf("%-18s %s\n", k, v) }
	var b strings.Builder
	if s.mouse {
		b.WriteString(sTitle.Render("Mouse") + "\n")
		b.WriteString(row("Node", m.found.Mouse.Node.Path))
		b.WriteString(row("Firmware", s.fw))
		b.WriteString(row("Serial", s.serial))
		if s.mouseErr != nil {
			b.WriteString(row("Last error", sBad.Render(s.mouseErr.Error())))
		}
	}
	if s.dock {
		b.WriteString(sTitle.Render("Dock") + "\n")
		b.WriteString(row("Node", m.found.Dock.Node.Path))
		b.WriteString(row("Firmware", s.dockFW))
		b.WriteString(row("Serial", s.dockSerial))
		if s.dockLive.Effect != "" {
			b.WriteString(row("Effect", string(s.dockLive.Effect)))
		}
	}
	b.WriteString(sTitle.Render("Service") + "\n")
	b.WriteString(row("State", s.service))
	b.WriteString(row("Config", config.Path()))
	b.WriteString(row("Log", "journalctl --user -u razerctl -f"))
	return strings.TrimRight(b.String(), "\n")
}
