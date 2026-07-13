package sdk

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/metrics"
	"github.com/priyanshu-s-rana/kv_store/server"
	"github.com/priyanshu-s-rana/kv_store/store"
)

const testAddr = "localhost:15040"

// fakePersistence is a no-op store.Persistence used to run a real store
// event loop in tests without touching disk.
type fakePersistence struct{}

func (fakePersistence) Append(constants.CmdName, []string) error        { return nil }
func (fakePersistence) Checkpoint(map[string]store.SnapshotEntry) error { return nil }
func (fakePersistence) CheckpointSuccess() error                        { return nil }
func (fakePersistence) Rebaseline(map[string]store.SnapshotEntry) error { return nil }

// TestMain starts a real kv-server on testAddr before any test runs and stops
// it when all tests finish.
func TestMain(m *testing.M) {
	metricsManager := metrics.New()
	cmdChan := make(chan store.Command)
	st := store.New(0, cmdChan, fakePersistence{}, metricsManager.Store)
	st.Start()
	srv := server.New(testAddr, cmdChan, st, metricsManager.Server)
	go srv.Start()

	// Wait until the server is ready.
	for i := 0; i < 20; i++ {
		conn, err := net.Dial("tcp", testAddr)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	os.Exit(m.Run())
}

// newTestClient connects to the test server and registers cleanup.
func newTestClient(t *testing.T) *KVStoreClient {
	t.Helper()
	c, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	// always start clean
	c.FlushAll()
	return c
}

// ---- Ping ----

func TestSDKPing(t *testing.T) {
	c := newTestClient(t)
	resp, err := c.Ping()
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if resp != "PONG" {
		t.Errorf("Ping = %q, want PONG", resp)
	}
}

// ---- Set / Get ----

func TestSDKSetGet(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.Set("k", "v"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	resp, err := c.Get("k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp != "v" {
		t.Errorf("Get = %q, want v", resp)
	}
}

func TestSDKGetMissing(t *testing.T) {
	c := newTestClient(t)
	resp, err := c.Get("missing")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp != "nil" {
		t.Errorf("Get missing = %q, want nil", resp)
	}
}

// ---- Del ----

func TestSDKDel(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "v")
	resp, err := c.Del("k")
	if err != nil {
		t.Fatalf("Del: %v", err)
	}
	if resp != "1" {
		t.Errorf("Del = %q, want 1", resp)
	}
	got, _ := c.Get("k")
	if got != "nil" {
		t.Errorf("Get after Del = %q, want nil", got)
	}
}

func TestSDKDelMissing(t *testing.T) {
	c := newTestClient(t)
	resp, err := c.Del("missing")
	if err != nil {
		t.Fatalf("Del: %v", err)
	}
	if resp != "0" {
		t.Errorf("Del missing = %q, want 0", resp)
	}
}

// ---- Expire / TTL ----

func TestSDKExpireAndTTL(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "v")
	c.Expire("k", 30)
	resp, err := c.TTL("k")
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if resp != "30" && resp != "29" {
		t.Errorf("TTL = %q, want ~30", resp)
	}
}

func TestSDKTTLNoExpiry(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "v")
	resp, _ := c.TTL("k")
	if resp != "-1" {
		t.Errorf("TTL no expiry = %q, want -1", resp)
	}
}

func TestSDKTTLMissing(t *testing.T) {
	c := newTestClient(t)
	resp, _ := c.TTL("missing")
	if resp != "-2" {
		t.Errorf("TTL missing = %q, want -2", resp)
	}
}

// ---- Incr / Decr ----

func TestSDKIncrNewKey(t *testing.T) {
	c := newTestClient(t)
	resp, err := c.Incr("n")
	if err != nil {
		t.Fatalf("Incr: %v", err)
	}
	if resp != "1" {
		t.Errorf("Incr new = %q, want 1", resp)
	}
}

func TestSDKIncrExistingKey(t *testing.T) {
	c := newTestClient(t)
	c.Set("n", "5")
	resp, _ := c.Incr("n")
	if resp != "6" {
		t.Errorf("Incr = %q, want 6", resp)
	}
}

func TestSDKDecrNewKey(t *testing.T) {
	c := newTestClient(t)
	resp, err := c.Decr("n")
	if err != nil {
		t.Fatalf("Decr: %v", err)
	}
	if resp != "-1" {
		t.Errorf("Decr new = %q, want -1", resp)
	}
}

func TestSDKDecrExistingKey(t *testing.T) {
	c := newTestClient(t)
	c.Set("n", "5")
	resp, _ := c.Decr("n")
	if resp != "4" {
		t.Errorf("Decr = %q, want 4", resp)
	}
}

// ---- MSet / MGet ----

func TestSDKMSetMGet(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.MSet("k1", "v1", "k2", "v2"); err != nil {
		t.Fatalf("MSet: %v", err)
	}
	vals, err := c.MGet("k1", "k2")
	if err != nil {
		t.Fatalf("MGet: %v", err)
	}
	if len(vals) != 2 || vals[0] != "v1" || vals[1] != "v2" {
		t.Errorf("MGet = %v, want [v1 v2]", vals)
	}
}

func TestSDKMSetOddArgsReturnsError(t *testing.T) {
	c := newTestClient(t)
	_, err := c.MSet("k1", "v1", "k2")
	if err == nil {
		t.Errorf("MSet with odd number of args should return error")
	}
}

func TestSDKMGetMissingReturnsEmptyString(t *testing.T) {
	c := newTestClient(t)
	c.Set("k1", "v1")
	vals, err := c.MGet("k1", "missing")
	if err != nil {
		t.Fatalf("MGet: %v", err)
	}
	if len(vals) != 2 || vals[0] != "v1" || vals[1] != "" {
		t.Errorf("MGet = %v, want [v1 ]", vals)
	}
}

// ---- Keys ----

func TestSDKKeys(t *testing.T) {
	c := newTestClient(t)
	c.Set("user:1", "a")
	c.Set("user:2", "b")
	c.Set("session:1", "c")

	resp, err := c.Keys("user:*")
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if resp == "" {
		t.Errorf("Keys(user:*) returned empty")
	}
}

// ---- FlushAll ----

func TestSDKFlushAll(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "v")
	c.FlushAll()
	got, _ := c.Get("k")
	if got != "nil" {
		t.Errorf("Get after FlushAll = %q, want nil", got)
	}
}

// ---- MemoryStats ----

func TestSDKMemoryStats(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "v")
	resp, err := c.MemoryStats()
	if err != nil {
		t.Fatalf("MemoryStats: %v", err)
	}
	if resp == "" {
		t.Errorf("MemoryStats returned empty")
	}
}

// ---- Publish / Subscribe ----

func TestSDKPublishNoSubscribers(t *testing.T) {
	c := newTestClient(t)
	resp, err := c.Publish("topic", "hello")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if resp != "0" {
		t.Errorf("Publish with no subs = %q, want 0", resp)
	}
}

func TestSDKSubscribeReceivesMessage(t *testing.T) {
	publisher := newTestClient(t)

	subClient, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient for sub: %v", err)
	}
	defer subClient.Close()

	subscription, err := subClient.Subscribe("news")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer subscription.Unsubscribe()

	// Drain the subscribe confirmation before publishing.
	select {
	case <-subscription.Message():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("no subscribe confirmation received")
	}

	publisher.Publish("news", "hello world")

	select {
	case msg := <-subscription.Message():
		if !strings.Contains(msg, "hello world") {
			t.Errorf("received %q, want message containing hello world", msg)
		}
	case <-time.After(500 * time.Millisecond):
		t.Errorf("no message received within timeout")
	}
}

func TestSDKSubscribeNoTopicError(t *testing.T) {
	c, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	_, err = c.Subscribe()
	if err == nil {
		t.Errorf("Subscribe with no topics should return error")
	}
}

// ---- Error responses returned as errors ----

func TestSDKIncrNonIntegerReturnsError(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "notanint")
	_, err := c.Incr("k")
	if err == nil {
		t.Errorf("Incr on non-integer should return error")
	}
}

// ---- Set modifiers ----

func TestSDKSetWithEX(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.Set("k", "v", WithEX(30)); err != nil {
		t.Fatalf("Set WithEX: %v", err)
	}
	ttl, _ := c.TTL("k")
	if ttl != "30" && ttl != "29" {
		t.Errorf("TTL after Set WithEX = %q, want ~30", ttl)
	}
}

func TestSDKSetWithNXKeyAbsent(t *testing.T) {
	c := newTestClient(t)
	resp, err := c.Set("k", "v", WithNX())
	if err != nil {
		t.Fatalf("Set WithNX: %v", err)
	}
	if resp != "OK" {
		t.Errorf("Set WithNX on absent key = %q, want OK", resp)
	}
}

func TestSDKSetWithNXKeyExists(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "original")
	resp, err := c.Set("k", "new", WithNX())
	if err != nil {
		t.Fatalf("Set WithNX: %v", err)
	}
	if resp != "nil" {
		t.Errorf("Set WithNX on existing key = %q, want nil", resp)
	}
	got, _ := c.Get("k")
	if got != "original" {
		t.Errorf("value changed after NX blocked write: got %q, want original", got)
	}
}

func TestSDKSetWithXXKeyExists(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "original")
	resp, err := c.Set("k", "updated", WithXX())
	if err != nil {
		t.Fatalf("Set WithXX: %v", err)
	}
	if resp != "OK" {
		t.Errorf("Set WithXX on existing key = %q, want OK", resp)
	}
	got, _ := c.Get("k")
	if got != "updated" {
		t.Errorf("value = %q, want updated", got)
	}
}

func TestSDKSetWithXXKeyAbsent(t *testing.T) {
	c := newTestClient(t)
	resp, err := c.Set("k", "v", WithXX())
	if err != nil {
		t.Fatalf("Set WithXX: %v", err)
	}
	if resp != "nil" {
		t.Errorf("Set WithXX on absent key = %q, want nil", resp)
	}
	got, _ := c.Get("k")
	if got != "nil" {
		t.Errorf("key should not exist after XX blocked write: got %q", got)
	}
}

func TestSDKSetWithEXAndNX(t *testing.T) {
	c := newTestClient(t)
	resp, err := c.Set("k", "v", WithEX(30), WithNX())
	if err != nil {
		t.Fatalf("Set WithEX+WithNX: %v", err)
	}
	if resp != "OK" {
		t.Errorf("Set WithEX+WithNX = %q, want OK", resp)
	}
	ttl, _ := c.TTL("k")
	if ttl != "30" && ttl != "29" {
		t.Errorf("TTL = %q, want ~30", ttl)
	}
}

func TestSDKSetGetRoundTrip(t *testing.T) {
	c := newTestClient(t)
	cases := []struct{ key, value string }{
		{"str", "hello"},
		{"num", "42"},
		{"spaces", "hello world"},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("key=%s", tc.key), func(t *testing.T) {
			c.Set(tc.key, tc.value)
			got, err := c.Get(tc.key)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got != tc.value {
				t.Errorf("Get(%q) = %q, want %q", tc.key, got, tc.value)
			}
		})
	}
}
