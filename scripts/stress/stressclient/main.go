// Command stressclient drives sustained, invariant-checked workloads against
// a running kv-server instance, using the real SDK (github.com/priyanshu-s-rana/kv_store/sdk)
// exactly as a real application would. It is invoked by the bash scripts in
// scripts/stress/ — it is not a Go test and is never run by `go test`.
//
// Every mode prints "INVARIANT VIOLATION: <what> — <evidence>" and exits 1
// on the first violation it detects; it exits 0 after running for the
// configured duration with no violations found.
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/priyanshu-s-rana/kv_store/sdk"
)

func main() {
	mode := flag.String("mode", "", "workload mode: readwrite | ttlchurn | pubsubchurn | contention")
	addr := flag.String("addr", "127.0.0.1:5040", "kv-server address")
	metricsAddr := flag.String("metrics-addr", "", "kv-server metrics address (required for pubsubchurn leak check)")
	workers := flag.Int("workers", 20, "number of concurrent clients")
	duration := flag.Duration("duration", 10*time.Second, "how long to run the workload")
	flag.Parse()

	var err error
	switch *mode {
	case "readwrite":
		err = readWrite(*addr, *workers, *duration)
	case "ttlchurn":
		err = ttlChurn(*addr, *workers, *duration)
	case "pubsubchurn":
		err = pubsubChurn(*addr, *metricsAddr, *workers, *duration)
	case "contention":
		err = contention(*addr, *workers, *duration)
	default:
		fmt.Fprintf(os.Stderr, "unknown -mode %q (want readwrite|ttlchurn|pubsubchurn|contention)\n", *mode)
		os.Exit(2)
	}
	if err != nil {
		fmt.Println("INVARIANT VIOLATION:", err)
		os.Exit(1)
	}
	fmt.Printf("mode=%s: completed %s with no invariant violations\n", *mode, *duration)
}

// ============================================================
// readwrite: random SET/GET across a bounded keyspace, each worker owns a
// disjoint key range so a read-your-writes check is unambiguous (no other
// worker can mutate a key this worker is verifying).
// ============================================================

func readWrite(addr string, workers int, duration time.Duration) error {
	const keysPerWorker = 50
	deadline := time.Now().Add(duration)
	var wg sync.WaitGroup
	violations := make(chan error, workers)

	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			c, err := sdk.NewClient(addr)
			if err != nil {
				violations <- fmt.Errorf("worker %d: dial: %v", w, err)
				return
			}
			defer c.Close()

			ref := make(map[string]string, keysPerWorker)
			rng := rand.New(rand.NewSource(int64(w) + time.Now().UnixNano()))
			for time.Now().Before(deadline) {
				key := fmt.Sprintf("rw-w%d-k%d", w, rng.Intn(keysPerWorker))
				val := strconv.FormatInt(rng.Int63(), 10)
				if _, err := c.Set(key, val); err != nil {
					violations <- fmt.Errorf("worker %d: SET %s: %v", w, key, err)
					return
				}
				ref[key] = val

				// periodically verify a random previously-written key
				if rng.Intn(5) == 0 && len(ref) > 0 {
					i, target := rng.Intn(len(ref)), ""
					for k := range ref {
						if i == 0 {
							target = k
							break
						}
						i--
					}
					got, err := c.Get(target)
					if err != nil {
						violations <- fmt.Errorf("worker %d: GET %s: %v", w, target, err)
						return
					}
					if got != ref[target] {
						violations <- fmt.Errorf("worker %d: key %s = %q, want %q (last write by this worker was lost or overwritten by another worker's key — keyspaces are disjoint per worker)", w, target, got, ref[target])
						return
					}
				}
			}
		}(w)
	}

	wg.Wait()
	close(violations)
	for err := range violations {
		return err
	}
	return nil
}

// ============================================================
// ttlchurn: set-with-TTL, then verify expiry happens within a bounded
// window after the TTL elapses — neither too early nor unboundedly late.
// ============================================================

func ttlChurn(addr string, workers int, duration time.Duration) error {
	const graceWindow = 3 * time.Second
	deadline := time.Now().Add(duration)
	var wg sync.WaitGroup
	violations := make(chan error, workers)

	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			c, err := sdk.NewClient(addr)
			if err != nil {
				violations <- fmt.Errorf("worker %d: dial: %v", w, err)
				return
			}
			defer c.Close()

			rng := rand.New(rand.NewSource(int64(w) + time.Now().UnixNano()))
			for i := 0; time.Now().Before(deadline); i++ {
				key := fmt.Sprintf("ttl-w%d-i%d", w, i)
				ttlSecs := 1 + rng.Intn(2) // 1-2s
				if _, err := c.Set(key, "v", sdk.WithEX(ttlSecs)); err != nil {
					violations <- fmt.Errorf("worker %d: SET %s EX %d: %v", w, key, ttlSecs, err)
					return
				}

				expiryTime := time.Now().Add(time.Duration(ttlSecs) * time.Second)
				time.Sleep(time.Duration(ttlSecs)*time.Second + graceWindow)

				got, err := c.Get(key)
				if err != nil {
					violations <- fmt.Errorf("worker %d: GET %s after expiry: %v", w, key, err)
					return
				}
				if got != "nil" {
					violations <- fmt.Errorf("worker %d: key %s still present %s after its %ds TTL elapsed (grace window %s) — value=%q", w, key, time.Since(expiryTime), ttlSecs, graceWindow, got)
					return
				}
			}
		}(w)
	}

	wg.Wait()
	close(violations)
	for err := range violations {
		return err
	}
	return nil
}

// ============================================================
// pubsubchurn: subscribe/publish/unsubscribe loops, then assert (via the
// metrics endpoint) that active topics/subscribers return to zero — a
// leaked subscription would show as a gauge that never drains back down.
//
// Finding this test process confirmed: server.go's forwardMessages never
// actively reads its connection, so it only discovers a disconnected
// subscriber the next time it tries to *write* to it — cleanup is entirely
// write-failure-triggered, not read/disconnect-triggered, and one failed
// write isn't always enough (a write immediately after the peer closes can
// succeed once before the RST arrives). In this workload, where every
// worker keeps publishing to its own topic, that lag is invisible — the
// next publish always flushes it. The real-world implication is narrower
// but genuine: a subscriber that disconnects from a topic nobody publishes
// to again (an idle topic) leaves its goroutine, channel, and
// active_subscribers/active_topics accounting registered indefinitely,
// with nothing to ever trigger the discovery. This harness's own
// leak-check therefore does two rounds of flush-publishes (see below)
// before concluding a real leak — without them, this workload alone would
// misreport the expected end-of-run lag as a leak.
// ============================================================

func pubsubChurn(addr, metricsAddr string, workers int, duration time.Duration) error {
	if metricsAddr == "" {
		return fmt.Errorf("pubsubchurn requires -metrics-addr for the leak check")
	}

	deadline := time.Now().Add(duration)
	var wg sync.WaitGroup
	var churns atomic.Int64
	violations := make(chan error, workers)

	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			pub, err := sdk.NewClient(addr)
			if err != nil {
				violations <- fmt.Errorf("worker %d: dial publisher: %v", w, err)
				return
			}
			defer pub.Close()

			topic := fmt.Sprintf("stress-topic-%d", w)
			for time.Now().Before(deadline) {
				sub, err := pub.Subscribe(topic)
				if err != nil {
					violations <- fmt.Errorf("worker %d: SUBSCRIBE %s: %v", w, topic, err)
					return
				}
				if _, err := pub.Publish(topic, "ping"); err != nil {
					violations <- fmt.Errorf("worker %d: PUBLISH %s: %v", w, topic, err)
					return
				}
				select {
				case <-sub.Message():
				case <-time.After(2 * time.Second):
					violations <- fmt.Errorf("worker %d: did not receive own publish on %s within 2s", w, topic)
					return
				}
				sub.Unsubscribe()
				churns.Add(1)
			}
		}(w)
	}
	wg.Wait()
	close(violations)
	for err := range violations {
		return err
	}

	// server.go's forwardMessages only discovers a closed client connection
	// when it next tries to write to it — it never actively reads, so a
	// subscriber that disconnects between publishes leaves its subscription
	// (and fan-in goroutine) registered until something publishes to that
	// same topic again. Each worker's very last Unsubscribe of the loop
	// above has no *following* publish on its topic to trigger that
	// cleanup, so one lingering subscription per topic is the *expected*
	// steady state at loop-exit, not yet a leak. A single flush publish per
	// topic forces that discovery; if metrics still don't drain to 0 after
	// that, it's a genuine leak rather than this lazy-cleanup lag.
	flush, err := sdk.NewClient(addr)
	if err != nil {
		return fmt.Errorf("dial flush publisher: %v", err)
	}
	defer flush.Close()

	// A write to a connection whose peer already closed its side doesn't
	// always fail on the very first attempt (a well-known TCP quirk: the
	// local OS may accept one write into its send buffer before the RST
	// from the closed peer arrives). Flushing every topic twice, with a
	// short pause between rounds, gives that RST time to arrive so the
	// *second* write reliably observes the failure.
	flushAll := func() {
		for w := range workers {
			_, _ = flush.Publish(fmt.Sprintf("stress-topic-%d", w), "flush")
		}
	}
	flushAll()
	time.Sleep(200 * time.Millisecond)
	flushAll()

	// The last Unsubscribe on each worker races the connection teardown;
	// give the server a bounded moment to process the final cleanup before
	// declaring a leak.
	deadline = time.Now().Add(5 * time.Second)
	var lastTopics, lastSubs float64
	for time.Now().Before(deadline) {
		lastTopics, _ = scrapeMetric(metricsAddr, "kv_store_active_topics")
		lastSubs, _ = scrapeMetric(metricsAddr, "kv_store_active_subscribers")
		if lastTopics == 0 && lastSubs == 0 {
			fmt.Printf("pubsubchurn: %d churns across %d workers, active_topics/active_subscribers drained to 0\n", churns.Load(), workers)
			return nil
		}
		flushAll() // keep nudging in case more writes are needed to observe every stale connection

		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("goroutine/subscription leak: active_topics=%v active_subscribers=%v did not drain to 0 within 5s of the last unsubscribe (%d churns across %d workers)", lastTopics, lastSubs, churns.Load(), workers)
}

func scrapeMetric(metricsAddr, name string) (float64, bool) {
	resp, err := http.Get("http://" + metricsAddr + "/metrics")
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 64*1024)
	tmp := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
	}
	for _, line := range strings.Split(string(buf), "\n") {
		if strings.HasPrefix(line, name+" ") {
			fields := strings.Fields(line)
			v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
			if err == nil {
				return v, true
			}
		}
	}
	return 0, false
}

// ============================================================
// contention: many clients incrementing one shared counter concurrently.
// There is no server-enforced lock primitive in this codebase (grep finds
// only the "lock-released:<key>" pub/sub notification convention — see the
// full test suite report), so this exercises the closest real analogue:
// mutual exclusion via the single-threaded event loop's serialization of
// INCR. A lost update here would mean the event-loop consistency boundary
// itself is broken.
// ============================================================

func contention(addr string, workers int, duration time.Duration) error {
	deadline := time.Now().Add(duration)
	var wg sync.WaitGroup
	var totalIncrs atomic.Int64
	violations := make(chan error, workers)

	counterKey := fmt.Sprintf("contention-counter-%d", time.Now().UnixNano())
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			c, err := sdk.NewClient(addr)
			if err != nil {
				violations <- fmt.Errorf("worker %d: dial: %v", w, err)
				return
			}
			defer c.Close()

			for time.Now().Before(deadline) {
				if _, err := c.Incr(counterKey); err != nil {
					violations <- fmt.Errorf("worker %d: INCR: %v", w, err)
					return
				}
				totalIncrs.Add(1)
			}
		}(w)
	}
	wg.Wait()
	close(violations)
	for err := range violations {
		return err
	}

	verifier, err := sdk.NewClient(addr)
	if err != nil {
		return fmt.Errorf("dial verifier: %v", err)
	}
	defer verifier.Close()

	got, err := verifier.Get(counterKey)
	if err != nil {
		return fmt.Errorf("GET %s: %v", counterKey, err)
	}
	gotN, err := strconv.ParseInt(got, 10, 64)
	if err != nil {
		return fmt.Errorf("counter %s = %q, not an integer: %v", counterKey, got, err)
	}
	if want := totalIncrs.Load(); gotN != want {
		return fmt.Errorf("counter %s = %d, want %d (%d INCRs from %d workers were acknowledged) — a mismatch means concurrent INCRs were lost, i.e. the event loop failed to serialize them", counterKey, gotN, want, want, workers)
	}
	fmt.Printf("contention: %d workers, %d total INCRs, counter matches exactly\n", workers, totalIncrs.Load())
	return nil
}
