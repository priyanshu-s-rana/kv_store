package persistence

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
	"testing"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/parser"
)

// FuzzSnapshotLoad feeds arbitrary bytes as the on-disk snapshot file.
// Invariant: Load() must react to any malformed content with an error, never
// a panic — gob decoding of an attacker- or corruption-controlled byte
// stream is exactly the kind of input where "return an error" and "panic"
// must not be confused.
func FuzzSnapshotLoad(f *testing.F) {
	var validSnapshot bytes.Buffer
	seedData := map[string]SnapshotEntry{"k": {Value: []byte("v")}}
	if err := gob.NewEncoder(&validSnapshot).Encode(&snapshotFile{Generation: 1, LastSequenceID: 2, Data: seedData}); err != nil {
		f.Fatalf("seed encode: %v", err)
	}

	f.Add(validSnapshot.Bytes())
	f.Add([]byte{})
	f.Add([]byte("not a gob stream at all"))
	f.Add(validSnapshot.Bytes()[:validSnapshot.Len()/2])                        // truncated mid-record
	f.Add(append(append([]byte{}, validSnapshot.Bytes()...), 0xFF, 0xFF, 0xFF)) // trailing garbage

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, "dump.gob")
		if err := os.WriteFile(path, data, constants.FilePerm); err != nil {
			t.Fatalf("write seed file: %v", err)
		}

		snap := NewSnapshot(&SnapshotConfig{FilePath: path}, noopPersistenceMetrics{})

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Snapshot.Load panicked on %d bytes of input: %v", len(data), r)
			}
		}()

		_, _ = snap.Load() // an error is a fine, expected outcome for malformed input; a panic is not.
	})
}

// FuzzAOFReplay feeds arbitrary bytes as an AOF journal file's on-disk
// content. Invariant: Replay must never panic on any input, and must never
// block forever — a truncated final entry (the normal crash-recovery case:
// the process died mid-write) must be handled without hanging, and a
// corrupted middle entry must surface as an error rather than silently
// skip into the wrong state.
func FuzzAOFReplay(f *testing.F) {
	header := parser.Array(constants.Header, constants.Generation, "0")
	goodEntry := parser.Array(constants.SequenceID, "1", "SET", "a", "1")

	f.Add([]byte{})                                                             // empty file
	f.Add(header)                                                               // header only, no commands — valid, replays nothing
	f.Add(append(append([]byte{}, header...), goodEntry...))                    // header + one well-formed command
	f.Add(append(append([]byte{}, header...), goodEntry[:len(goodEntry)/2]...)) // truncated final entry — the normal crash case
	f.Add([]byte("garbage, not RESP at all\n"))
	f.Add(append(append([]byte{}, header...), []byte("*1\r\n$-5\r\n")...)) // valid header, then a malformed entry (negative bulk length)
	f.Add(goodEntry)                                                       // command record with no header at all

	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "journal.aof")
		if err := os.WriteFile(path, data, constants.FilePerm); err != nil {
			t.Fatalf("write seed file: %v", err)
		}

		aof, err := NewAOF(&AOFConfig{FilePath: path, SyncPolicy: constants.SyncAlways}, noopPersistenceMetrics{})
		if err != nil {
			t.Fatalf("NewAOF: %v", err)
		}
		defer aof.file.Close()

		// Replay does a synchronous round trip through cmdChan for every
		// well-formed command it finds (via sendCommandToEventLoop). With no
		// consumer, any input containing at least one well-formed command
		// would block Replay forever, turning a single fuzz input into a
		// hung test process. Draining unconditionally makes that
		// structurally impossible regardless of what the fuzzer generates.
		cmdChan := make(chan Command)
		go func() {
			for cmd := range cmdChan {
				cmd.Resp <- Response{}
			}
		}()
		defer close(cmdChan)

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("AOF.Replay panicked on %d bytes of input: %v", len(data), r)
			}
		}()

		_, _, _ = aof.Replay(cmdChan, 0)
	})
}
