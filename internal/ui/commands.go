// File commands.go defines Bubble Tea command constructors for tmuxwatch.
package ui

import (
	"context"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// fetchSnapshotCmd captures the current tmux snapshot or returns an error
// message when it fails. Runtime cards deliberately stay off this path: they
// load on their own cadence (fetchRuntimeCardsCmd) and merge from cache, so a
// slow runtime script can never stretch the structural snapshot cadence.
func fetchSnapshotCmd(client *tmux.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		snap, err := client.Snapshot(ctx)
		if err != nil {
			return errMsg{err: err}
		}
		return snapshotMsg{snapshot: snap}
	}
}

// fetchRuntimeCardsCmd loads OpenClaw runtime cards asynchronously. Load
// failures already materialize as a source-error card inside
// openClawRuntimeSessions, so this command never returns errMsg.
func fetchRuntimeCardsCmd(source RuntimeSource) tea.Cmd {
	return func() tea.Msg {
		now := time.Now()
		return runtimeCardsMsg{sessions: openClawRuntimeSessions(source, now), loadedAt: now}
	}
}

// scheduleTick creates a periodic timer used to refresh tmux snapshots.
func scheduleTick(interval time.Duration) tea.Cmd {
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

// scheduleRuntimeTick re-arms the runtime-card refresh loop.
func scheduleRuntimeTick(interval time.Duration) tea.Cmd {
	if interval <= 0 {
		interval = runtimeCardInterval
	}
	return tea.Tick(interval, func(time.Time) tea.Msg {
		return runtimeTickMsg{}
	})
}

// fastCaptureSignalsDirty reports whether any watched pane-log signal differs
// from its recorded state. A path whose stat fails while its recorded state is
// already statErr is known-bad, not a change; a successful stat over a statErr
// record is a change (the log came back).
func fastCaptureSignalsDirty(signals []fastCaptureSignal) bool {
	for _, signal := range signals {
		info, err := os.Stat(signal.path)
		if err != nil {
			if signal.statErr {
				continue
			}
			return true
		}
		if signal.statErr || !signal.seen || info.Size() != signal.size || !info.ModTime().Equal(signal.modTime) {
			return true
		}
	}
	return false
}

// scheduleFastCaptureWatch watches the given pane-log signals off the update
// loop. It always sleeps one sweep interval before checking, so it can never
// deliver messages faster than the sweep rate: a stale or non-dispatchable
// signal throttles the loop instead of spinning it.
func scheduleFastCaptureWatch(signals []fastCaptureSignal, fallback, deadline time.Duration) tea.Cmd {
	if fallback <= 0 {
		fallback = fastCaptureIdleTick
	}
	if deadline <= 0 {
		deadline = fastCaptureIdleTick
	}
	return func() tea.Msg {
		if len(signals) == 0 {
			time.Sleep(fallback)
			return fastTickMsg{}
		}
		end := time.Now().Add(deadline)
		for {
			time.Sleep(fastCaptureInterval)
			if fastCaptureSignalsDirty(signals) {
				return fastTickMsg{}
			}
			if time.Now().After(end) {
				return fastTickMsg{}
			}
		}
	}
}

func scheduleFastCaptureTick(interval time.Duration) tea.Cmd {
	if interval <= 0 {
		interval = fastCaptureInterval
	}
	return func() tea.Msg {
		time.Sleep(interval)
		return fastTickMsg{}
	}
}

// emitMsg replays the provided message during the next update cycle.
func emitMsg(msg tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return msg
	}
}

// fetchPaneContentCmd grabs the latest pane output for preview rendering.
func fetchPaneContentCmd(client *tmux.Client, sessionID, paneID string, lines int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		text, err := client.CapturePane(ctx, paneID, lines)
		return paneContentMsg{sessionID: sessionID, paneID: paneID, text: text, err: err}
	}
}

// fetchPaneVarsCmd loads user-defined tmux variables for the provided pane.
func fetchPaneVarsCmd(client *tmux.Client, sessionID, paneID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		vars, err := client.PaneVariables(ctx, paneID)
		return paneVarsMsg{sessionID: sessionID, paneID: paneID, vars: vars, err: err}
	}
}

// showStatusMessage emits a statusMsg for later handling in the update loop.
func showStatusMessage(msg string) tea.Cmd {
	return func() tea.Msg {
		return statusMsg(msg)
	}
}

// sendKeysCmd forwards named key tokens to a tmux pane within a context
// deadline.
func sendKeysCmd(client *tmux.Client, paneID string, keys ...string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.SendKeys(ctx, paneID, keys...); err != nil {
			return errMsg{err: err}
		}
		return nil
	}
}

// sendLiteralKeysCmd forwards printable text to a tmux pane with literal-key
// semantics within a context deadline.
func sendLiteralKeysCmd(client *tmux.Client, paneID string, text string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.SendLiteralKeys(ctx, paneID, text); err != nil {
			return errMsg{err: err}
		}
		return nil
	}
}
