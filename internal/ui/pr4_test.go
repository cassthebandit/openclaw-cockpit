package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

func TestPR4DeclaredJSONSymlinkAndMissingRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "blob"), []byte(`{"status":"FAIL"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("blob", filepath.Join(root, "result.json")); err != nil {
		t.Fatal(err)
	}
	pane := tmux.Pane{Cockpit: &tmux.CockpitMeta{RunRoot: root, EvidencePath: "result.json"}}
	if got := artifactOutcomeState(pane); got != "failed" {
		t.Fatalf("got %q", got)
	}
	pane.Cockpit.RunRoot = filepath.Join(root, "missing")
	if got := artifactOutcomeState(pane); got != "review" {
		t.Fatalf("missing root: %q", got)
	}
}

func TestPR4ExpiredHoldDoesNotClaimProtection(t *testing.T) {
	now := time.Now()
	pane := tmux.Pane{Dead: true, Cockpit: &tmux.CockpitMeta{HoldReason: "review", HoldUntil: now.Add(-time.Minute).Format(time.RFC3339)}}
	got := cockpitCleanupLine(nil, tmux.Session{}, pane, now)
	if strings.Contains(got, "hold blocks cleanup") || !strings.Contains(got, "hold expired") {
		t.Fatal(got)
	}
}

func TestPR4SidecarRejectsRespawnPID(t *testing.T) {
	created := time.Unix(100, 0)
	session := tmux.Session{CreatedAt: created, Windows: []tmux.Window{{Panes: []tmux.Pane{{ID: "%1", PID: "200", CreatedAt: created}}}}}
	row := janitorSessionStatus{PaneID: "%1", PanePID: "199", PaneCreated: "100"}
	if janitorRowJoin(row, session) != janitorJoinMismatch {
		t.Fatal("stale PID joined")
	}
	row.PanePID = "200"
	if janitorRowJoin(row, session) != janitorJoinOK {
		t.Fatal("current PID refused")
	}
}

func TestPR4OpenCLICompletionIsDisplayOnly(t *testing.T) {
	pane := tmux.Pane{ID: "%1", CurrentCmd: "claude", PreviewText: "Worked for 2m\n❯", Cockpit: &tmux.CockpitMeta{ManagedBy: "agent_wall", Kind: "visible-agent", Agent: "fable", State: "running"}}
	session := tmux.Session{Windows: []tmux.Window{{Panes: []tmux.Pane{pane}}}}
	got := cockpitCleanupLine(nil, session, pane, time.Now())
	if !strings.Contains(got, "CLI open") {
		t.Fatal(got)
	}
	if pane.Cockpit.State != "running" {
		t.Fatal("display mutated cleanup state")
	}
}
