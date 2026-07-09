package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadJanitorStatusFileStates(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 7, 20, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	missing := loadJanitorStatusFile(filepath.Join(dir, "missing.json"), now)
	if missing.State != "missing" {
		t.Fatalf("missing state = %q", missing.State)
	}

	path := filepath.Join(dir, "status.json")
	if err := os.WriteFile(path, []byte(`{"status_version":1,"generated_at":"2026-07-07T19:59:00Z","sessions":{},"last_cycle":{"mark":1,"refuse":2}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ok := loadJanitorStatusFile(path, now)
	if ok.State != "ok" || ok.Cycle.Mark != 1 || ok.Cycle.Refuse != 2 {
		t.Fatalf("ok status = %#v", ok)
	}

	if err := os.WriteFile(path, []byte(`{"status_version":1,"generated_at":"2026-07-07T19:50:00Z","sessions":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := loadJanitorStatusFile(path, now)
	if stale.State != "stale" {
		t.Fatalf("stale state = %q", stale.State)
	}

	if err := os.WriteFile(path, []byte(`{"status_version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	invalid := loadJanitorStatusFile(path, now)
	if invalid.State != "invalid" {
		t.Fatalf("invalid state = %q", invalid.State)
	}
}

func TestJanitorStatusFooterLine(t *testing.T) {
	t.Parallel()

	m := &Model{janitorStatus: janitorStatusView{State: "ok", Cycle: janitorCycleStatus{Mark: 1, Refuse: 2}}}
	line := m.janitorStatusLine(120)
	for _, want := range []string{"janitor: ok", "marked 1", "held/refused 2"} {
		if !strings.Contains(line, want) {
			t.Fatalf("status line missing %q in %q", want, line)
		}
	}
}
