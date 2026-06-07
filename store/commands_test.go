package store

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/data_type/heap"
)

// newTestStore builds a Store without starting eventLoop/eviction goroutines,
// so tests can call command methods synchronously and inspect state directly.
func newTestStore() *Store {
	return &Store{
		data: make(map[string]*entry),
		ttls: heap.New[ttlItem](func(a, b ttlItem) bool {
			return a.expiresAt.Before(b.expiresAt)
		}),
		pubsub: make(map[string][]chan []byte),
	}
}

func assertValue(t *testing.T, got Response, want string) {
	t.Helper()
	if !bytes.Equal(got.Value, []byte(want)) {
		t.Errorf("Value = %q, want %q", got.Value, want)
	}
}

// ---- PING ----

func TestPing(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.ping(), constants.PONG)
}

// ---- GET ----

func TestGetMissing(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.get([]string{"missing"}), constants.NIL)
}

func TestGetPresent(t *testing.T) {
	s := newTestStore()
	s.data["foo"] = &entry{value: []byte("bar")}
	resp := s.get([]string{"foo"})
	want := "$3\r\nbar\r\n"
	assertValue(t, resp, want)
}

func TestGetExpiredIsLazilyDeleted(t *testing.T) {
	s := newTestStore()
	s.data["foo"] = &entry{
		value:  []byte("bar"),
		expiry: time.Now().Add(-1 * time.Second), // already expired
	}
	assertValue(t, s.get([]string{"foo"}), constants.NIL)
	if _, exists := s.data["foo"]; exists {
		t.Errorf("expired key was not lazily deleted")
	}
}

func TestGetWrongArgs(t *testing.T) {
	s := newTestStore()
	resp := s.get([]string{})
	want := fmt.Sprintf(constants.WRONG_NUM_ARGS, "GET")
	assertValue(t, resp, want)
}

// ---- SET ----

func TestSetBasic(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.set([]string{"k", "v"}), constants.OK)

	e, ok := s.data["k"]
	if !ok {
		t.Fatalf("key not stored")
	}
	if string(e.value) != "v" {
		t.Errorf("value = %q, want %q", e.value, "v")
	}
	if !e.expiry.IsZero() {
		t.Errorf("expiry should be zero for SET without EX, got %v", e.expiry)
	}
}

func TestSetWrongArgs(t *testing.T) {
	s := newTestStore()
	resp := s.set([]string{"k"})
	want := fmt.Sprintf(constants.WRONG_NUM_ARGS, "SET")
	assertValue(t, resp, want)
}

func TestSetNXKeyMissing(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.set([]string{"k", "v", constants.NX}), constants.OK)
	if _, ok := s.data["k"]; !ok {
		t.Errorf("NX should have set key when absent")
	}
}

func TestSetNXKeyExists(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("old")}
	assertValue(t, s.set([]string{"k", "new", constants.NX}), constants.NIL)
	if string(s.data["k"].value) != "old" {
		t.Errorf("NX should not overwrite, got %q", s.data["k"].value)
	}
}

func TestSetXXKeyMissing(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.set([]string{"k", "v", constants.XX}), constants.NIL)
	if _, ok := s.data["k"]; ok {
		t.Errorf("XX should not have set key when absent")
	}
}

func TestSetXXKeyExists(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("old")}
	assertValue(t, s.set([]string{"k", "new", constants.XX}), constants.OK)
	if string(s.data["k"].value) != "new" {
		t.Errorf("XX should overwrite, got %q", s.data["k"].value)
	}
}

func TestSetEX(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.set([]string{"k", "v", constants.EX, "10"}), constants.OK)

	e := s.data["k"]
	if e.expiry.IsZero() {
		t.Fatalf("expiry not set")
	}
	d := time.Until(e.expiry)
	if d < 9*time.Second || d > 11*time.Second {
		t.Errorf("expiry off, expected ~10s, got %v", d)
	}
	if s.ttls.Len() != 1 {
		t.Errorf("ttls heap len = %d, want 1", s.ttls.Len())
	}
}

func TestSetEXMissingSeconds(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.set([]string{"k", "v", constants.EX}), constants.INV_EXPIRY)
}

func TestSetEXInvalidSeconds(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.set([]string{"k", "v", constants.EX, "abc"}), constants.INV_EXPIRY)

	s2 := newTestStore()
	assertValue(t, s2.set([]string{"k", "v", constants.EX, "-5"}), constants.INV_EXPIRY)
}

// ---- DEL ----

func TestDelExists(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("v")}
	assertValue(t, s.del([]string{"k"}), constants.ONE)
	if _, ok := s.data["k"]; ok {
		t.Errorf("key not deleted")
	}
}

func TestDelPublishesLockReleased(t *testing.T) {
	s := newTestStore()
	s.data["mykey"] = &entry{value: []byte("v")}

	ch := s.Subscribe("lock-released:mykey")

	assertValue(t, s.del([]string{"mykey"}), constants.ONE)

	select {
	case msg := <-ch:
		if string(msg) != "released" {
			t.Errorf("notification = %q, want %q", msg, "released")
		}
	case <-time.After(100 * time.Millisecond):
		t.Errorf("no lock-released notification published on DEL")
	}
}

func TestDelMissingDoesNotPublish(t *testing.T) {
	s := newTestStore()
	ch := s.Subscribe("lock-released:nope")

	assertValue(t, s.del([]string{"nope"}), constants.ZERO)

	select {
	case msg := <-ch:
		t.Errorf("unexpected notification on missing key DEL: %q", msg)
	case <-time.After(50 * time.Millisecond):
		// expected: no message
	}
}

func TestDelMissing(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.del([]string{"missing"}), constants.ZERO)
}

func TestDelWrongArgs(t *testing.T) {
	s := newTestStore()
	resp := s.del([]string{})
	want := fmt.Sprintf(constants.WRONG_NUM_ARGS, "DEL")
	assertValue(t, resp, want)
}

// ---- EXPIRE ----

func TestExpireKeyMissing(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.expire([]string{"missing", "10"}), constants.ZERO)
}

func TestExpireSetsTTL(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("v")}
	assertValue(t, s.expire([]string{"k", "30"}), constants.ONE)

	d := time.Until(s.data["k"].expiry)
	if d < 29*time.Second || d > 31*time.Second {
		t.Errorf("expiry off, got %v", d)
	}
	if s.ttls.Len() != 1 {
		t.Errorf("ttls heap len = %d, want 1", s.ttls.Len())
	}
}

func TestExpireInvalidSeconds(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("v")}
	assertValue(t, s.expire([]string{"k", "abc"}), constants.INV_EXPIRY)
	assertValue(t, s.expire([]string{"k", "-1"}), constants.INV_EXPIRY)
}

func TestExpireWrongArgs(t *testing.T) {
	s := newTestStore()
	resp := s.expire([]string{"k"})
	want := fmt.Sprintf(constants.WRONG_NUM_ARGS, "EXPIRE")
	assertValue(t, resp, want)
}

// ---- TTL ----

func TestTTLKeyMissing(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.ttl([]string{"missing"}), constants.TTL_KEY_NOT_EXIST)
}

func TestTTLKeyNoExpiry(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("v")}
	assertValue(t, s.ttl([]string{"k"}), constants.TTL_KEY_NO_EXPIRY)
}

func TestTTLKeyWithExpiry(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("v"), expiry: time.Now().Add(20 * time.Second)}
	resp := s.ttl([]string{"k"})

	// Should be a positive integer near 20
	str := string(resp.Value)
	if !strings.HasPrefix(str, ":") || !strings.HasSuffix(str, "\r\n") {
		t.Fatalf("malformed TTL response: %q", str)
	}
	// Accept ":19\r\n" or ":20\r\n" depending on rounding
	if str != ":19\r\n" && str != ":20\r\n" {
		t.Errorf("TTL = %q, want ~20", str)
	}
}

func TestTTLExpiredKey(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("v"), expiry: time.Now().Add(-1 * time.Second)}
	assertValue(t, s.ttl([]string{"k"}), constants.TTL_KEY_NOT_EXIST)
}

func TestTTLWrongArgs(t *testing.T) {
	s := newTestStore()
	resp := s.ttl([]string{})
	want := fmt.Sprintf(constants.WRONG_NUM_ARGS, "TTL")
	assertValue(t, resp, want)
}

// ---- PUBSUB ----

func TestSubscribeAndPublish(t *testing.T) {
	s := newTestStore()
	ch := s.Subscribe("news")

	resp := s.publish([]string{"news", "hello"})
	assertValue(t, resp, ":1\r\n")

	select {
	case msg := <-ch:
		if string(msg) != "hello" {
			t.Errorf("got %q, want %q", msg, "hello")
		}
	case <-time.After(100 * time.Millisecond):
		t.Errorf("subscriber did not receive message")
	}
}

func TestPublishNoSubscribers(t *testing.T) {
	s := newTestStore()
	resp := s.publish([]string{"empty", "msg"})
	assertValue(t, resp, ":0\r\n")
}

func TestPublishMultipleSubscribers(t *testing.T) {
	s := newTestStore()
	ch1 := s.Subscribe("topic")
	ch2 := s.Subscribe("topic")

	resp := s.publish([]string{"topic", "broadcast"})
	assertValue(t, resp, ":2\r\n")

	for i, ch := range []chan []byte{ch1, ch2} {
		select {
		case msg := <-ch:
			if string(msg) != "broadcast" {
				t.Errorf("sub %d got %q", i, msg)
			}
		case <-time.After(100 * time.Millisecond):
			t.Errorf("sub %d did not receive", i)
		}
	}
}

func TestPublishDropsOnFullBuffer(t *testing.T) {
	s := newTestStore()
	ch := s.Subscribe("t")

	// Fill the buffered chan (capacity 16)
	for i := 0; i < 16; i++ {
		s.publish([]string{"t", "msg"})
	}
	// 17th send should be dropped
	resp := s.publish([]string{"t", "overflow"})
	assertValue(t, resp, ":0\r\n")

	_ = ch
}

func TestUnsubscribe(t *testing.T) {
	s := newTestStore()
	ch := s.Subscribe("t")
	s.Unsubscribe("t", ch)

	resp := s.publish([]string{"t", "msg"})
	assertValue(t, resp, ":0\r\n")
}

func TestPublishWrongArgs(t *testing.T) {
	s := newTestStore()
	resp := s.publish([]string{"only-topic"})
	want := fmt.Sprintf(constants.WRONG_NUM_ARGS, "PUBLISH")
	assertValue(t, resp, want)
}

// ---- EVICT ----

func TestEvictRemovesExpired(t *testing.T) {
	s := newTestStore()
	now := time.Now()

	s.data["expired1"] = &entry{value: []byte("a"), expiry: now.Add(-2 * time.Second)}
	s.data["expired2"] = &entry{value: []byte("b"), expiry: now.Add(-1 * time.Second)}
	s.data["alive"] = &entry{value: []byte("c"), expiry: now.Add(10 * time.Second)}

	s.ttls.Push(ttlItem{key: "expired1", expiresAt: s.data["expired1"].expiry})
	s.ttls.Push(ttlItem{key: "expired2", expiresAt: s.data["expired2"].expiry})
	s.ttls.Push(ttlItem{key: "alive", expiresAt: s.data["alive"].expiry})

	s.evict()

	if _, ok := s.data["expired1"]; ok {
		t.Errorf("expired1 should be removed")
	}
	if _, ok := s.data["expired2"]; ok {
		t.Errorf("expired2 should be removed")
	}
	if _, ok := s.data["alive"]; !ok {
		t.Errorf("alive should remain")
	}
	if s.ttls.Len() != 1 {
		t.Errorf("ttls len = %d, want 1 (just 'alive')", s.ttls.Len())
	}
}

func TestEvictEmptyHeap(t *testing.T) {
	s := newTestStore()
	s.evict() // should not panic or block
}

func TestEvictPublishesLockReleased(t *testing.T) {
	s := newTestStore()
	now := time.Now()

	s.data["expired"] = &entry{value: []byte("a"), expiry: now.Add(-1 * time.Second)}
	s.ttls.Push(ttlItem{key: "expired", expiresAt: s.data["expired"].expiry})

	ch := s.Subscribe("lock-released:expired")
	s.evict()

	select {
	case msg := <-ch:
		if string(msg) != "released" {
			t.Errorf("notification = %q, want %q", msg, "released")
		}
	case <-time.After(100 * time.Millisecond):
		t.Errorf("no lock-released notification published on eviction")
	}
}

// When SET ... EX is called twice on the same key, the heap accumulates entries
// for both expiries. The older (sooner) entry must NOT delete a key whose
// current expiry has been extended.
func TestEvictSkipsStaleHeapEntry(t *testing.T) {
	s := newTestStore()
	now := time.Now()

	staleExpiry := now.Add(-1 * time.Second)   // old, already past
	currentExpiry := now.Add(10 * time.Second) // refreshed, still alive

	s.data["k"] = &entry{value: []byte("v"), expiry: currentExpiry}
	// Heap has the stale entry (simulating an earlier EX that got overwritten)
	s.ttls.Push(ttlItem{key: "k", expiresAt: staleExpiry})

	s.evict()

	if _, ok := s.data["k"]; !ok {
		t.Errorf("key was deleted by stale heap entry — staleness check failed")
	}
}

func TestEvictRemovesHeapEntryEvenIfKeyAlreadyDeleted(t *testing.T) {
	s := newTestStore()
	now := time.Now()

	// Heap has an entry but the data was already removed (e.g. via DEL)
	s.ttls.Push(ttlItem{key: "ghost", expiresAt: now.Add(-1 * time.Second)})

	s.evict()

	if s.ttls.Len() != 0 {
		t.Errorf("orphan heap entry not cleaned up, len = %d", s.ttls.Len())
	}
}

func TestEvictStopsAtFirstAlive(t *testing.T) {
	s := newTestStore()
	now := time.Now()

	s.data["future"] = &entry{value: []byte("a"), expiry: now.Add(5 * time.Second)}
	s.ttls.Push(ttlItem{key: "future", expiresAt: s.data["future"].expiry})

	s.evict()

	if _, ok := s.data["future"]; !ok {
		t.Errorf("future-expiring key should remain")
	}
	if s.ttls.Len() != 1 {
		t.Errorf("ttls len = %d, want 1", s.ttls.Len())
	}
}

// ---- SNAPSHOT ----

// Verifies snapshot copies every key's value and expiry into the response
// map and delivers it on the snapResp channel.
func TestSnapshotCopiesData(t *testing.T) {
	s := newTestStore()
	s.snapResp = make(chan SnapshotResponse, 1)

	expiry := time.Now().Add(time.Hour)
	s.data["k1"] = &entry{value: []byte("v1")}
	s.data["k2"] = &entry{value: []byte("v2"), expiry: expiry}

	s.snapshot()

	resp := <-s.snapResp
	if resp.err != nil {
		t.Fatalf("snapshot err = %v, want nil", resp.err)
	}
	if len(resp.data) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(resp.data))
	}
	if string(resp.data["k1"].Value) != "v1" {
		t.Errorf("k1 value = %q, want %q", resp.data["k1"].Value, "v1")
	}
	if !resp.data["k1"].Expiry.IsZero() {
		t.Errorf("k1 expiry = %v, want zero", resp.data["k1"].Expiry)
	}
	if string(resp.data["k2"].Value) != "v2" {
		t.Errorf("k2 value = %q, want %q", resp.data["k2"].Value, "v2")
	}
	if !resp.data["k2"].Expiry.Equal(expiry) {
		t.Errorf("k2 expiry = %v, want %v", resp.data["k2"].Expiry, expiry)
	}
}

// Verifies snapshotting an empty store yields an empty (non-nil) map.
func TestSnapshotEmptyStore(t *testing.T) {
	s := newTestStore()
	s.snapResp = make(chan SnapshotResponse, 1)

	s.snapshot()

	resp := <-s.snapResp
	if resp.err != nil {
		t.Fatalf("snapshot err = %v, want nil", resp.err)
	}
	if resp.data == nil {
		t.Fatalf("snapshot data is nil, want empty map")
	}
	if len(resp.data) != 0 {
		t.Errorf("snapshot len = %d, want 0", len(resp.data))
	}
}

// Verifies the snapshot map is independent of the live store: mutating
// s.data after the snapshot does not change the captured response.
func TestSnapshotIsDecoupledFromStore(t *testing.T) {
	s := newTestStore()
	s.snapResp = make(chan SnapshotResponse, 1)
	s.data["k"] = &entry{value: []byte("v")}

	s.snapshot()
	resp := <-s.snapResp

	// Add a new key after the snapshot was taken.
	s.data["new"] = &entry{value: []byte("late")}

	if _, ok := resp.data["new"]; ok {
		t.Errorf("snapshot captured a key added after snapshot()")
	}
	if len(resp.data) != 1 {
		t.Errorf("snapshot len = %d, want 1", len(resp.data))
	}
}
