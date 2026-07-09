package store

import (
	"testing"
	"time"
)

// ---- SnapshotEntry helpers ----

// Verifies HasExpiry reports whether an expiry timestamp is set,
// regardless of whether it has already elapsed.
func TestSnapshotEntryHasExpiry(t *testing.T) {
	cases := []struct {
		name string
		se   SnapshotEntry
		want bool
	}{
		{"zero expiry", SnapshotEntry{}, false},
		{"future expiry", SnapshotEntry{Expiry: time.Now().Add(time.Hour)}, true},
		{"past expiry", SnapshotEntry{Expiry: time.Now().Add(-time.Hour)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.se.HasExpiry(); got != c.want {
				t.Errorf("HasExpiry() = %v, want %v", got, c.want)
			}
		})
	}
}

// Verifies IsExpired is true only when an expiry is set and in the past.
func TestSnapshotEntryIsExpired(t *testing.T) {
	cases := []struct {
		name string
		se   SnapshotEntry
		want bool
	}{
		{"zero expiry (no TTL)", SnapshotEntry{}, false},
		{"future expiry", SnapshotEntry{Expiry: time.Now().Add(time.Hour)}, false},
		{"past expiry", SnapshotEntry{Expiry: time.Now().Add(-time.Hour)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.se.IsExpired(); got != c.want {
				t.Errorf("IsExpired() = %v, want %v", got, c.want)
			}
		})
	}
}

// ---- capture ----
// capture() drives what the persistence package checkpoints/rebaselines to
// disk; TestSnapshotCopiesData / TestSnapshotEmptyStore / TestSnapshotIsDecoupledFromStore
// in commands_test.go cover its round-trip behaviour directly. Disk load/save
// coverage now lives with the persistence package (persistence.Snapshot,
// persistence.Journal), which owns that responsibility.

// Verifies capture skips expired entries.
func TestCaptureSkipsExpired(t *testing.T) {
	s := newTestStore()
	s.data["alive"] = &entry{value: []byte("a"), expiry: time.Now().Add(time.Hour)}
	s.data["expired"] = &entry{value: []byte("e"), expiry: time.Now().Add(-time.Hour)}
	s.data["noexp"] = &entry{value: []byte("n")}

	data, err := s.capture()
	if err != nil {
		t.Fatalf("capture err = %v, want nil", err)
	}

	if _, ok := data["expired"]; ok {
		t.Errorf("expired key should not be captured")
	}
	if _, ok := data["alive"]; !ok {
		t.Errorf("alive key should be captured")
	}
	if _, ok := data["noexp"]; !ok {
		t.Errorf("no-expiry key should be captured")
	}
}
