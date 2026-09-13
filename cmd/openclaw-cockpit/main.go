// File main.go wires command-line flags and the Bubble Tea program together.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/cassthebandit/openclaw-cockpit/internal/zone"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
	"github.com/cassthebandit/openclaw-cockpit/internal/ui"
)

const productName = "OpenClaw Cockpit"

var version = "0.9.5"

// main configures the tmux client, handles flag modes, and launches Bubble Tea.
func main() {
	zone.NewGlobal()

	var (
		showVer        = flag.Bool("version", false, "print version and exit")
		showBuildInfo  = flag.Bool("build-info", false, "print machine-readable build identity JSON and exit")
		dump           = flag.Bool("dump", false, "print current tmux snapshot as JSON and exit")
		monitor        = flag.Bool("monitor-only", true, "compatibility flag; monitor-only is always enabled unless --control is set")
		fitNative      = flag.Bool("fit-native", false, "allow fitting detached managed agent terminals to cards; does not enable key forwarding")
		control        = flag.Bool("control", false, "enable interactive control actions such as key forwarding")
		simulate       = flag.String("debug-click", "", "simulate a mouse left-click at the given coordinates (x,y)")
		traceMouse     = flag.Bool("trace-mouse", false, "log mouse hit testing details to stderr")
		configPath     = flag.String("config", "", "path to wall config JSON (default ~/.config/openclaw-cockpit/config.json)")
		validateConfig = flag.Bool("validate-config", false, "validate effective settings without starting tmux or the TUI")
		explainConfig  = flag.Bool("explain-config", false, "print explicit setting override sources to stderr")
		dumpConfig     = flag.Bool("dump-config", false, "print the effective wall config as JSON and exit")
	)
	registerWallFlags(flag.CommandLine)
	flag.Parse()

	if *showVer {
		fmt.Println(readBuildIdentity().human())
		return
	}
	if *showBuildInfo {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(readBuildIdentity()); err != nil {
			fmt.Fprintf(os.Stderr, "failed to encode build identity: %v\n", err)
			os.Exit(1)
		}
		return
	}

	overrides := map[string]string{}
	explicitConfig := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "config" {
			explicitConfig = true
		}
		if key, ok := configFlagKeys[f.Name]; ok {
			overrides[key] = f.Value.String()
		}
	})
	if !explicitConfig {
		if path, ok := os.LookupEnv("OPENCLAW_COCKPIT_CONFIG"); ok {
			*configPath = path
		}
	}
	wallConfig, sources, configErr := ui.ResolveWallConfig(*configPath, overrides, os.LookupEnv)
	if *explainConfig {
		keys := make([]string, 0, len(sources))
		for key := range sources {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		path := *configPath
		if path == "" {
			path = ui.DefaultWallConfigPath()
		}
		fmt.Fprintf(os.Stderr, "config file: %s (missing default file uses built-ins)\n", path)
		for _, key := range keys {
			fmt.Fprintf(os.Stderr, "%s: %s\n", key, sources[key])
		}
	}

	if configErr != nil {
		// Invalid config fails visibly and falls back safely to defaults; it
		// must never silently change what the wall means.
		fmt.Fprintf(os.Stderr, "wall config error: %v (using built-in defaults)\n", configErr)
		wallConfig = ui.DefaultWallConfig()
	}
	if *validateConfig && !*dumpConfig {
		if configErr != nil {
			os.Exit(1)
		}
		fmt.Println("configuration valid")
		return
	}
	if *dumpConfig {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(wallConfig); err != nil {
			fmt.Fprintf(os.Stderr, "failed to encode wall config: %v\n", err)
			os.Exit(1)
		}
		if configErr != nil {
			os.Exit(1)
		}
		return
	}

	var debugMsgs []tea.Msg
	if sim := strings.TrimSpace(*simulate); sim != "" {
		parts := strings.Split(sim, ",")
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "invalid --debug-click value %q (want x,y)\n", sim)
			os.Exit(1)
		}
		x, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid debug-click x coordinate %q: %v\n", parts[0], err)
			os.Exit(1)
		}
		y, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid debug-click y coordinate %q: %v\n", parts[1], err)
			os.Exit(1)
		}
		debugMsgs = append(debugMsgs, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		if !*traceMouse {
			*traceMouse = true
		}
	}

	client, err := tmux.NewClient(wallConfig.Tmux)
	// If tmux isn't running, inform the user early.
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to set up tmux client: %v\n", err)
		os.Exit(1)
	}
	client.SetPreserveColors(wallConfig.PreserveColors)
	client.SetExcludedSessions(wallConfig.ExcludeSessions)
	monitorOnly := effectiveMonitorOnly(*monitor, *control)
	client.SetMonitorOnly(monitorOnly)
	client.SetNativeSizeAllowed(*fitNative)

	if *dump {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		snap, err := client.Snapshot(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to fetch tmux snapshot: %v\n", err)
			os.Exit(1)
		}
		source := ui.RuntimeSource{Enabled: wallConfig.OpenClawRuntime, Script: wallConfig.RuntimeScript, Limit: wallConfig.RuntimeLimit, Timeout: time.Duration(wallConfig.DumpRuntimeTimeout)}
		snap = ui.AppendOpenClawRuntimeSessions(snap, source)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(snap); err != nil {
			fmt.Fprintf(os.Stderr, "failed to encode snapshot: %v\n", err)
			os.Exit(1)
		}
		return
	}

	model := ui.NewModel(client, time.Duration(wallConfig.Interval), wallConfig.CaptureBudget, debugMsgs, *traceMouse, monitorOnly)
	var nativeSizer *tmux.NativeSizer
	if *fitNative {
		nativeSizer = tmux.NewNativeSizer(client)
		model.SetNativeSizer(nativeSizer)
	}
	model.ApplyWallConfig(wallConfig)
	if configErr != nil {
		model.SetStartupError(fmt.Errorf("wall config error: %w (using built-in defaults)", configErr))
	}
	restoreTabs := disableHardTabOptimization()
	defer restoreTabs()
	program := tea.NewProgram(model, tea.WithFPS(wallConfig.FPS))

	_, runErr := program.Run()
	if nativeSizer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := nativeSizer.Close(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "native terminal restoration: %v\n", err)
		}
		cancel()
	}
	if err := runErr; err != nil {
		fmt.Fprintf(os.Stderr, "%s exited with error: %v\n", productName, err)
		os.Exit(1)
	}
}

func effectiveMonitorOnly(_ bool, control bool) bool {
	return !control
}

// Only explicitly visited flags enter the override layer; parser defaults do
// not mask file values (including false booleans and automatic columns).
var configFlagKeys = map[string]string{
	"interval": "interval", "fps": "fps", "cols": "cols", "capture-budget": "capture_budget", "tmux": "tmux",
	"organize": "organize", "preserve-colors": "preserve_colors", "exclude-session": "exclude_sessions",
	"openclaw-runtime": "openclaw_runtime", "openclaw-runtime-script": "runtime_script", "openclaw-runtime-limit": "runtime_limit",
	"openclaw-runtime-interval": "runtime_interval", "janitor-status": "janitor_status",
}

func registerWallFlags(flags *flag.FlagSet) {
	cfg := ui.DefaultWallConfig()
	flags.Duration("interval", time.Duration(cfg.Interval), "tmux poll interval")
	flags.Int("fps", cfg.FPS, "maximum UI render frames per second")
	flags.Int("cols", cfg.Columns, "preferred overview columns (0 = auto)")
	flags.Int("capture-budget", cfg.CaptureBudget, "maximum background pane captures per tick")
	flags.String("tmux", cfg.Tmux, "path to tmux binary (defaults to PATH)")
	flags.Bool("organize", cfg.Organize, "organize overview cards into cockpit groups")
	flags.Bool("preserve-colors", cfg.PreserveColors, "preserve ANSI colors in pane previews")
	flags.String("exclude-session", strings.Join(cfg.ExcludeSessions, ","), "comma-separated exact tmux session names to exclude")
	flags.Bool("openclaw-runtime", cfg.OpenClawRuntime, "include optional read-only OpenClaw runtime cards")
	flags.String("openclaw-runtime-script", cfg.RuntimeScript, "path to public runtime snapshot script")
	flags.Int("openclaw-runtime-limit", cfg.RuntimeLimit, "maximum OpenClaw runtime cards to deliver")
	flags.Duration("openclaw-runtime-interval", time.Duration(cfg.RuntimeInterval), "independent runtime-card refresh interval")
	flags.String("janitor-status", cfg.JanitorStatus, "optional janitor status JSON path")
}
