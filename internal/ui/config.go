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
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// WallConfig holds the extractable presentation policies. Defaults reproduce
// the built-in wall behavior with no config file present.
type WallConfig struct {
	Interval           configDuration `json:"interval"`
	FPS                int            `json:"fps"`
	Columns            int            `json:"cols"`
	CaptureBudget      int            `json:"capture_budget"`
	CaptureRate        int            `json:"capture_rate"`
	CaptureMinLines    int            `json:"capture_min_lines"`
	CaptureMaxLines    int            `json:"capture_max_lines"`
	CaptureSlackLines  int            `json:"capture_slack_lines"`
	Organize           bool           `json:"organize"`
	PreserveColors     bool           `json:"preserve_colors"`
	ExcludeSessions    []string       `json:"exclude_sessions"`
	Tmux               string         `json:"tmux"`
	OpenClawRuntime    bool           `json:"openclaw_runtime"`
	RuntimeScript      string         `json:"runtime_script"`
	RuntimeLimit       int            `json:"runtime_limit"`
	RuntimeInterval    configDuration `json:"runtime_interval"`
	RuntimeTimeout     configDuration `json:"runtime_timeout"`
	DumpRuntimeTimeout configDuration `json:"dump_runtime_timeout"`
	JanitorStatus      string         `json:"janitor_status"`
	Grouping           GroupingConfig `json:"grouping"`

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
		Interval: configDuration(defaultPollInterval), FPS: 60, CaptureBudget: maxCapturesPerTick,
		CaptureRate: aggregateCaptureBudgetPerSecond, CaptureMinLines: minCaptureLines, CaptureMaxLines: maxCaptureLines, CaptureSlackLines: captureSlackLines,
		ExcludeSessions: []string{}, RuntimeLimit: 80, RuntimeInterval: configDuration(runtimeCardInterval), RuntimeTimeout: configDuration(defaultOpenClawRuntimeTimeout), DumpRuntimeTimeout: configDuration(45 * time.Second),
		Grouping:          DefaultGroupingConfig(),
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
	if !strings.HasPrefix(strings.TrimSpace(string(data)), "{") {
		return DefaultWallConfig(), fmt.Errorf("parse %s: config must be a JSON object", path)
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
	if err := rejectNullSettings(data, ""); err != nil {
		return DefaultWallConfig(), fmt.Errorf("invalid config %s: %w", path, err)
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
	for _, item := range []struct {
		name            string
		value, min, max int
	}{
		{"fps", c.FPS, 1, 120},
		{"cols", c.Columns, 0, 32},
		{"capture_budget", c.CaptureBudget, 1, 120},
		{"capture_rate", c.CaptureRate, 1, 120},
		{"capture_min_lines", c.CaptureMinLines, 1, 5000},
		{"capture_max_lines", c.CaptureMaxLines, 1, 5000},
		{"capture_slack_lines", c.CaptureSlackLines, 0, 1000},
		{"runtime_limit", c.RuntimeLimit, 1, 1000},
	} {
		if item.value < item.min || item.value > item.max {
			problems = append(problems, fmt.Sprintf("%s must be between %d and %d", item.name, item.min, item.max))
		}
	}
	if c.CaptureMinLines > c.CaptureMaxLines {
		problems = append(problems, "capture_min_lines must not exceed capture_max_lines")
	}
	for _, item := range []struct {
		name     string
		value    configDuration
		min, max time.Duration
	}{
		{"interval", c.Interval, 100 * time.Millisecond, time.Hour},
		{"runtime_interval", c.RuntimeInterval, 100 * time.Millisecond, time.Hour},
		{"runtime_timeout", c.RuntimeTimeout, 100 * time.Millisecond, 5 * time.Minute},
		{"dump_runtime_timeout", c.DumpRuntimeTimeout, 100 * time.Millisecond, 5 * time.Minute},
	} {
		if time.Duration(item.value) < item.min || time.Duration(item.value) > item.max {
			problems = append(problems, fmt.Sprintf("%s must be between %s and %s", item.name, item.min, item.max))
		}
	}
	for name, tokens := range map[string][]string{"agent_keywords": c.Grouping.AgentKeywords, "service_keywords": c.Grouping.ServiceKeywords, "dashboard_keywords": c.Grouping.DashboardKeywords, "viewer_keywords": c.Grouping.ViewerKeywords} {
		if len(tokens) > 100 {
			problems = append(problems, "grouping."+name+" allows at most 100 keywords")
		}
		for _, token := range tokens {
			if strings.TrimSpace(token) == "" || len(token) > 100 || cardSafeLine(token) != strings.TrimSpace(token) {
				problems = append(problems, "grouping."+name+" keywords must be nonempty safe text (at most 100 bytes)")
			}
		}
	}
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
	m.pollInterval = time.Duration(cfg.Interval)
	m.captureBudget = cfg.CaptureBudget
	m.captureRate = cfg.CaptureRate
	m.captureMinLines = cfg.CaptureMinLines
	m.captureMaxLines = cfg.CaptureMaxLines
	m.captureSlackLines = cfg.CaptureSlackLines
	m.grouping = &cfg.Grouping
	m.SetPreferredColumns(cfg.Columns)
	m.SetOrganized(cfg.Organize)
	m.SetJanitorStatusFile(cfg.JanitorStatus)
	m.runtime = NormalizeRuntimeSource(RuntimeSource{Enabled: cfg.OpenClawRuntime, Script: cfg.RuntimeScript, Limit: cfg.RuntimeLimit, Timeout: time.Duration(cfg.RuntimeTimeout), Interval: time.Duration(cfg.RuntimeInterval)})
	m.invalidateClassifications()
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

// Environment aliases keep supported older launchers working. The public name
// wins when both it and a legacy alias are explicitly set. Empty values are
// explicit too; no wrapper-injected defaults override a user file here.
var ConfigEnvironment = map[string][]string{
	"cols": {"OPENCLAW_COCKPIT_COLS", "CASS_WALL_COLS"}, "fps": {"OPENCLAW_COCKPIT_FPS", "CASS_WALL_FPS"},
	"interval":         {"OPENCLAW_COCKPIT_INTERVAL", "CASS_WALL_INTERVAL"},
	"capture_budget":   {"OPENCLAW_COCKPIT_CAPTURE_BUDGET", "CASS_WALL_CAPTURE_BUDGET"},
	"runtime_limit":    {"OPENCLAW_COCKPIT_RUNTIME_LIMIT", "CASS_WALL_RUNTIME_LIMIT"},
	"runtime_interval": {"OPENCLAW_COCKPIT_RUNTIME_INTERVAL", "CASS_WALL_RUNTIME_INTERVAL"},
	"exclude_sessions": {"OPENCLAW_COCKPIT_EXCLUDE_SESSIONS", "CASS_WALL_EXCLUDE_SESSIONS"},
	"janitor_status":   {"OPENCLAW_COCKPIT_JANITOR_STATUS", "CASS_TMUX_HYGIENE_STATUS_FILE"},
	"runtime_script":   {"OPENCLAW_COCKPIT_RUNTIME_SCRIPT"},
}

// ResolveWallConfig applies only explicit environment and CLI overrides, then
// resolves integration paths. Sources describe actual overrides for diagnostics.
func ResolveWallConfig(path string, cli map[string]string, lookup func(string) (string, bool)) (WallConfig, map[string]string, error) {
	cfg, err := LoadWallConfig(path)
	sources := map[string]string{}
	if err != nil {
		return cfg, sources, err
	}
	values := map[string]string{}
	for key, names := range ConfigEnvironment {
		for _, name := range names {
			if value, ok := lookup(name); ok {
				values[key] = value
				sources[key] = "environment " + name
				break
			}
		}
	}
	for key, value := range cli {
		values[key] = value
		sources[key] = "CLI --" + configFlagName(key)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := applyConfigOverride(&cfg, key, values[key]); err != nil {
			return DefaultWallConfig(), sources, fmt.Errorf("%s (%s): %w", key, sources[key], err)
		}
	}
	if err := cfg.Validate(); err != nil {
		detail := []string{}
		for _, key := range keys {
			detail = append(detail, key+" from "+sources[key])
		}
		return DefaultWallConfig(), sources, fmt.Errorf("%w; overrides: %s", err, strings.Join(detail, ", "))
	}
	for _, field := range []*string{&cfg.RuntimeScript, &cfg.JanitorStatus, &cfg.Tmux} {
		value := strings.TrimSpace(*field)
		if strings.HasPrefix(value, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return DefaultWallConfig(), sources, fmt.Errorf("resolve home in config path: %w", err)
			}
			value = filepath.Join(home, value[2:])
		}
		if value != "" && (strings.ContainsAny(value, "/\\") || field != &cfg.Tmux) {
			if absolute, err := filepath.Abs(value); err == nil {
				value = absolute
			}
		}
		*field = value
	}
	if cfg.RuntimeScript == "" {
		cfg.RuntimeScript = DefaultRuntimeScript()
	}
	if cfg.Tmux == "" {
		if path, err := exec.LookPath("tmux"); err == nil {
			cfg.Tmux = path
		}
	}
	return cfg, sources, nil
}

func applyConfigOverride(cfg *WallConfig, key, value string) error {
	var encoded []byte
	var err error
	switch key {
	case "fps", "cols", "capture_budget", "runtime_limit":
		var number int
		number, err = strconv.Atoi(value)
		if err == nil {
			encoded, err = json.Marshal(number)
		}
	case "organize", "preserve_colors", "openclaw_runtime":
		var boolean bool
		boolean, err = strconv.ParseBool(value)
		if err == nil {
			encoded, err = json.Marshal(boolean)
		}
	case "exclude_sessions":
		list := []string{}
		for _, name := range strings.Split(value, ",") {
			if strings.TrimSpace(name) != "" {
				list = append(list, strings.TrimSpace(name))
			}
		}
		encoded, err = json.Marshal(list)
	case "interval", "runtime_interval", "runtime_script", "janitor_status", "tmux":
		encoded, err = json.Marshal(value)
	default:
		return fmt.Errorf("unsupported override")
	}
	if err != nil {
		return err
	}
	patch, err := json.Marshal(map[string]json.RawMessage{key: encoded})
	if err != nil {
		return err
	}
	return json.Unmarshal(patch, cfg)
}

func rejectNullSettings(data []byte, prefix string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	for key, value := range object {
		text := strings.TrimSpace(string(value))
		if text == "null" {
			return fmt.Errorf("%s%s must not be null", prefix, key)
		}
		if strings.HasPrefix(text, "{") {
			if err := rejectNullSettings(value, prefix+key+"."); err != nil {
				return err
			}
		}
	}
	return nil
}

func configFlagName(key string) string {
	switch key {
	case "runtime_script":
		return "openclaw-runtime-script"
	case "runtime_limit":
		return "openclaw-runtime-limit"
	case "runtime_interval":
		return "openclaw-runtime-interval"
	case "exclude_sessions":
		return "exclude-session"
	}
	return strings.ReplaceAll(key, "_", "-")
}
