package sdk

import (
	"fmt"
	"sync"
	"testing"
)

// ---- Ping ----

func TestSDKPingRepeatedSequential(t *testing.T) {
	c := newTestClient(t)
	for i := 0; i < 20; i++ {
		resp, err := c.Ping()
		if err != nil {
			t.Fatalf("Ping #%d: %v", i, err)
		}
		if resp != "PONG" {
			t.Fatalf("Ping #%d = %q, want PONG", i, resp)
		}
	}
}

// TestSDKPingConcurrentIndependentClients spins up many clients, each on its
// own goroutine, and Pings once. Each goroutine owns its client exclusively,
// consistent with the documented "no shared client across goroutines" rule.
func TestSDKPingConcurrentIndependentClients(t *testing.T) {
	const n = 25
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := NewClient(testAddr)
			if err != nil {
				errs <- fmt.Errorf("NewClient: %w", err)
				return
			}
			defer c.Close()

			resp, err := c.Ping()
			if err != nil {
				errs <- fmt.Errorf("Ping: %w", err)
				return
			}
			if resp != "PONG" {
				errs <- fmt.Errorf("Ping = %q, want PONG", resp)
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent ping: %v", err)
	}
}
