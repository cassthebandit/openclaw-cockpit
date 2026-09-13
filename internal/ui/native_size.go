package ui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

type nativeSizeMsg struct{ err error }

// SetNativeSizer opts into terminal mutation separately from card rendering.
func (m *Model) SetNativeSizer(sizer *tmux.NativeSizer) { m.nativeSizer = sizer }

func (m *Model) syncNativeSizeCmd() tea.Cmd {
	if m.nativeSizer == nil || m.nativeSizing {
		return nil
	}
	m.nativeSizing = true
	targets := append([]tmux.NativeSize(nil), m.nativeTargets...)
	sizer := m.nativeSizer
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return nativeSizeMsg{err: sizer.Sync(ctx, targets)}
	}
}
