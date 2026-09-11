package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func configCommand(t *testing.T, body string, environment []string, args ...string) ([]byte, error) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(testBinPath, append([]string{"--config", path}, args...)...)
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "HOME=") || strings.HasPrefix(item, "OPENCLAW_COCKPIT_") || strings.HasPrefix(item, "CASS_WALL_") || strings.HasPrefix(item, "CASS_TMUX_HYGIENE_") {
			continue
		}
		command.Env = append(command.Env, item)
	}
	command.Env = append(command.Env, "HOME="+home)
	command.Env = append(command.Env, environment...)
	return command.CombinedOutput()
}

func TestConfigCLIDefaultsDoNotMaskFile(t *testing.T) {
	out, err := configCommand(t, `{"cols":2,"fps":30,"interval":"3s","organize":true,"preserve_colors":true,"runtime_limit":12}`, nil, "--dump-config")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var cfg struct {
		Columns  int    `json:"cols"`
		FPS      int    `json:"fps"`
		Interval string `json:"interval"`
		Organize bool   `json:"organize"`
		Colors   bool   `json:"preserve_colors"`
		Limit    int    `json:"runtime_limit"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Columns != 2 || cfg.FPS != 30 || cfg.Interval != "3s" || !cfg.Organize || !cfg.Colors || cfg.Limit != 12 {
		t.Fatalf("parser defaults masked config: %s", out)
	}
	out, err = configCommand(t, `{"cols":2,"organize":true}`, []string{"CASS_WALL_COLS=3"}, "--cols=0", "--organize=false", "--dump-config")
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Columns != 0 || cfg.Organize {
		t.Fatalf("explicit zero/false lost: %s", out)
	}
}

func TestValidateConfigWithoutTmuxOrTUI(t *testing.T) {
	out, err := configCommand(t, `{"cols":2,"tmux":"/missing/tmux"}`, nil, "--validate-config")
	if err != nil || !strings.Contains(string(out), "configuration valid") {
		t.Fatalf("validation started runtime: %v %s", err, out)
	}
	for _, body := range []string{`{"cols":-1}`, `{"fps":null}`, `{"unknown":1}`, `{} {}`, `null`, `{"runtime_limit":0}`, `{"interval":"1ms"}`} {
		out, err := configCommand(t, body, nil, "--validate-config")
		if err == nil || !strings.Contains(string(out), "config") {
			t.Fatalf("invalid config accepted: %s: %s", body, out)
		}
	}
}

func TestConfigOverrideDiagnosticsNameSource(t *testing.T) {
	out, err := configCommand(t, `{}`, []string{"CASS_WALL_COLS=bad"}, "--validate-config")
	if err == nil || !strings.Contains(string(out), "CASS_WALL_COLS") {
		t.Fatalf("missing override source: %v %s", err, out)
	}
	out, err = configCommand(t, `{}`, []string{"CASS_WALL_COLS=3"}, "--cols=2", "--explain-config", "--validate-config")
	if err != nil || !strings.Contains(string(out), "CLI --cols") {
		t.Fatalf("missing CLI source: %v %s", err, out)
	}
}

func TestEverySupportedCLISettingOverridesFile(t *testing.T) {
	values := map[string]string{"interval": "2s", "fps": "30", "cols": "2", "capture-budget": "9", "tmux": "/tmp/test-tmux", "organize": "true", "preserve-colors": "true", "exclude-session": "one,two", "openclaw-runtime": "true", "openclaw-runtime-script": "/tmp/test-source.py", "openclaw-runtime-limit": "7", "openclaw-runtime-interval": "8s", "janitor-status": "/tmp/test-status.json"}
	for name, key := range configFlagKeys {
		t.Run(name, func(t *testing.T) {
			value, ok := values[name]
			if !ok {
				t.Fatalf("missing test for supported flag %s", name)
			}
			out, err := configCommand(t, `{}`, nil, "--"+name+"="+value, "--dump-config")
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			var result map[string]json.RawMessage
			if err := json.Unmarshal(out, &result); err != nil {
				t.Fatal(err)
			}
			got := string(result[key])
			switch name {
			case "exclude-session":
				var names []string
				if err := json.Unmarshal(result[key], &names); err != nil {
					t.Fatal(err)
				}
				if strings.Join(names, ",") != "one,two" {
					t.Fatal(got)
				}
			case "organize", "preserve-colors", "openclaw-runtime", "fps", "cols", "capture-budget", "openclaw-runtime-limit":
				if got != value {
					t.Fatal(got)
				}
			default:
				var text string
				if err := json.Unmarshal(result[key], &text); err != nil {
					t.Fatal(err)
				}
				if text != value {
					t.Fatal(text)
				}
			}
		})
	}
}
