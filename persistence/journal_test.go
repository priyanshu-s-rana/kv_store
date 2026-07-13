package persistence

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

// ---- test helpers ----

// newTestJournal builds a Journal backed by two temp-file AOFs, without
// starting any background goroutines (no Start()/everySec ticker).
func newTestJournal(t *testing.T, policy string) (*Journal, [2]string) {
	t.Helper()
	dir := t.TempDir()
	paths := [2]string{
		filepath.Join(dir, "journal-0.aof"),
		filepath.Join(dir, "journal-1.aof"),
	}
	configs := [2]*AOFConfig{
		{FilePath: paths[0], SyncPolicy: policy},
		{FilePath: paths[1], SyncPolicy: policy},
	}
	j, err := NewJournal(configs, noopPersistenceMetrics{})
	if err != nil {
		t.Fatalf("NewJournal: %v", err)
	}
	t.Cleanup(func() {
		j.aofs[0].file.Close()
		j.aofs[1].file.Close()
	})
	return j, paths
}

// initCurrent writes the initial header to the journal's current AOF,
// equivalent to what Journal.Start does, without spinning up the everySec
// goroutine.
func initCurrent(t *testing.T, j *Journal) {
	t.Helper()
	if err := j.current().ensureInitialize(j.generation.Load()); err != nil {
		t.Fatalf("ensureInitialize current: %v", err)
	}
}

// reopenJournal simulates a process restart: builds a brand new Journal
// (generation/sequenceID both zero-valued) against the same on-disk files,
// so recovery-oriented tests exercise state restored purely from disk.
func reopenJournal(t *testing.T, paths [2]string, policy string) *Journal {
	t.Helper()
	configs := [2]*AOFConfig{
		{FilePath: paths[0], SyncPolicy: policy},
		{FilePath: paths[1], SyncPolicy: policy},
	}
	j, err := NewJournal(configs, noopPersistenceMetrics{})
	if err != nil {
		t.Fatalf("NewJournal (reopen): %v", err)
	}
	t.Cleanup(func() {
		j.aofs[0].file.Close()
		j.aofs[1].file.Close()
	})
	return j
}

// ============================================================
// Append / SequenceID
// ============================================================

func TestJournalAppendIncrementsSequenceID(t *testing.T) {
	j, paths := newTestJournal(t, constants.SyncAlways)
	initCurrent(t, j)

	for i, args := range [][]string{{"a", "1"}, {"b", "2"}, {"c", "3"}} {
		if err := j.Append(constants.Set, args); err != nil {
			t.Fatalf("Append #%d: %v", i, err)
		}
	}

	if got := j.sequenceID.Load(); got != 3 {
		t.Errorf("sequenceID = %d, want 3", got)
	}

	cmds := readAllCommands(t, paths[0])
	if len(cmds) != 4 { // header + 3 entries
		t.Fatalf("commands = %d, want 4", len(cmds))
	}
	for i, want := range []string{"1", "2", "3"} {
		if got := cmds[i+1].Args[0]; got != want {
			t.Errorf("entry #%d sequenceID = %q, want %q", i, got, want)
		}
	}
}

func TestJournalAppendWritesToCurrentFile(t *testing.T) {
	j, paths := newTestJournal(t, constants.SyncAlways)
	initCurrent(t, j)

	if err := j.Append(constants.Set, []string{"k", "v"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if cmds := readAllCommands(t, paths[0]); len(cmds) != 2 {
		t.Errorf("current file (paths[0]) commands = %d, want 2", len(cmds))
	}
	if cmds := readAllCommands(t, paths[1]); len(cmds) != 0 {
		t.Errorf("standby file (paths[1]) commands = %d, want 0 (untouched, not even initialized)", len(cmds))
	}
}

func TestJournalSequenceIDContinuesAcrossRotation(t *testing.T) {
	j, _ := newTestJournal(t, constants.SyncAlways)
	initCurrent(t, j)

	if err := j.Append(constants.Set, []string{"a", "1"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := j.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if err := j.Append(constants.Set, []string{"b", "2"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if got := j.sequenceID.Load(); got != 2 {
		t.Errorf("sequenceID after rotate = %d, want 2 (must not reset)", got)
	}
}

// ============================================================
// Rotate
// ============================================================

func TestJournalRotateAdvancesGenerationAndFlipsParity(t *testing.T) {
	j, _ := newTestJournal(t, constants.SyncAlways)
	initCurrent(t, j)
	oldCurrent := j.current()

	if err := j.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	if got := j.generation.Load(); got != 1 {
		t.Errorf("generation after Rotate = %d, want 1", got)
	}
	if j.current() == oldCurrent {
		t.Errorf("current() did not flip to the standby journal after Rotate")
	}
	if j.standby() != oldCurrent {
		t.Errorf("standby() should now be the pre-rotate current journal")
	}
}

func TestJournalRotateInitializesStandbyWithNewGeneration(t *testing.T) {
	j, paths := newTestJournal(t, constants.SyncAlways)
	initCurrent(t, j)
	if err := j.Append(constants.Set, []string{"a", "1"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if err := j.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	// paths[1] was standby, now current — freshly initialized with gen=1,
	// containing only the header.
	newCurrentMeta, err := readHeaderFromStart(t, j.aofs[1])
	if err != nil {
		t.Fatalf("readHeader on new current: %v", err)
	}
	if newCurrentMeta.Generation != 1 {
		t.Errorf("new current generation = %d, want 1", newCurrentMeta.Generation)
	}
	if cmds := readAllCommands(t, paths[1]); len(cmds) != 1 {
		t.Errorf("new current commands = %d, want 1 (header only)", len(cmds))
	}
}

func TestJournalRotateLeavesOldJournalIntact(t *testing.T) {
	j, paths := newTestJournal(t, constants.SyncAlways)
	initCurrent(t, j)
	if err := j.Append(constants.Set, []string{"a", "1"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := j.Append(constants.Set, []string{"b", "2"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	if err := j.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	// paths[0] was current (gen0, 2 entries), now standby — must be untouched.
	cmds := readAllCommands(t, paths[0])
	if len(cmds) != 3 { // header + 2 entries
		t.Fatalf("old journal commands = %d, want 3, data lost on rotate", len(cmds))
	}
	if cmds[1].Args[3] != "1" || cmds[2].Args[3] != "2" {
		t.Errorf("old journal entries corrupted: %+v", cmds)
	}
}

func TestJournalRotateSecondRotationReusesAndWipesOldestSlot(t *testing.T) {
	j, paths := newTestJournal(t, constants.SyncAlways)
	initCurrent(t, j)
	if err := j.Append(constants.Set, []string{"gen0", "v"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := j.Rotate(); err != nil { // -> generation 1, paths[1] fresh
		t.Fatalf("Rotate #1: %v", err)
	}
	if err := j.Append(constants.Set, []string{"gen1", "v"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := j.Rotate(); err != nil { // -> generation 2, paths[0] reused/truncated
		t.Fatalf("Rotate #2: %v", err)
	}

	if got := j.generation.Load(); got != 2 {
		t.Fatalf("generation = %d, want 2", got)
	}
	// paths[0] (the gen0 file, orphaned after rotate #1) is now truncated and
	// reinitialized to gen2 — its gen0 data is gone, by design: a second
	// rotation only happens after a checkpoint successfully snapshotted it.
	meta, err := readHeaderFromStart(t, j.aofs[0])
	if err != nil {
		t.Fatalf("readHeader: %v", err)
	}
	if meta.Generation != 2 {
		t.Errorf("reused slot generation = %d, want 2", meta.Generation)
	}
	if cmds := readAllCommands(t, paths[0]); len(cmds) != 1 {
		t.Errorf("reused slot commands = %d, want 1 (header only, old data wiped)", len(cmds))
	}
}

func TestJournalRotateFsyncFailureAbortsWithoutGenerationChange(t *testing.T) {
	j, _ := newTestJournal(t, constants.SyncAlways)
	initCurrent(t, j)

	// Force fsync of the current journal to fail by closing its fd early.
	if err := j.current().file.Close(); err != nil {
		t.Fatalf("pre-close current: %v", err)
	}

	if err := j.Rotate(); err == nil {
		t.Fatalf("Rotate should fail when fsyncing the current journal fails")
	}
	if got := j.generation.Load(); got != 0 {
		t.Errorf("generation = %d, want 0 (must not change on fsync failure)", got)
	}
}

func TestJournalRotateInitializeFailureAbortsWithoutGenerationChange(t *testing.T) {
	j, _ := newTestJournal(t, constants.SyncAlways)
	initCurrent(t, j)

	// Force Initialize of the standby journal to fail by closing its fd early.
	if err := j.standby().file.Close(); err != nil {
		t.Fatalf("pre-close standby: %v", err)
	}

	if err := j.Rotate(); err == nil {
		t.Fatalf("Rotate should fail when initializing the standby journal fails")
	}
	if got := j.generation.Load(); got != 0 {
		t.Errorf("generation = %d, want 0 (rotation must abort before publishing new generation)", got)
	}
}

// ============================================================
// Replay (multi-file, generation-aware)
// ============================================================

func TestJournalReplaySkipsGenerationsAtOrBelowSnapshot(t *testing.T) {
	j, paths := newTestJournal(t, constants.SyncAlways)
	initCurrent(t, j)
	if err := j.Append(constants.Set, []string{"a", "1"}); err != nil { // seq 1, gen0
		t.Fatalf("Append: %v", err)
	}
	if err := j.Append(constants.Set, []string{"b", "2"}); err != nil { // seq 2, gen0
		t.Fatalf("Append: %v", err)
	}
	if err := j.Rotate(); err != nil { // -> gen1
		t.Fatalf("Rotate: %v", err)
	}
	if err := j.Append(constants.Set, []string{"c", "3"}); err != nil { // seq 3, gen1
		t.Fatalf("Append: %v", err)
	}
	if err := j.Append(constants.Set, []string{"d", "4"}); err != nil { // seq 4, gen1
		t.Fatalf("Append: %v", err)
	}

	fresh := reopenJournal(t, paths, constants.SyncAlways)
	cmdChan, records, stop := newRecordingEventLoop(t)
	defer stop()

	snap := &snapshotFile{Generation: 0, LastSequenceID: 2}
	replayed, err := fresh.Replay(snap, cmdChan)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !replayed {
		t.Fatalf("replayed = false, want true")
	}

	got := records()
	if len(got) != 2 {
		t.Fatalf("dispatched %d commands, want 2 (only gen1 entries c,d)", len(got))
	}
	if got[0].Args[0] != "c" || got[1].Args[0] != "d" {
		t.Errorf("dispatched commands = %+v, want c then d", got)
	}
}

func TestJournalReplayNoNewerGenerationYieldsNoReplay(t *testing.T) {
	j, paths := newTestJournal(t, constants.SyncAlways)
	initCurrent(t, j)
	if err := j.Append(constants.Set, []string{"a", "1"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	fresh := reopenJournal(t, paths, constants.SyncAlways)
	cmdChan, records, stop := newRecordingEventLoop(t)
	defer stop()

	// Snapshot already covers current generation and sequence entirely.
	snap := &snapshotFile{Generation: 0, LastSequenceID: 1}
	replayed, err := fresh.Replay(snap, cmdChan)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if replayed {
		t.Errorf("replayed = true, want false — snapshot already covers all data")
	}
	if len(records()) != 0 {
		t.Errorf("dispatched commands, want none")
	}
}

func TestJournalReplayRestoresGenerationAndSequenceID(t *testing.T) {
	j, paths := newTestJournal(t, constants.SyncAlways)
	initCurrent(t, j)
	if err := j.Append(constants.Set, []string{"a", "1"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := j.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if err := j.Append(constants.Set, []string{"b", "2"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := j.Append(constants.Set, []string{"c", "3"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	fresh := reopenJournal(t, paths, constants.SyncAlways)
	if got := fresh.generation.Load(); got != 0 {
		t.Fatalf("sanity: fresh journal generation = %d, want 0 before Replay", got)
	}

	cmdChan, _, stop := newRecordingEventLoop(t)
	defer stop()

	snap := &snapshotFile{Generation: 0, LastSequenceID: 1}
	if _, err := fresh.Replay(snap, cmdChan); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if got := fresh.generation.Load(); got != 1 {
		t.Errorf("generation after Replay = %d, want 1 (restored from on-disk headers)", got)
	}
	if got := fresh.sequenceID.Load(); got != 3 {
		t.Errorf("sequenceID after Replay = %d, want 3 (restored from last replayed entry)", got)
	}
}

func TestJournalReplayPreservesOrderAcrossGenerations(t *testing.T) {
	j, paths := newTestJournal(t, constants.SyncAlways)
	initCurrent(t, j)
	// No rotation: snapshot starts from zero, everything in gen0 should replay in order.
	for i, args := range [][]string{{"a", "1"}, {"b", "2"}, {"c", "3"}} {
		if err := j.Append(constants.Set, args); err != nil {
			t.Fatalf("Append #%d: %v", i, err)
		}
	}

	fresh := reopenJournal(t, paths, constants.SyncAlways)
	cmdChan, records, stop := newRecordingEventLoop(t)
	defer stop()

	snap := &snapshotFile{Generation: 0, LastSequenceID: 0}
	if _, err := fresh.Replay(snap, cmdChan); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	got := records()
	if len(got) != 3 {
		t.Fatalf("dispatched %d commands, want 3", len(got))
	}
	for i, want := range []string{"a", "b", "c"} {
		if got[i].Args[0] != want {
			t.Errorf("command #%d key = %q, want %q (ordering not preserved)", i, got[i].Args[0], want)
		}
	}
}

// ============================================================
// Start / everySec sync policy (slow — skipped under -short)
// ============================================================

func TestJournalStartFsyncsPeriodicallyUnderSyncEverySec(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timer-based fsync test in -short mode")
	}
	j, paths := newTestJournal(t, constants.SyncEverySec)

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	if err := j.Start(ctx, &wg); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := j.Append(constants.Set, []string{"k", "v"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Immediately after Append, under SyncEverySec, the entry should still be buffered.
	if cmds := readAllCommands(t, paths[0]); len(cmds) != 1 {
		t.Fatalf("commands visible before ticker fires = %d, want 1 (header only)", len(cmds))
	}

	deadline := time.After(2 * time.Second)
	for {
		if cmds := readAllCommands(t, paths[0]); len(cmds) == 2 {
			return // ticker fsync'd the entry — success
		}
		select {
		case <-deadline:
			t.Fatalf("everySec ticker did not fsync the buffered entry within 2s")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
