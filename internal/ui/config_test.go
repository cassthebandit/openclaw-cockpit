package ui

import (
	"encoding/json"
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
	t.Setenv("HOME", t.TempDir())
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

func TestEveryEnvironmentAliasAndCLIOverridePrecedence(t *testing.T) {
	values := map[string]string{"cols": "3", "fps": "30", "interval": "2s", "capture_budget": "9", "runtime_limit": "11", "runtime_interval": "7s", "exclude_sessions": "alpha,beta", "janitor_status": "/tmp/status.json", "runtime_script": "/tmp/snapshot.py"}
	for key, names := range ConfigEnvironment {
		for _, name := range names {
			t.Run(name, func(t *testing.T) {
				env := map[string]string{name: values[key]}
				lookup := func(key string) (string, bool) { v, ok := env[key]; return v, ok }
				_, _, err := ResolveWallConfig(filepath.Join(t.TempDir(), "missing"), nil, lookup)
				if err == nil {
					t.Fatal("explicit missing config accepted")
				}
				path := filepath.Join(t.TempDir(), "config.json")
				if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
				cfg, sources, err := ResolveWallConfig(path, nil, lookup)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(sources[key], name) {
					t.Fatalf("alias not applied: %v", sources)
				}
				want := DefaultWallConfig()
				if err := applyConfigOverride(&want, key, values[key]); err != nil {
					t.Fatal(err)
				}
				gotJSON, err := json.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				wantJSON, err := json.Marshal(want)
				if err != nil {
					t.Fatal(err)
				}
				var gotFields, wantFields map[string]json.RawMessage
				if err := json.Unmarshal(gotJSON, &gotFields); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(wantJSON, &wantFields); err != nil {
					t.Fatal(err)
				}
				if string(gotFields[key]) != string(wantFields[key]) {
					t.Fatalf("override %s not effective", key)
				}
				_, sources, err = ResolveWallConfig(path, map[string]string{key: values[key]}, lookup)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(sources[key], "CLI") {
					t.Fatalf("CLI did not win: %v", sources)
				}
			})
		}
	}
}

func TestConfigWiresGroupingRefreshAndCaptureWithoutAuthority(t *testing.T) {
	cfg := DefaultWallConfig()
	cfg.Interval = configDuration(2 * time.Second)
	cfg.CaptureRate = 2
	cfg.CaptureMinLines = 12
	cfg.CaptureMaxLines = 30
	cfg.CaptureSlackLines = 5
	cfg.Grouping.ServiceKeywords = []string{"CUSTOM-WATCHER"}
	cfg.Organize = true
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.ApplyWallConfig(cfg)
	if m.pollInterval != 2*time.Second || m.captureLines(20) != 25 || m.captureLines(100) != 30 {
		t.Fatal("timing/capture config not wired")
	}
	now := time.Now()
	first, second, third := m.takeCaptureToken(now), m.takeCaptureToken(now), m.takeCaptureToken(now)
	if !first || !second || third {
		t.Fatal("capture rate ignored")
	}
	session := sessionForGroup("custom-watcher", "node", "", "")
	if group := cockpitGroupFor(m, session); group != groupServices {
		t.Fatalf("custom service keyword ignored: %+v", group)
	}
	session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{Kind: "agent", State: "running"}
	m.invalidateClassifications()
	if group := cockpitGroupFor(m, session); group != groupActiveAgents {
		t.Fatal("keyword stole managed agent")
	}
	if !m.monitorOnly {
		t.Fatal("configuration acquired control authority")
	}
}

func TestConfigExampleMatchesDefaults(t *testing.T) {
	cfg, err := LoadWallConfig("../../examples/config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(DefaultWallConfig())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("example defaults drifted\ngot %s\nwant %s", got, want)
	}
	if _, err := LoadWallConfig("../../examples/config.organized.json"); err != nil {
		t.Fatal(err)
	}
}

func TestPublicEnvironmentAliasWinsAndCLIReallyReplacesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"cols":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"CASS_WALL_COLS": "2", "OPENCLAW_COCKPIT_COLS": "3"}
	lookup := func(key string) (string, bool) { value, ok := env[key]; return value, ok }
	cfg, _, err := ResolveWallConfig(path, nil, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Columns != 3 {
		t.Fatalf("public alias lost: %d", cfg.Columns)
	}
	cfg, _, err = ResolveWallConfig(path, map[string]string{"cols": "4"}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Columns != 4 {
		t.Fatalf("CLI lost: %d", cfg.Columns)
	}
}
