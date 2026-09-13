package tmux

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// NativeSize identifies a single-pane worker and its allocated terminal area.
// IDs and PID are revalidated in tmux, not trusted from the UI snapshot.
type NativeSize struct {
	Session, Window, Pane, PID string
	Width, Height              int
}

type nativeLease struct {
	target        NativeSize
	width, height int
	mode          string
}

// NativeSizer owns only detached, unlinked, single-pane windows. The tmux
// option arbitrates multiple Cockpits; a single server-side conditional guards
// each resize. No keys, hooks, or global tmux settings are changed.
type NativeSizer struct {
	mu     sync.Mutex
	client *Client
	token  string
	leases map[string]nativeLease
	closed bool
}

func NewNativeSizer(client *Client) *NativeSizer {
	return &NativeSizer{client: client, token: strconv.FormatInt(time.Now().UnixNano(), 10), leases: make(map[string]nativeLease)}
}

func numericID(s string, prefix byte) bool {
	if len(s) < 2 || s[0] != prefix {
		return false
	}
	_, err := strconv.ParseUint(s[1:], 10, 64)
	return err == nil
}

func (s NativeSize) valid() bool {
	_, err := strconv.ParseUint(s.PID, 10, 64)
	return err == nil && numericID(s.Session, '$') && numericID(s.Window, '@') && numericID(s.Pane, '%') && s.Width >= 20 && s.Height >= 3
}

func allFormats(parts ...string) string {
	out := parts[0]
	for _, p := range parts[1:] {
		out = "#{&&:" + out + "," + p + "}"
	}
	return out
}
func equalFormat(key, value string) string { return "#{==:#{" + key + "}," + value + "}" }
func identity(s NativeSize) string {
	return allFormats(equalFormat("session_id", s.Session), equalFormat("window_id", s.Window), equalFormat("pane_id", s.Pane), equalFormat("pane_pid", s.PID))
}

func detached(s NativeSize) string {
	return allFormats(identity(s), equalFormat("session_attached", "0"), equalFormat("window_linked", "0"), equalFormat("window_panes", "1"), equalFormat("pane_dead", "0"), equalFormat("pane_in_mode", "0"))
}

const nativeOwner = "@cockpit_native_size_owner"

func (n *NativeSizer) conditional(ctx context.Context, s NativeSize, condition, commands string) error {
	_, err := n.client.runTmux(ctx, "if-shell", "-F", "-t", s.Pane, condition, commands)
	if err != nil {
		// A worker can disappear between a snapshot and release. Absence is a
		// completed release, but server/transport failures must remain visible.
		out, listErr := n.client.runTmux(ctx, "list-panes", "-a", "-F", "#{pane_id}")
		if listErr == nil && !slices.Contains(strings.Fields(string(out)), s.Pane) {
			return nil
		}
	}
	return err
}

// Sync reconciles a frame's targets. Empty targets restore all owned windows.
func (n *NativeSizer) Sync(ctx context.Context, targets []NativeSize) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closed || (n.client.monitorOnly && !n.client.nativeSizeAllowed) {
		return nil
	}
	wanted := make(map[string]NativeSize)
	for _, s := range targets {
		if s.valid() {
			wanted[s.Pane] = s
		}
	}
	var errs []error
	for pane, l := range n.leases {
		s, ok := wanted[pane]
		if !ok || s.Session != l.target.Session || s.Window != l.target.Window || s.PID != l.target.PID {
			if err := n.release(ctx, l); err != nil {
				errs = append(errs, err)
				continue
			}
			delete(n.leases, pane)
		}
	}
	for pane, s := range wanted {
		if err := n.fit(ctx, s); err != nil {
			errs = append(errs, fmt.Errorf("native fit %s: %w", pane, err))
		}
	}
	return errors.Join(errs...)
}

func (n *NativeSizer) fit(ctx context.Context, s NativeSize) error {
	l, owned := n.leases[s.Pane]
	if !owned {
		// Inspect local (not inherited) sizing policy so release preserves inheritance.
		out, err := n.client.runTmux(ctx, "show-options", "-wqv", "-t", s.Window, "window-size")
		if err != nil {
			return err
		}
		mode := strings.TrimSpace(string(out))
		switch mode {
		case "", "latest", "largest", "smallest", "manual":
		default:
			return fmt.Errorf("unknown sizing policy %q", mode)
		}
		out, err = n.client.runTmux(ctx, "display-message", "-p", "-t", s.Pane, "#{pane_width} #{pane_height}")
		if err != nil {
			return err
		}
		l = nativeLease{target: s, mode: mode}
		if _, err = fmt.Sscan(string(out), &l.width, &l.height); err != nil {
			return err
		}
		guard := allFormats(detached(s), equalFormat(nativeOwner, ""), equalFormat("pane_width", strconv.Itoa(l.width)), equalFormat("pane_height", strconv.Itoa(l.height)))
		// Record before dispatch so a cancelled readback still permits cleanup.
		n.leases[s.Pane] = l
		if err = n.conditional(ctx, s, guard, "set-option -w -t "+s.Window+" "+nativeOwner+" "+n.token); err != nil {
			return err
		}
		out, err = n.client.runTmux(ctx, "show-options", "-wqv", "-t", s.Window, nativeOwner)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(out)) != n.token {
			delete(n.leases, s.Pane)
			return nil
		}
		n.leases[s.Pane] = l
	}
	guard := allFormats(detached(s), equalFormat(nativeOwner, n.token))
	different := "#{||:#{!=:#{pane_width}," + strconv.Itoa(s.Width) + "},#{!=:#{pane_height}," + strconv.Itoa(s.Height) + "}}"
	commands := fmt.Sprintf("resize-window -t %s -x %d -y %d", s.Window, s.Width, s.Height)
	if err := n.conditional(ctx, s, allFormats(guard, different), commands); err != nil {
		return err
	}
	// Attachment, splitting, linking, or copy mode hands ownership back. Never
	// continue forcing manual size while a direct terminal owns the window.
	out, err := n.client.runTmux(ctx, "if-shell", "-F", "-t", s.Pane, guard, "display-message -p 1", "display-message -p 0")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(out)) != "1" {
		if err = n.release(ctx, l); err != nil {
			return err
		}
		delete(n.leases, s.Pane)
	}
	return nil
}

func (n *NativeSizer) release(ctx context.Context, l nativeLease) error {
	s := l.target
	restore := "set-option -wu -t " + s.Window + " window-size"
	if l.mode != "" {
		restore = "set-option -w -t " + s.Window + " window-size " + l.mode
	}
	// Restore launch dimensions only while still detached and unsplit. An
	// attached client gets its original sizing policy, not the old launch size.
	resize := fmt.Sprintf("resize-window -t %s -x %d -y %d", s.Window, l.width, l.height)
	commands := "if-shell -F -t " + s.Pane + " '" + detached(s) + "' '" + resize + "' ; " + restore + " ; set-option -wu -t " + s.Window + " " + nativeOwner
	return n.conditional(ctx, s, allFormats(identity(s), equalFormat(nativeOwner, n.token)), commands)
}

// Close prevents in-flight UI commands reacquiring ownership after shutdown.
func (n *NativeSizer) Close(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.closed = true
	var errs []error
	for pane, l := range n.leases {
		if err := n.release(ctx, l); err != nil {
			errs = append(errs, err)
		} else {
			delete(n.leases, pane)
		}
	}
	return errors.Join(errs...)
}
