// File config.go implements the first-pass durable configuration surface
// defined by docs/config-contract.md. Config tunes presentation and timing
// only: it deliberately has no cleanup authority. There are no kill, hold,
// evidence, or countdown settings here, and unknown fields are rejected so a
// config file cannot smuggle one in. The janitor sidecar's kill_not_before
// always wins for countdown facts regardless of configuration.
package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WallConfig holds the extractable presentation policies. Defaults reproduce
// the built-in wall behavior with no config file present.
type WallConfig struct {
	// FooterMaxHeight bounds the status footer height in lines.
	FooterMaxHeight int `json:"footer_max_height"`
	// JanitorStaleAfter is how old the janitor status sidecar may be before
	// Cockpit reports it stale. Display health only — it never changes
	// cleanup eligibility.
	JanitorStaleAfter configDuration `json:"janitor_stale_after"`
	// StaleThreshold is how long a detached session may be quiet before it
	// renders as stale. Display only.
	StaleThreshold configDuration `json:"stale_threshold"`
	// CollapsedGroups and ExpandedGroups override the named groups' default
	// accordion state. Manual operator toggles still win afterward.
	CollapsedGroups []string `json:"collapsed_groups,omitempty"`
	ExpandedGroups  []string `json:"expanded_groups,omitempty"`
}

// configDuration parses human durations ("3m", "1h30m") in JSON config.
type configDuration time.Duration

func (d *configDuration) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("durations must be strings like \"3m\": %w", err)
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	*d = configDuration(parsed)
	return nil
}

func (d configDuration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// DefaultWallConfig returns the built-in defaults; with no config file the
// wall behaves exactly as these describe.
func DefaultWallConfig() WallConfig {
	return WallConfig{
		FooterMaxHeight:   maxFooterHeight,
		JanitorStaleAfter: configDuration(janitorStatusStaleAfter),
		StaleThreshold:    configDuration(staleThreshold),
	}
}

// DefaultWallConfigPath is the user config location when --config is not set.
func DefaultWallConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "openclaw-cockpit", "config.json")
}

// LoadWallConfig reads and validates the wall config. An empty path means the
// default location, where a missing file is not an error (defaults apply). An
// explicit path that cannot be read fails visibly. Invalid content fails
// visibly; callers fall back to defaults rather than guessing.
func LoadWallConfig(path string) (WallConfig, error) {
	cfg := DefaultWallConfig()
	explicit := strings.TrimSpace(path) != ""
	if !explicit {
		path = DefaultWallConfigPath()
		if path == "" {
			return cfg, nil
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !explicit && errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return DefaultWallConfig(), fmt.Errorf("read %s: %w", path, err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	// Unknown fields are rejected: config must not be able to introduce
	// cleanup-authority settings (kill targets, evidence rules, hold
	// overrides, countdown values) that Cockpit is not allowed to own.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return DefaultWallConfig(), fmt.Errorf("parse %s: %w", path, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return DefaultWallConfig(), fmt.Errorf("parse %s: expected EOF after config object", path)
	}
	if err := cfg.Validate(); err != nil {
		return DefaultWallConfig(), fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

// Validate rejects values that would corrupt layout or silently change
// meaning.
func (c WallConfig) Validate() error {
	var problems []string
	if c.FooterMaxHeight < 1 {
		problems = append(problems, "footer_max_height must be >= 1")
	}
	if time.Duration(c.JanitorStaleAfter) <= 0 {
		problems = append(problems, "janitor_stale_after must be a positive duration")
	}
	if time.Duration(c.StaleThreshold) <= 0 {
		problems = append(problems, "stale_threshold must be a positive duration")
	}
	known := knownGroupNames()
	seen := map[string]string{}
	for _, name := range c.CollapsedGroups {
		if _, ok := known[name]; !ok {
			problems = append(problems, fmt.Sprintf("collapsed_groups: unknown group %q", name))
		}
		seen[name] = "collapsed"
	}
	for _, name := range c.ExpandedGroups {
		if _, ok := known[name]; !ok {
			problems = append(problems, fmt.Sprintf("expanded_groups: unknown group %q", name))
		}
		if seen[name] == "collapsed" {
			problems = append(problems, fmt.Sprintf("group %q is in both collapsed_groups and expanded_groups", name))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// allCockpitGroups is the full canonical registry (docs/group-registry.md).
func allCockpitGroups() []cockpitGroup {
	return []cockpitGroup{
		groupActiveAgents,
		groupHeldAgents,
		groupInactiveAgents,
		groupCleanupBlocked,
		groupFailedAgents,
		groupOperationalFailures,
		groupSubsystemFailures,
		groupServices,
		groupDoneHeld,
		groupWork,
		groupDashboard,
		groupViewers,
		groupIdle,
	}
}

func knownGroupNames() map[string]struct{} {
	names := make(map[string]struct{})
	for _, group := range allCockpitGroups() {
		names[group.name] = struct{}{}
	}
	return names
}

// ApplyWallConfig installs the validated presentation config on the model.
// Cleanup facts (kill_not_before, eligibility, refusal reasons) always come
// from the janitor sidecar; nothing here can override them.
func (m *Model) ApplyWallConfig(cfg WallConfig) {
	if m == nil {
		return
	}
	if cfg.FooterMaxHeight >= 1 {
		m.footerMaxHeight = cfg.FooterMaxHeight
	}
	if d := time.Duration(cfg.JanitorStaleAfter); d > 0 {
		m.janitorStaleAfter = d
	}
	if d := time.Duration(cfg.StaleThreshold); d > 0 {
		m.staleLimit = d
	}
	if len(cfg.CollapsedGroups) > 0 || len(cfg.ExpandedGroups) > 0 {
		if m.groupCollapseOverride == nil {
			m.groupCollapseOverride = make(map[string]bool)
		}
		for _, name := range cfg.CollapsedGroups {
			m.groupCollapseOverride[name] = true
		}
		for _, name := range cfg.ExpandedGroups {
			m.groupCollapseOverride[name] = false
		}
	}
}
