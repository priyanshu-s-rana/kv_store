package persistence

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/parser"
)

// ---- test helpers ----

// newTestAOF opens a fresh AOF at a temp path with the given sync policy.
// The underlying file is closed automatically via t.Cleanup.
func newTestAOF(t *testing.T, policy string) (*AOF, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journal.aof")
	aof, err := NewAOF(&AOFConfig{FilePath: path, SyncPolicy: policy}, noopPersistenceMetrics{})
	if err != nil {
		t.Fatalf("NewAOF: %v", err)
	}
	t.Cleanup(func() { aof.file.Close() })
	return aof, path
}

// readAllCommands parses every RESP command sequentially out of the file at path.
func readAllCommands(t *testing.T, path string) []parser.Command {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	p := parser.New(f)
	var cmds []parser.Command
	for {
		cmd, err := p.ReadCommand()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("ReadCommand: %v", err)
		}
		cmds = append(cmds, cmd)
	}
	return cmds
}

// readHeaderFromStart seeks aof's own fd back to the start before reading the
// header, mirroring how every production call site (Journal.Replay) uses
// readHeader — it never seeks internally, callers own file positioning.
func readHeaderFromStart(t *testing.T, aof *AOF) (*AOFMetadata, error) {
	t.Helper()
	if _, err := aof.file.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	return aof.readHeader()
}

// rawFileBytes re-opens path independently of aof's own fd and returns its
// current on-disk contents, to verify durability without relying on aof's
// buffered writer.
func rawFileBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return b
}

// ============================================================
// 1. Journal (AOF) Initialization
// ============================================================

func TestAOFInitializeEmptyJournal(t *testing.T) {
	aof, _ := newTestAOF(t, constants.SyncEverySec)

	if err := aof.Initialize(0); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	meta, err := readHeaderFromStart(t, aof)
	if err != nil {
		t.Fatalf("readHeader: %v", err)
	}
	if meta.Generation != 0 {
		t.Errorf("Generation = %d, want 0", meta.Generation)
	}
}

func TestAOFInitializeTruncatesOldContents(t *testing.T) {
	aof, path := newTestAOF(t, constants.SyncEverySec)

	if err := aof.Initialize(0); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := aof.Append(constants.Set, []string{"k", "v"}, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := aof.fsync(); err != nil {
		t.Fatalf("fsync: %v", err)
	}

	sizeBefore := len(rawFileBytes(t, path))
	if sizeBefore == 0 {
		t.Fatalf("expected non-empty file before re-Initialize")
	}

	if err := aof.Initialize(5); err != nil {
		t.Fatalf("Initialize(5): %v", err)
	}

	cmds := readAllCommands(t, path)
	if len(cmds) != 1 {
		t.Fatalf("commands after re-Initialize = %d, want 1 (just the new header)", len(cmds))
	}
	if string(cmds[0].Name) != constants.Header {
		t.Errorf("only remaining command should be HEADER, got %q", cmds[0].Name)
	}
}

func TestAOFInitializeRewritesHeader(t *testing.T) {
	aof, _ := newTestAOF(t, constants.SyncEverySec)

	if err := aof.Initialize(0); err != nil {
		t.Fatalf("Initialize(0): %v", err)
	}
	if err := aof.Initialize(3); err != nil {
		t.Fatalf("Initialize(3): %v", err)
	}

	meta, err := readHeaderFromStart(t, aof)
	if err != nil {
		t.Fatalf("readHeader: %v", err)
	}
	if meta.Generation != 3 {
		t.Errorf("Generation = %d, want 3 (latest Initialize wins)", meta.Generation)
	}
}

func TestAOFInitializeIsIdempotent(t *testing.T) {
	aof, path := newTestAOF(t, constants.SyncEverySec)

	if err := aof.Initialize(2); err != nil {
		t.Fatalf("Initialize #1: %v", err)
	}
	firstSize := len(rawFileBytes(t, path))

	if err := aof.Initialize(2); err != nil {
		t.Fatalf("Initialize #2: %v", err)
	}
	secondSize := len(rawFileBytes(t, path))

	if firstSize != secondSize {
		t.Errorf("re-Initialize with same generation changed file size: %d -> %d", firstSize, secondSize)
	}
	cmds := readAllCommands(t, path)
	if len(cmds) != 1 {
		t.Fatalf("commands after repeated Initialize = %d, want 1", len(cmds))
	}
}

func TestAOFInvalidHeaderDetection(t *testing.T) {
	aof, path := newTestAOF(t, constants.SyncEverySec)

	// Write a non-header command as the very first entry, bypassing Initialize.
	if err := aof.Append(constants.Set, []string{"k", "v"}, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := aof.fsync(); err != nil {
		t.Fatalf("fsync: %v", err)
	}
	_ = path

	if _, err := readHeaderFromStart(t, aof); err == nil {
		t.Errorf("readHeader on a file without a HEADER command should error")
	}
}

func TestAOFEmptyJournalHeaderHandling(t *testing.T) {
	aof, _ := newTestAOF(t, constants.SyncEverySec)

	// Never Initialize'd or Appended to: zero-byte file, readHeader should
	// treat EOF as "no header yet" rather than an error.
	meta, err := readHeaderFromStart(t, aof)
	if err != nil {
		t.Fatalf("readHeader on empty file: %v", err)
	}
	if meta.Generation != 0 {
		t.Errorf("Generation on empty file = %d, want 0", meta.Generation)
	}
}

func TestAOFEnsureInitializeOnlyWritesWhenEmpty(t *testing.T) {
	aof, path := newTestAOF(t, constants.SyncEverySec)

	if err := aof.ensureInitialize(0); err != nil {
		t.Fatalf("ensureInitialize: %v", err)
	}
	sizeAfterFirst := len(rawFileBytes(t, path))

	// A second call on a non-empty file must be a no-op.
	if err := aof.ensureInitialize(7); err != nil {
		t.Fatalf("ensureInitialize (no-op): %v", err)
	}
	sizeAfterSecond := len(rawFileBytes(t, path))

	if sizeAfterFirst != sizeAfterSecond {
		t.Errorf("ensureInitialize wrote again on a non-empty file: %d -> %d", sizeAfterFirst, sizeAfterSecond)
	}
	meta, err := readHeaderFromStart(t, aof)
	if err != nil {
		t.Fatalf("readHeader: %v", err)
	}
	if meta.Generation != 0 {
		t.Errorf("Generation = %d, want 0 (second ensureInitialize must not overwrite)", meta.Generation)
	}
}

// ============================================================
// 2. Append
// ============================================================

func TestAOFSingleAppend(t *testing.T) {
	aof, path := newTestAOF(t, constants.SyncEverySec)
	if err := aof.Initialize(0); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := aof.Append(constants.Set, []string{"k", "v"}, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := aof.fsync(); err != nil {
		t.Fatalf("fsync: %v", err)
	}

	cmds := readAllCommands(t, path)
	if len(cmds) != 2 {
		t.Fatalf("commands = %d, want 2 (header + 1 entry)", len(cmds))
	}
	entry := cmds[1]
	if string(entry.Name) != constants.SequenceID {
		t.Fatalf("entry.Name = %q, want %q", entry.Name, constants.SequenceID)
	}
	if entry.Args[0] != "1" || entry.Args[1] != string(constants.Set) || entry.Args[2] != "k" || entry.Args[3] != "v" {
		t.Errorf("entry.Args = %v, want [1 SET k v]", entry.Args)
	}
}

func TestAOFMultipleAppendOrdering(t *testing.T) {
	aof, path := newTestAOF(t, constants.SyncEverySec)
	if err := aof.Initialize(0); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	for i, seq := range []uint64{1, 2, 3} {
		key := []string{"k", string(rune('a' + i))}
		if err := aof.Append(constants.Set, key, seq); err != nil {
			t.Fatalf("Append #%d: %v", i, err)
		}
	}
	if err := aof.fsync(); err != nil {
		t.Fatalf("fsync: %v", err)
	}

	cmds := readAllCommands(t, path)
	if len(cmds) != 4 { // header + 3 entries
		t.Fatalf("commands = %d, want 4", len(cmds))
	}
	for i, want := range []string{"1", "2", "3"} {
		got := cmds[i+1].Args[0]
		if got != want {
			t.Errorf("entry #%d sequenceID = %q, want %q (out of order)", i, got, want)
		}
	}
}

func TestAOFAppendAfterInitialize(t *testing.T) {
	aof, path := newTestAOF(t, constants.SyncEverySec)
	if err := aof.Initialize(4); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := aof.Append(constants.Del, []string{"k"}, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := aof.fsync(); err != nil {
		t.Fatalf("fsync: %v", err)
	}

	cmds := readAllCommands(t, path)
	if len(cmds) != 2 {
		t.Fatalf("commands = %d, want 2", len(cmds))
	}
	if string(cmds[0].Name) != constants.Header {
		t.Errorf("first command = %q, want HEADER", cmds[0].Name)
	}
	if string(cmds[1].Name) != constants.SequenceID {
		t.Errorf("second command = %q, want SEQUENCEID", cmds[1].Name)
	}
}

func TestAOFSyncAlwaysDurableImmediately(t *testing.T) {
	aof, path := newTestAOF(t, constants.SyncAlways)
	if err := aof.Initialize(0); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	if err := aof.Append(constants.Set, []string{"k", "v"}, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// No manual fsync — SyncAlways must have already flushed+synced.
	cmds := readAllCommands(t, path)
	if len(cmds) != 2 {
		t.Fatalf("commands visible immediately = %d, want 2 under SyncAlways", len(cmds))
	}
}

func TestAOFSyncEverySecBuffersUntilFlushed(t *testing.T) {
	aof, path := newTestAOF(t, constants.SyncEverySec)
	if err := aof.Initialize(0); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	if err := aof.Append(constants.Set, []string{"k", "v"}, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Under SyncEverySec, Append does not flush: the entry should still be
	// sitting in the bufio.Writer buffer, invisible to an independent reader.
	cmds := readAllCommands(t, path)
	if len(cmds) != 1 {
		t.Fatalf("commands visible before flush = %d, want 1 (header only)", len(cmds))
	}

	if err := aof.fsync(); err != nil {
		t.Fatalf("fsync: %v", err)
	}

	cmds = readAllCommands(t, path)
	if len(cmds) != 2 {
		t.Fatalf("commands visible after fsync = %d, want 2", len(cmds))
	}
}

// ============================================================
// 3. Replay (single AOF)
// ============================================================

// fakeCommand mirrors what sendCommandToEventLoop dispatches, recorded by a
// stub event loop for assertions.
type fakeCommand struct {
	Name constants.CmdName
	Args []string
}

// newRecordingEventLoop starts a goroutine that drains cmdChan, records every
// command it receives, and replies with a successful (non-error) Response.
// The goroutine stops when the returned stop func is called.
func newRecordingEventLoop(t *testing.T) (cmdChan chan Command, records func() []fakeCommand, stop func()) {
	t.Helper()
	ch := make(chan Command)
	var recorded []fakeCommand
	done := make(chan struct{})

	go func() {
		for {
			select {
			case cmd := <-ch:
				recorded = append(recorded, fakeCommand{Name: cmd.Name, Args: cmd.Args})
				select {
				case cmd.Resp <- Response{}:
				default:
				}
			case <-done:
				return
			}
		}
	}()

	return ch, func() []fakeCommand { return append([]fakeCommand(nil), recorded...) }, func() { close(done) }
}

func TestAOFReplayEmpty(t *testing.T) {
	aof, _ := newTestAOF(t, constants.SyncEverySec)
	if err := aof.Initialize(0); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	cmdChan, records, stop := newRecordingEventLoop(t)
	defer stop()

	replayed, lastSeq, err := aof.Replay(cmdChan, 0)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if replayed {
		t.Errorf("replayed = true, want false for empty journal")
	}
	if lastSeq != 0 {
		t.Errorf("lastSeq = %d, want 0", lastSeq)
	}
	if len(records()) != 0 {
		t.Errorf("commands dispatched = %d, want 0", len(records()))
	}
}

func TestAOFReplaySingleCommand(t *testing.T) {
	aof, _ := newTestAOF(t, constants.SyncAlways)
	if err := aof.Initialize(0); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := aof.Append(constants.Set, []string{"k", "v"}, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}

	cmdChan, records, stop := newRecordingEventLoop(t)
	defer stop()

	replayed, lastSeq, err := aof.Replay(cmdChan, 0)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !replayed {
		t.Errorf("replayed = false, want true")
	}
	if lastSeq != 1 {
		t.Errorf("lastSeq = %d, want 1", lastSeq)
	}
	got := records()
	if len(got) != 1 || got[0].Name != constants.Set || got[0].Args[0] != "k" || got[0].Args[1] != "v" {
		t.Errorf("dispatched commands = %+v, want [SET k v]", got)
	}
}

func TestAOFReplayMultipleCommandsPreservesOrder(t *testing.T) {
	aof, _ := newTestAOF(t, constants.SyncAlways)
	if err := aof.Initialize(0); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := aof.Append(constants.Set, []string{"a", "1"}, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := aof.Append(constants.Set, []string{"b", "2"}, 2); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := aof.Append(constants.Del, []string{"a"}, 3); err != nil {
		t.Fatalf("Append: %v", err)
	}

	cmdChan, records, stop := newRecordingEventLoop(t)
	defer stop()

	replayed, lastSeq, err := aof.Replay(cmdChan, 0)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !replayed {
		t.Fatalf("replayed = false, want true")
	}
	if lastSeq != 3 {
		t.Errorf("lastSeq = %d, want 3", lastSeq)
	}

	got := records()
	wantNames := []constants.CmdName{constants.Set, constants.Set, constants.Del}
	if len(got) != len(wantNames) {
		t.Fatalf("dispatched %d commands, want %d: %+v", len(got), len(wantNames), got)
	}
	for i, want := range wantNames {
		if got[i].Name != want {
			t.Errorf("command #%d = %q, want %q (ordering not preserved)", i, got[i].Name, want)
		}
	}
}

func TestAOFReplaySkipsHeader(t *testing.T) {
	aof, _ := newTestAOF(t, constants.SyncAlways)
	if err := aof.Initialize(9); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := aof.Append(constants.Set, []string{"k", "v"}, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}

	cmdChan, records, stop := newRecordingEventLoop(t)
	defer stop()

	if _, _, err := aof.Replay(cmdChan, 0); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	for _, cmd := range records() {
		if string(cmd.Name) == constants.Header {
			t.Errorf("HEADER command was dispatched to the event loop, should be skipped")
		}
	}
}

func TestAOFReplaySkipsCommandsAtOrBelowWatermark(t *testing.T) {
	aof, _ := newTestAOF(t, constants.SyncAlways)
	if err := aof.Initialize(0); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	for _, seq := range []uint64{1, 2, 3, 4} {
		if err := aof.Append(constants.Incr, []string{"n"}, seq); err != nil {
			t.Fatalf("Append seq=%d: %v", seq, err)
		}
	}

	cmdChan, records, stop := newRecordingEventLoop(t)
	defer stop()

	// Watermark = 2: only sequenceIDs 3 and 4 should replay.
	replayed, lastSeq, err := aof.Replay(cmdChan, 2)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if !replayed {
		t.Fatalf("replayed = false, want true")
	}
	if lastSeq != 4 {
		t.Errorf("lastSeq = %d, want 4", lastSeq)
	}
	if got := len(records()); got != 2 {
		t.Errorf("dispatched %d commands, want 2 (only seq 3 and 4)", got)
	}
}

func TestAOFReplayAllAtOrBelowWatermarkYieldsNoReplay(t *testing.T) {
	aof, _ := newTestAOF(t, constants.SyncAlways)
	if err := aof.Initialize(0); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := aof.Append(constants.Set, []string{"k", "v"}, 1); err != nil {
		t.Fatalf("Append: %v", err)
	}

	cmdChan, records, stop := newRecordingEventLoop(t)
	defer stop()

	replayed, lastSeq, err := aof.Replay(cmdChan, 5) // watermark ahead of the only entry
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if replayed {
		t.Errorf("replayed = true, want false when watermark already covers all entries")
	}
	if lastSeq != 5 {
		t.Errorf("lastSeq = %d, want unchanged watermark 5", lastSeq)
	}
	if len(records()) != 0 {
		t.Errorf("dispatched commands, want none")
	}
}

func TestAOFReplayHandlesMalformedFile(t *testing.T) {
	aof, path := newTestAOF(t, constants.SyncEverySec)
	if err := aof.Initialize(0); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if err := aof.fsync(); err != nil {
		t.Fatalf("fsync: %v", err)
	}

	// Corrupt the file by appending an invalid RESP array header directly.
	if _, err := aof.file.WriteString("*abc\r\n"); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	_ = path

	cmdChan, _, stop := newRecordingEventLoop(t)
	defer stop()

	if _, _, err := aof.Replay(cmdChan, 0); err == nil {
		t.Errorf("Replay on malformed file should return an error")
	}
}

func TestAOFReplayReturnsLatestSequenceIDEvenWithGaps(t *testing.T) {
	aof, _ := newTestAOF(t, constants.SyncAlways)
	if err := aof.Initialize(0); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// SequenceIDs need not be contiguous from the AOF's point of view.
	for _, seq := range []uint64{1, 5, 9} {
		if err := aof.Append(constants.Incr, []string{"n"}, seq); err != nil {
			t.Fatalf("Append seq=%d: %v", seq, err)
		}
	}

	cmdChan, _, stop := newRecordingEventLoop(t)
	defer stop()

	_, lastSeq, err := aof.Replay(cmdChan, 0)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if lastSeq != 9 {
		t.Errorf("lastSeq = %d, want 9", lastSeq)
	}
}
