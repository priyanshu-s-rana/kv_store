package store

import (
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

	s := New()
	send(t, s, constants.Set, "k1", "v1")
	send(t, s, constants.Set, "k2", "v2", constants.EX, "100")

	if err := s.SaveToDisk(path); err != nil {
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

	s := New()
	send(t, s, constants.Set, "k", "v")

	if err := s.SaveToDisk(path); err != nil {
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

	s := New()
	if err := s.SaveToDisk(path); err != nil {
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

// ---- StartSnapshotting ----

// Verifies the background snapshotter writes a file on its tick interval.
func TestStartSnapshotting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timed snapshotting test in -short mode")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "snap.gob")

	s := New()
	send(t, s, constants.Set, "k", "v")

	s.StartSnapshotting(path, 50*time.Millisecond)

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
