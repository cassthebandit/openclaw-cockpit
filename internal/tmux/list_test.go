// File list_test.go validates helper parsing in the tmux list routines.
package tmux

import (
	"context"
	"testing"
	"time"
)

// TestParseUnix ensures numeric strings convert to time correctly.
func TestParseUnix(t *testing.T) {
	t.Parallel()

	ts := time.Unix(100, 0)
	got, err := parseUnix("100")
	if err != nil {
		t.Fatalf("parseUnix returned unexpected error: %v", err)
	}
	if !got.Equal(ts) {
		t.Fatalf("parseUnix() = %v, want %v", got, ts)
	}
}

// TestParseUnixEmpty confirms blank values return zero time.
func TestParseUnixEmpty(t *testing.T) {
	t.Parallel()

	got, err := parseUnix(" ")
	if err != nil {
		t.Fatalf("parseUnix returned unexpected error: %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("parseUnix() = %v, want zero time", got)
	}
}

// TestParseUnixInvalid verifies invalid input errors out.
func TestParseUnixInvalid(t *testing.T) {
	t.Parallel()

	if _, err := parseUnix("abc"); err == nil {
		t.Fatal("parseUnix expected error for invalid input")
	}
}

func TestAcceptedPaneFieldCountIncludesJanitorMetadata(t *testing.T) {
	t.Parallel()

	for _, count := range []int{14, 33, 37, 41, 42} {
		if !acceptedPaneFieldCount(count) {
			t.Fatalf("field count %d should be accepted", count)
		}
	}
	for _, count := range []int{13, 36, 40, 43} {
		if acceptedPaneFieldCount(count) {
			t.Fatalf("field count %d should be rejected", count)
		}
	}
}

func TestListSessionsAttachedClientCounts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		count    string
		attached bool
		bad      bool
	}{
		{"0", false, false},
		{"1", true, false},
		{"2", true, false},
		{"25", true, false},
		{"-1", false, true},
		{"", false, true},
		{"yes", false, true},
		{"1.5", false, true},
		{"18446744073709551616", false, true},
	} {
		t.Run(test.count, func(t *testing.T) {
			c := &Client{run: func(context.Context, string, ...string) ([]byte, error) {
				return []byte("$1~~example~~" + test.count + "~~1750000000~~1750000000\n"), nil
			}}
			sessions, err := c.listSessions(context.Background())
			if test.bad {
				if err == nil {
					t.Fatalf("malformed client count %q accepted", test.count)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(sessions) != 1 || sessions[0].Attached != test.attached {
				t.Fatalf("count %s: sessions=%+v", test.count, sessions)
			}
		})
	}
}
