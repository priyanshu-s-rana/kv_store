package store

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/data_type/heap"
	"github.com/priyanshu-s-rana/kv_store/lru"
)

// newTestStore builds a Store without starting eventLoop/eviction goroutines,
// so tests can call command methods synchronously and inspect state directly.
func newTestStore() *Store {
	return &Store{
		data: make(map[string]*entry),
		ttls: heap.New[ttlItem](func(a, b ttlItem) bool {
			return a.expiresAt.Before(b.expiresAt)
		}),
		pubsub:        make(map[string][]chan []byte),
		lru:           lru.New(),
		memoryProfile: NewMemProfile(0),
		pubSubStats:   &pubSubStats{},
	}
}

func assertValue(t *testing.T, got Response, want string) {
	t.Helper()
	if !bytes.Equal(got.Value, []byte(want)) {
		t.Errorf("Value = %q, want %q", got.Value, want)
	}
}

// RESP wire-format builders for assertions. Reimplemented independently of
// the parser encoder so the tests pin the exact protocol bytes rather than
// trusting the encoder under test.
const respNil = "$-1\r\n"

func respSimple(s string) string { return "+" + s + "\r\n" }
func respError(s string) string  { return "-ERR " + s + "\r\n" }
func respInt(n int) string       { return fmt.Sprintf(":%d\r\n", n) }
func respBulk(s string) string   { return fmt.Sprintf("$%d\r\n%s\r\n", len(s), s) }

// ---- PING ----

func TestPing(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.ping(nil), respSimple(constants.PONG))
}

// ---- GET ----

func TestGetMissing(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.get([]string{"missing"}), respNil)
}

func TestGetPresent(t *testing.T) {
	s := newTestStore()
	s.data["foo"] = &entry{value: []byte("bar")}
	resp := s.get([]string{"foo"})
	assertValue(t, resp, respBulk("bar"))
}

func TestGetExpiredIsLazilyDeleted(t *testing.T) {
	s := newTestStore()
	s.data["foo"] = &entry{
		value:  []byte("bar"),
		expiry: time.Now().Add(-1 * time.Second), // already expired
	}
	assertValue(t, s.get([]string{"foo"}), respNil)
	if _, exists := s.data["foo"]; exists {
		t.Errorf("expired key was not lazily deleted")
	}
}

func TestGetWrongArgs(t *testing.T) {
	s := newTestStore()
	resp := s.get([]string{})
	want := respError(fmt.Sprintf(constants.WRONG_NUM_ARGS, "GET"))
	assertValue(t, resp, want)
}

func TestGetMovesLRUToFront(t *testing.T) {
	s := newTestStore()
	s.set([]string{"a", "1"})
	s.set([]string{"b", "2"})
	s.set([]string{"c", "3"}) // LRU order: c(head) → b → a(tail)

	s.get([]string{"a"}) // access "a" → moves to front: a(head) → c → b(tail)

	tail, ok := s.lru.PeekBack()
	if !ok {
		t.Fatal("LRU is empty")
	}
	if tail != "b" {
		t.Errorf("tail after GET a = %q, want 'b' (a should have moved to front)", tail)
	}
}

// ---- SET ----

func TestSetBasic(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.set([]string{"k", "v"}), respSimple(constants.OK))

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
	want := respError(fmt.Sprintf(constants.WRONG_NUM_ARGS, "SET"))
	assertValue(t, resp, want)
}

func TestSetNXKeyMissing(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.set([]string{"k", "v", constants.NX}), respSimple(constants.OK))
	if _, ok := s.data["k"]; !ok {
		t.Errorf("NX should have set key when absent")
	}
}

func TestSetNXKeyExists(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("old")}
	assertValue(t, s.set([]string{"k", "new", constants.NX}), respNil)
	if string(s.data["k"].value) != "old" {
		t.Errorf("NX should not overwrite, got %q", s.data["k"].value)
	}
}

func TestSetXXKeyMissing(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.set([]string{"k", "v", constants.XX}), respNil)
	if _, ok := s.data["k"]; ok {
		t.Errorf("XX should not have set key when absent")
	}
}

func TestSetXXKeyExists(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("old")}
	assertValue(t, s.set([]string{"k", "new", constants.XX}), respSimple(constants.OK))
	if string(s.data["k"].value) != "new" {
		t.Errorf("XX should overwrite, got %q", s.data["k"].value)
	}
}

func TestSetEX(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.set([]string{"k", "v", constants.EX, "10"}), respSimple(constants.OK))

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
	assertValue(t, s.set([]string{"k", "v", constants.EX}), respError(constants.INV_EXPIRY))
}

func TestSetEXInvalidSeconds(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.set([]string{"k", "v", constants.EX, "abc"}), respError(constants.INV_EXPIRY))

	s2 := newTestStore()
	assertValue(t, s2.set([]string{"k", "v", constants.EX, "-5"}), respError(constants.INV_EXPIRY))
}

func TestSetNXFailureNoMemoryCharge(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k", "v1"})
	before := s.memoryProfile.currentMemorySize()

	s.set([]string{"k", "new", constants.NX}) // NX fails — key exists

	if s.memoryProfile.currentMemorySize() != before {
		t.Errorf("memory changed after NX failure: before=%d after=%d",
			before, s.memoryProfile.currentMemorySize())
	}
	if s.memoryProfile.keyCount != 1 {
		t.Errorf("keyCount = %d, want 1 (NX failure should not add a key)", s.memoryProfile.keyCount)
	}
}

func TestSetXXValueBytesUpdated(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k", "short"})
	afterFirst := s.memoryProfile.valueBytes

	s.set([]string{"k", "muchlonger", constants.XX})

	expected := afterFirst + int64(len("muchlonger")) - int64(len("short"))
	if s.memoryProfile.valueBytes != expected {
		t.Errorf("valueBytes = %d, want %d after XX update", s.memoryProfile.valueBytes, expected)
	}
	if s.memoryProfile.keyCount != 1 {
		t.Errorf("keyCount = %d, want 1 (XX updates, not adds)", s.memoryProfile.keyCount)
	}
}

func TestMemoryProfilePlainSetOverwriteNoDoubleCharge(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k", "v1"})
	afterFirst := s.memoryProfile.keyBytes
	afterFirstCount := s.memoryProfile.keyCount

	s.set([]string{"k", "v2"}) // plain SET on existing key — must not re-charge key overhead

	if s.memoryProfile.keyBytes != afterFirst {
		t.Errorf("keyBytes changed on plain overwrite: was %d, now %d", afterFirst, s.memoryProfile.keyBytes)
	}
	if s.memoryProfile.keyCount != afterFirstCount {
		t.Errorf("keyCount changed on plain overwrite: was %d, now %d", afterFirstCount, s.memoryProfile.keyCount)
	}
}

// ---- DEL ----

func TestDelExists(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("v")}
	assertValue(t, s.del([]string{"k"}), respInt(constants.ONE))
	if _, ok := s.data["k"]; ok {
		t.Errorf("key not deleted")
	}
}

func TestDelPublishesLockReleased(t *testing.T) {
	s := newTestStore()
	s.data["mykey"] = &entry{value: []byte("v")}

	ch := s.Subscribe("lock-released:mykey")

	assertValue(t, s.del([]string{"mykey"}), respInt(constants.ONE))

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

	assertValue(t, s.del([]string{"nope"}), respInt(constants.ZERO))

	select {
	case msg := <-ch:
		t.Errorf("unexpected notification on missing key DEL: %q", msg)
	case <-time.After(50 * time.Millisecond):
		// expected: no message
	}
}

func TestDelMissing(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.del([]string{"missing"}), respInt(constants.ZERO))
}

func TestDelWrongArgs(t *testing.T) {
	s := newTestStore()
	resp := s.del([]string{})
	want := respError(fmt.Sprintf(constants.WRONG_NUM_ARGS, "DEL"))
	assertValue(t, resp, want)
}

// ---- EXPIRE ----

func TestExpireKeyMissing(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.expire([]string{"missing", "10"}), respInt(constants.ZERO))
}

func TestExpireSetsTTL(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("v")}
	assertValue(t, s.expire([]string{"k", "30"}), respInt(constants.ONE))

	d := time.Until(s.data["k"].expiry)
	if d < 29*time.Second || d > 31*time.Second {
		t.Errorf("expiry off, got %v", d)
	}
	if s.ttls.Len() != 1 {
		t.Errorf("ttls heap len = %d, want 1", s.ttls.Len())
	}
}

func TestExpireChargesMemory(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("v")}
	before := s.memoryProfile.ttlBytes

	s.expire([]string{"k", "30"})

	if s.memoryProfile.ttlBytes <= before {
		t.Errorf("ttlBytes = %d, want > %d after EXPIRE", s.memoryProfile.ttlBytes, before)
	}
}

func TestExpireInvalidSeconds(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("v")}
	assertValue(t, s.expire([]string{"k", "abc"}), respError(constants.INV_EXPIRY))
	assertValue(t, s.expire([]string{"k", "-1"}), respError(constants.INV_EXPIRY))
}

func TestExpireWrongArgs(t *testing.T) {
	s := newTestStore()
	resp := s.expire([]string{"k"})
	want := respError(fmt.Sprintf(constants.WRONG_NUM_ARGS, "EXPIRE"))
	assertValue(t, resp, want)
}

// ---- TTL ----

func TestTTLKeyMissing(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.ttl([]string{"missing"}), respInt(constants.TTL_KEY_NOT_EXIST))
}

func TestTTLKeyNoExpiry(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("v")}
	assertValue(t, s.ttl([]string{"k"}), respInt(constants.TTL_KEY_NO_EXPIRY))
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
	assertValue(t, s.ttl([]string{"k"}), respInt(constants.TTL_KEY_NOT_EXIST))
}

func TestTTLWrongArgs(t *testing.T) {
	s := newTestStore()
	resp := s.ttl([]string{})
	want := respError(fmt.Sprintf(constants.WRONG_NUM_ARGS, "TTL"))
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
	want := respError(fmt.Sprintf(constants.WRONG_NUM_ARGS, "PUBLISH"))
	assertValue(t, resp, want)
}

// ---- PUBSUB STATS ----

func TestPubSubStatsGetStats(t *testing.T) {
	ps := &pubSubStats{}
	ps.activeTopics.Store(3)
	ps.activeSubscribers.Store(7)
	ps.messagesPublished.Store(42)

	got := ps.GetStats()
	if got.ActiveTopics != 3 {
		t.Errorf("ActiveTopics = %d, want 3", got.ActiveTopics)
	}
	if got.ActiveSubscribers != 7 {
		t.Errorf("ActiveSubscribers = %d, want 7", got.ActiveSubscribers)
	}
	if got.MessagesPublished != 42 {
		t.Errorf("MessagesPublished = %d, want 42", got.MessagesPublished)
	}
}

func TestSubscribeIncrementsActiveTopicsAndSubscribers(t *testing.T) {
	s := newTestStore()
	ch := s.Subscribe("sports")
	defer s.Unsubscribe("sports", ch)

	got := s.pubSubStats.GetStats()
	if got.ActiveTopics != 1 {
		t.Errorf("ActiveTopics = %d, want 1", got.ActiveTopics)
	}
	if got.ActiveSubscribers != 1 {
		t.Errorf("ActiveSubscribers = %d, want 1", got.ActiveSubscribers)
	}
}

func TestSubscribeSecondSubscriberSameTopicOnlyOneActiveTopic(t *testing.T) {
	s := newTestStore()
	ch1 := s.Subscribe("news")
	ch2 := s.Subscribe("news")
	defer s.Unsubscribe("news", ch1)
	defer s.Unsubscribe("news", ch2)

	got := s.pubSubStats.GetStats()
	if got.ActiveTopics != 1 {
		t.Errorf("ActiveTopics = %d, want 1 (same topic)", got.ActiveTopics)
	}
	if got.ActiveSubscribers != 2 {
		t.Errorf("ActiveSubscribers = %d, want 2", got.ActiveSubscribers)
	}
}

func TestSubscribeTwoDistinctTopicsCountsBoth(t *testing.T) {
	s := newTestStore()
	ch1 := s.Subscribe("sports")
	ch2 := s.Subscribe("news")
	defer s.Unsubscribe("sports", ch1)
	defer s.Unsubscribe("news", ch2)

	got := s.pubSubStats.GetStats()
	if got.ActiveTopics != 2 {
		t.Errorf("ActiveTopics = %d, want 2", got.ActiveTopics)
	}
	if got.ActiveSubscribers != 2 {
		t.Errorf("ActiveSubscribers = %d, want 2", got.ActiveSubscribers)
	}
}

func TestUnsubscribeDecrementsStats(t *testing.T) {
	s := newTestStore()
	ch := s.Subscribe("sports")
	s.Unsubscribe("sports", ch)

	got := s.pubSubStats.GetStats()
	if got.ActiveTopics != 0 {
		t.Errorf("ActiveTopics = %d, want 0 after unsubscribe", got.ActiveTopics)
	}
	if got.ActiveSubscribers != 0 {
		t.Errorf("ActiveSubscribers = %d, want 0 after unsubscribe", got.ActiveSubscribers)
	}
}

func TestPublishIncrementsMessagesPublished(t *testing.T) {
	s := newTestStore()
	ch := s.Subscribe("events")
	defer s.Unsubscribe("events", ch)

	s.publish([]string{"events", "hello"})

	got := s.pubSubStats.GetStats()
	if got.MessagesPublished != 1 {
		t.Errorf("MessagesPublished = %d, want 1", got.MessagesPublished)
	}
}

func TestPublishNoSubscribersDoesNotIncrementMessagesPublished(t *testing.T) {
	s := newTestStore()
	s.publish([]string{"empty-topic", "hello"})

	got := s.pubSubStats.GetStats()
	if got.MessagesPublished != 0 {
		t.Errorf("MessagesPublished = %d, want 0 (no subscribers)", got.MessagesPublished)
	}
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

	s.evict(nil)

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
	s.evict(nil) // should not panic or block
}

func TestEvictPublishesLockReleased(t *testing.T) {
	s := newTestStore()
	now := time.Now()

	s.data["expired"] = &entry{value: []byte("a"), expiry: now.Add(-1 * time.Second)}
	s.ttls.Push(ttlItem{key: "expired", expiresAt: s.data["expired"].expiry})

	ch := s.Subscribe("lock-released:expired")
	s.evict(nil)

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

	s.evict(nil)

	if _, ok := s.data["k"]; !ok {
		t.Errorf("key was deleted by stale heap entry — staleness check failed")
	}
}

func TestEvictRemovesHeapEntryEvenIfKeyAlreadyDeleted(t *testing.T) {
	s := newTestStore()
	now := time.Now()

	// Heap has an entry but the data was already removed (e.g. via DEL)
	s.ttls.Push(ttlItem{key: "ghost", expiresAt: now.Add(-1 * time.Second)})

	s.evict(nil)

	if s.ttls.Len() != 0 {
		t.Errorf("orphan heap entry not cleaned up, len = %d", s.ttls.Len())
	}
}

func TestEvictStopsAtFirstAlive(t *testing.T) {
	s := newTestStore()
	now := time.Now()

	s.data["future"] = &entry{value: []byte("a"), expiry: now.Add(5 * time.Second)}
	s.ttls.Push(ttlItem{key: "future", expiresAt: s.data["future"].expiry})

	s.evict(nil)

	if _, ok := s.data["future"]; !ok {
		t.Errorf("future-expiring key should remain")
	}
	if s.ttls.Len() != 1 {
		t.Errorf("ttls len = %d, want 1", s.ttls.Len())
	}
}

func TestEvictDecrementsttlBytes(t *testing.T) {
	s := newTestStore()
	now := time.Now()

	s.data["k"] = &entry{value: []byte("v"), expiry: now.Add(-1 * time.Second)}
	item := ttlItem{key: "k", expiresAt: now.Add(-1 * time.Second)}
	s.ttls.Push(item)
	s.memoryProfile.recordTTLSize(&item)

	before := s.memoryProfile.ttlBytes
	if before == 0 {
		t.Fatal("ttlBytes should be > 0 before eviction")
	}

	s.evict(nil)

	if s.memoryProfile.ttlBytes >= before {
		t.Errorf("ttlBytes not decremented after evict: before=%d after=%d", before, s.memoryProfile.ttlBytes)
	}
}

// ---- KEYS ----

func TestKeysExactMatch(t *testing.T) {
	s := newTestStore()
	s.data["foo"] = &entry{value: []byte("bar")}
	s.data["baz"] = &entry{value: []byte("qux")}

	assertValue(t, s.keys([]string{"foo"}), respBulk("1) foo"))
}

func TestKeysExactNoMatch(t *testing.T) {
	s := newTestStore()
	s.data["foo"] = &entry{value: []byte("bar")}

	assertValue(t, s.keys([]string{"missing"}), respBulk(""))
}

func TestKeysWildcardAll(t *testing.T) {
	s := newTestStore()
	s.data["foo"] = &entry{value: []byte("1")}
	s.data["bar"] = &entry{value: []byte("2")}

	resp := s.keys([]string{"*"})
	if !bytes.Contains(resp.Value, []byte("foo")) {
		t.Errorf("keys(*) missing foo: %q", resp.Value)
	}
	if !bytes.Contains(resp.Value, []byte("bar")) {
		t.Errorf("keys(*) missing bar: %q", resp.Value)
	}
}

func TestKeysPrefixWildcard(t *testing.T) {
	s := newTestStore()
	s.data["user:1"] = &entry{value: []byte("a")}
	s.data["user:2"] = &entry{value: []byte("b")}
	s.data["session:1"] = &entry{value: []byte("c")}

	resp := s.keys([]string{"user:*"})
	if !bytes.Contains(resp.Value, []byte("user:1")) {
		t.Errorf("keys(user:*) missing user:1: %q", resp.Value)
	}
	if !bytes.Contains(resp.Value, []byte("user:2")) {
		t.Errorf("keys(user:*) missing user:2: %q", resp.Value)
	}
	if bytes.Contains(resp.Value, []byte("session:1")) {
		t.Errorf("keys(user:*) should not contain session:1: %q", resp.Value)
	}
}

func TestKeysSuffixWildcard(t *testing.T) {
	s := newTestStore()
	s.data["user:1"] = &entry{value: []byte("a")}
	s.data["admin:1"] = &entry{value: []byte("b")}
	s.data["user:2"] = &entry{value: []byte("c")}

	resp := s.keys([]string{"*:1"})
	if !bytes.Contains(resp.Value, []byte("user:1")) {
		t.Errorf("keys(*:1) missing user:1: %q", resp.Value)
	}
	if !bytes.Contains(resp.Value, []byte("admin:1")) {
		t.Errorf("keys(*:1) missing admin:1: %q", resp.Value)
	}
	if bytes.Contains(resp.Value, []byte("user:2")) {
		t.Errorf("keys(*:1) should not contain user:2: %q", resp.Value)
	}
}

func TestKeysContainsWildcard(t *testing.T) {
	s := newTestStore()
	s.data["foo:bar"] = &entry{value: []byte("a")}
	s.data["baz:bar"] = &entry{value: []byte("b")}
	s.data["foo:qux"] = &entry{value: []byte("c")}

	resp := s.keys([]string{"*:bar*"})
	if !bytes.Contains(resp.Value, []byte("foo:bar")) {
		t.Errorf("keys(*:bar*) missing foo:bar: %q", resp.Value)
	}
	if !bytes.Contains(resp.Value, []byte("baz:bar")) {
		t.Errorf("keys(*:bar*) missing baz:bar: %q", resp.Value)
	}
	if bytes.Contains(resp.Value, []byte("foo:qux")) {
		t.Errorf("keys(*:bar*) should not contain foo:qux: %q", resp.Value)
	}
}

func TestKeysEmptyStore(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.keys([]string{"*"}), respBulk(""))
}

func TestKeysWrongArgs(t *testing.T) {
	s := newTestStore()
	resp := s.keys([]string{})
	want := respError(fmt.Sprintf(constants.WRONG_NUM_ARGS, "KEYS"))
	assertValue(t, resp, want)
}

// ---- FLUSHALL ----

func TestFlushAll(t *testing.T) {
	s := newTestStore()
	s.data["k1"] = &entry{value: []byte("v1")}
	s.data["k2"] = &entry{value: []byte("v2"), expiry: time.Now().Add(time.Hour)}
	s.ttls.Push(ttlItem{key: "k2", expiresAt: s.data["k2"].expiry})

	assertValue(t, s.flushAll(nil), respSimple(constants.OK))

	if len(s.data) != 0 {
		t.Errorf("data not cleared, len = %d", len(s.data))
	}
	if s.ttls.Len() != 0 {
		t.Errorf("ttls not cleared, len = %d", s.ttls.Len())
	}
}

func TestFlushAllEmptyStore(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.flushAll(nil), respSimple(constants.OK))
}

// ---- Memory Profile ----

func TestMemoryProfileNewKeyTracked(t *testing.T) {
	s := newTestStore()
	s.set([]string{"key", "value"})

	if s.memoryProfile.keyCount != 1 {
		t.Errorf("keyCount = %d, want 1", s.memoryProfile.keyCount)
	}
	if s.memoryProfile.keyBytes <= 0 {
		t.Errorf("keyBytes = %d, want > 0", s.memoryProfile.keyBytes)
	}
	if s.memoryProfile.valueBytes <= 0 {
		t.Errorf("valueBytes = %d, want > 0", s.memoryProfile.valueBytes)
	}
	if s.memoryProfile.lruBytes <= 0 {
		t.Errorf("lruBytes = %d, want > 0", s.memoryProfile.lruBytes)
	}
}

func TestMemoryProfileUpdateDoesNotDoubleCountLRU(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k", "v1"})
	afterFirst := s.memoryProfile.lruBytes

	s.set([]string{"k", "v2", constants.XX})

	if s.memoryProfile.lruBytes != afterFirst {
		t.Errorf("lruBytes after XX update = %d, want %d (no double count)", s.memoryProfile.lruBytes, afterFirst)
	}
	if s.memoryProfile.keyCount != 1 {
		t.Errorf("keyCount = %d, want 1 after update", s.memoryProfile.keyCount)
	}
}

func TestMemoryProfileSetWithoutEXNoTTLCharge(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k", "v"})

	if s.memoryProfile.ttlBytes != 0 {
		t.Errorf("ttlBytes = %d, want 0 for SET without EX", s.memoryProfile.ttlBytes)
	}
}

func TestMemoryProfileSetWithEXChargesTTL(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k", "v", constants.EX, "10"})

	if s.memoryProfile.ttlBytes <= 0 {
		t.Errorf("ttlBytes = %d, want > 0 for SET with EX", s.memoryProfile.ttlBytes)
	}
}

func TestMemoryProfileDelDecrementsAll(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k", "v"})
	s.del([]string{"k"})

	if s.memoryProfile.keyCount != 0 {
		t.Errorf("keyCount = %d, want 0 after DEL", s.memoryProfile.keyCount)
	}
	if s.memoryProfile.keyBytes != 0 {
		t.Errorf("keyBytes = %d, want 0 after DEL", s.memoryProfile.keyBytes)
	}
	if s.memoryProfile.lruBytes != 0 {
		t.Errorf("lruBytes = %d, want 0 after DEL", s.memoryProfile.lruBytes)
	}
}

func TestMemoryDelSameKeyTwiceNoNegative(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k", "v"})
	s.del([]string{"k"})
	s.del([]string{"k"}) // second DEL is a no-op

	if s.memoryProfile.keyBytes < 0 {
		t.Errorf("keyBytes = %d, went negative on double DEL", s.memoryProfile.keyBytes)
	}
	if s.memoryProfile.lruBytes < 0 {
		t.Errorf("lruBytes = %d, went negative on double DEL", s.memoryProfile.lruBytes)
	}
	if s.memoryProfile.keyCount < 0 {
		t.Errorf("keyCount = %d, went negative on double DEL", s.memoryProfile.keyCount)
	}
}

func TestMemoryIsOverLimit(t *testing.T) {
	s := newTestStore()
	s.memoryProfile.maxBytes = 1 // tiny limit

	s.set([]string{"k", "v"})

	if !s.memoryProfile.isOverLimit() {
		t.Errorf("isOverLimit() = false, want true with maxBytes=1")
	}
}

func TestMemoryUnlimitedWhenZero(t *testing.T) {
	s := newTestStore() // maxBytes defaults to 0 = unlimited

	for i := 0; i < 100; i++ {
		s.set([]string{fmt.Sprintf("k%d", i), "v"})
	}

	if s.memoryProfile.isOverLimit() {
		t.Errorf("isOverLimit() = true with maxBytes=0, should always be false")
	}
}

func TestMemoryProfileSubscribeTopicChargedOnce(t *testing.T) {
	s := newTestStore()
	s.Subscribe("news")
	afterFirst := s.memoryProfile.pubsubBytes

	s.Subscribe("news")
	diff := s.memoryProfile.pubsubBytes - afterFirst

	if diff != constants.BYTE_CHANNEL_OVERHEAD {
		t.Errorf("second subscriber added %d bytes, want %d (chan only, no topic overhead)", diff, constants.BYTE_CHANNEL_OVERHEAD)
	}
}

func TestMemoryProfileUnsubscribeFreesTopicOnLastSubscriber(t *testing.T) {
	s := newTestStore()
	ch := s.Subscribe("news")
	s.Unsubscribe("news", ch)

	if s.memoryProfile.pubsubBytes != 0 {
		t.Errorf("pubsubBytes = %d, want 0 after last subscriber leaves", s.memoryProfile.pubsubBytes)
	}
}

func TestMemoryProfileUnsubscribePartialKeepsTopic(t *testing.T) {
	s := newTestStore()
	ch1 := s.Subscribe("news")
	ch2 := s.Subscribe("news")
	afterBoth := s.memoryProfile.pubsubBytes

	s.Unsubscribe("news", ch1)

	// topic overhead should still be charged — ch2 is still subscribed
	expected := afterBoth - constants.BYTE_CHANNEL_OVERHEAD
	if s.memoryProfile.pubsubBytes != expected {
		t.Errorf("pubsubBytes = %d, want %d (topic kept, one chan removed)", s.memoryProfile.pubsubBytes, expected)
	}
	_ = ch2
}

// ---- FLUSHALL LRU ----

func TestFlushAllClearsLRU(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k1", "v1"})
	s.set([]string{"k2", "v2"})

	s.flushAll(nil)

	if s.lru.GetNode("k1") != nil {
		t.Errorf("LRU still has k1 after flushAll")
	}
	if s.lru.GetNode("k2") != nil {
		t.Errorf("LRU still has k2 after flushAll")
	}
	dynamicSize := s.memoryProfile.keyBytes + s.memoryProfile.valueBytes +
		s.memoryProfile.lruBytes + s.memoryProfile.ttlBytes + s.memoryProfile.pubsubBytes
	if dynamicSize != 0 {
		t.Errorf("memory profile dynamic fields not reset after flushAll, size = %d", dynamicSize)
	}
}

// ---- MEMORY STATS ----

// statsBody strips the RESP bulk-string framing and returns the raw stat lines.
func statsBody(t *testing.T, s *Store) string {
	t.Helper()
	raw := string(s.memoryStats(nil).Value)
	first := strings.Index(raw, "\r\n")
	if first == -1 {
		t.Fatalf("memoryStats: not a bulk string: %q", raw)
	}
	return raw[first+2 : len(raw)-2]
}

// assertStat checks that body contains "label: value".
func assertStat(t *testing.T, body, label, value string) {
	t.Helper()
	want := label + ": " + value
	if !strings.Contains(body, want) {
		t.Errorf("memoryStats: want %q in:\n%s", want, body)
	}
}

func TestMemoryStatsContainsAllLabels(t *testing.T) {
	body := statsBody(t, newTestStore())
	for _, label := range []string{
		"currentSize", "maxSize", "keyCount",
		"keySize", "valueSize", "ttlSize", "lruSize", "pubsubSize",
	} {
		if !strings.Contains(body, label) {
			t.Errorf("missing label %q in:\n%s", label, body)
		}
	}
}

func TestMemoryStatsEachStatOnOwnLine(t *testing.T) {
	body := statsBody(t, newTestStore())
	if lines := strings.Split(body, "\n"); len(lines) != 9 {
		t.Errorf("expected 9 stat lines, got %d:\n%s", len(lines), body)
	}
}

func TestMemoryStatsEmptyStore(t *testing.T) {
	body := statsBody(t, newTestStore())
	assertStat(t, body, "keyCount", "0")
	assertStat(t, body, "keySize", "0 B")
	assertStat(t, body, "valueSize", "0 B")
	assertStat(t, body, "ttlSize", "0 B")
	assertStat(t, body, "lruSize", "0 B")
	assertStat(t, body, "pubsubSize", "0 B")
	assertStat(t, body, "maxSize", "0 B")
}

func TestMemoryStatsExactValuesAfterSet(t *testing.T) {
	s := newTestStore()
	s.set([]string{"key", "val"}) // key=3B, value=3B

	expKeySize := fmt.Sprintf("%d B", constants.STRING_OVERHEAD+3)
	expValSize := fmt.Sprintf("%d B", constants.ENTRY_OVERHEAD+3)
	expLRUSize := fmt.Sprintf("%d B", constants.LRU_NODE_OVERHEAD+3)

	body := statsBody(t, s)
	assertStat(t, body, "keyCount", "1")
	assertStat(t, body, "keySize", expKeySize)
	assertStat(t, body, "valueSize", expValSize)
	assertStat(t, body, "lruSize", expLRUSize)
	assertStat(t, body, "ttlSize", "0 B")
	assertStat(t, body, "pubsubSize", "0 B")
}

func TestMemoryStatsKeySizeIncreasesWithEachSet(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k1", "v"})
	body1 := statsBody(t, s)

	s.set([]string{"k2", "v"})
	body2 := statsBody(t, s)

	assertStat(t, body1, "keyCount", "1")
	assertStat(t, body2, "keyCount", "2")

	// Each key: STRING_OVERHEAD + len("k1"/"k2") = 16+2 = 18 B
	perKey := constants.STRING_OVERHEAD + 2
	assertStat(t, body2, "keySize", fmt.Sprintf("%d B", 2*perKey))
}

func TestMemoryStatsValueSizeUpdatedOnOverwrite(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k", "short"})      // 5 B
	s.set([]string{"k", "muchlonger"}) // 10 B — overwrite, not new key

	expValSize := constants.ENTRY_OVERHEAD + int64(len("muchlonger"))

	body := statsBody(t, s)
	assertStat(t, body, "keyCount", "1") // still 1 key
	assertStat(t, body, "valueSize", fmt.Sprintf("%d B", expValSize))
}

func TestMemoryStatsDecreasesAfterDel(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k", "v"})
	s.del([]string{"k"})

	body := statsBody(t, s)
	assertStat(t, body, "keyCount", "0")
	assertStat(t, body, "keySize", "0 B")
	assertStat(t, body, "valueSize", "0 B")
	assertStat(t, body, "lruSize", "0 B")
}

func TestMemoryStatsTTLSizeAfterEX(t *testing.T) {
	s := newTestStore()
	s.set([]string{"mykey", "v", constants.EX, "10"}) // key = 5 B

	expTTL := fmt.Sprintf("%d B", constants.TTL_ITEM_OVERHEAD+int64(len("mykey")))

	body := statsBody(t, s)
	assertStat(t, body, "ttlSize", expTTL)
}

func TestMemoryStatsPubSubSizeAfterSubscribe(t *testing.T) {
	s := newTestStore()
	s.Subscribe("news") // topic = 4 B

	expPubSub := fmt.Sprintf("%d B",
		constants.STRING_OVERHEAD+int64(len("news"))+constants.BYTE_CHANNEL_OVERHEAD)

	body := statsBody(t, s)
	assertStat(t, body, "pubsubSize", expPubSub)
}

func TestMemoryStatsPubSubSizeDecreasesAfterUnsubscribe(t *testing.T) {
	s := newTestStore()
	ch := s.Subscribe("news")
	s.Unsubscribe("news", ch)

	body := statsBody(t, s)
	assertStat(t, body, "pubsubSize", "0 B")
}

func TestMemoryStatsCurrentSizeIsSum(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k", "v"})

	body := statsBody(t, s)
	expCurrent := fmt.Sprintf("%d B", s.memoryProfile.currentMemorySize())
	assertStat(t, body, "currentSize", expCurrent)
}

func TestMemoryStatsMaxSizeReflectsLimit(t *testing.T) {
	s := newTestStore()
	s.memoryProfile.maxBytes = 4096

	body := statsBody(t, s)
	assertStat(t, body, "maxSize", "4096 B")
}

func TestMemoryProfileGetStats(t *testing.T) {
	memProf := NewMemProfile(1000)
	e := &entry{value: []byte("val")}
	memProf.recordDataSize("key", e)

	stats := memProf.GetStats()

	if stats.KeyCount != 1 {
		t.Errorf("KeyCount = %d, want 1", stats.KeyCount)
	}
	if stats.KeyBytes <= 0 {
		t.Errorf("KeyBytes = %d, want > 0", stats.KeyBytes)
	}
	if stats.ValueBytes <= 0 {
		t.Errorf("ValueBytes = %d, want > 0", stats.ValueBytes)
	}
	if stats.MaxBytes != 1000 {
		t.Errorf("MaxBytes = %d, want 1000", stats.MaxBytes)
	}
	if stats.CurrentBytes <= 0 {
		t.Errorf("CurrentBytes = %d, want > 0 (includes fixed overhead)", stats.CurrentBytes)
	}
	if stats.Utilization <= 0 {
		t.Errorf("Utilization = %f, want > 0", stats.Utilization)
	}
}

func TestMemoryProfileGetStatsUnlimited(t *testing.T) {
	memProf := NewMemProfile(0)
	stats := memProf.GetStats()

	if stats.MaxBytes != 0 {
		t.Errorf("MaxBytes = %d, want 0", stats.MaxBytes)
	}
	if stats.Utilization != 0 {
		t.Errorf("Utilization = %f, want 0 when unlimited", stats.Utilization)
	}
}

func TestMemoryProfileGetStatsPeakBytesIsRetained(t *testing.T) {
	memProf := NewMemProfile(0)
	e := &entry{value: []byte("value")}
	memProf.recordDataSize("key", e)
	peakAfterAdd := memProf.peakBytes
	memProf.recordDataRemove("key", e)

	stats := memProf.GetStats()
	if stats.PeakBytes != peakAfterAdd {
		t.Errorf("PeakBytes = %d, want %d (should not drop after remove)", stats.PeakBytes, peakAfterAdd)
	}
}

// ---- SNAPSHOT ----

// Verifies capture copies every key's value and expiry into the returned map.
func TestSnapshotCopiesData(t *testing.T) {
	s := newTestStore()

	expiry := time.Now().Add(time.Hour)
	s.data["k1"] = &entry{value: []byte("v1")}
	s.data["k2"] = &entry{value: []byte("v2"), expiry: expiry}

	data, err := s.capture()
	if err != nil {
		t.Fatalf("capture err = %v, want nil", err)
	}
	if len(data) != 2 {
		t.Fatalf("capture len = %d, want 2", len(data))
	}
	if string(data["k1"].Value) != "v1" {
		t.Errorf("k1 value = %q, want %q", data["k1"].Value, "v1")
	}
	if !data["k1"].Expiry.IsZero() {
		t.Errorf("k1 expiry = %v, want zero", data["k1"].Expiry)
	}
	if string(data["k2"].Value) != "v2" {
		t.Errorf("k2 value = %q, want %q", data["k2"].Value, "v2")
	}
	if !data["k2"].Expiry.Equal(expiry) {
		t.Errorf("k2 expiry = %v, want %v", data["k2"].Expiry, expiry)
	}
}

// Verifies capturing an empty store yields an empty (non-nil) map.
func TestSnapshotEmptyStore(t *testing.T) {
	s := newTestStore()

	data, err := s.capture()
	if err != nil {
		t.Fatalf("capture err = %v, want nil", err)
	}
	if data == nil {
		t.Fatalf("capture data is nil, want empty map")
	}
	if len(data) != 0 {
		t.Errorf("capture len = %d, want 0", len(data))
	}
}

// Verifies the captured map is independent of the live store: mutating
// s.data after capture does not change the captured result.
func TestSnapshotIsDecoupledFromStore(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("v")}

	data, err := s.capture()
	if err != nil {
		t.Fatalf("capture err = %v, want nil", err)
	}

	// Add a new key after the snapshot was taken.
	s.data["new"] = &entry{value: []byte("late")}

	if _, ok := data["new"]; ok {
		t.Errorf("capture captured a key added after capture()")
	}
	if len(data) != 1 {
		t.Errorf("capture len = %d, want 1", len(data))
	}
}

// Verifies capture deep-copies value bytes, not just the map: mutating the
// live entry's underlying byte slice after capture must not corrupt the
// already-captured snapshot.
func TestSnapshotCaptureDeepCopiesValueBytes(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("value")}

	data, err := s.capture()
	if err != nil {
		t.Fatalf("capture err = %v, want nil", err)
	}

	s.data["k"].value[0] = 'X'

	if string(data["k"].Value) != "value" {
		t.Errorf("captured value = %q, want %q (capture must not alias the live entry's bytes)", data["k"].Value, "value")
	}
}

// ---- MSET ----

func TestMSetBasic(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.mset([]string{"k1", "v1", "k2", "v2"}), respSimple(constants.OK))

	if string(s.data["k1"].value) != "v1" {
		t.Errorf("k1 = %q, want v1", s.data["k1"].value)
	}
	if string(s.data["k2"].value) != "v2" {
		t.Errorf("k2 = %q, want v2", s.data["k2"].value)
	}
}

func TestMSetOverwritesExisting(t *testing.T) {
	s := newTestStore()
	s.mset([]string{"k", "old"})
	s.mset([]string{"k", "new"})
	if string(s.data["k"].value) != "new" {
		t.Errorf("k = %q, want new", s.data["k"].value)
	}
}

func TestMSetOddArgsError(t *testing.T) {
	s := newTestStore()
	resp := s.mset([]string{"k1", "v1", "k2"})
	want := respError(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Mset))
	assertValue(t, resp, want)
}

// Verifies MSET validates all arguments before writing anything: an invalid
// (odd-length) MSET must not create any new keys or disturb existing data.
func TestMSetOddArgsDoesNotPartiallyApply(t *testing.T) {
	s := newTestStore()
	s.set([]string{"sentinel", "untouched"})

	resp := s.mset([]string{"sentinel", "clobbered", "k1", "v1", "k2"})
	want := respError(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Mset))
	assertValue(t, resp, want)

	if string(s.data["sentinel"].value) != "untouched" {
		t.Errorf("sentinel = %q, want untouched (invalid MSET must not modify existing data)", s.data["sentinel"].value)
	}
	if len(s.data) != 1 {
		t.Errorf("data len = %d, want 1 (no new keys from an invalid MSET)", len(s.data))
	}
}

func TestMSetWrongArgs(t *testing.T) {
	s := newTestStore()
	resp := s.mset([]string{"k1"})
	want := respError(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Mset))
	assertValue(t, resp, want)
}

func TestMSetChargesMemoryForAllKeys(t *testing.T) {
	s := newTestStore()
	s.mset([]string{"k1", "v1", "k2", "v2"})
	if s.memoryProfile.keyCount != 2 {
		t.Errorf("keyCount = %d, want 2", s.memoryProfile.keyCount)
	}
}

// ---- MGET ----

func TestMGetBasic(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k1", "v1"})
	s.set([]string{"k2", "v2"})
	resp := s.mget([]string{"k1", "k2"})
	if !bytes.Contains(resp.Value, []byte("v1")) {
		t.Errorf("mget missing v1: %q", resp.Value)
	}
	if !bytes.Contains(resp.Value, []byte("v2")) {
		t.Errorf("mget missing v2: %q", resp.Value)
	}
}

func TestMGetMissingKeyReturnsNil(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k1", "v1"})
	resp := s.mget([]string{"k1", "missing"})
	if !bytes.Contains(resp.Value, []byte("v1")) {
		t.Errorf("mget missing v1: %q", resp.Value)
	}
	if !bytes.Contains(resp.Value, []byte(constants.NIL_DISPLAY)) {
		t.Errorf("mget missing nil for missing key: %q", resp.Value)
	}
}

func TestMGetExpiredKeyReturnsNil(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("v"), expiry: time.Now().Add(-1 * time.Second)}
	resp := s.mget([]string{"k"})
	if !bytes.Contains(resp.Value, []byte(constants.NIL_DISPLAY)) {
		t.Errorf("mget should return nil for expired key: %q", resp.Value)
	}
	if _, ok := s.data["k"]; ok {
		t.Errorf("expired key should be lazily deleted by mget")
	}
}

func TestMGetMovesLRUToFront(t *testing.T) {
	s := newTestStore()
	s.set([]string{"a", "1"})
	s.set([]string{"b", "2"})
	s.set([]string{"c", "3"}) // order: c(head) → b → a(tail)

	s.mget([]string{"a"}) // access "a" → moves to front

	tail, ok := s.lru.PeekBack()
	if !ok {
		t.Fatal("LRU is empty")
	}
	if tail != "b" {
		t.Errorf("tail after MGET a = %q, want 'b'", tail)
	}
}

func TestMGetWrongArgs(t *testing.T) {
	s := newTestStore()
	resp := s.mget([]string{})
	want := respError(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Mget))
	assertValue(t, resp, want)
}

// ---- INCR ----

func TestIncrMissingKeyInitialisesToOne(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.incr([]string{"k"}), respInt(1))
	if string(s.data["k"].value) != "1" {
		t.Errorf("value = %q, want 1", s.data["k"].value)
	}
}

func TestIncrIncrementsExistingKey(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k", "5"})
	assertValue(t, s.incr([]string{"k"}), respInt(6))
	if string(s.data["k"].value) != "6" {
		t.Errorf("value = %q, want 6", s.data["k"].value)
	}
}

func TestIncrNonIntegerError(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k", "notanint"})
	assertValue(t, s.incr([]string{"k"}), respError(constants.NOT_INTEGER))
}

func TestIncrExpiredKeyInitialisesToOne(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("5"), expiry: time.Now().Add(-1 * time.Second)}
	assertValue(t, s.incr([]string{"k"}), respInt(1))
}

func TestIncrPreservesExpiry(t *testing.T) {
	s := newTestStore()
	expiry := time.Now().Add(10 * time.Second)
	s.data["k"] = &entry{value: []byte("1"), expiry: expiry}
	s.incr([]string{"k"})
	if !s.data["k"].expiry.Equal(expiry) {
		t.Errorf("expiry changed after INCR")
	}
}

func TestIncrWrongArgs(t *testing.T) {
	s := newTestStore()
	resp := s.incr([]string{})
	want := respError(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Incr))
	assertValue(t, resp, want)
}

// Verifies INCR on a MaxInt64-valued key reports an error instead of
// silently wrapping to a negative number. NOT_INTEGER already documents the
// "or out of range" contract this exercises.
func TestIncrOverflowReturnsError(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("9223372036854775807")} // math.MaxInt64
	assertValue(t, s.incr([]string{"k"}), respError(constants.NOT_INTEGER))
}

// ---- DECR ----

func TestDecrMissingKeyInitialisesToMinusOne(t *testing.T) {
	s := newTestStore()
	assertValue(t, s.decr([]string{"k"}), respInt(-1))
	if string(s.data["k"].value) != "-1" {
		t.Errorf("value = %q, want -1", s.data["k"].value)
	}
}

func TestDecrDecrementsExistingKey(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k", "5"})
	assertValue(t, s.decr([]string{"k"}), respInt(4))
	if string(s.data["k"].value) != "4" {
		t.Errorf("value = %q, want 4", s.data["k"].value)
	}
}

func TestDecrNonIntegerError(t *testing.T) {
	s := newTestStore()
	s.set([]string{"k", "notanint"})
	assertValue(t, s.decr([]string{"k"}), respError(constants.NOT_INTEGER))
}

func TestDecrExpiredKeyInitialisesToMinusOne(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("5"), expiry: time.Now().Add(-1 * time.Second)}
	assertValue(t, s.decr([]string{"k"}), respInt(-1))
}

func TestDecrPreservesExpiry(t *testing.T) {
	s := newTestStore()
	expiry := time.Now().Add(10 * time.Second)
	s.data["k"] = &entry{value: []byte("1"), expiry: expiry}
	s.decr([]string{"k"})
	if !s.data["k"].expiry.Equal(expiry) {
		t.Errorf("expiry changed after DECR")
	}
}

func TestDecrWrongArgs(t *testing.T) {
	s := newTestStore()
	resp := s.decr([]string{})
	want := respError(fmt.Sprintf(constants.WRONG_NUM_ARGS, constants.Decr))
	assertValue(t, resp, want)
}

// Verifies DECR on a MinInt64-valued key reports an error instead of
// silently wrapping to a positive number.
func TestDecrOverflowReturnsError(t *testing.T) {
	s := newTestStore()
	s.data["k"] = &entry{value: []byte("-9223372036854775808")} // math.MinInt64
	assertValue(t, s.decr([]string{"k"}), respError(constants.NOT_INTEGER))
}
