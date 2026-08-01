//go:build integration

package integration

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

// Invariant: recovery must apply exactly the acknowledged writes, in a way
// that preserves their cumulative effect — never replaying a command twice,
// never dropping one, never applying it out of order relative to its
// dependencies. INCR on a single shared counter makes any reordering,
// duplication, or drop directly observable as a wrong final count: unlike
// independent SETs (where a lost or duplicated write on an unrelated key
// wouldn't corrupt other keys), every INCR is order- and count-sensitive
// against the same value.
//
// This complements persistence.TestPropertyRandomizedRecoveryConvergesToExpectedState
// (in-process, randomized) with a real subprocess crash under concurrent
// real clients.
func TestRecoveryPreservesIncrementOrdering(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	in := startInstance(t, dataDir, constants.SyncAlways)

	const clients = 10
	const incrsPerClient = 25
	total := 0

	runIncrBurst := func() {
		var wg sync.WaitGroup
		for range clients {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c := in.dial()
				for range incrsPerClient {
					// See crash_cycles_test.go: mustDo/Fatalf is unsafe from
					// a non-test goroutine, so report failures via Errorf.
					if _, err := c.do("INCR", "counter"); err != nil {
						t.Errorf("INCR counter: %v", err)
					}
				}
			}()
		}
		wg.Wait()
		total += clients * incrsPerClient
	}

	runIncrBurst()
	in.checkpointAndWait(in.dial())
	runIncrBurst() // more writes land in the post-checkpoint journal tail

	in.sigkill()
	in.waitExit(5 * time.Second)

	fresh := startInstance(t, dataDir, constants.SyncAlways)
	fc := fresh.dial()
	got := fc.mustDo("GET", "counter")
	gotN, err := strconv.Atoi(got)
	if err != nil {
		t.Fatalf("GET counter = %q, not an integer: %v", got, err)
	}
	if gotN != total {
		t.Errorf("recovered counter = %d, want %d — recovery must apply exactly the acknowledged increments, in order, with no duplication or loss", gotN, total)
	}
}
