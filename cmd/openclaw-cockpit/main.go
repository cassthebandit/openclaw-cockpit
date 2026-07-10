// File main.go wires command-line flags and the Bubble Tea program together.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/cassthebandit/openclaw-cockpit/internal/zone"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
	"github.com/cassthebandit/openclaw-cockpit/internal/ui"
)

const productName = "OpenClaw Cockpit"
const defaultOpenClawRuntimeLimit = 80

var version = "0.9.5"

// main configures the tmux client, handles flag modes, and launches Bubble Tea.
func main() {
	zone.NewGlobal()

	var (
		interval         = flag.Duration("interval", time.Second, "tmux poll interval")
		fps              = flag.Int("fps", 60, "maximum UI render frames per second")
		cols             = flag.Int("cols", 0, "preferred number of preview columns in overview mode (0 = auto)")
		captureBudget    = flag.Int("capture-budget", 0, "maximum unfocused pane captures per tick (default 6)")
		tmuxBin          = flag.String("tmux", "", "path to tmux binary (defaults to PATH lookup)")
		showVer          = flag.Bool("version", false, "print version and exit")
		dump             = flag.Bool("dump", false, "print current tmux snapshot as JSON and exit")
		monitor          = flag.Bool("monitor-only", true, "compatibility flag; monitor-only is always enabled unless --control is set")
		control          = flag.Bool("control", false, "enable interactive control actions such as key forwarding")
		organize         = flag.Bool("organize", false, "organize overview cards into cockpit groups")
		openclaw         = flag.Bool("openclaw-runtime", false, "include read-only OpenClaw runtime cards")
		openclawScript   = flag.String("openclaw-runtime-script", "", "path to OpenClaw runtime snapshot script")
		openclawLimit    = flag.Int("openclaw-runtime-limit", defaultOpenClawRuntimeLimit, "maximum OpenClaw runtime cards to show")
		openclawInterval = flag.Duration("openclaw-runtime-interval", 15*time.Second, "OpenClaw runtime card refresh interval (cards load off the tmux snapshot path and merge from cache)")
		janitorStatus    = flag.String("janitor-status", "", "path to tmux janitor status JSON")
		colors           = flag.Bool("preserve-colors", false, "preserve ANSI colours in captured pane previews")
		exclude          = flag.String("exclude-session", "", "comma-separated tmux session names to hide from snapshots")
		simulate         = flag.String("debug-click", "", "simulate a mouse left-click at the given coordinates (x,y)")
		traceMouse       = flag.Bool("trace-mouse", false, "log mouse hit testing details to stderr")
		configPath       = flag.String("config", "", "path to wall config JSON (default ~/.config/openclaw-cockpit/config.json)")
		dumpConfig       = flag.Bool("dump-config", false, "print the effective wall config as JSON and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println(productName, version)
		return
	}

	wallConfig, configErr := ui.LoadWallConfig(*configPath)
	if configErr != nil {
		// Invalid config fails visibly and falls back safely to defaults; it
		// must never silently change what the wall means.
		fmt.Fprintf(os.Stderr, "wall config error: %v (using built-in defaults)\n", configErr)
		wallConfig = ui.DefaultWallConfig()
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

	client, err := tmux.NewClient(*tmuxBin)
	// If tmux isn't running, inform the user early.
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to set up tmux client: %v\n", err)
		os.Exit(1)
	}
	client.SetPreserveColors(*colors)
	client.SetExcludedSessions(parseSessionList(*exclude))
	monitorOnly := effectiveMonitorOnly(*monitor, *control)
	client.SetMonitorOnly(monitorOnly)

	if *dump {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		snap, err := client.Snapshot(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to fetch tmux snapshot: %v\n", err)
			os.Exit(1)
		}
		snap = ui.AppendOpenClawRuntimeSessions(snap, runtimeSource(*openclaw, *openclawScript, *openclawLimit))
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(snap); err != nil {
			fmt.Fprintf(os.Stderr, "failed to encode snapshot: %v\n", err)
			os.Exit(1)
		}
		return
	}

	model := ui.NewModel(client, *interval, *captureBudget, debugMsgs, *traceMouse, monitorOnly)
	model.ApplyWallConfig(wallConfig)
	if configErr != nil {
		model.SetStartupError(fmt.Errorf("wall config error: %w (using built-in defaults)", configErr))
	}
	model.SetPreferredColumns(*cols)
	model.SetOrganized(*organize)
	model.SetJanitorStatusFile(*janitorStatus)
	if *openclaw {
		model.SetOpenClawRuntimeSource(*openclawScript, *openclawLimit, 20*time.Second)
		model.SetOpenClawRuntimeInterval(*openclawInterval)
	}
	restoreTabs := disableHardTabOptimization()
	defer restoreTabs()
	program := tea.NewProgram(model, tea.WithFPS(*fps))

	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s exited with error: %v\n", productName, err)
		os.Exit(1)
	}
}

func effectiveMonitorOnly(_ bool, control bool) bool {
	return !control
}

func runtimeSource(enabled bool, script string, limit int) ui.RuntimeSource {
	if !enabled {
		return ui.RuntimeSource{}
	}
	if limit <= 0 {
		limit = defaultOpenClawRuntimeLimit
	}
	return ui.RuntimeSource{
		Enabled: true,
		Script:  script,
		Limit:   limit,
		Timeout: 45 * time.Second,
	}
}

func parseSessionList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		name := strings.TrimSpace(part)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}
