package store

import (
	"bytes"
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

// send dispatches a command through the public CmdChan and returns the response.
// Times out after 1s to avoid hanging if the event loop misbehaves.
func send(t *testing.T, s *Store, name constants.CmdName, args ...string) Response {
	t.Helper()
	cmd := Command{
		Name: name,
		Args: args,
		Resp: make(chan Response, 1),
	}
	s.CmdChan() <- cmd

	select {
	case resp := <-cmd.Resp:
		return resp
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for response to %s", name)
		return Response{}
	}
}

// ---- entry helpers ----

func TestEntryIsExpired(t *testing.T) {
	cases := []struct {
		name   string
		expiry time.Time
		want   bool
	}{
		{"zero expiry (no TTL)", time.Time{}, false},
		{"future expiry", time.Now().Add(10 * time.Second), false},
		{"past expiry", time.Now().Add(-1 * time.Second), true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &entry{expiry: c.expiry}
			if got := e.isExpired(); got != c.want {
				t.Errorf("isExpired() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestEntryHasExpiry(t *testing.T) {
	cases := []struct {
		name   string
		expiry time.Time
		want   bool
	}{
		{"zero expiry", time.Time{}, false},
		{"future expiry", time.Now().Add(10 * time.Second), true},
		{"past expiry (still counts as having one)", time.Now().Add(-1 * time.Second), true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &entry{expiry: c.expiry}
			if got := e.hasExpiry(); got != c.want {
				t.Errorf("hasExpiry() = %v, want %v", got, c.want)
			}
		})
	}
}

// ---- New ----

func TestNewInitializesFields(t *testing.T) {
	s := New()

	if s == nil {
		t.Fatalf("New() returned nil")
	}
	if s.data == nil {
		t.Errorf("data map not initialized")
	}
	if s.cmdChan == nil {
		t.Errorf("cmdChan not initialized")
	}
	if s.ttls == nil {
		t.Errorf("ttls heap not initialized")
	}
	if s.pubsub == nil {
		t.Errorf("pubsub map not initialized")
	}
}

func TestNewStartsEventLoop(t *testing.T) {
	s := New()

	// If the event loop wasn't running, this send would block past the timeout.
	resp := send(t, s, constants.Ping)
	want := respSimple(constants.PONG)
	if !bytes.Equal(resp.Value, []byte(want)) {
		t.Errorf("PING response = %q, want %q", resp.Value, want)
	}
}

// ---- CmdChan ----

func TestCmdChanReturnsSendOnly(t *testing.T) {
	s := New()
	ch := s.CmdChan()
	if ch == nil {
		t.Errorf("CmdChan() returned nil")
	}
	// Compile-time guarantee: ch is chan<- Command (send-only).
	// Reading would be a compile error. Just verify send works.
	ch <- Command{
		Name: constants.Ping,
		Resp: make(chan Response, 1),
	}
}

// ---- eventLoop dispatch ----

func TestEventLoopDispatchesPing(t *testing.T) {
	s := New()
	resp := send(t, s, constants.Ping)
	want := respSimple(constants.PONG)
	if !bytes.Equal(resp.Value, []byte(want)) {
		t.Errorf("got %q, want %q", resp.Value, want)
	}
}

func TestEventLoopDispatchesSetGet(t *testing.T) {
	s := New()

	setResp := send(t, s, constants.Set, "k", "v")
	if !bytes.Equal(setResp.Value, []byte(respSimple(constants.OK))) {
		t.Fatalf("SET = %q, want %q", setResp.Value, respSimple(constants.OK))
	}

	getResp := send(t, s, constants.Get, "k")
	want := respBulk("v")
	if !bytes.Equal(getResp.Value, []byte(want)) {
		t.Errorf("GET = %q, want %q", getResp.Value, want)
	}
}

func TestEventLoopDispatchesDel(t *testing.T) {
	s := New()
	send(t, s, constants.Set, "k", "v")

	delResp := send(t, s, constants.Del, "k")
	if !bytes.Equal(delResp.Value, []byte(respInt(constants.ONE))) {
		t.Errorf("DEL = %q, want %q", delResp.Value, respInt(constants.ONE))
	}

	getResp := send(t, s, constants.Get, "k")
	if !bytes.Equal(getResp.Value, []byte(respNil)) {
		t.Errorf("GET after DEL = %q, want %q", getResp.Value, respNil)
	}
}

func TestEventLoopDispatchesExpireAndTTL(t *testing.T) {
	s := New()
	send(t, s, constants.Set, "k", "v")
	send(t, s, constants.Expire, "k", "30")

	ttlResp := send(t, s, constants.TTL, "k")
	// Should be roughly :30
	got := string(ttlResp.Value)
	if got != ":29\r\n" && got != ":30\r\n" {
		t.Errorf("TTL = %q, want ~:30", got)
	}
}

func TestEventLoopUnknownCommand(t *testing.T) {
	s := New()
	resp := send(t, s, "FAKECMD")

	if !bytes.Contains(resp.Value, []byte("FAKECMD")) {
		t.Errorf("unknown command response = %q, should mention FAKECMD", resp.Value)
	}
	if !bytes.HasPrefix(resp.Value, []byte("-ERR")) {
		t.Errorf("unknown command response = %q, should start with -ERR", resp.Value)
	}
}

// ---- TTL eviction integration ----
// Slow test — uses real ticker. Run with -short to skip.

func TestTTLEvictionRemovesExpiredKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping eviction integration test in -short mode")
	}
	s := New()
	send(t, s, constants.Set, "ephemeral", "v", constants.EX, "1")

	// Key alive immediately
	getResp := send(t, s, constants.Get, "ephemeral")
	if bytes.Equal(getResp.Value, []byte(constants.NIL)) {
		t.Fatalf("key should be present right after SET, got NIL")
	}

	// Wait for ticker (1s) + key expiry (1s) + safety margin
	time.Sleep(2500 * time.Millisecond)

	getResp = send(t, s, constants.Get, "ephemeral")
	if !bytes.Equal(getResp.Value, []byte(constants.NIL)) {
		t.Errorf("key should be evicted, got %q", getResp.Value)
	}
}

// ---- Non-blocking response send ----

func TestEventLoopDoesNotBlockOnUnreadResponse(t *testing.T) {
	s := New()

	// Send a command with a buffered chan but never read it.
	// The event loop's `select { case Resp <- resp: default: }` should drop silently.
	cmd := Command{
		Name: constants.Ping,
		Resp: make(chan Response), // unbuffered, no receiver
	}
	s.CmdChan() <- cmd

	// If event loop blocked, this follow-up send would time out.
	resp := send(t, s, constants.Ping)
	if !bytes.Equal(resp.Value, []byte(respSimple(constants.PONG))) {
		t.Errorf("event loop appears stuck — follow-up PING failed: %q", resp.Value)
	}
}
