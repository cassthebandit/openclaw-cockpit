package zone

import (
	"strings"
	"testing"
)

func TestScanStripsMarkersAndTracksBounds(t *testing.T) {
	m := New()
	frame := "before " + m.Mark("card:1", "inside") + " after"

	got := m.Scan(frame)
	if got != "before inside after" {
		t.Fatalf("Scan() output = %q", got)
	}

	zone := m.Get("card:1")
	if zone == nil {
		t.Fatal("expected zone")
	}
	if zone.StartX != 7 || zone.StartY != 0 || zone.EndX != 12 || zone.EndY != 0 {
		t.Fatalf("zone bounds = %+v, want start=(7,0) end=(12,0)", zone)
	}
}

func TestScanTracksBoundsAcrossANSIAndNewlines(t *testing.T) {
	m := New()
	frame := "a\n\x1b[38;5;250m" + m.Mark("card:1", "wide text") + "\x1b[0m"

	got := m.Scan(frame)
	if got != "a\n\x1b[38;5;250mwide text\x1b[0m" {
		t.Fatalf("Scan() output = %q", got)
	}

	zone := m.Get("card:1")
	if zone == nil {
		t.Fatal("expected zone")
	}
	if zone.StartX != 0 || zone.StartY != 1 || zone.EndX != 8 || zone.EndY != 1 {
		t.Fatalf("zone bounds = %+v, want start=(0,1) end=(8,1)", zone)
	}
}

func TestScanIgnoresUnpairedMarker(t *testing.T) {
	m := New()
	marker := m.Mark("card:1", "inside")
	idx := strings.Index(marker, "inside")
	if idx < 0 {
		t.Fatalf("test marker missing content: %q", marker)
	}
	unpaired := marker[:idx]
	// A clipped frame can contain only one marker. It should be stripped from
	// output but should not publish a stale or partial zone.
	got := m.Scan(unpaired + "inside")
	if got != "inside" {
		t.Fatalf("Scan() output = %q", got)
	}
	if zone := m.Get("card:1"); zone != nil {
		t.Fatalf("expected no zone for unpaired marker, got %+v", zone)
	}
}

func TestScanDisabledStripsMarkersWithoutTracking(t *testing.T) {
	m := New()
	frame := m.Mark("card:1", "inside")
	m.SetEnabled(false)

	got := m.Scan(frame)
	if got != "inside" {
		t.Fatalf("Scan() output = %q", got)
	}
	if zone := m.Get("card:1"); zone != nil {
		t.Fatalf("expected no zone while disabled, got %+v", zone)
	}
}
