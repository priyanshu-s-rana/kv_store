package parser

import (
	"strings"
	"testing"
)

// FuzzReadCommand feeds arbitrary bytes to the RESP/inline command parser.
// Invariant under test: ReadCommand must never panic, loop forever, or
// attempt an unbounded allocation, regardless of what a connected client
// sends. A finite input can never cause an infinite loop here (readLine and
// readBulkString both terminate on EOF), so the two properties this test
// actually exercises are "never panics" and "never allocates before
// validating the declared length."
func FuzzReadCommand(f *testing.F) {
	seeds := []string{
		"PING\r\n",
		"GET foo\r\n",
		"get   foo  \r\n",
		"*3\r\n$3\r\nSET\r\n$1\r\na\r\n$1\r\n1\r\n",
		"*1\r\n$4\r\nPING\r\n",
		"",
		"\r\n",
		"\n\n\n",
		"   \r\n",
		"*0\r\n",
		"*-1\r\n",
		"*abc\r\n",
		"*1\r\nabc\r\n",       // missing '$' marker on the bulk string
		"*1\r\n$abc\r\n",      // non-numeric bulk length
		"*1\r\n$-1\r\n",       // null bulk string — valid, distinct from "$0"
		"*1\r\n$0\r\n\r\n",    // empty-but-present bulk string — valid
		"*2\r\n$3\r\nfoo\r\n", // declares 2 elements, stream has only 1
		"*1\r\n$5\r\nhi\r\n",  // declared length longer than actual payload (truncated)
		"SET a b\textra\r\n",
		"*1\r\n$3\r\nfoo", // missing trailing CRLF after payload

		// Known crash bugs in readBulkString / ReadCommand — see
		// parser/parser.go: neither arrLength (line 60) nor a bulk string's
		// declared length (line 133) is checked against any upper bound
		// before the corresponding make() call, and a negative bulk length
		// other than the special-cased "-1" falls straight through to
		// make([]byte, length+2) with a negative size. Both are directly
		// reachable by any connected client. The two "2^60" seeds below are
		// deliberately chosen to exceed the Go runtime's internal maxAlloc
		// threshold, so the resulting panic ("makeslice: len out of range")
		// is raised by the length check itself, before any real allocation
		// is attempted — safe to fuzz without risking an actual OOM.
		"*1\r\n$-5\r\n",                  // negative bulk length other than -1: make([]byte, -3) panics
		"*1152921504606846976\r\n",       // 2^60 array length: must be rejected, not allocated
		"*1\r\n$1152921504606846976\r\n", // 2^60 bulk length: must be rejected, not allocated
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("ReadCommand panicked on input %q: %v", input, r)
			}
		}()

		p := New(strings.NewReader(input))
		// A single input may encode several pipelined commands; drain them
		// all like a real connection would. This can never loop forever:
		// every path through ReadCommand either returns an error/io.EOF
		// (stopping the loop) or consumes a strictly positive number of
		// bytes from the finite input.
		for {
			if _, err := p.ReadCommand(); err != nil {
				return
			}
		}
	})
}
