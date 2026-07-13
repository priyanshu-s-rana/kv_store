package persistence

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestSnapshot(t *testing.T, dir string) *Snapshot {
	t.Helper()
	return NewSnapshot(&SnapshotConfig{FilePath: filepath.Join(dir, "snap.gob")}, noopPersistenceMetrics{})
}

// ============================================================
// Snapshot
// ============================================================

func TestSnapshotSaveEmpty(t *testing.T) {
	dir := t.TempDir()
	snap := newTestSnapshot(t, dir)

	if err := snap.SaveToDisk(map[string]SnapshotEntry{}, 0, 0); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}

	loaded, err := snap.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Data) != 0 {
		t.Errorf("Data len = %d, want 0", len(loaded.Data))
	}
}

func TestSnapshotSavePopulated(t *testing.T) {
	dir := t.TempDir()
	snap := newTestSnapshot(t, dir)

	data := map[string]SnapshotEntry{
		"a": {Value: []byte("1")},
		"b": {Value: []byte("2")},
	}
	if err := snap.SaveToDisk(data, 0, 0); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}

	loaded, err := snap.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Data) != 2 {
		t.Fatalf("Data len = %d, want 2", len(loaded.Data))
	}
	if string(loaded.Data["a"].Value) != "1" || string(loaded.Data["b"].Value) != "2" {
		t.Errorf("Data = %+v, values corrupted on round trip", loaded.Data)
	}
}

func TestSnapshotSavePreservesEmptyValue(t *testing.T) {
	dir := t.TempDir()
	snap := newTestSnapshot(t, dir)

	data := map[string]SnapshotEntry{"empty": {Value: []byte{}}}
	if err := snap.SaveToDisk(data, 0, 0); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}

	loaded, err := snap.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	entry, ok := loaded.Data["empty"]
	if !ok {
		t.Fatalf("empty-valued key did not survive the round trip")
	}
	if len(entry.Value) != 0 {
		t.Errorf("Value len = %d, want 0", len(entry.Value))
	}
}

func TestSnapshotSavePreservesArbitraryBinaryData(t *testing.T) {
	dir := t.TempDir()
	snap := newTestSnapshot(t, dir)

	want := []byte{0, 1, 2, 3, 255, 128, 42}
	data := map[string]SnapshotEntry{"bin": {Value: want}}
	if err := snap.SaveToDisk(data, 0, 0); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}

	loaded, err := snap.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !bytes.Equal(loaded.Data["bin"].Value, want) {
		t.Errorf("Value = %v, want %v", loaded.Data["bin"].Value, want)
	}
}

func TestSnapshotSaveTTLEntries(t *testing.T) {
	dir := t.TempDir()
	snap := newTestSnapshot(t, dir)

	expiry := time.Now().Add(time.Hour).Truncate(0) // strip monotonic reading for exact gob round trip
	data := map[string]SnapshotEntry{
		"withttl": {Value: []byte("v"), Expiry: expiry},
		"nottl":   {Value: []byte("v")},
	}
	if err := snap.SaveToDisk(data, 0, 0); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}

	loaded, err := snap.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.Data["withttl"].Expiry.Equal(expiry) {
		t.Errorf("withttl expiry = %v, want %v", loaded.Data["withttl"].Expiry, expiry)
	}
	if !loaded.Data["nottl"].Expiry.IsZero() {
		t.Errorf("nottl expiry = %v, want zero", loaded.Data["nottl"].Expiry)
	}
}

func TestSnapshotSaveGenerationAndSequenceID(t *testing.T) {
	dir := t.TempDir()
	snap := newTestSnapshot(t, dir)

	if err := snap.SaveToDisk(map[string]SnapshotEntry{}, 7, 42); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}

	loaded, err := snap.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Generation != 7 {
		t.Errorf("Generation = %d, want 7", loaded.Generation)
	}
	if loaded.LastSequenceID != 42 {
		t.Errorf("LastSequenceID = %d, want 42", loaded.LastSequenceID)
	}
}

func TestSnapshotSaveIsAtomicNoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	snap := newTestSnapshot(t, dir)

	if err := snap.SaveToDisk(map[string]SnapshotEntry{"k": {Value: []byte("v")}}, 0, 0); err != nil {
		t.Fatalf("SaveToDisk: %v", err)
	}

	if _, err := os.Stat(snap.snapshotConfig.FilePath); err != nil {
		t.Errorf("final snapshot file missing: %v", err)
	}
	if _, err := os.Stat(snap.tempFilePath); !os.IsNotExist(err) {
		t.Errorf("temp file should not remain after a successful save, stat err = %v", err)
	}
}

func TestSnapshotSaveOverwritesPreviousVersionAtomically(t *testing.T) {
	dir := t.TempDir()
	snap := newTestSnapshot(t, dir)

	if err := snap.SaveToDisk(map[string]SnapshotEntry{"old": {Value: []byte("v1")}}, 0, 0); err != nil {
		t.Fatalf("SaveToDisk #1: %v", err)
	}
	if err := snap.SaveToDisk(map[string]SnapshotEntry{"new": {Value: []byte("v2")}}, 1, 1); err != nil {
		t.Fatalf("SaveToDisk #2: %v", err)
	}

	loaded, err := snap.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := loaded.Data["old"]; ok {
		t.Errorf("stale snapshot data (%q) survived overwrite", "old")
	}
	if _, ok := loaded.Data["new"]; !ok {
		t.Errorf("latest snapshot data missing after overwrite")
	}
}

func TestSnapshotSaveCleansUpTempFileOnFailure(t *testing.T) {
	dir := t.TempDir()
	snap := newTestSnapshot(t, dir)

	// Make the final destination an existing directory so the atomic rename
	// step fails, forcing SaveToDisk down its error/cleanup path.
	if err := os.Mkdir(snap.snapshotConfig.FilePath, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}

	err := snap.SaveToDisk(map[string]SnapshotEntry{"k": {Value: []byte("v")}}, 0, 0)
	if err == nil {
		t.Fatalf("SaveToDisk should fail when the destination path is a directory")
	}
	if _, statErr := os.Stat(snap.tempFilePath); !os.IsNotExist(statErr) {
		t.Errorf("temp file should be cleaned up after a failed save, stat err = %v", statErr)
	}
}

func TestSnapshotLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	snap := newTestSnapshot(t, dir)

	loaded, err := snap.Load()
	if err != nil {
		t.Fatalf("Load on missing file should not error, got %v", err)
	}
	if loaded.Data == nil {
		t.Fatalf("Data should be an empty (non-nil) map, not nil")
	}
	if len(loaded.Data) != 0 {
		t.Errorf("Data len = %d, want 0", len(loaded.Data))
	}
}

func TestSnapshotLoadCorruptedFile(t *testing.T) {
	dir := t.TempDir()
	snap := newTestSnapshot(t, dir)

	if err := os.WriteFile(snap.snapshotConfig.FilePath, []byte("not a gob stream"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	if _, err := snap.Load(); err == nil {
		t.Errorf("Load on a corrupted file should return an error")
	}
}
