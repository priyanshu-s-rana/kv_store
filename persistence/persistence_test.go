package persistence

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/parser"
	"github.com/priyanshu-s-rana/kv_store/store"
)

// ============================================================
// test harness — a real Store wired to a real Persistence, exactly as
// cmd/kv-server/main.go wires them. This exercises the persistence engine
// through its actual public surface rather than poking at internals, so
// passing tests are real end-to-end correctness proof.
// ============================================================

type testNode struct {
	t       *testing.T
	ctx     context.Context
	cancel  context.CancelFunc
	cmdChan chan Command
	persist *Persistence
	store   *store.Store
}

// newTestNode builds and starts a Store + Persistence pair against the given
// journal/snapshot paths. It does NOT call Recovery or Start — callers decide
// when to recover, mirroring main.go's explicit ordering.
func newTestNode(t *testing.T, paths [2]string, snapPath string, policy string) *testNode {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmdChan := make(chan Command)

	journalConfigs := [2]*AOFConfig{
		{FilePath: paths[0], SyncPolicy: policy},
		{FilePath: paths[1], SyncPolicy: policy},
	}
	persist, err := New(ctx, cancel, cmdChan, journalConfigs, &SnapshotConfig{FilePath: snapPath}, noopPersistenceMetrics{})
	if err != nil {
		t.Fatalf("persistence.New: %v", err)
	}

	st := store.New(0, cmdChan, persist, noopStoreMetrics{})
	st.Start()

	return &testNode{t: t, ctx: ctx, cancel: cancel, cmdChan: cmdChan, persist: persist, store: st}
}

// recover runs the normal boot-time recovery sequence, mirroring
// cmd/kv-server/main.go's ordering exactly: load the last snapshot, replay
// whatever journal tail is newer, rebaseline if anything was replayed, then
// Start the journal (which writes the current AOF's header if it doesn't
// have one yet — required before any Append will produce a valid file).
// Interval is left at zero in every test's SnapshotConfig, so Start here
// never spins up the periodic snapshot goroutine, only ensureInitialize.
func (n *testNode) recover() {
	n.t.Helper()
	if err := n.persist.Recovery(); err != nil {
		n.t.Fatalf("Recovery: %v", err)
	}
	if err := n.persist.Start(); err != nil {
		n.t.Fatalf("persist.Start: %v", err)
	}
}

// send dispatches a command to the store's real event loop and returns its response.
func (n *testNode) send(name constants.CmdName, args ...string) Response {
	n.t.Helper()
	cmd := Command{Name: name, Args: args, Resp: make(chan Response, 1)}
	n.cmdChan <- cmd
	select {
	case resp := <-cmd.Resp:
		return resp
	case <-time.After(2 * time.Second):
		n.t.Fatalf("timeout waiting for response to %s", name)
		return Response{}
	}
}

// get returns the decoded value of key, or "nil" if missing/expired.
func (n *testNode) get(key string) string {
	n.t.Helper()
	resp := n.send(constants.Get, key)
	return parser.DecodeBulkString(nil, resp.Value)
}

// checkpoint triggers a CHECKPOINT command and blocks until the async
// snapshot-save/rotate work it schedules has finished.
func (n *testNode) checkpoint() {
	n.t.Helper()
	resp := n.send(constants.Checkpoint)
	if err := resp.IsError(); err != nil {
		n.t.Fatalf("CHECKPOINT command error: %v", err)
	}
	n.waitCheckpointDone()
}

// waitCheckpointDone polls the (package-internal) checkpoint state until the
// async goroutine scheduled by Persistence.Checkpoint finishes. There is no
// channel-based completion signal exposed, so a short bounded poll is the
// only way to synchronize without sleeping blindly.
func (n *testNode) waitCheckpointDone() {
	n.t.Helper()
	deadline := time.After(2 * time.Second)
	for n.persist.checkpointState.InProgress.Load() {
		select {
		case <-deadline:
			n.t.Fatalf("checkpoint did not complete within timeout")
		case <-time.After(time.Millisecond):
		}
	}
}

// crash simulates an unclean shutdown: the raw journal file descriptors are
// closed directly, with no graceful Close(), no final checkpoint, and no
// flush beyond whatever the configured sync policy already guaranteed.
func (n *testNode) crash() {
	n.persist.journal.aofs[0].file.Close()
	n.persist.journal.aofs[1].file.Close()
}

func newTestPaths(t *testing.T) (paths [2]string, snapPath string) {
	t.Helper()
	dir := t.TempDir()
	return [2]string{filepath.Join(dir, "j0.aof"), filepath.Join(dir, "j1.aof")}, filepath.Join(dir, "snap.gob")
}

// ============================================================
// 6. Recovery
// ============================================================

func TestRecoveryEmptyDatabase(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	if got := node.get("missing"); got != "nil" {
		t.Errorf("GET on empty database = %q, want nil", got)
	}
}

// A single checkpoint always rotates (LastSucceeded starts true), so writes
// made after it land in a strictly newer generation than the snapshot. This
// is the well-behaved path: snapshot + newer-generation journal replay.
func TestRecoverySnapshotPlusJournalNewerGeneration(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	node.send(constants.Set, "a", "1")
	node.send(constants.Set, "b", "2")
	node.checkpoint() // rotates: gen 0 -> 1

	node.send(constants.Set, "c", "3") // lands in gen1, never checkpointed
	node.crash()

	fresh := newTestNode(t, paths, snapPath, constants.SyncAlways)
	fresh.recover()

	for k, want := range map[string]string{"a": "1", "b": "2", "c": "3"} {
		if got := fresh.get(k); got != want {
			t.Errorf("recovered %s = %q, want %q", k, got, want)
		}
	}
}

// Verifies that once a generation is fully captured by a later snapshot, its
// AOF file is correctly skipped on the next recovery (no re-application, no
// use of stale data) — while the newest, not-yet-snapshotted generation is
// still replayed.
func TestRecoveryIgnoresOldGenerationCoveredBySnapshot(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	node.send(constants.Set, "a", "1")
	node.checkpoint() // gen 0 -> 1, snapshot covers "a"

	node.send(constants.Set, "a", "overwritten-in-gen1")
	node.checkpoint() // gen 1 -> 2, snapshot now covers the overwrite too

	node.send(constants.Set, "b", "only-in-gen2")
	node.crash()

	fresh := newTestNode(t, paths, snapPath, constants.SyncAlways)
	fresh.recover()

	if got := fresh.get("a"); got != "overwritten-in-gen1" {
		t.Errorf(`a = %q, want "overwritten-in-gen1" (must reflect latest snapshot, not stale gen0 data)`, got)
	}
	if got := fresh.get("b"); got != "only-in-gen2" {
		t.Errorf(`b = %q, want "only-in-gen2" (newest generation must still be replayed)`, got)
	}
}

// Replay must not double-apply commands already folded into the snapshot.
// INCR makes any duplicate application immediately visible as a wrong count.
func TestRecoveryWatermarkPreventsDuplicateReplay(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	for range 5 {
		node.send(constants.Incr, "counter")
	}
	node.checkpoint() // gen 0 -> 1, counter=5 captured in snapshot

	for range 3 {
		node.send(constants.Incr, "counter")
	}
	node.crash()

	fresh := newTestNode(t, paths, snapPath, constants.SyncAlways)
	fresh.recover()

	if got := fresh.get("counter"); got != "8" {
		t.Errorf("counter = %q, want 8 (5 snapshotted + 3 replayed, no duplication)", got)
	}
}

func TestRecoveryPreservesExactState(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	node.send(constants.Set, "keep", "v1")
	node.send(constants.Set, "willdelete", "v2")
	node.send(constants.Expire, "keep", "3600")
	node.checkpoint()

	node.send(constants.Del, "willdelete")
	node.send(constants.Set, "late", "v3")
	node.crash()

	fresh := newTestNode(t, paths, snapPath, constants.SyncAlways)
	fresh.recover()

	if got := fresh.get("keep"); got != "v1" {
		t.Errorf("keep = %q, want v1", got)
	}
	if got := fresh.get("willdelete"); got != "nil" {
		t.Errorf("willdelete = %q, want nil (was deleted before crash)", got)
	}
	if got := fresh.get("late"); got != "v3" {
		t.Errorf("late = %q, want v3", got)
	}
	ttl := fresh.send(constants.TTL, "keep")
	if parser.DecodeInteger(nil, ttl.Value) == "-1" {
		t.Errorf("keep should still carry its TTL after recovery")
	}
}

// ============================================================
// 7. Checkpoint
// ============================================================

func TestCheckpointSuccessRotatesAndAdvancesGeneration(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	node.send(constants.Set, "k", "v")
	genBefore := node.persist.journal.generation.Load()

	node.checkpoint()

	if !node.persist.checkpointState.LastSucceeded.Load() {
		t.Fatalf("checkpoint should have succeeded")
	}
	if got := node.persist.journal.generation.Load(); got != genBefore+1 {
		t.Errorf("generation after successful checkpoint = %d, want %d", got, genBefore+1)
	}

	loaded, err := node.persist.snapshot.Load()
	if err != nil {
		t.Fatalf("Load snapshot: %v", err)
	}
	if loaded.Generation != genBefore {
		t.Errorf("saved snapshot Generation = %d, want %d (sealed generation)", loaded.Generation, genBefore)
	}
	if string(loaded.Data["k"].Value) != "v" {
		t.Errorf("saved snapshot missing key k")
	}
}

// When a checkpoint's snapshot save fails, LastSucceeded must flip false and
// the next checkpoint attempt must skip rotation (guards the orphaned
// unsaved-generation AOF file from being wiped before it's ever captured).
func TestCheckpointFailureSkipsNextRotation(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	node.send(constants.Set, "k", "v")

	// Force the snapshot save to fail: occupy the target path with a directory.
	if err := os.Mkdir(snapPath, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	genAfterFirstAttempt := node.persist.journal.generation.Load() // before attempt
	node.send(constants.Checkpoint)
	node.waitCheckpointDone()

	if node.persist.checkpointState.LastSucceeded.Load() {
		t.Fatalf("expected first checkpoint to fail (snapshot path occupied by a directory)")
	}
	// The first-ever checkpoint still rotates (LastSucceeded started true),
	// only the async snapshot save fails.
	if got := node.persist.journal.generation.Load(); got != genAfterFirstAttempt+1 {
		t.Errorf("generation after first (failed-save) checkpoint = %d, want %d (rotate still happens on first attempt)", got, genAfterFirstAttempt+1)
	}
	genBeforeSecondAttempt := node.persist.journal.generation.Load()

	node.send(constants.Set, "k2", "v2")
	node.send(constants.Checkpoint)
	node.waitCheckpointDone()

	// Second attempt: LastSucceeded was false going in, so rotation must be skipped.
	if got := node.persist.journal.generation.Load(); got != genBeforeSecondAttempt {
		t.Errorf("generation after second attempt = %d, want unchanged %d (rotation must be skipped while unsaved data is orphaned)", got, genBeforeSecondAttempt)
	}
}

// Writes continue to be accepted and durable while checkpoints are failing.
func TestCheckpointFailureDoesNotBlockWrites(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	if err := os.Mkdir(snapPath, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	node.send(constants.Checkpoint)
	node.waitCheckpointDone()
	if node.persist.checkpointState.LastSucceeded.Load() {
		t.Fatalf("expected checkpoint to fail")
	}

	resp := node.send(constants.Set, "k", "v")
	if err := resp.IsError(); err != nil {
		t.Fatalf("SET after failed checkpoint returned an error: %v", err)
	}
	if got := node.get("k"); got != "v" {
		t.Errorf("GET after failed checkpoint = %q, want v", got)
	}
}

// ---- THE CRITICAL TEST ----
//
// checkpoint fails -> commands continue -> later checkpoint succeeds -> crash -> recover
//
// Expectation per the design's stated invariant ("replay executes only
// commands with SequenceID > Snapshot.LastSequenceID"): every key ever
// SET, whether before the failed checkpoint, between the failed and the
// successful checkpoint, or after the successful checkpoint, must survive
// recovery.
//
// CURRENT BEHAVIOR: this test FAILS. Journal.Replay gates replay of an
// entire AOF file on `fileGeneration > snapshotFile.Generation` *before*
// ever consulting the per-command SequenceID watermark. A checkpoint that
// succeeds without a preceding rotation (exactly the case guarded by
// TestCheckpointFailureSkipsNextRotation) produces a snapshot whose
// Generation equals the still-current AOF file's generation. On the next
// recovery, `fileGeneration > snapshot.Generation` is false for that file,
// so the whole file — including every command appended *after* the
// successful checkpoint — is skipped outright, regardless of SequenceID.
// Those post-checkpoint writes are silently lost.
//
// This is not a contrived edge case: the same generation-equals-snapshot
// condition also holds on the very first boot ever (both default to
// generation 0), so *any* crash before the first successful checkpoint loses
// all data (see TestCrashBeforeFirstCheckpointLosesUncapturedWrites below).
func TestCheckpointFailThenSucceedThenCrashRecoversAllWrites(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	// Phase 1: writes before any checkpoint attempt.
	node.send(constants.Set, "a", "1")
	node.send(constants.Set, "b", "2")

	// Force the first checkpoint's snapshot save to fail.
	if err := os.Mkdir(snapPath, 0o755); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	node.send(constants.Checkpoint)
	node.waitCheckpointDone()
	if node.persist.checkpointState.LastSucceeded.Load() {
		t.Fatalf("setup invariant broken: expected first checkpoint to fail")
	}

	// Phase 2: writes made after the failed checkpoint, before the next one.
	node.send(constants.Set, "c", "3")
	node.send(constants.Set, "d", "4")

	// Clear the obstruction so the next checkpoint can succeed.
	if err := os.Remove(snapPath); err != nil {
		t.Fatalf("remove obstruction: %v", err)
	}
	node.send(constants.Checkpoint)
	node.waitCheckpointDone()
	if !node.persist.checkpointState.LastSucceeded.Load() {
		t.Fatalf("setup invariant broken: expected second checkpoint to succeed")
	}

	// Phase 3: writes made after the successful (but unrotated) checkpoint.
	node.send(constants.Set, "e", "5")

	node.crash()

	fresh := newTestNode(t, paths, snapPath, constants.SyncAlways)
	fresh.recover()

	want := map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5"}
	for k, v := range want {
		if got := fresh.get(k); got != v {
			t.Errorf("recovered %s = %q, want %q — commands must survive a checkpoint that succeeds without rotating", k, got, v)
		}
	}
}

// A more minimal repro of the same root cause: no checkpoint has ever run,
// so the on-disk "snapshot" is the empty default (Generation 0), which
// collides with the initial journal generation (also 0). Every write is lost.
func TestCrashBeforeFirstCheckpointLosesUncapturedWrites(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	node.send(constants.Set, "a", "1")
	node.send(constants.Set, "b", "2")
	node.crash()

	fresh := newTestNode(t, paths, snapPath, constants.SyncAlways)
	fresh.recover()

	for k, want := range map[string]string{"a": "1", "b": "2"} {
		if got := fresh.get(k); got != want {
			t.Errorf("recovered %s = %q, want %q — writes made before the first ever checkpoint must still survive a crash", k, got, want)
		}
	}
}

// ============================================================
// 8. Rebaseline
// ============================================================

// Uses a scenario where a rotation has already occurred, so the replay that
// triggers rebaseline is itself unaffected by the generation-filter gap
// documented above — this isolates rebaseline's own behavior.
func TestRebaselineTriggeredWhenReplayOccurs(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	node.send(constants.Set, "a", "1")
	node.checkpoint() // gen 0 -> 1

	node.send(constants.Set, "b", "2") // lands in gen1, uncheckpointed
	node.crash()

	fresh := newTestNode(t, paths, snapPath, constants.SyncAlways)
	fresh.recover()

	// Rebaseline should have produced a fresh snapshot covering both "a" and
	// "b", and advanced the generation once more (1 -> 2).
	loaded, err := fresh.persist.snapshot.Load()
	if err != nil {
		t.Fatalf("Load snapshot: %v", err)
	}
	if string(loaded.Data["a"].Value) != "1" || string(loaded.Data["b"].Value) != "2" {
		t.Errorf("rebaseline snapshot incomplete: %+v", loaded.Data)
	}
	if got := fresh.persist.journal.generation.Load(); got != 2 {
		t.Errorf("generation after rebaseline = %d, want 2", got)
	}
}

func TestRebaselineNotTriggeredWhenSnapshotAlreadyCurrent(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	node.send(constants.Set, "a", "1")
	node.checkpoint() // gen 0 -> 1, snapshot fully covers this state
	node.crash()

	genBefore := uint64(1) // journal.generation restored by ReplayJournal, no replay needed

	fresh := newTestNode(t, paths, snapPath, constants.SyncAlways)
	fresh.recover()

	if got := fresh.persist.journal.generation.Load(); got != genBefore {
		t.Errorf("generation = %d, want %d (rebaseline must not fire when nothing was replayed)", got, genBefore)
	}
}

func TestRebaselineFutureWritesGoToNewGeneration(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	node.send(constants.Set, "a", "1")
	node.checkpoint() // gen 0 -> 1
	node.send(constants.Set, "b", "2")
	node.crash()

	mid := newTestNode(t, paths, snapPath, constants.SyncAlways)
	mid.recover() // rebaseline fires: gen 1 -> 2

	mid.send(constants.Set, "c", "3") // must land in gen2
	mid.crash()

	fresh := newTestNode(t, paths, snapPath, constants.SyncAlways)
	fresh.recover()

	for k, want := range map[string]string{"a": "1", "b": "2", "c": "3"} {
		if got := fresh.get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

// ============================================================
// 9. Crash scenarios
// ============================================================

func TestCrashDuringRotationDoesNotCorruptCurrentJournal(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	node.send(constants.Set, "a", "1")

	// Force Rotate's Initialize(standby) step to fail mid-checkpoint.
	if err := node.persist.journal.standby().file.Close(); err != nil {
		t.Fatalf("pre-close standby: %v", err)
	}
	resp := node.send(constants.Checkpoint)
	if err := resp.IsError(); err == nil {
		t.Fatalf("checkpoint should surface the rotation failure synchronously")
	}

	// The live (current) journal must still be intact and usable afterwards.
	if got := node.get("a"); got != "1" {
		t.Errorf("a = %q, want 1 (failed rotation corrupted live state)", got)
	}
	resp = node.send(constants.Set, "b", "2")
	if err := resp.IsError(); err != nil {
		t.Fatalf("SET after failed rotation returned an error: %v", err)
	}
	if got := node.get("b"); got != "2" {
		t.Errorf("b = %q, want 2 (writes must keep working after a failed rotation)", got)
	}
}

func TestCrashAfterSuccessfulRotationRecoversCleanly(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	node.send(constants.Set, "a", "1")
	node.checkpoint()
	node.crash() // crash immediately after a clean, fully-rotated checkpoint

	fresh := newTestNode(t, paths, snapPath, constants.SyncAlways)
	fresh.recover()

	if got := fresh.get("a"); got != "1" {
		t.Errorf("a = %q, want 1", got)
	}
}

// ============================================================
// 10 / 11. SequenceID & Generation (integration-level)
// ============================================================

func TestGenerationStartsAtZero(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	if got := node.persist.journal.generation.Load(); got != 0 {
		t.Errorf("initial generation = %d, want 0", got)
	}
}

func TestSequenceIDContinuesAfterRecovery(t *testing.T) {
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	node.send(constants.Set, "a", "1")
	node.checkpoint()                  // gen0 -> 1, sequenceID=1 sealed
	node.send(constants.Set, "b", "2") // sequenceID=2, gen1
	node.crash()

	fresh := newTestNode(t, paths, snapPath, constants.SyncAlways)
	fresh.recover()

	if got := fresh.persist.journal.sequenceID.Load(); got != 2 {
		t.Errorf("sequenceID after recovery = %d, want 2 (restored from replay)", got)
	}

	fresh.send(constants.Set, "c", "3") // must continue from 3, not restart at 1
	if got := fresh.persist.journal.sequenceID.Load(); got != 3 {
		t.Errorf("sequenceID after post-recovery append = %d, want 3", got)
	}
}

// ============================================================
// 12. Property / randomized test
// ============================================================

// Drives a random stream of SET/DEL/INCR against a live node, interleaved
// with checkpoints that are always allowed to succeed (no fault injection),
// then crashes and recovers into a fresh node. The recovered state must
// exactly match an independently maintained in-memory reference model.
//
// This intentionally avoids the checkpoint-failure interleaving covered by
// TestCheckpointFailThenSucceedThenCrashRecoversAllWrites: every checkpoint
// here succeeds and therefore always rotates, so this test isolates general
// read/write/recovery correctness from the known generation-filter gap.
func TestPropertyRandomizedRecoveryConvergesToExpectedState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping randomized recovery test in -short mode")
	}
	paths, snapPath := newTestPaths(t)
	node := newTestNode(t, paths, snapPath, constants.SyncAlways)
	node.recover()

	rng := rand.New(rand.NewSource(42))
	keys := []string{"k1", "k2", "k3", "k4", "k5", "k6"}
	expected := map[string]string{}

	const totalOps = 300
	for i := 0; i < totalOps; i++ {
		key := keys[rng.Intn(len(keys))]
		switch rng.Intn(10) {
		case 0, 1, 2, 3: // SET
			val := strconv.Itoa(rng.Intn(1000))
			node.send(constants.Set, key, val)
			expected[key] = val
		case 4, 5: // DEL
			node.send(constants.Del, key)
			delete(expected, key)
		case 6, 7: // INCR — only ever applied to numeric or absent keys here
			node.send(constants.Incr, key)
			n := 0
			if cur, ok := expected[key]; ok {
				parsed, err := strconv.Atoi(cur)
				if err != nil {
					t.Fatalf("reference model corrupted: %s=%q is not numeric", key, cur)
				}
				n = parsed
			}
			expected[key] = strconv.Itoa(n + 1)
		case 8: // MSET a pair
			k2 := keys[rng.Intn(len(keys))]
			v1, v2 := strconv.Itoa(rng.Intn(1000)), strconv.Itoa(rng.Intn(1000))
			node.send(constants.Mset, key, v1, k2, v2)
			expected[key] = v1
			expected[k2] = v2
		case 9: // occasionally checkpoint
			node.checkpoint()
			if !node.persist.checkpointState.LastSucceeded.Load() {
				t.Fatalf("checkpoint unexpectedly failed at op %d", i)
			}
		}
	}

	node.crash()

	fresh := newTestNode(t, paths, snapPath, constants.SyncAlways)
	fresh.recover()

	for _, k := range keys {
		want, ok := expected[k]
		got := fresh.get(k)
		if !ok {
			if got != "nil" {
				t.Errorf("key %s = %q, want missing (nil)", k, got)
			}
			continue
		}
		if got != want {
			t.Errorf("key %s = %q, want %q", k, got, want)
		}
	}
}
