package persistence

import (
	"encoding/gob"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

const benchPayloadSize = 64

// BenchmarkAOFAppend measures AOF.Append — RESP-encoding one command and
// writing it through the buffered journal writer. Uses SyncEverySec (the
// production default set in cmd/kv-server/main.go), so, like production,
// Append itself never calls fsync; periodic fsync is a separate concern
// handled by the journal's background flusher, not part of the hot path
// this benchmark isolates.
func BenchmarkAOFAppend(b *testing.B) {
	path := filepath.Join(b.TempDir(), "journal.aof")
	aof, err := NewAOF(&AOFConfig{FilePath: path, SyncPolicy: constants.SyncEverySec}, noopPersistenceMetrics{})
	if err != nil {
		b.Fatalf("NewAOF: %v", err)
	}
	defer aof.file.Close()

	args := []string{"bench:key", strings.Repeat("v", benchPayloadSize)}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := aof.Append(constants.Set, args, uint64(i)); err != nil {
			b.Fatalf("Append: %v", err)
		}
	}
}

// benchSnapshotData builds n SnapshotEntry records the same shape
// Store.capture() produces (see store/persistence.go), for benchmarks that
// need snapshot-sized input without depending on the store package.
func benchSnapshotData(n int) map[string]SnapshotEntry {
	value := []byte(strings.Repeat("v", benchPayloadSize))
	data := make(map[string]SnapshotEntry, n)
	for i := range n {
		data[fmt.Sprintf("bench:key:%06d", i)] = SnapshotEntry{Value: value}
	}
	return data
}

// BenchmarkSnapshotEncode measures just the gob-encoding step Snapshot.
// SaveToDisk performs (persistence/snapshot.go), against io.Discard rather
// than a real file — isolating serialization cost from the disk I/O
// (open/fsync/rename) that surrounds it in SaveToDisk. Complements
// store.BenchmarkSnapshotCapture, which measures the step immediately
// before this one.
func BenchmarkSnapshotEncode(b *testing.B) {
	sf := &snapshotFile{
		Generation:     1,
		LastSequenceID: 1000,
		Data:           benchSnapshotData(10000),
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := gob.NewEncoder(io.Discard).Encode(sf); err != nil {
			b.Fatalf("Encode: %v", err)
		}
	}
}
