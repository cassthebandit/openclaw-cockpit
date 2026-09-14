// File messages.go owns transient status/toast helpers that decorate the
// footer.
package ui

import (
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// toastState tracks the current toast message and its expiration time.
type toastState struct {
	text string
	exp  time.Time
}

// showToast updates the transient toast message and schedules its expiration.
// Toast text can embed untrusted session names or command input, so it goes
// through the strict chrome sanitizer.
func (m *Model) showToast(msg string) {
	m.markRenderDirty()
	if m.toast == nil {
		m.toast = &toastState{}
	}
	m.toast.text = cardSafeLine(msg)
	m.toast.exp = m.clockNow().Add(3 * time.Second)
}

// toastView renders the toast centred on the footer or returns an empty string
// when the toast is inactive or expired.
func (m *Model) toastView(width int) string {
	if m.toast == nil || m.toast.text == "" {
		return ""
	}
	if !m.clockNow().Before(m.toast.exp) {
		m.toast.text = ""
		return ""
	}
	return centerText(m.toast.text, width)
}

// centerText pads text with spaces so it appears centred for the requested
// width.
func centerText(text string, width int) string {
	if width <= 0 {
		width = runewidth.StringWidth(text)
	}
	if runewidth.StringWidth(text) >= width {
		return text
	}
	pad := (width - runewidth.StringWidth(text)) / 2
	return strings.Repeat(" ", pad) + text
}
