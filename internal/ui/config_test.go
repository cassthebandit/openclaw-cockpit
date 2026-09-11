package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

func TestDefaultWallConfigReproducesBuiltins(t *testing.T) {
	t.Parallel()

	cfg := DefaultWallConfig()
	if cfg.FooterMaxHeight != maxFooterHeight {
		t.Fatalf("footer default = %d, want %d", cfg.FooterMaxHeight, maxFooterHeight)
	}
	if time.Duration(cfg.JanitorStaleAfter) != janitorStatusStaleAfter {
		t.Fatalf("janitor stale default = %v", time.Duration(cfg.JanitorStaleAfter))
	}
	if time.Duration(cfg.StaleThreshold) != staleThreshold {
		t.Fatalf("stale threshold default = %v", time.Duration(cfg.StaleThreshold))
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}

func TestLoadWallConfigMissingDefaultPathUsesDefaults(t *testing.T) {
	cfg, err := LoadWallConfig("")
	if err != nil {
		// A user config may legitimately exist on this machine; only a
		// parse/validation failure is a test failure.
		t.Fatalf("default-path load error: %v", err)
	}
	if cfg.FooterMaxHeight < 1 {
		t.Fatalf("footer height = %d", cfg.FooterMaxHeight)
	}
}

func TestLoadWallConfigExplicitMissingPathFailsVisibly(t *testing.T) {
	t.Parallel()

	if _, err := LoadWallConfig(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatalf("explicit missing config path must error")
	}
}

func TestLoadWallConfigParsesAndValidates(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.json")
	body := `{
		"footer_max_height": 2,
		"janitor_stale_after": "5m",
		"stale_threshold": "30m",
		"collapsed_groups": ["Services"],
		"expanded_groups": ["Completed Agent Runs"]
	}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWallConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.FooterMaxHeight != 2 || time.Duration(cfg.JanitorStaleAfter) != 5*time.Minute || time.Duration(cfg.StaleThreshold) != 30*time.Minute {
		t.Fatalf("parsed config = %#v", cfg)
	}

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.ApplyWallConfig(cfg)
	if m.footerMaxHeight != 2 || m.janitorStaleAfter != 5*time.Minute || m.staleLimit != 30*time.Minute {
		t.Fatalf("applied config: footer=%d janitor=%v stale=%v", m.footerMaxHeight, m.janitorStaleAfter, m.staleLimit)
	}
	m.seedGroupCollapse(allCockpitGroups())
	if !m.isGroupCollapsed(groupServices.name) {
		t.Fatalf("config collapsed_groups override not applied to Services")
	}
	if m.isGroupCollapsed(groupCompletedAgents.name) {
		t.Fatalf("config expanded_groups override not applied to Completed Agent Runs")
	}
	// Manual toggles still win after seeding.
	m.toggleGroupCollapsed(groupServices.name)
	if m.isGroupCollapsed(groupServices.name) {
		t.Fatalf("manual toggle must beat config default")
	}
}

func TestLoadWallConfigRejectsCleanupAuthorityFields(t *testing.T) {
	t.Parallel()

	// Config must not be able to own cleanup meaning: unknown fields —
	// including anything countdown/kill/evidence shaped — fail visibly.
	for _, body := range []string{
		`{"kill_not_before": "2026-07-09T23:00:00Z"}`,
		`{"teardown_grace": "1s"}`,
		`{"override_hold": true}`,
		`{"evidence_required": false}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadWallConfig(path); err == nil {
			t.Fatalf("config %s must be rejected", body)
		}
	}
}

func TestLoadWallConfigRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"zero-footer":      `{"footer_max_height": 0}`,
		"negative-timer":   `{"janitor_stale_after": "-3m"}`,
		"unknown-group":    `{"collapsed_groups": ["No Such Group"]}`,
		"conflicted-group": `{"collapsed_groups": ["Services"], "expanded_groups": ["Services"]}`,
		"non-string-timer": `{"stale_threshold": 42}`,
	}
	for name, body := range cases {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadWallConfig(path); err == nil {
				t.Fatalf("config %s must be rejected", body)
			}
		})
	}
}

func TestConfiguredJanitorStaleAfterChangesFreshness(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	payload := `{"status_version":1,"generated_at":"2026-07-07T19:50:00Z","sessions":{}}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 7, 20, 0, 0, 0, time.UTC)
	// 10 minutes old: stale at the 3m default, fresh at a configured 15m.
	if got := loadJanitorStatusFile(path, now, janitorStatusStaleAfter); got.State != "stale" {
		t.Fatalf("default stale-after state = %q", got.State)
	}
	if got := loadJanitorStatusFile(path, now, 15*time.Minute); got.State != "ok" {
		t.Fatalf("configured stale-after state = %q", got.State)
	}
}

func TestConfigCannotOverrideSidecarCountdown(t *testing.T) {
	t.Parallel()

	// Even with aggressive presentation config applied, the rendered
	// countdown still derives from the sidecar kill_not_before.
	now := time.Date(2026, 7, 9, 23, 0, 0, 0, time.UTC)
	session := sidecarJoinSession("marked-lane")
	m := modelWithJanitorSidecar(map[string]janitorSessionStatus{
		"marked-lane": sidecarRowFor(session, janitorSessionStatus{JanitorState: "marked_for_teardown", KillNotBefore: "2026-07-09T23:06:00Z"}),
	})
	m.ApplyWallConfig(WallConfig{
		FooterMaxHeight:   1,
		JanitorStaleAfter: configDuration(time.Minute),
		StaleThreshold:    configDuration(time.Minute),
	})
	pane := tmux.Pane{Cockpit: &tmux.CockpitMeta{TeardownMarkedAt: "2026-07-09T22:58:00Z"}}
	got := cockpitCleanupLine(m, session, pane, now)
	if !strings.Contains(got, "cleanup in ") {
		t.Fatalf("countdown must still come from sidecar kill_not_before, got %q", got)
	}
}

func TestStartupConfigErrorSurfacesInFooter(t *testing.T) {
	t.Parallel()

	m := &Model{width: 120}
	m.SetStartupError(errors.New("wall config error: invalid config"))
	got := m.buildStatusLine(120)
	if !strings.Contains(got, "wall config error") {
		t.Fatalf("startup config error missing from footer: %q", got)
	}
}
