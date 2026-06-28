package store

import (
	"context"
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

// writeSnapshot gob-encodes data to path, mimicking what SaveToDisk produces.
// Used to set up LoadFromDisk tests without going through a running store.
func writeSnapshot(t *testing.T, path string, data map[string]snapshotEntry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create snapshot file: %v", err)
	}
	defer f.Close()
	if err := gob.NewEncoder(f).Encode(data); err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
}

// ---- snapshotEntry helpers ----

// Verifies hasExpiry reports whether an expiry timestamp is set,
// regardless of whether it has already elapsed.
func TestSnapshotEntryHasExpiry(t *testing.T) {
	cases := []struct {
		name string
		se   snapshotEntry
		want bool
	}{
		{"zero expiry", snapshotEntry{}, false},
		{"future expiry", snapshotEntry{Expiry: time.Now().Add(time.Hour)}, true},
		{"past expiry", snapshotEntry{Expiry: time.Now().Add(-time.Hour)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.se.hasExpiry(); got != c.want {
				t.Errorf("hasExpiry() = %v, want %v", got, c.want)
			}
		})
	}
}

// Verifies isExpired is true only when an expiry is set and in the past.
func TestSnapshotEntryIsExpired(t *testing.T) {
	cases := []struct {
		name string
		se   snapshotEntry
		want bool
	}{
		{"zero expiry (no TTL)", snapshotEntry{}, false},
		{"future expiry", snapshotEntry{Expiry: time.Now().Add(time.Hour)}, false},
		{"past expiry", snapshotEntry{Expiry: time.Now().Add(-time.Hour)}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.se.isExpired(); got != c.want {
				t.Errorf("isExpired() = %v, want %v", got, c.want)
			}
		})
	}
}

// ---- LoadFromDisk ----

// Verifies a missing snapshot file is treated as a clean start: no error,
// store left empty.
func TestLoadFromDiskMissingFile(t *testing.T) {
	s := newTestStore()
	path := filepath.Join(t.TempDir(), "does-not-exist.gob")

	if err := s.LoadFromDisk(path); err != nil {
		t.Errorf("expected nil error for missing file, got %v", err)
	}
	if len(s.data) != 0 {
		t.Errorf("data len = %d, want 0", len(s.data))
	}
}

// Verifies loading restores values and expiry into the live store.
func TestLoadFromDiskRestoresEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob")

	expiry := time.Now().Add(time.Hour)
	writeSnapshot(t, path, map[string]snapshotEntry{
		"plain":      {Value: []byte("v1")},
		"withexpiry": {Value: []byte("v2"), Expiry: expiry},
	})

	s := newTestStore()
	if err := s.LoadFromDisk(path); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}

	if string(s.data["plain"].value) != "v1" {
		t.Errorf("plain value = %q, want %q", s.data["plain"].value, "v1")
	}
	if !s.data["plain"].expiry.IsZero() {
		t.Errorf("plain expiry = %v, want zero", s.data["plain"].expiry)
	}
	if string(s.data["withexpiry"].value) != "v2" {
		t.Errorf("withexpiry value = %q, want %q", s.data["withexpiry"].value, "v2")
	}
	if !s.data["withexpiry"].expiry.Equal(expiry) {
		t.Errorf("withexpiry expiry = %v, want %v", s.data["withexpiry"].expiry, expiry)
	}
}

// Verifies only keys carrying an expiry are pushed onto the TTL heap.
func TestLoadFromDiskPushesTTLs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob")

	writeSnapshot(t, path, map[string]snapshotEntry{
		"a": {Value: []byte("a")},
		"b": {Value: []byte("b"), Expiry: time.Now().Add(time.Hour)},
		"c": {Value: []byte("c"), Expiry: time.Now().Add(2 * time.Hour)},
	})

	s := newTestStore()
	if err := s.LoadFromDisk(path); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}

	// Only b and c have an expiry.
	if s.ttls.Len() != 2 {
		t.Errorf("ttls len = %d, want 2", s.ttls.Len())
	}
}

// Verifies expired entries are dropped on load (not restored, no TTL pushed).
func TestLoadFromDiskSkipsExpired(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob")

	writeSnapshot(t, path, map[string]snapshotEntry{
		"alive":   {Value: []byte("a"), Expiry: time.Now().Add(time.Hour)},
		"expired": {Value: []byte("e"), Expiry: time.Now().Add(-time.Hour)},
		"noexp":   {Value: []byte("n")},
	})

	s := newTestStore()
	if err := s.LoadFromDisk(path); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}

	if _, ok := s.data["expired"]; ok {
		t.Errorf("expired key should not be loaded")
	}
	if _, ok := s.data["alive"]; !ok {
		t.Errorf("alive key should be loaded")
	}
	if _, ok := s.data["noexp"]; !ok {
		t.Errorf("no-expiry key should be loaded")
	}
	// Only "alive" survives with an expiry → one TTL entry.
	if s.ttls.Len() != 1 {
		t.Errorf("ttls len = %d, want 1", s.ttls.Len())
	}
}

// Verifies a corrupt / non-gob file returns a decode error.
func TestLoadFromDiskCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.gob")
	if err := os.WriteFile(path, []byte("this is not a gob stream"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	s := newTestStore()
	if err := s.LoadFromDisk(path); err == nil {
		t.Errorf("expected decode error for corrupt file, got nil")
	}
}

// ---- SaveToDisk ----

// Verifies SaveToDisk drives the running event loop, writes a snapshot, and
// that LoadFromDisk on a fresh store reproduces the data (full round trip).
func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob")

	s := New(0)
	send(t, s, constants.Set, "k1", "v1")
	send(t, s, constants.Set, "k2", "v2", constants.EX, "100")

	if err := s.SaveToDisk(path, &SnapshotStats{}); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}

	// Load into a fresh, event-loop-free store and inspect directly.
	s2 := newTestStore()
	if err := s2.LoadFromDisk(path); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}

	if string(s2.data["k1"].value) != "v1" {
		t.Errorf("k1 = %q, want %q", s2.data["k1"].value, "v1")
	}
	if string(s2.data["k2"].value) != "v2" {
		t.Errorf("k2 = %q, want %q", s2.data["k2"].value, "v2")
	}
	if !s2.data["k2"].hasExpiry() {
		t.Errorf("k2 should have an expiry after round trip")
	}
	if s2.ttls.Len() != 1 {
		t.Errorf("ttls len = %d, want 1 (k2)", s2.ttls.Len())
	}
}

// Verifies SaveToDisk writes atomically: the final file exists and no
// ".tmp" scratch file is left behind.
func TestSaveToDiskAtomicNoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob")

	s := New(0)
	send(t, s, constants.Set, "k", "v")

	if err := s.SaveToDisk(path, &SnapshotStats{}); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("snapshot file missing: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf(".tmp file should not remain after successful save")
	}
}

// Verifies SaveToDisk on an empty store produces a loadable, empty snapshot.
func TestSaveToDiskEmptyStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob")

	s := New(0)
	if err := s.SaveToDisk(path, &SnapshotStats{}); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}

	s2 := newTestStore()
	if err := s2.LoadFromDisk(path); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}
	if len(s2.data) != 0 {
		t.Errorf("data len = %d, want 0", len(s2.data))
	}
}

// ---- LoadFromDisk memory profile ----

// Verifies that loading a snapshot charges keyBytes, valueBytes, lruBytes,
// and ttlBytes on the memory profile.
func TestLoadFromDiskChargesMemoryProfile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob")

	writeSnapshot(t, path, map[string]snapshotEntry{
		"foo": {Value: []byte("bar")},
		"baz": {Value: []byte("qux"), Expiry: time.Now().Add(time.Hour)},
	})

	s := newTestStore()
	if err := s.LoadFromDisk(path); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}

	mp := s.memoryProfile
	if mp.keyCount != 2 {
		t.Errorf("keyCount = %d, want 2", mp.keyCount)
	}
	if mp.keyBytes <= 0 {
		t.Errorf("keyBytes = %d, want > 0", mp.keyBytes)
	}
	if mp.valueBytes <= 0 {
		t.Errorf("valueBytes = %d, want > 0", mp.valueBytes)
	}
	if mp.lruBytes <= 0 {
		t.Errorf("lruBytes = %d, want > 0", mp.lruBytes)
	}
	// "baz" carries a TTL so ttlBytes must be non-zero.
	if mp.ttlBytes <= 0 {
		t.Errorf("ttlBytes = %d, want > 0 (baz has TTL)", mp.ttlBytes)
	}
}

// Verifies that a key with no expiry does not charge ttlBytes.
func TestLoadFromDiskNoTTLChargeForPlainKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob")

	writeSnapshot(t, path, map[string]snapshotEntry{
		"plain": {Value: []byte("v")},
	})

	s := newTestStore()
	if err := s.LoadFromDisk(path); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}

	if s.memoryProfile.ttlBytes != 0 {
		t.Errorf("ttlBytes = %d, want 0 for key with no TTL", s.memoryProfile.ttlBytes)
	}
}

// Verifies makeRoom is called after loading: when the snapshot exceeds maxBytes,
// the least-recently-loaded keys are evicted until the store is under the limit.
//
// currentMemorySize includes FIXED_OVERHEAD (136 B). Each single-char key + value adds:
//
//	STRING_OVERHEAD(16)+1 + ENTRY_OVERHEAD(48)+1 + LRU_NODE_OVERHEAD(32)+1 = 99 bytes
//
// One key  → 136+99 = 235 B
// Two keys → 136+198 = 334 B
// maxBytes=250 fits one key but not two, so one key must be evicted.
func TestLoadFromDiskEvictsWhenOverLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob")

	writeSnapshot(t, path, map[string]snapshotEntry{
		"a": {Value: []byte("1")},
		"b": {Value: []byte("2")},
	})

	s := newTestStore()
	s.memoryProfile = NewMemProfile(250)

	if err := s.LoadFromDisk(path); err != nil {
		t.Fatalf("LoadFromDisk: %v", err)
	}

	if len(s.data) != 1 {
		t.Errorf("data len = %d, want 1 (one key evicted to stay under limit)", len(s.data))
	}
	if s.memoryProfile.isOverLimit() {
		t.Errorf("store should be under memory limit after makeRoom; currentSize = %d, max = %d",
			s.memoryProfile.currentMemorySize(), s.memoryProfile.maxBytes)
	}
}

// ---- StartSnapshotting ----

// Verifies the background snapshotter writes a file on its tick interval.
func TestStartSnapshotting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timed snapshotting test in -short mode")
	}

	// Cancel before TempDir cleanup so the goroutine stops before the directory is deleted.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob")

	s := New(0)
	send(t, s, constants.Set, "k", "v")

	s.StartSnapshotting(ctx, path, 50*time.Millisecond, &SnapshotStats{})

	deadline := time.After(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return // file appeared — success
		}
		select {
		case <-deadline:
			t.Fatalf("StartSnapshotting did not create a file within 2s")
		case <-time.After(20 * time.Millisecond):
		}
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
