package tmux

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestSnapshotRoundTripsSeparatorBearingNamesOnRealTmux proves AC7's framing
// half on a real disposable tmux server: window names, pane titles, session
// names, and @oc_* metadata containing the parser's active separator ("~~"),
// the escape token ("~e"), and the legacy sentinel ("::OC_FIELD::") all
// round-trip exactly.
func TestSnapshotRoundTripsSeparatorBearingNamesOnRealTmux(t *testing.T) {
	t.Parallel()

	wrapper := disposableTmux(t)
	// Session names may not contain ':' or '.', but '~' is legal.
	sessionName := "evil~~sess~e"
	windowName := "win~~mid::OC_FIELD::~e~end~"
	paneTitle := "title~~one~e::OC_FIELD::two~"
	goal := "goal ~~ with ~e escapes :: and text"

	runDisposable(t, wrapper, "new-session", "-d", "-s", sessionName, "-x", "80", "-y", "24", "sleep 60")
	runDisposable(t, wrapper, "rename-window", "-t", sessionName+":0", windowName)
	paneID := strings.TrimSpace(runDisposable(t, wrapper, "display-message", "-p", "-t", sessionName+":0.0", "#{pane_id}"))
	runDisposable(t, wrapper, "select-pane", "-t", paneID, "-T", paneTitle)
	runDisposable(t, wrapper, "set-option", "-p", "-t", paneID, "@oc_managed_by", "agent_wall")
	runDisposable(t, wrapper, "set-option", "-p", "-t", paneID, "@oc_kind", "agent")
	runDisposable(t, wrapper, "set-option", "-p", "-t", paneID, "@oc_goal", goal)

	client := &Client{bin: wrapper}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snap, err := client.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(snap.Sessions))
	}
	session := snap.Sessions[0]
	if session.Name != sessionName {
		t.Fatalf("session name = %q, want %q", session.Name, sessionName)
	}
	if len(session.Windows) != 1 || session.Windows[0].Name != windowName {
		t.Fatalf("window name = %q, want %q", session.Windows[0].Name, windowName)
	}
	if len(session.Windows[0].Panes) != 1 {
		t.Fatalf("panes = %d, want 1", len(session.Windows[0].Panes))
	}
	pane := session.Windows[0].Panes[0]
	if pane.Title != paneTitle {
		t.Fatalf("pane title = %q, want %q", pane.Title, paneTitle)
	}
	if pane.Cockpit == nil || pane.Cockpit.Goal != goal {
		t.Fatalf("pane goal did not round-trip: %+v", pane.Cockpit)
	}
	if snap.PaneParseWarnings != 0 {
		t.Fatalf("separator-bearing names must not create parse warnings, got %d", snap.PaneParseWarnings)
	}
}

// TestSnapshotParsesSeventyKilobyteAggregateMetadataOnRealTmux proves the
// second AC7 half: aggregate valid pane metadata around 70 KB (several
// accepted @oc_* options; a single 70 KB option is rejected by tmux command
// length) parses through the explicit row cap without truncation.
func TestSnapshotParsesSeventyKilobyteAggregateMetadataOnRealTmux(t *testing.T) {
	t.Parallel()

	wrapper := disposableTmux(t)
	runDisposable(t, wrapper, "new-session", "-d", "-s", "big", "-x", "80", "-y", "24", "sleep 60")
	paneID := strings.TrimSpace(runDisposable(t, wrapper, "display-message", "-p", "-t", "big:0.0", "#{pane_id}"))

	// Eleven fields of ~7 KB each: tmux rejects a single oversized option at
	// its command-length limit (committee-proven), but several accepted
	// options legitimately aggregate past 70 KB in one output row.
	chunk := strings.Repeat("x~", 3500) // 7 KB with escape-relevant tildes
	fields := []struct {
		option string
		get    func(*CockpitMeta) string
	}{
		{"@oc_goal", func(m *CockpitMeta) string { return m.Goal }},
		{"@oc_evidence_path", func(m *CockpitMeta) string { return m.EvidencePath }},
		{"@oc_progress_path", func(m *CockpitMeta) string { return m.ProgressPath }},
		{"@oc_hold_reason", func(m *CockpitMeta) string { return m.HoldReason }},
		{"@oc_why_headless", func(m *CockpitMeta) string { return m.WhyHeadless }},
		{"@oc_project", func(m *CockpitMeta) string { return m.Project }},
		{"@oc_owner", func(m *CockpitMeta) string { return m.Owner }},
		{"@oc_run_root", func(m *CockpitMeta) string { return m.RunRoot }},
		{"@oc_end_reason", func(m *CockpitMeta) string { return m.EndReason }},
		{"@oc_teardown_reason", func(m *CockpitMeta) string { return m.TeardownReason }},
		{"@oc_route_failure_reason", func(m *CockpitMeta) string { return m.RouteFailure }},
	}
	want := make(map[string]string, len(fields))
	for _, field := range fields {
		value := field.option[4:] + "-" + chunk
		want[field.option] = value
		runDisposable(t, wrapper, "set-option", "-p", "-t", paneID, field.option, value)
	}

	client := &Client{bin: wrapper}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	snap, err := client.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Sessions) != 1 || len(snap.Sessions[0].Windows) != 1 || len(snap.Sessions[0].Windows[0].Panes) != 1 {
		t.Fatalf("unexpected snapshot shape")
	}
	pane := snap.Sessions[0].Windows[0].Panes[0]
	if pane.Cockpit == nil {
		t.Fatal("cockpit metadata missing")
	}
	total := 0
	for _, field := range fields {
		got := field.get(pane.Cockpit)
		if got != want[field.option] {
			t.Fatalf("%s did not round-trip (%d bytes vs %d)", field.option, len(got), len(want[field.option]))
		}
		total += len(got)
	}
	if total < 70_000 {
		t.Fatalf("aggregate metadata = %d bytes, fixture must exceed 70KB", total)
	}
	if snap.PaneParseWarnings != 0 {
		t.Fatalf("valid 70KB metadata must not be skipped, warnings = %d", snap.PaneParseWarnings)
	}
}
