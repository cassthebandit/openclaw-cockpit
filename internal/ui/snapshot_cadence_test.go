package ui

import (
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// These commands use an explicitly nonexistent binary, never the operator's
// tmux server. Executing them distinguishes snapshot requests from timers.
func snapshotCommandMessages(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var messages []tea.Msg
		for _, child := range batch {
			messages = append(messages, snapshotCommandMessages(child)...)
		}
		return messages
	}
	return []tea.Msg{msg}
}

func assertSnapshotCommands(t *testing.T, cmd tea.Cmd, ticks, fetches int) {
	t.Helper()
	gotTicks, gotFetches := 0, 0
	for _, msg := range snapshotCommandMessages(cmd) {
		switch msg.(type) {
		case tickMsg:
			gotTicks++
		case errMsg:
			gotFetches++
		default:
			t.Fatalf("unexpected command message %T", msg)
		}
	}
	if gotTicks != ticks || gotFetches != fetches {
		t.Fatalf("ticks/fetches = %d/%d, want %d/%d", gotTicks, gotFetches, ticks, fetches)
	}
}

func TestSnapshotTickHasOneOwnerAcrossCompletions(t *testing.T) {
	client, err := tmux.NewClient(filepath.Join(t.TempDir(), "nonexistent-tmux"))
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(client, time.Nanosecond, 4, nil, false, true)
	assertSnapshotCommands(t, m.Init(), 1, 1)
	if !m.inflight {
		t.Fatal("initial fetch must own single-flight guard")
	}
	// Arbitrarily slow initial/manual/timed acquisition cannot overlap and
	// cannot stop the only tick lineage while its response is pending.
	for range 3 {
		_, cmd := m.handleMessage(tickMsg{})
		assertSnapshotCommands(t, cmd, 1, 0)
	}
	for _, completion := range []tea.Msg{snapshotMsg{}, errMsg{}} {
		_, cmd := m.handleMessage(completion)
		assertSnapshotCommands(t, cmd, 0, 0)
		if m.inflight {
			t.Fatal("completion did not release single-flight guard")
		}
		_, cmd = m.handleMessage(tickMsg{})
		assertSnapshotCommands(t, cmd, 1, 1)
	}
	_, _ = m.handleMessage(snapshotMsg{})
	for _, item := range m.buildCommandItems() {
		if item.label != "Force refresh from tmux" {
			continue
		}
		assertSnapshotCommands(t, item.run(m), 0, 1)
		assertSnapshotCommands(t, item.run(m), 0, 0)
		_, cmd := m.handleMessage(snapshotMsg{})
		assertSnapshotCommands(t, cmd, 0, 0)
		_, cmd = m.handleMessage(tickMsg{})
		assertSnapshotCommands(t, cmd, 1, 1)
		return
	}
	t.Fatal("force-refresh palette action missing")
}
