package sdk

import (
	"fmt"
	"strconv"
	"sync"
	"testing"
)

// ---- SDK Concurrency ----
//
// The SDK is NOT safe for concurrent use through a single shared client:
// KVStoreClient wraps one net.Conn and one bufio.Reader with no locking, so
// concurrent goroutines issuing commands on the same *KVStoreClient would
// interleave writes and misalign reads of the responses. None of the tests
// below share a client across goroutines — each goroutine dials its own
// connection via NewClient, consistent with that constraint. What IS
// expected to be safe is many independent clients concurrently talking to
// the same server, since the server's store commands (SET/GET/DEL/MSET/
// INCR/DECR — everything except SUBSCRIBE/UNSUBSCRIBE, see
// sdk_pubsub_test.go for that exception) are serialized through the store's
// single-threaded event loop (store/store.go eventLoop, fed by cmdChan).

// runConcurrently runs n goroutines calling fn(i), collecting any error each
// reports, and fails the test with all collected errors after every
// goroutine finishes. Keeps every concurrency test below symmetric and
// avoids calling t.Fatal from non-test goroutines (unsafe).
func runConcurrently(t *testing.T, n int, fn func(i int) error) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := fn(i); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestSDKConcurrentSetDistinctKeys verifies many goroutines, each owning its
// own client, can SET distinct keys at the same time with no lost writes and
// no cross-contamination between keys.
func TestSDKConcurrentSetDistinctKeys(t *testing.T) {
	verifier := newTestClient(t)
	const n = 50

	runConcurrently(t, n, func(i int) error {
		c, err := NewClient(testAddr)
		if err != nil {
			return fmt.Errorf("goroutine %d: NewClient: %w", i, err)
		}
		defer c.Close()

		key := fmt.Sprintf("conc-set-%03d", i)
		val := fmt.Sprintf("val-%03d", i)
		if _, err := c.Set(key, val); err != nil {
			return fmt.Errorf("goroutine %d: Set: %w", i, err)
		}
		return nil
	})

	for i := 0; i < n; i++ {
		key := fmt.Sprintf("conc-set-%03d", i)
		want := fmt.Sprintf("val-%03d", i)
		got, err := verifier.Get(key)
		if err != nil {
			t.Fatalf("Get(%s): %v", key, err)
		}
		if got != want {
			t.Errorf("Get(%s) = %q, want %q", key, got, want)
		}
	}
}

// TestSDKConcurrentGetSameKey verifies many goroutines can concurrently GET
// the same key, each on its own client, and all observe the correct value
// with no errors.
func TestSDKConcurrentGetSameKey(t *testing.T) {
	setup := newTestClient(t)
	setup.Set("shared-read-key", "shared-value")

	const n = 50
	runConcurrently(t, n, func(i int) error {
		c, err := NewClient(testAddr)
		if err != nil {
			return fmt.Errorf("goroutine %d: NewClient: %w", i, err)
		}
		defer c.Close()

		got, err := c.Get("shared-read-key")
		if err != nil {
			return fmt.Errorf("goroutine %d: Get: %w", i, err)
		}
		if got != "shared-value" {
			return fmt.Errorf("goroutine %d: Get = %q, want shared-value", i, got)
		}
		return nil
	})
}

// TestSDKConcurrentDelDistinctKeys verifies many goroutines can concurrently
// DEL distinct pre-existing keys, each on its own client, with every key
// ending up removed.
func TestSDKConcurrentDelDistinctKeys(t *testing.T) {
	setup := newTestClient(t)
	const n = 50
	for i := 0; i < n; i++ {
		setup.Set(fmt.Sprintf("conc-del-%03d", i), "v")
	}

	runConcurrently(t, n, func(i int) error {
		c, err := NewClient(testAddr)
		if err != nil {
			return fmt.Errorf("goroutine %d: NewClient: %w", i, err)
		}
		defer c.Close()

		key := fmt.Sprintf("conc-del-%03d", i)
		resp, err := c.Del(key)
		if err != nil {
			return fmt.Errorf("goroutine %d: Del: %w", i, err)
		}
		if resp != "1" {
			return fmt.Errorf("goroutine %d: Del(%s) = %q, want 1", i, key, resp)
		}
		return nil
	})

	for i := 0; i < n; i++ {
		key := fmt.Sprintf("conc-del-%03d", i)
		got, err := setup.Get(key)
		if err != nil {
			t.Fatalf("Get(%s): %v", key, err)
		}
		if got != "nil" {
			t.Errorf("Get(%s) after concurrent Del = %q, want nil", key, got)
		}
	}
}

// TestSDKConcurrentMSetDistinctBatches verifies many goroutines can
// concurrently MSET disjoint batches of keys, each on its own client, with
// every pair from every batch landing correctly.
func TestSDKConcurrentMSetDistinctBatches(t *testing.T) {
	verifier := newTestClient(t)
	const goroutines = 20
	const pairsPerGoroutine = 5

	runConcurrently(t, goroutines, func(i int) error {
		c, err := NewClient(testAddr)
		if err != nil {
			return fmt.Errorf("goroutine %d: NewClient: %w", i, err)
		}
		defer c.Close()

		args := make([]string, 0, pairsPerGoroutine*2)
		for j := 0; j < pairsPerGoroutine; j++ {
			key := fmt.Sprintf("conc-mset-%03d-%d", i, j)
			val := fmt.Sprintf("val-%03d-%d", i, j)
			args = append(args, key, val)
		}
		if _, err := c.MSet(args...); err != nil {
			return fmt.Errorf("goroutine %d: MSet: %w", i, err)
		}
		return nil
	})

	for i := 0; i < goroutines; i++ {
		for j := 0; j < pairsPerGoroutine; j++ {
			key := fmt.Sprintf("conc-mset-%03d-%d", i, j)
			want := fmt.Sprintf("val-%03d-%d", i, j)
			got, err := verifier.Get(key)
			if err != nil {
				t.Fatalf("Get(%s): %v", key, err)
			}
			if got != want {
				t.Errorf("Get(%s) = %q, want %q", key, got, want)
			}
		}
	}
}

// TestSDKConcurrentIncrSameKey is the strongest correctness test in this
// section: many goroutines, each on its own client, all INCR the *same*
// shared key at the same time. Because every command is serialized through
// the store's single-threaded event loop, no increment should ever be lost
// — the final value must equal exactly goroutines * incrementsEach.
func TestSDKConcurrentIncrSameKey(t *testing.T) {
	setup := newTestClient(t)
	const goroutines = 20
	const incrementsEach = 15
	const want = goroutines * incrementsEach

	runConcurrently(t, goroutines, func(i int) error {
		c, err := NewClient(testAddr)
		if err != nil {
			return fmt.Errorf("goroutine %d: NewClient: %w", i, err)
		}
		defer c.Close()

		for j := 0; j < incrementsEach; j++ {
			if _, err := c.Incr("shared-counter"); err != nil {
				return fmt.Errorf("goroutine %d incr %d: %w", i, j, err)
			}
		}
		return nil
	})

	got, err := setup.Get("shared-counter")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != strconv.Itoa(want) {
		t.Errorf("shared-counter = %q, want %d (no increments lost under concurrency)", got, want)
	}
}

// TestSDKConcurrentMixedOperations runs a mix of SET, GET, DEL, and INCR
// concurrently across many independently-owned clients against a shared
// small keyspace, verifying no panics, no deadlocks, and no unexpected
// errors — a general-purpose stress pass rather than an exact-state check
// (the whole point is that operations interleave unpredictably).
func TestSDKConcurrentMixedOperations(t *testing.T) {
	setup := newTestClient(t)
	const keyspace = 10
	for i := 0; i < keyspace; i++ {
		setup.Set(fmt.Sprintf("mixed-%d", i), "initial")
	}

	const n = 60
	runConcurrently(t, n, func(i int) error {
		c, err := NewClient(testAddr)
		if err != nil {
			return fmt.Errorf("goroutine %d: NewClient: %w", i, err)
		}
		defer c.Close()

		key := fmt.Sprintf("mixed-%d", i%keyspace)
		switch i % 4 {
		case 0:
			if _, err := c.Set(key, fmt.Sprintf("from-%d", i)); err != nil {
				return fmt.Errorf("goroutine %d: Set: %w", i, err)
			}
		case 1:
			if _, err := c.Get(key); err != nil {
				return fmt.Errorf("goroutine %d: Get: %w", i, err)
			}
		case 2:
			if _, err := c.Del(key); err != nil {
				return fmt.Errorf("goroutine %d: Del: %w", i, err)
			}
		case 3:
			if _, err := c.Incr("mixed-counter"); err != nil {
				return fmt.Errorf("goroutine %d: Incr: %w", i, err)
			}
		}
		return nil
	})

	// The keyspace itself may be in any state (some keys deleted, some
	// overwritten) since operations raced by design — but the server must
	// still be responsive and consistent afterward.
	if resp, err := setup.Ping(); err != nil || resp != "PONG" {
		t.Fatalf("Ping after mixed concurrency = (%q, %v), want (PONG, nil)", resp, err)
	}
	got, err := setup.Get("mixed-counter")
	if err != nil {
		t.Fatalf("Get(mixed-counter): %v", err)
	}
	// n/4 goroutines took the Incr branch (i%4==3).
	wantIncrCount := 0
	for i := 0; i < n; i++ {
		if i%4 == 3 {
			wantIncrCount++
		}
	}
	if got != strconv.Itoa(wantIncrCount) {
		t.Errorf("mixed-counter = %q, want %d", got, wantIncrCount)
	}
}
