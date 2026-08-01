package store

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/priyanshu-s-rana/kv_store/utils"
)

// Representative sizes shared across the store benchmarks, matching the
// defaults scripts/redis_benchmark.sh uses for its payload/keyspace/batch
// sweeps so Go-level and end-to-end numbers stay comparable.
const (
	benchPayloadSize = 64
	benchKeyspace    = 10000
	benchBatchSize   = 10
)

// newBenchStore builds a Store wired to no-op persistence/metrics without
// starting its event loop goroutine. Benchmarks call the unexported command
// handlers (set/get/mset/...) directly, bypassing cmdChan entirely, so what
// gets measured is the store's own map/LRU/TTL-heap work rather than
// channel or event-loop scheduling overhead.
func newBenchStore() *Store {
	cmdChan := make(chan Command)
	subscribeChan := make(chan SubscribeReq)
	unsubscribeChan := make(chan UnsubscribeReq)
	return New(0, cmdChan, subscribeChan, unsubscribeChan, fakePersistence{}, newSpyStoreMetrics())
}

func benchKeys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("bench:key:%06d", i)
	}
	return keys
}

// BenchmarkStoreSet measures repeated SET over a fixed keyspace (steady
// state: keys get overwritten rather than growing the map unboundedly,
// matching how redis-benchmark's -r keyspace flag drives SET).
func BenchmarkStoreSet(b *testing.B) {
	s := newBenchStore()
	keys := benchKeys(benchKeyspace)
	value := strings.Repeat("v", benchPayloadSize)
	args := make([]string, 2)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		args[0] = keys[i%benchKeyspace]
		args[1] = value
		s.set(args)
	}
}

// BenchmarkStoreGet measures repeated GET against a pre-seeded keyspace of
// existing, non-expired keys.
func BenchmarkStoreGet(b *testing.B) {
	s := newBenchStore()
	keys := benchKeys(benchKeyspace)
	value := strings.Repeat("v", benchPayloadSize)
	for _, k := range keys {
		s.set([]string{k, value})
	}
	args := make([]string, 1)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		args[0] = keys[i%benchKeyspace]
		s.get(args)
	}
}

// BenchmarkStoreMSet measures MSET with a fixed batch size, cycling through
// the keyspace so the map reaches and holds a steady-state size instead of
// growing for the whole run.
func BenchmarkStoreMSet(b *testing.B) {
	s := newBenchStore()
	keys := benchKeys(benchKeyspace)
	value := strings.Repeat("v", benchPayloadSize)
	args := make([]string, benchBatchSize*2)
	for j := 0; j < benchBatchSize; j++ {
		args[j*2+1] = value
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		base := (i * benchBatchSize) % benchKeyspace
		for j := 0; j < benchBatchSize; j++ {
			args[j*2] = keys[(base+j)%benchKeyspace]
		}
		s.mset(args)
	}
}

// BenchmarkStoreMGet measures MGET with a fixed batch size against a
// pre-seeded keyspace.
func BenchmarkStoreMGet(b *testing.B) {
	s := newBenchStore()
	keys := benchKeys(benchKeyspace)
	value := strings.Repeat("v", benchPayloadSize)
	for _, k := range keys {
		s.set([]string{k, value})
	}
	args := make([]string, benchBatchSize)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		base := (i * benchBatchSize) % benchKeyspace
		for j := 0; j < benchBatchSize; j++ {
			args[j] = keys[(base+j)%benchKeyspace]
		}
		s.mget(args)
	}
}

// BenchmarkStoreTTL measures PEXPIREAT, one call per key against b.N
// distinct pre-seeded keys. Unlike Set/Get/MSet/MGet this deliberately does
// NOT cycle over a fixed keyspace: pexpireAt unconditionally pushes onto
// the TTL heap on every call (store/commands.go), so repeatedly targeting
// the same key would pile up stale heap entries and make the heap grow
// with iteration count instead of representing steady-state cost. One
// PEXPIREAT per key mirrors how TTLs are actually set in practice and
// still exercises real O(log n) heap growth over the run. The expiry is
// always far in the future so no key actually expires mid-run.
func BenchmarkStoreTTL(b *testing.B) {
	s := newBenchStore()
	value := strings.Repeat("v", benchPayloadSize)
	keys := make([]string, b.N)
	for i := range keys {
		keys[i] = fmt.Sprintf("bench:ttl:%06d", i)
		s.set([]string{keys[i], value})
	}
	expiry := strconv.FormatInt(utils.AbsoluteTimeNow()+int64(1)<<40, 10) // far future, never hit during the run
	args := make([]string, 2)
	args[1] = expiry

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		args[0] = keys[i]
		s.pexpireAt(args)
	}
}

// BenchmarkSnapshotCapture measures Store.capture() — the deep-copy step
// that turns live entries into a snapshot map, run just before Persistence
// encodes and writes it (see store/persistence.go). It lives here rather
// than in persistence/benchmark_test.go because capture() is unexported
// store-internal logic; Persistence only ever receives its already-built
// result through the Persistence interface (Checkpoint/Rebaseline), so
// benchmarking capture() from outside this package isn't possible without
// reimplementing it. BenchmarkSnapshotEncode in persistence/benchmark_test.go
// covers the complementary gob-encoding step.
func BenchmarkSnapshotCapture(b *testing.B) {
	s := newBenchStore()
	keys := benchKeys(benchKeyspace)
	value := strings.Repeat("v", benchPayloadSize)
	for _, k := range keys {
		s.set([]string{k, value})
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.capture(); err != nil {
			b.Fatalf("capture: %v", err)
		}
	}
}
