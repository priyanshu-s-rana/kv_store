package sdk

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// topicSeq guarantees every call to uniqueTopic returns a distinct string,
// even across repeated runs of the same test within one process
// (go test -count=N reruns test functions in the same process).
var topicSeq atomic.Int64

// uniqueTopic returns a topic name that cannot collide with the same base
// name used by an earlier run of the same test. This matters because
// server-side unsubscribe cleanup (server.cleanupSubscription) runs
// asynchronously in a deferred call after a connection's read loop returns,
// triggered by the client closing its TCP connection — there is no
// synchronous "unsubscribe acknowledged" round trip. Under `-count=5` a
// fixed topic name can still have a stale subscriber registered from the
// previous run when the next run starts, corrupting subscriber-count and
// no-history assertions. Using a fresh topic per run sidesteps that
// timing dependency entirely rather than adding a wait for cleanup to land.
func uniqueTopic(base string) string {
	return fmt.Sprintf("%s-%d", base, topicSeq.Add(1))
}

// recvOrTimeout reads one message off sub's channel, failing the test if
// nothing arrives within timeout. Used throughout instead of a fixed sleep
// so tests only wait as long as actually needed.
func recvOrTimeout(t *testing.T, sub *Subscription, timeout time.Duration) string {
	t.Helper()
	select {
	case msg, ok := <-sub.Message():
		if !ok {
			t.Fatal("subscription channel closed unexpectedly")
		}
		return msg
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a message")
		return ""
	}
}

// expectNoMessage asserts nothing arrives on sub's channel within timeout —
// used to prove isolation (a subscriber to topic A must not see topic B's
// traffic) and no-duplicate-delivery guarantees.
func expectNoMessage(t *testing.T, sub *Subscription, timeout time.Duration) {
	t.Helper()
	select {
	case msg, ok := <-sub.Message():
		if ok {
			t.Fatalf("received unexpected message %q", msg)
		}
	case <-time.After(timeout):
		// expected: nothing arrived
	}
}

const shortWait = 300 * time.Millisecond

// ---- Pub/Sub ----

// TestSDKPubSubMultipleSubscribersSameTopic verifies every subscriber on a
// topic receives a published message — not just the first one registered.
func TestSDKPubSubMultipleSubscribersSameTopic(t *testing.T) {
	publisher := newTestClient(t)
	topic := uniqueTopic("fanout")

	const n = 4
	subs := make([]*Subscription, n)
	for i := 0; i < n; i++ {
		client, err := NewClient(testAddr)
		if err != nil {
			t.Fatalf("NewClient #%d: %v", i, err)
		}
		defer client.Close()

		sub, err := client.Subscribe(topic)
		if err != nil {
			t.Fatalf("Subscribe #%d: %v", i, err)
		}
		defer sub.Unsubscribe()
		recvOrTimeout(t, sub, shortWait) // drain subscribe ack
		subs[i] = sub
	}

	publisher.Publish(topic, "broadcast")

	for i, sub := range subs {
		msg := recvOrTimeout(t, sub, shortWait)
		if !strings.Contains(msg, "broadcast") {
			t.Errorf("subscriber #%d received %q, want it to contain broadcast", i, msg)
		}
	}
}

// TestSDKPubSubMultipleTopicsSingleSubscription verifies subscribing to
// several topics in one call delivers one subscribe acknowledgement per
// topic (in the requested order) before any real messages, and that
// publishes to each topic are correctly routed to the same subscription.
func TestSDKPubSubMultipleTopicsSingleSubscription(t *testing.T) {
	publisher := newTestClient(t)
	topicA := uniqueTopic("topic-a")
	topicB := uniqueTopic("topic-b")

	client, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	sub, err := client.Subscribe(topicA, topicB)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	// One subscribe acknowledgement is delivered per requested topic.
	ack1 := recvOrTimeout(t, sub, shortWait)
	ack2 := recvOrTimeout(t, sub, shortWait)
	acks := ack1 + "|" + ack2
	if !strings.Contains(acks, topicA) || !strings.Contains(acks, topicB) {
		t.Fatalf("subscribe acks = %q, want to mention both %q and %q", acks, topicA, topicB)
	}

	publisher.Publish(topicA, "msg-a")
	msgA := recvOrTimeout(t, sub, shortWait)
	if !strings.Contains(msgA, "msg-a") {
		t.Errorf("message = %q, want it to contain msg-a", msgA)
	}

	publisher.Publish(topicB, "msg-b")
	msgB := recvOrTimeout(t, sub, shortWait)
	if !strings.Contains(msgB, "msg-b") {
		t.Errorf("message = %q, want it to contain msg-b", msgB)
	}
}

// TestSDKPubSubUnsubscribeStopsDelivery verifies that after Unsubscribe, the
// message channel is closed and no further messages are delivered even if
// something is published to the same topic afterward.
func TestSDKPubSubUnsubscribeStopsDelivery(t *testing.T) {
	publisher := newTestClient(t)
	topic := uniqueTopic("temp-topic")

	client, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	sub, err := client.Subscribe(topic)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	recvOrTimeout(t, sub, shortWait) // drain ack

	sub.Unsubscribe()

	// The channel must close (not merely stay silent) once the connection
	// tears down, since Message() is documented to close on disconnect.
	deadline := time.After(2 * time.Second)
	closed := false
	for !closed {
		select {
		case _, ok := <-sub.Message():
			if !ok {
				closed = true
			}
		case <-deadline:
			t.Fatal("subscription channel did not close within timeout after Unsubscribe")
		}
	}

	publisher.Publish(topic, "should-not-be-delivered")
	// No assertion needed beyond not panicking/hanging: the channel is
	// closed, so there is nothing further to read.
}

// TestSDKPubSubOrderingPreservedForSingleTopic verifies sequential publishes
// to one topic arrive at a single subscriber in the same order they were
// sent, not reordered by the fan-in machinery.
func TestSDKPubSubOrderingPreservedForSingleTopic(t *testing.T) {
	publisher := newTestClient(t)
	topic := uniqueTopic("ordered-topic")

	client, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	sub, err := client.Subscribe(topic)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	recvOrTimeout(t, sub, shortWait) // drain ack

	const n = 20
	for i := 0; i < n; i++ {
		if _, err := publisher.Publish(topic, fmt.Sprintf("seq-%03d", i)); err != nil {
			t.Fatalf("Publish #%d: %v", i, err)
		}
	}

	for i := 0; i < n; i++ {
		msg := recvOrTimeout(t, sub, shortWait)
		want := fmt.Sprintf("seq-%03d", i)
		if !strings.Contains(msg, want) {
			t.Errorf("message #%d = %q, want it to contain %q", i, msg, want)
		}
	}
}

// TestSDKPublishBeforeSubscribeIsNotDelivered verifies the pub/sub system
// has no history/replay: a publish with zero subscribers is simply
// discarded, and a subscriber that joins afterward never sees it.
func TestSDKPublishBeforeSubscribeIsNotDelivered(t *testing.T) {
	publisher := newTestClient(t)
	topic := uniqueTopic("late-join-topic")

	resp, err := publisher.Publish(topic, "nobody-here")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if resp != "0" {
		t.Errorf("Publish with no subscribers = %q, want 0", resp)
	}

	client, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	sub, err := client.Subscribe(topic)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	recvOrTimeout(t, sub, shortWait) // drain ack

	publisher.Publish(topic, "actually-delivered")
	msg := recvOrTimeout(t, sub, shortWait)
	if !strings.Contains(msg, "actually-delivered") {
		t.Errorf("message = %q, want it to contain actually-delivered", msg)
	}
	if strings.Contains(msg, "nobody-here") {
		t.Errorf("message = %q, should not contain the pre-subscribe publish", msg)
	}
}

// TestSDKPubSubSubscriberIsolation verifies a subscriber on topic A never
// receives traffic published to topic B.
func TestSDKPubSubSubscriberIsolation(t *testing.T) {
	publisher := newTestClient(t)
	topicA := uniqueTopic("isolated-a")
	topicB := uniqueTopic("isolated-b")

	clientA, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient A: %v", err)
	}
	defer clientA.Close()
	subA, err := clientA.Subscribe(topicA)
	if err != nil {
		t.Fatalf("Subscribe A: %v", err)
	}
	defer subA.Unsubscribe()
	recvOrTimeout(t, subA, shortWait)

	clientB, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient B: %v", err)
	}
	defer clientB.Close()
	subB, err := clientB.Subscribe(topicB)
	if err != nil {
		t.Fatalf("Subscribe B: %v", err)
	}
	defer subB.Unsubscribe()
	recvOrTimeout(t, subB, shortWait)

	publisher.Publish(topicA, "only-for-a")

	msg := recvOrTimeout(t, subA, shortWait)
	if !strings.Contains(msg, "only-for-a") {
		t.Errorf("subA message = %q, want it to contain only-for-a", msg)
	}
	expectNoMessage(t, subB, shortWait)
}

// TestSDKPubSubNoDuplicateDelivery verifies a single publish results in
// exactly one message on the subscriber's channel, not two.
func TestSDKPubSubNoDuplicateDelivery(t *testing.T) {
	publisher := newTestClient(t)
	topic := uniqueTopic("dup-check")

	client, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()
	sub, err := client.Subscribe(topic)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	recvOrTimeout(t, sub, shortWait)

	publisher.Publish(topic, "once")
	msg := recvOrTimeout(t, sub, shortWait)
	if !strings.Contains(msg, "once") {
		t.Fatalf("message = %q, want it to contain once", msg)
	}
	expectNoMessage(t, sub, shortWait)
}

// TestSDKPublishReturnsSubscriberCount verifies Publish's return value
// tracks the number of currently-subscribed channels.
func TestSDKPublishReturnsSubscriberCount(t *testing.T) {
	publisher := newTestClient(t)
	topic := uniqueTopic("count-topic")

	var clients []*KVStoreClient
	var subs []*Subscription
	defer func() {
		for _, s := range subs {
			s.Unsubscribe()
		}
		for _, c := range clients {
			c.Close()
		}
	}()

	for i := 0; i < 3; i++ {
		c, err := NewClient(testAddr)
		if err != nil {
			t.Fatalf("NewClient #%d: %v", i, err)
		}
		clients = append(clients, c)
		sub, err := c.Subscribe(topic)
		if err != nil {
			t.Fatalf("Subscribe #%d: %v", i, err)
		}
		subs = append(subs, sub)
		recvOrTimeout(t, sub, shortWait)
	}

	resp, err := publisher.Publish(topic, "hello")
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if resp != "3" {
		t.Errorf("Publish delivered count = %q, want 3", resp)
	}
}

// TestSDKNewSubscriptionDirectDoesNotDeliverMessages documents an API
// inconsistency: NewSubscription is exported but, used on its own (without
// going through Client.Subscribe), it never sends a SUBSCRIBE command and
// never starts the goroutine that forwards server responses into the
// Message() channel. The channel simply blocks forever and Unsubscribe only
// closes the TCP connection without ever closing the channel — a caller
// using this exported constructor directly gets a connection that looks
// subscribed but delivers nothing.
// Classification: SDK API inconsistency / documentation gap. NewSubscription
// is only meaningfully usable internally via Client.Subscribe.
func TestSDKNewSubscriptionDirectDoesNotDeliverMessages(t *testing.T) {
	sub, err := NewSubscription(testAddr)
	if err != nil {
		t.Fatalf("NewSubscription: %v", err)
	}
	defer sub.Unsubscribe()

	select {
	case msg, ok := <-sub.Message():
		t.Fatalf("expected no message from a bare NewSubscription (no SUBSCRIBE was ever sent), got (%q, ok=%v)", msg, ok)
	case <-time.After(shortWait):
		// expected: NewSubscription alone never sends SUBSCRIBE, so nothing
		// is ever forwarded into Message().
	}
}

// TestSDKPubSubConcurrentSubscribersDistinctTopics exercises several
// independent subscriber clients concurrently, each on its own topic, and
// verifies every one receives exactly its own message with no cross-talk —
// a concurrency-flavored variant of the isolation guarantee.
//
// Functionally this test passes reliably under plain `go test`. Under
// `go test -race` it reliably reports a data race and this documents a real,
// pre-existing bug on the server side, not a flaw in the SDK or this test:
//
//   - server.registerSubscription/cleanupSubscription call store.Subscribe /
//     store.Unsubscribe directly from each connection's own goroutine
//     (server/server.go), instead of routing through the store's cmdChan /
//     single-threaded event loop the way every other command does.
//   - store.Subscribe and store.Unsubscribe mutate MemoryProfile counters
//     (pubsubBytes, activeTopics, activeSubscribers, etc. — store/commands.go,
//     store/memory_profile.go) with no synchronization. The store's `mut`
//     mutex only guards the `pubsub` map itself, not the memory-profile
//     bookkeeping.
//   - Two clients subscribing at the same time therefore race on those
//     int64 fields.
//
// Classification: Server bug (concurrency / data race). Message delivery,
// ordering, and isolation are unaffected — only the memory accounting
// bookkeeping races. Per the bug-handling policy, this is documented here
// rather than fixed, since fixing it means changing production code
// (routing Subscribe/Unsubscribe through the event loop, or synchronizing
// MemoryProfile independently). Expect `go test ./sdk -race` to fail on
// this specific test until that is addressed.
func TestSDKPubSubConcurrentSubscribersDistinctTopics(t *testing.T) {
	// Flush once up front via the shared test-managed client; each goroutine
	// below opens its own publisher and subscriber connections since the SDK
	// is not safe for concurrent use through a shared client.
	newTestClient(t)

	const n = 5
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			topic := uniqueTopic(fmt.Sprintf("concurrent-topic-%d", i))
			client, err := NewClient(testAddr)
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: NewClient: %w", i, err)
				return
			}
			defer client.Close()

			sub, err := client.Subscribe(topic)
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: Subscribe: %w", i, err)
				return
			}
			defer sub.Unsubscribe()

			select {
			case <-sub.Message(): // drain ack
			case <-time.After(shortWait):
				errs <- fmt.Errorf("goroutine %d: no subscribe ack", i)
				return
			}

			publisher, err := NewClient(testAddr)
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: NewClient publisher: %w", i, err)
				return
			}
			defer publisher.Close()

			publisher.Publish(topic, fmt.Sprintf("payload-%d", i))

			select {
			case msg := <-sub.Message():
				want := fmt.Sprintf("payload-%d", i)
				if !strings.Contains(msg, want) {
					errs <- fmt.Errorf("goroutine %d: message = %q, want it to contain %q", i, msg, want)
				}
			case <-time.After(shortWait):
				errs <- fmt.Errorf("goroutine %d: no message received", i)
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}
