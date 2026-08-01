package parser

import (
	"fmt"
	"strings"
	"testing"
)

// repeatReader cycles over a fixed byte slice indefinitely, standing in for
// a long-lived client connection that keeps sending the same pipelined
// command. It lets ReadCommand be benchmarked b.N times without holding
// b.N encoded copies of the command in memory.
type repeatReader struct {
	data []byte
	pos  int
}

func (r *repeatReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		r.pos = 0
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// benchPayload is a representative value size for SET/MSET benchmarks,
// matching the default payload used by scripts/redis_benchmark.sh.
const benchPayload = 64

func benchmarkReadCommand(b *testing.B, encoded []byte) {
	b.Helper()
	p := New(&repeatReader{data: encoded})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.ReadCommand(); err != nil {
			b.Fatalf("ReadCommand: %v", err)
		}
	}
}

// BenchmarkRESPParseSET measures parsing a single RESP-encoded SET command.
func BenchmarkRESPParseSET(b *testing.B) {
	encoded := Array("SET", "bench:key:001", strings.Repeat("v", benchPayload))
	benchmarkReadCommand(b, encoded)
}

// BenchmarkRESPParseGET measures parsing a single RESP-encoded GET command.
func BenchmarkRESPParseGET(b *testing.B) {
	encoded := Array("GET", "bench:key:001")
	benchmarkReadCommand(b, encoded)
}

// BenchmarkRESPParseMSET measures parsing a RESP-encoded MSET command with
// 10 key/value pairs, matching the batch size scripts/redis_benchmark.sh
// uses by default for its batch sweep.
func BenchmarkRESPParseMSET(b *testing.B) {
	const batch = 10
	value := strings.Repeat("v", benchPayload)
	parts := make([]string, 0, 1+batch*2)
	parts = append(parts, "MSET")
	for i := range batch {
		parts = append(parts, fmt.Sprintf("bench:key:%03d", i), value)
	}
	encoded := Array(parts...)
	benchmarkReadCommand(b, encoded)
}
