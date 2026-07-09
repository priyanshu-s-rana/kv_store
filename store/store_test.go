package store

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

// fakePersistence is a no-op Persistence used to run a real event loop in
// tests without touching disk.
type fakePersistence struct{}

func (fakePersistence) Append(constants.CmdName, []string) error  { return nil }
func (fakePersistence) Checkpoint(map[string]SnapshotEntry) error { return nil }
func (fakePersistence) CheckpointSuccess() error                  { return nil }
func (fakePersistence) RebaseLine(map[string]SnapshotEntry) error { return nil }

// newStore builds a Store with its event loop and TTL eviction goroutines
// running against a fresh command channel, wired to a no-op Persistence.
func newStore(t *testing.T, memorySize int64) (*Store, chan Command) {
	t.Helper()
	cmdChan := make(chan Command)
	s := New(memorySize, cmdChan, fakePersistence{})
	s.Start()
	return s, cmdChan
}

// send dispatches a command through cmdChan and returns the response.
// Times out after 1s to avoid hanging if the event loop misbehaves.
func send(t *testing.T, cmdChan chan Command, name constants.CmdName, args ...string) Response {
	t.Helper()
	cmd := Command{
		Name: name,
		Args: args,
		Resp: make(chan Response, 1),
	}
	cmdChan <- cmd

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
	s, _ := newStore(t, 0)

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
	if s.lru == nil {
		t.Errorf("lru not initialized")
	}
	if s.memoryProfile == nil {
		t.Errorf("memoryProfile not initialized")
	}
}

func TestNewStartsEventLoop(t *testing.T) {
	_, cmdChan := newStore(t, 0)

	// If the event loop wasn't running, this send would block past the timeout.
	resp := send(t, cmdChan, constants.Ping)
	want := respSimple(constants.PONG)
	if !bytes.Equal(resp.Value, []byte(want)) {
		t.Errorf("PING response = %q, want %q", resp.Value, want)
	}
}

// ---- eventLoop dispatch ----

func TestEventLoopDispatchesPing(t *testing.T) {
	_, cmdChan := newStore(t, 0)
	resp := send(t, cmdChan, constants.Ping)
	want := respSimple(constants.PONG)
	if !bytes.Equal(resp.Value, []byte(want)) {
		t.Errorf("got %q, want %q", resp.Value, want)
	}
}

func TestEventLoopDispatchesSetGet(t *testing.T) {
	_, cmdChan := newStore(t, 0)

	setResp := send(t, cmdChan, constants.Set, "k", "v")
	if !bytes.Equal(setResp.Value, []byte(respSimple(constants.OK))) {
		t.Fatalf("SET = %q, want %q", setResp.Value, respSimple(constants.OK))
	}

	getResp := send(t, cmdChan, constants.Get, "k")
	want := respBulk("v")
	if !bytes.Equal(getResp.Value, []byte(want)) {
		t.Errorf("GET = %q, want %q", getResp.Value, want)
	}
}

func TestEventLoopDispatchesDel(t *testing.T) {
	_, cmdChan := newStore(t, 0)
	send(t, cmdChan, constants.Set, "k", "v")

	delResp := send(t, cmdChan, constants.Del, "k")
	if !bytes.Equal(delResp.Value, []byte(respInt(constants.ONE))) {
		t.Errorf("DEL = %q, want %q", delResp.Value, respInt(constants.ONE))
	}

	getResp := send(t, cmdChan, constants.Get, "k")
	if !bytes.Equal(getResp.Value, []byte(respNil)) {
		t.Errorf("GET after DEL = %q, want %q", getResp.Value, respNil)
	}
}

func TestEventLoopDispatchesExpireAndTTL(t *testing.T) {
	_, cmdChan := newStore(t, 0)
	send(t, cmdChan, constants.Set, "k", "v")
	send(t, cmdChan, constants.Expire, "k", "30")

	ttlResp := send(t, cmdChan, constants.TTL, "k")
	// Should be roughly :30
	got := string(ttlResp.Value)
	if got != ":29\r\n" && got != ":30\r\n" {
		t.Errorf("TTL = %q, want ~:30", got)
	}
}

func TestEventLoopUnknownCommand(t *testing.T) {
	_, cmdChan := newStore(t, 0)
	resp := send(t, cmdChan, "FAKECMD")

	if !bytes.Contains(resp.Value, []byte("FAKECMD")) {
		t.Errorf("unknown command response = %q, should mention FAKECMD", resp.Value)
	}
	if !bytes.HasPrefix(resp.Value, []byte("-ERR")) {
		t.Errorf("unknown command response = %q, should start with -ERR", resp.Value)
	}
}

func TestEventLoopDispatchesKeys(t *testing.T) {
	_, cmdChan := newStore(t, 0)
	send(t, cmdChan, constants.Set, "foo", "1")
	send(t, cmdChan, constants.Set, "bar", "2")

	resp := send(t, cmdChan, constants.Keys, "*")
	if !bytes.Contains(resp.Value, []byte("foo")) {
		t.Errorf("KEYS * missing 'foo': %q", resp.Value)
	}
	if !bytes.Contains(resp.Value, []byte("bar")) {
		t.Errorf("KEYS * missing 'bar': %q", resp.Value)
	}
}

func TestEventLoopDispatchesFlushAll(t *testing.T) {
	_, cmdChan := newStore(t, 0)
	send(t, cmdChan, constants.Set, "k1", "v1")
	send(t, cmdChan, constants.Set, "k2", "v2")

	resp := send(t, cmdChan, constants.FlushAll)
	if !bytes.Equal(resp.Value, []byte(respSimple(constants.OK))) {
		t.Errorf("FLUSHALL = %q, want OK", resp.Value)
	}

	getResp := send(t, cmdChan, constants.Get, "k1")
	if !bytes.Equal(getResp.Value, []byte(respNil)) {
		t.Errorf("GET after FLUSHALL = %q, want nil", getResp.Value)
	}
}

func TestEventLoopDispatchesMemoryStats(t *testing.T) {
	_, cmdChan := newStore(t, 0)
	send(t, cmdChan, constants.Set, "k", "v")

	resp := send(t, cmdChan, constants.MemoryStats)
	if !bytes.HasPrefix(resp.Value, []byte("$")) {
		t.Errorf("MEMORY STATS response = %q, want RESP bulk string", resp.Value)
	}
	if !bytes.Contains(resp.Value, []byte("currentSize")) {
		t.Errorf("MEMORY STATS missing 'currentSize': %q", resp.Value)
	}
}

func TestNewWithMemoryLimit(t *testing.T) {
	s, _ := newStore(t, 1024)
	if s.memoryProfile.maxBytes != 1024 {
		t.Errorf("maxBytes = %d, want 1024", s.memoryProfile.maxBytes)
	}
}

// ---- TTL countdown ----

// Samples TTL every second so you can see it count down in real time.
// Run with: go test -v -run TestTTLCountdown ./store/
func TestTTLCountdown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping TTL countdown test in -short mode")
	}
	_, cmdChan := newStore(t, 0)
	send(t, cmdChan, constants.Set, "countdown", "v", constants.EX, "10")

	for tick := range 13 {
		resp := send(t, cmdChan, constants.TTL, "countdown")
		t.Logf("t+%2ds → TTL = %s", tick, bytes.TrimRight(resp.Value, "\r\n"))
		time.Sleep(time.Second)
	}
}

// ---- TTL eviction integration ----
// Slow test — uses real ticker. Run with -short to skip.

func TestTTLEvictionRemovesExpiredKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping eviction integration test in -short mode")
	}
	_, cmdChan := newStore(t, 0)
	send(t, cmdChan, constants.Set, "ephemeral", "v", constants.EX, "1")

	// Key alive immediately
	getResp := send(t, cmdChan, constants.Get, "ephemeral")
	if bytes.Equal(getResp.Value, []byte(constants.NIL)) {
		t.Fatalf("key should be present right after SET, got NIL")
	}

	// Wait for ticker (1s) + key expiry (1s) + safety margin
	time.Sleep(2500 * time.Millisecond)

	getResp = send(t, cmdChan, constants.Get, "ephemeral")
	if !bytes.Equal(getResp.Value, []byte(constants.NIL)) {
		t.Errorf("key should be evicted, got %q", getResp.Value)
	}
}

// ---- Non-blocking response send ----

func TestEventLoopDoesNotBlockOnUnreadResponse(t *testing.T) {
	_, cmdChan := newStore(t, 0)

	// Send a command with a buffered chan but never read it.
	// The event loop's `select { case Resp <- resp: default: }` should drop silently.
	cmd := Command{
		Name: constants.Ping,
		Resp: make(chan Response), // unbuffered, no receiver
	}
	cmdChan <- cmd

	// If event loop blocked, this follow-up send would time out.
	resp := send(t, cmdChan, constants.Ping)
	if !bytes.Equal(resp.Value, []byte(respSimple(constants.PONG))) {
		t.Errorf("event loop appears stuck — follow-up PING failed: %q", resp.Value)
	}
}

func TestEventLoopDispatchesMSet(t *testing.T) {
	_, cmdChan := newStore(t, 0)
	resp := send(t, cmdChan, constants.Mset, "k1", "v1", "k2", "v2")
	if !bytes.Equal(resp.Value, []byte(respSimple(constants.OK))) {
		t.Errorf("MSET = %q, want OK", resp.Value)
	}
	if got := send(t, cmdChan, constants.Get, "k1"); !bytes.Contains(got.Value, []byte("v1")) {
		t.Errorf("k1 after MSET = %q, want v1", got.Value)
	}
	if got := send(t, cmdChan, constants.Get, "k2"); !bytes.Contains(got.Value, []byte("v2")) {
		t.Errorf("k2 after MSET = %q, want v2", got.Value)
	}
}

func TestEventLoopDispatchesMGet(t *testing.T) {
	_, cmdChan := newStore(t, 0)
	send(t, cmdChan, constants.Mset, "k1", "v1", "k2", "v2")
	resp := send(t, cmdChan, constants.Mget, "k1", "k2", "missing")
	if !bytes.Contains(resp.Value, []byte("v1")) {
		t.Errorf("MGET missing v1: %q", resp.Value)
	}
	if !bytes.Contains(resp.Value, []byte("v2")) {
		t.Errorf("MGET missing v2: %q", resp.Value)
	}
}

func TestEventLoopDispatchesIncr(t *testing.T) {
	_, cmdChan := newStore(t, 0)
	resp := send(t, cmdChan, constants.Incr, "counter")
	if !bytes.Equal(resp.Value, []byte(":1\r\n")) {
		t.Errorf("INCR on missing key = %q, want :1", resp.Value)
	}
	resp = send(t, cmdChan, constants.Incr, "counter")
	if !bytes.Equal(resp.Value, []byte(":2\r\n")) {
		t.Errorf("INCR second call = %q, want :2", resp.Value)
	}
}

func TestEventLoopDispatchesDecr(t *testing.T) {
	_, cmdChan := newStore(t, 0)
	resp := send(t, cmdChan, constants.Decr, "counter")
	if !bytes.Equal(resp.Value, []byte(":-1\r\n")) {
		t.Errorf("DECR on missing key = %q, want :-1", resp.Value)
	}
	resp = send(t, cmdChan, constants.Decr, "counter")
	if !bytes.Equal(resp.Value, []byte(":-2\r\n")) {
		t.Errorf("DECR second call = %q, want :-2", resp.Value)
	}
}

// ---- concurrency ----

// Verifies the event loop fully serializes commands sent concurrently from
// many goroutines: 100 concurrent INCRs on the same key must never race,
// converging to exactly 100.
func TestEventLoopSerializesConcurrentIncr(t *testing.T) {
	_, cmdChan := newStore(t, 0)

	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			respCh := make(chan Response, 1)
			cmdChan <- Command{Name: constants.Incr, Args: []string{"counter"}, Resp: respCh}
			<-respCh
		}()
	}
	wg.Wait()

	resp := send(t, cmdChan, constants.Get, "counter")
	want := respBulk("100")
	if !bytes.Equal(resp.Value, []byte(want)) {
		t.Errorf("counter = %q, want %q", resp.Value, want)
	}
}
