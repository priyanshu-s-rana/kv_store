//go:build integration

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

// Invariant: a server with no prior data directory boots cleanly and serves
// an empty keyspace — recovery must handle "nothing to recover" without error.
func TestRestartEmptyDatabase(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	in := startInstance(t, dataDir, constants.SyncAlways)
	c := in.dial()
	if got := c.mustDo("PING"); got != "PONG" {
		t.Fatalf("PING = %q, want PONG", got)
	}
	if got := c.mustDo("GET", "nope"); got != "nil" {
		t.Fatalf("GET on empty db = %q, want nil", got)
	}
	in.sigterm()
	in.waitExit(5 * time.Second)

	fresh := startInstance(t, dataDir, constants.SyncAlways)
	fc := fresh.dial()
	if got := fc.mustDo("GET", "nope"); got != "nil" {
		t.Fatalf("GET after restart of empty db = %q, want nil", got)
	}
}

// Invariant: with no checkpoint ever taken, every acknowledged write must
// still be recoverable purely from journal replay after a hard crash.
func TestJournalReplayRecoversWritesWithoutCheckpoint(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	in := startInstance(t, dataDir, constants.SyncAlways)
	c := in.dial()
	want := map[string]string{"a": "1", "b": "2", "c": "3"}
	for k, v := range want {
		if got := c.mustDo("SET", k, v); got != "OK" {
			t.Fatalf("SET %s: got %q", k, got)
		}
	}

	in.sigkill()
	in.waitExit(5 * time.Second)

	fresh := startInstance(t, dataDir, constants.SyncAlways)
	fc := fresh.dial()
	for k, v := range want {
		if got := fc.mustDo("GET", k); got != v {
			t.Errorf("recovered %s = %q, want %q", k, got, v)
		}
	}
}

// Invariant: keys captured by a completed checkpoint AND keys written to the
// journal afterward must both survive a crash — snapshot plus journal tail
// must compose into the full, correct state.
func TestSnapshotAndJournalTailBothReplay(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	in := startInstance(t, dataDir, constants.SyncAlways)
	c := in.dial()

	c.mustDo("SET", "before-a", "1")
	c.mustDo("SET", "before-b", "2")
	in.checkpointAndWait(c)

	c.mustDo("SET", "after-a", "3")
	c.mustDo("SET", "after-b", "4")

	in.sigkill()
	in.waitExit(5 * time.Second)

	fresh := startInstance(t, dataDir, constants.SyncAlways)
	fc := fresh.dial()
	want := map[string]string{"before-a": "1", "before-b": "2", "after-a": "3", "after-b": "4"}
	for k, v := range want {
		if got := fc.mustDo("GET", k); got != v {
			t.Errorf("recovered %s = %q, want %q", k, got, v)
		}
	}
}

// Invariant: SIGTERM must trigger persist.Close()'s final checkpoint before
// exit, so a clean shutdown never relies on journal replay to recover the
// most recent writes.
func TestGracefulShutdownPersistsViaFinalCheckpoint(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	in := startInstance(t, dataDir, constants.SyncAlways)
	c := in.dial()
	c.mustDo("SET", "k1", "v1")
	c.mustDo("SET", "k2", "v2")

	in.sigterm()
	in.waitExit(5 * time.Second)

	snapPath := filepath.Join(dataDir, "dump.gob")
	if _, err := os.Stat(snapPath); err != nil {
		t.Fatalf("expected a snapshot file at %s after graceful shutdown: %v", snapPath, err)
	}

	fresh := startInstance(t, dataDir, constants.SyncAlways)
	fc := fresh.dial()
	for k, v := range map[string]string{"k1": "v1", "k2": "v2"} {
		if got := fc.mustDo("GET", k); got != v {
			t.Errorf("recovered %s = %q, want %q", k, got, v)
		}
	}
}

// Invariant: repeated checkpoint cycles must rotate the journal each time
// (alternating between the two AOF slots) without losing any data across
// the rotations, and the restored generation must reflect the number of
// rotations that actually happened.
func TestRestartAfterMultipleRotations(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	in := startInstance(t, dataDir, constants.SyncAlways)
	c := in.dial()

	const rotations = 5
	want := make(map[string]string, rotations)
	for i := range rotations {
		key := fmt.Sprintf("k%d", i)
		val := fmt.Sprintf("v%d", i)
		c.mustDo("SET", key, val)
		want[key] = val
		in.checkpointAndWait(c)
	}

	gotGen, ok := metricValue(t, in.metricsAddr, "kv_persistence_current_journal_generation")
	if !ok || gotGen < rotations {
		t.Fatalf("current_journal_generation = %v (present=%v), want >= %d after %d checkpoints", gotGen, ok, rotations, rotations)
	}

	in.sigkill()
	in.waitExit(5 * time.Second)

	fresh := startInstance(t, dataDir, constants.SyncAlways)
	fc := fresh.dial()
	for k, v := range want {
		if got := fc.mustDo("GET", k); got != v {
			t.Errorf("recovered %s = %q, want %q", k, got, v)
		}
	}
	restoredGen, ok := metricValue(t, fresh.metricsAddr, "kv_persistence_current_journal_generation")
	if !ok || restoredGen < rotations {
		t.Errorf("restored current_journal_generation = %v (present=%v), want >= %d", restoredGen, ok, rotations)
	}
}

// Invariant: a hard kill immediately after a checkpoint has fully completed
// (snapshot on disk, generation advanced) must never lose or corrupt the
// checkpointed state — the system must be crash-safe at rest.
func TestCrashImmediatelyAfterCheckpointCompletes(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	in := startInstance(t, dataDir, constants.SyncAlways)
	c := in.dial()
	c.mustDo("SET", "settled", "value")
	in.checkpointAndWait(c)

	in.sigkill()
	in.waitExit(5 * time.Second)

	fresh := startInstance(t, dataDir, constants.SyncAlways)
	fc := fresh.dial()
	if got := fc.mustDo("GET", "settled"); got != "value" {
		t.Errorf("recovered settled = %q, want value", got)
	}
}

// Invariant (best-effort — see comment below): a hard kill in the window
// between journal rotation (synchronous, completes before the CHECKPOINT
// reply is sent) and the asynchronous snapshot save finishing must never
// corrupt on-disk state, and every write acknowledged before the checkpoint
// was triggered must remain recoverable via the journal even if the new
// snapshot never made it to disk. The atomic temp-file+rename snapshot save
// guarantees a crash mid-write leaves the previous snapshot (if any)
// untouched; this test's job is to prove the composed system honors that
// at the process level.
//
// Note on precision: without a fault-injection hook in production code (out
// of scope per this suite's constraints), we cannot deterministically pause
// the async snapshot save mid-flight. We approximate the window by killing
// as fast as possible after receiving the CHECKPOINT reply (which is only
// sent after rotation completes, so we are provably at-or-after rotation,
// and — for a snapshot this small — very likely before the save finishes).
// The assertion below holds regardless of which side of that race we land
// on, which is what makes it a meaningful regression guard rather than a
// flaky one.
func TestCrashRacingCheckpointSaveNeverCorrupts(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	in := startInstance(t, dataDir, constants.SyncAlways)
	c := in.dial()
	c.mustDo("SET", "racer", "1")

	resp, err := c.do("CHECKPOINT")
	if err != nil || resp != "OK" {
		t.Fatalf("CHECKPOINT: resp=%q err=%v", resp, err)
	}
	in.sigkill() // as immediate as possible after the synchronous rotation
	in.waitExit(5 * time.Second)

	tmpSnap := filepath.Join(dataDir, "dump.gob.tmp")
	if _, err := os.Stat(tmpSnap); err == nil {
		t.Logf("leftover .tmp snapshot file present (expected if killed mid-save): %s", tmpSnap)
	}

	fresh := startInstance(t, dataDir, constants.SyncAlways)
	fc := fresh.dial()
	if got := fc.mustDo("GET", "racer"); got != "1" {
		t.Errorf("recovered racer = %q, want 1 — write acknowledged before the checkpoint must never be lost, regardless of whether the new snapshot finished saving", got)
	}
	if got := fc.mustDo("PING"); got != "PONG" {
		t.Fatalf("PING after recovery = %q, want PONG — recovery must not be blocked by a partial/leftover .tmp snapshot", got)
	}
}
