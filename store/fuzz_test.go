package store

import (
	"strings"
	"testing"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

// FuzzCommandDispatch feeds arbitrary command names and argument combinations
// straight to the registry handlers (bypassing the channel-based event loop,
// which requires Start() and its goroutines — see below for why this test
// deliberately avoids that).
//
// Every handler in store/commands.go runs directly inside the single-
// threaded event loop with no recover() anywhere in the call chain
// (store.go eventLoop -> cmdMeta.handler). A panic in any handler is not a
// contained failure: it takes down the entire event loop goroutine, and
// with it the whole server process, for every connected client. That
// makes "no panic, ever, on any input" a correctness property worth
// fuzzing directly, independent of RESP-level parsing (already fuzzed in
// parser.FuzzReadCommand).
//
// Note: this intentionally constructs the Store via New() only, never
// Start(). Start() spawns a persistent eventLoop goroutine and a 1-second
// ttlEviction ticker that never stop (Store has no Close/Stop method) —
// fine for the handful of seed-corpus runs under plain `go test`, but fatal
// for a long local `go test -fuzz=` session, which would leak one goroutine
// and ticker per iteration. Calling registry handlers directly exercises
// exactly the same code paths without that leak.
func FuzzCommandDispatch(f *testing.F) {
	type seed struct {
		name string
		args string // args joined with \x00, since f.Add can't take []string
	}
	seeds := []seed{
		{"GET", "key"},
		{"GET", ""},
		{"SET", "key\x00value"},
		{"SET", ""},
		{"SET", "key\x00value\x00EX\x00notanumber"},
		{"SET", "key\x00value\x00EX"},
		{"DEL", ""},
		{"DEL", "key"},
		{"EXPIRE", "key\x00notanumber"},
		{"EXPIRE", "key\x00-1"},
		{"TTL", ""},
		{"INCR", "key"},
		{"DECR", "key"},
		{"MSET", "a\x00b\x00c"},
		{"MSET", ""},
		{"MGET", ""},
		{"MGET", "a\x00b\x00c"},
		// KEYS with an empty pattern: store/helper.go's keyMatcher indexes
		// pattern[0] and pattern[len(pattern)-1] unconditionally. An empty
		// bulk string is a perfectly valid RESP argument ("*2\r\n$4\r\nKEYS\r\n
		// $0\r\n\r\n" on the wire), so this seed is not a contrived input —
		// it is directly reachable by any connected client and currently
		// panics with "index out of range [0] with length 0", which — with
		// no recover() in the event-loop call chain — crashes the whole
		// server process for every client, not just the one that sent it.
		{"KEYS", ""},
		{"KEYS", "*"},
		{"KEYS", "*mid*"},
		{"FLUSHALL", ""},
		{"MEMORYSTATS", ""},
		{"PUBLISH", "topic\x00message"},
		{"PUBLISH", ""},
		{"PING", ""},
		{"", ""},
		{"UNKNOWN_CMD_XYZ", "a\x00b"},
	}
	for _, s := range seeds {
		f.Add(s.name, s.args)
	}

	f.Fuzz(func(t *testing.T, name string, argsBlob string) {
		// strings.Split("", "\x00") deliberately yields [""] (one empty-
		// string argument), not zero args — that distinction is exactly
		// what the KEYS seed above needs to reach keyMatcher("").
		args := strings.Split(argsBlob, "\x00")

		// Mirror handleCommand's real dispatch order exactly: normaliseCommand
		// runs (and can rewrite both cmd.Name and cmd.Args — e.g. EXPIRE ->
		// PExpireAt with an absolute-ms deadline) before the registry lookup.
		// Skipping this, as an earlier version of this test did, would look up
		// "EXPIRE" directly — which isn't in the registry at all post-rewrite —
		// silently no-op every EXPIRE/SET-EX seed instead of exercising them.
		cmd := &Command{Name: constants.CmdName(strings.ToUpper(name)), Args: args}
		normaliseCommand(cmd)

		cmdMeta, ok := registry[cmd.Name]
		if !ok {
			return // not a registered command; store._default handles this path, exercised in server_test.go.
		}

		st := New(0, make(chan Command), make(chan SubscribeReq), make(chan UnsubscribeReq), fakePersistence{}, newSpyStoreMetrics())

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("handler for %q panicked on args %q (post-normalize: name=%q args=%q): %v", name, args, cmd.Name, cmd.Args, r)
			}
		}()

		_ = cmdMeta.handler(st, cmd.Args)
	})
}
