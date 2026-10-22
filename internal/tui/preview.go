package tui

import (
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/mschmidt/razerctl/internal/daemon"
	"github.com/mschmidt/razerctl/internal/razer"
)

// Live preview: while the colour picker is open, the colour is written to
// the volatile slot 0 only (what the LED shows), never to the saved slot.
// The service is paused meanwhile so it does not "correct" the preview.

type target int

const (
	targetNone target = iota - 1
	targetMouse
	targetDock
)

type previewTickMsg int
type previewDoneMsg struct{ err error }

// pickTargetFor maps a colour field to the device it previews on.
func (m *model) pickTargetFor(f *field) target {
	switch f {
	case m.logo.color, m.logo.color2:
		return targetMouse
	case m.dock.color, m.dock.color2:
		return targetDock
	}
	return targetNone
}

func (m *model) openPicker(f *field) {
	m.picker = newPicker(f.label, f.col)
	m.pickField = f
	m.pickTarget = m.pickTargetFor(f)
	if m.pickTarget != targetNone {
		m.pickWasDirty = m.dirty[m.pickTarget]
		if !m.dirty[m.pickTarget] {
			m.restore[m.pickTarget] = m.liveLighting(m.pickTarget)
		}
	}
}

// liveLighting is what the device showed at the last refresh.
func (m *model) liveLighting(t target) razer.Lighting {
	if t == targetMouse {
		return m.snap.logo
	}
	// The dock cannot report brightness; assume the saved preset's.
	l := m.snap.dockLive
	l.Brightness = m.cfg.Dock.Brightness
	return l
}

func (m *model) formFor(t target) *lightingForm {
	if t == targetMouse {
		return m.logo
	}
	return m.dock
}

// previewLighting is the form's lighting with the picker colour in place.
func (m *model) previewLighting() razer.Lighting {
	form := m.formFor(m.pickTarget)
	l, _ := form.get()
	if m.pickField == form.color2 {
		l.Color2 = m.picker.col
	} else {
		l.Color = m.picker.col
	}
	return l
}

// schedulePreview debounces: only the newest change within 60 ms is sent.
func (m *model) schedulePreview() tea.Cmd {
	if m.pickTarget == targetNone {
		return nil
	}
	m.previewSeq++
	seq := m.previewSeq
	return tea.Tick(60*time.Millisecond, func(time.Time) tea.Msg { return previewTickMsg(seq) })
}

func (m *model) sendPreview(l razer.Lighting) tea.Cmd {
	if m.previewBusy {
		m.previewPending = true
		return nil
	}
	m.previewBusy = true
	m.dirty[m.pickTarget] = true
	return writeLive(m.found, m.pickTarget, l)
}

func writeLive(f razer.Found, t target, l razer.Lighting) tea.Cmd {
	return func() tea.Msg {
		daemon.PausePreview()
		var err error
		switch {
		case t == targetMouse && f.Mouse != nil:
			_, err = f.Mouse.SetLighting(l, razer.NoStore)
		case t == targetDock && f.Dock != nil:
			// Previews change colours only; the brightness stays as is.
			_, err = f.Dock.SetLighting(l, false, razer.NoStore)
		default:
			err = errors.New("device not connected")
		}
		return previewDoneMsg{err}
	}
}

func (m *model) updatePreview(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case previewTickMsg:
		if int(msg) == m.previewSeq && m.picker != nil {
			return m.sendPreview(m.previewLighting())
		}
	case previewDoneMsg:
		m.previewBusy = false
		if c := m.afterPreview; c != nil {
			m.afterPreview = nil
			return c
		}
		if msg.err != nil {
			m.msg, m.msgErr = "preview: "+msg.err.Error(), true
			if errors.Is(msg.err, razer.ErrUnreachable) {
				m.msg = "preview: mouse is asleep, move it"
			}
		}
		if m.previewPending && m.picker != nil {
			m.previewPending = false
			return m.sendPreview(m.previewLighting())
		}
	}
	return nil
}

func (m *model) updatePicker(k tea.KeyMsg) tea.Cmd {
	act, changed := m.picker.update(k)
	switch act {
	case pickConfirm:
		m.pickField.col = m.picker.col
		m.picker = nil
		if m.pickTarget != targetNone {
			m.msg, m.msgErr = "colour chosen: press a to apply and save it as the preset", false
		}
	case pickCancel:
		m.picker = nil
		return m.cancelPreview()
	}
	if changed {
		return m.schedulePreview()
	}
	return nil
}

// cancelPreview puts back what the device showed before the picker opened.
func (m *model) cancelPreview() tea.Cmd {
	t := m.pickTarget
	if t == targetNone || !m.dirty[t] {
		return nil
	}
	m.previewSeq++ // drop pending ticks
	m.previewPending = false
	var cmd tea.Cmd
	if m.pickWasDirty {
		// An earlier, still unsaved colour is showing; go back to it.
		l, _ := m.formFor(t).get()
		cmd = writeLive(m.found, t, l)
	} else {
		m.dirty[t] = false
		cmd = tea.Sequence(writeLive(m.found, t, m.restore[t]), m.resumeIfClean())
	}
	if m.previewBusy {
		// Let the write in flight finish first, or it would land last.
		m.afterPreview = cmd
		return nil
	}
	return cmd
}

// saved is called once a tab's lighting has been applied to both slots.
func (m *model) saved(t target) {
	m.dirty[t] = false
}

func (m *model) clean() bool { return !m.dirty[targetMouse] && !m.dirty[targetDock] }

func (m *model) resumeIfClean() tea.Cmd {
	if !m.clean() {
		return nil
	}
	return func() tea.Msg { daemon.ResumePreview(); return nil }
}

// restoreAll runs on quit, so an unsaved preview does not stay on the LED.
func (m *model) restoreAll() {
	for _, t := range []target{targetMouse, targetDock} {
		if !m.dirty[t] {
			continue
		}
		if cmd := writeLive(m.found, t, m.restore[t]); cmd != nil {
			cmd()
		}
	}
	daemon.ResumePreview()
}
