package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// TestJanitorStatusFileOverCapReportsInvalid proves AC8's janitor half: a
// status file above the documented 4 MiB cap is rejected as a visible invalid
// state instead of being decoded, and the model keeps working.
func TestJanitorStatusFileOverCapReportsInvalid(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "status.json")
	huge := make([]byte, janitorStatusCapBytes+1024)
	for i := range huge {
		huge[i] = 'x'
	}
	if err := os.WriteFile(path, huge, 0o644); err != nil {
		t.Fatalf("write oversized status: %v", err)
	}
	view := loadJanitorStatusFile(path, time.Now(), time.Minute)
	if view.State != "invalid" {
		t.Fatalf("over-cap status state = %q, want invalid", view.State)
	}
	if !strings.Contains(view.Detail, "cap") {
		t.Fatalf("over-cap detail must name the cap, got %q", view.Detail)
	}
}

// writeRuntimeScript drops an executable python snapshot producer for the
// bounded-ingestion tests.
func writeRuntimeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snapshot.py")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write runtime script: %v", err)
	}
	return path
}

// TestRuntimeSnapshotOutputOverCapFailsVisibly proves runtime subprocess
// output is bounded before allocation/JSON decoding.
func TestRuntimeSnapshotOutputOverCapFailsVisibly(t *testing.T) {
	t.Parallel()

	script := writeRuntimeScript(t, `import sys
sys.stdout.write("x" * (5 * 1024 * 1024))
`)
	_, err := loadOpenClawRuntimeCards(RuntimeSource{
		Enabled: true,
		Script:  script,
		Limit:   10,
		Timeout: 30 * time.Second,
	})
	if err == nil {
		t.Fatal("over-cap runtime output must fail")
	}
	if !strings.Contains(err.Error(), "cap") {
		t.Fatalf("over-cap error must name the cap, got %v", err)
	}
}

// TestRuntimeCardLimitEnforcedLocally proves the requested card limit binds
// even when the producer ignores --limit and returns more cards.
func TestRuntimeCardLimitEnforcedLocally(t *testing.T) {
	t.Parallel()

	script := writeRuntimeScript(t, `import json
cards = [{"id": "c%d" % i, "cardContract": "runtime-card.v1"} for i in range(25)]
print(json.dumps({"cardContract": "runtime-card.v1", "summary": {}, "cards": cards}))
`)
	cards, err := loadOpenClawRuntimeCards(RuntimeSource{
		Enabled: true,
		Script:  script,
		Limit:   7,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("loadOpenClawRuntimeCards: %v", err)
	}
	if len(cards) != 7 {
		t.Fatalf("cards = %d, want the locally enforced limit 7", len(cards))
	}
}

// TestRuntimeOverCapSurfacesAsSourceErrorCard proves the failure stays inside
// the runtime lane: the tmux snapshot loop keeps its sessions and the runtime
// failure renders as one source-error card.
func TestRuntimeOverCapSurfacesAsSourceErrorCard(t *testing.T) {
	t.Parallel()

	script := writeRuntimeScript(t, `import sys
sys.stdout.write("x" * (5 * 1024 * 1024))
`)
	sessions := openClawRuntimeSessions(RuntimeSource{
		Enabled: true,
		Script:  script,
		Limit:   5,
		Timeout: 30 * time.Second,
	}, time.Now())
	if len(sessions) != 1 {
		t.Fatalf("runtime failure must yield one source-error card, got %d", len(sessions))
	}
	pane := sessions[0].Windows[0].Panes[0]
	if pane.Cockpit == nil || pane.Cockpit.State != "failed" {
		t.Fatalf("source-error card must be failed, got %+v", pane.Cockpit)
	}
}

// TestSnapshotErrorKeepsLastKnownSessions proves the update loop retains the
// last known wall on a snapshot error (for example the over-cap list error)
// instead of clearing it, while the error is surfaced in the footer.
func TestSnapshotErrorKeepsLastKnownSessions(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.sessions = []tmux.Session{sessionForGroup("survivor", "zsh", "/tmp", "")}
	m.inflight = true

	_, _ = m.handleMessage(errMsg{err: fmt.Errorf("list-panes: output row exceeds the %d byte cap", 1<<20)})

	if len(m.sessions) != 1 || m.sessions[0].Name != "survivor" {
		t.Fatalf("snapshot error must keep the last known sessions, got %+v", m.sessions)
	}
	if m.err == nil {
		t.Fatal("snapshot error must be visible on the model")
	}
	m.width = 120
	if footer := m.buildStatusLine(120); !strings.Contains(footer, "cap") {
		t.Fatalf("footer must surface the snapshot error, got %q", footer)
	}
}
