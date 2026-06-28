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

// ---- SnapshotStats ----

// Verifies SaveToDisk populates all SnapshotStats fields on success.
func TestSaveToDiskUpdatesStats(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob")

	s := New(0)
	send(t, s, constants.Set, "k", "v")

	snpStats := &SnapshotStats{}
	before := time.Now()
	if err := s.SaveToDisk(path, snpStats); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}

	if snpStats.SnapshotCount != 1 {
		t.Errorf("SnapshotCount = %d, want 1", snpStats.SnapshotCount)
	}
	if snpStats.LastSnapshotTime.Before(before) {
		t.Errorf("LastSnapshotTime = %v, want >= %v", snpStats.LastSnapshotTime, before)
	}
	if snpStats.SnapshotSizeBytes <= 0 {
		t.Errorf("SnapshotSizeBytes = %d, want > 0", snpStats.SnapshotSizeBytes)
	}
	if snpStats.LastSnapshotDuration <= 0 {
		t.Errorf("LastSnapshotDuration = %v, want > 0", snpStats.LastSnapshotDuration)
	}
	if snpStats.SnapshotInProgress {
		t.Error("SnapshotInProgress should be false after successful save")
	}
}

// Verifies SnapshotCount accumulates across multiple SaveToDisk calls.
func TestSaveToDiskMultipleSavesAccumulateCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob")

	s := New(0)
	snpStats := &SnapshotStats{}

	for i := range 3 {
		if err := s.SaveToDisk(path, snpStats); err != nil {
			t.Fatalf("SaveToDisk call %d: %v", i, err)
		}
	}

	if snpStats.SnapshotCount != 3 {
		t.Errorf("SnapshotCount = %d, want 3", snpStats.SnapshotCount)
	}
}

// Verifies GetStats returns a plain copy of the current snapshot stats.
func TestSnapshotStatsGetStats(t *testing.T) {
	snpStats := &SnapshotStats{
		SnapshotCount:        5,
		SnapshotFailures:     2,
		SnapshotSizeBytes:    4096,
		LastSnapshotDuration: 3 * time.Millisecond,
	}

	got := snpStats.GetStats()

	if got.SnapshotCount != 5 {
		t.Errorf("SnapshotCount = %d, want 5", got.SnapshotCount)
	}
	if got.SnapshotFailures != 2 {
		t.Errorf("SnapshotFailures = %d, want 2", got.SnapshotFailures)
	}
	if got.SnapshotSizeBytes != 4096 {
		t.Errorf("SnapshotSizeBytes = %d, want 4096", got.SnapshotSizeBytes)
	}
}

// Verifies StartSnapshotting increments SnapshotCount after at least one tick.
func TestStartSnapshottingIncrementsCount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timed snapshotting test in -short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob")

	s := New(0)
	send(t, s, constants.Set, "k", "v")

	snpStats := &SnapshotStats{}
	s.StartSnapshotting(ctx, path, 50*time.Millisecond, snpStats)

	time.Sleep(300 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)

	if snpStats.SnapshotCount < 1 {
		t.Errorf("SnapshotCount = %d, want >= 1 after snapshotting", snpStats.SnapshotCount)
	}
}
