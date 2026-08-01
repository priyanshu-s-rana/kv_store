//go:build integration

package integration

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

// Invariant: repeated crash/restart cycles against the same data directory
// must never lose a previously-acknowledged write, no matter how many times
// the process has been killed and restarted before. Each iteration writes a
// new, uniquely-keyed value and restarts; every prior iteration's value must
// still be there.
func TestRepeatedCrashRestartCycles(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	const iterations = 10
	want := make(map[string]string, iterations)

	for i := range iterations {
		in := startInstance(t, dataDir, constants.SyncAlways)
		c := in.dial()

		for k, v := range want {
			if got := c.mustDo("GET", k); got != v {
				t.Fatalf("iteration %d: recovered %s = %q, want %q", i, k, got, v)
			}
		}

		key := fmt.Sprintf("cycle-%d", i)
		val := fmt.Sprintf("val-%d", i)
		if got := c.mustDo("SET", key, val); got != "OK" {
			t.Fatalf("iteration %d: SET %s: got %q", i, key, got)
		}
		want[key] = val

		in.sigkill()
		in.waitExit(5 * time.Second)
	}

	final := startInstance(t, dataDir, constants.SyncAlways)
	fc := final.dial()
	for k, v := range want {
		if got := fc.mustDo("GET", k); got != v {
			t.Errorf("final restart: recovered %s = %q, want %q", k, got, v)
		}
	}
}

// Invariant: writes from multiple concurrent clients racing against an
// in-flight checkpoint must be neither lost nor torn. The checkpoint
// captures a point-in-time snapshot of the map while the event loop
// continues serializing new writes (checkpoint/rotation happen synchronously
// inside the same single-threaded event loop as every other command), so
// every write — whether captured by the snapshot or landing in the
// post-rotation journal — must survive a crash after the burst.
func TestConcurrentWritesDuringCheckpointNotLostOrTorn(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	in := startInstance(t, dataDir, constants.SyncAlways)

	const writers = 20
	const keysPerWriter = 10

	var wg sync.WaitGroup
	start := make(chan struct{})
	want := make(map[string]string, writers*keysPerWriter)
	var mu sync.Mutex

	for w := range writers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			c := in.dial()
			<-start
			for i := range keysPerWriter {
				key := fmt.Sprintf("w%d-k%d", w, i)
				val := fmt.Sprintf("w%d-v%d", w, i)
				// t.Fatalf/mustDo must not be called from a non-test
				// goroutine (testing.T.FailNow calls runtime.Goexit, which
				// is only valid on the test's own goroutine), so errors are
				// reported via t.Errorf instead.
				got, err := c.do("SET", key, val)
				if err != nil || got != "OK" {
					t.Errorf("writer %d: SET %s: got %q, err %v", w, key, got, err)
					continue
				}
				mu.Lock()
				want[key] = val
				mu.Unlock()
			}
		}(w)
	}

	// Trigger a couple of checkpoints concurrently with the write burst.
	wg.Add(1)
	go func() {
		defer wg.Done()
		checkpointer := in.dial()
		<-start
		for range 3 {
			// Best-effort: CHECKPOINT may return an error if one is already
			// in progress (by design — see persistence.Checkpoint), which is
			// not a failure of this test, just a missed opportunity to
			// checkpoint mid-burst.
			_, _ = checkpointer.do("CHECKPOINT")
			time.Sleep(20 * time.Millisecond)
		}
	}()

	close(start)
	wg.Wait()

	// Ensure a final checkpoint settles everything before the crash, so the
	// assertion isolates "writes during checkpoint" from "writes never
	// checkpointed at all" (the latter is already covered by
	// TestJournalReplayRecoversWritesWithoutCheckpoint).
	in.checkpointAndWait(in.dial())

	in.sigkill()
	in.waitExit(5 * time.Second)

	fresh := startInstance(t, dataDir, constants.SyncAlways)
	fc := fresh.dial()
	for k, v := range want {
		if got := fc.mustDo("GET", k); got != v {
			t.Errorf("recovered %s = %q, want %q — concurrent write during checkpoint was lost or torn", k, got, v)
		}
	}
}
