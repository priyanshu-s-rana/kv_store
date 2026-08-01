//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

// Invariant: a key that expired before the crash must never resurrect after
// restart, whether it expired via lazy (read-time) or active (ttlEviction
// goroutine) expiry.
//
// This test originally documented a real bug: EXPIRE was normalized to an
// absolute deadline (PEXPIREAT) before being appended to the journal, but
// replaying a PEXPIREAT whose deadline had since passed made the handler
// return an error — and AOF.Replay aborted the *entire rest of the journal
// file* on any command error, silently dropping every subsequent write, not
// just mishandling the expired key. Fixed by giving "deadline already
// elapsed" its own distinct error (constants.ALRDY_EXPIRED, separate from
// genuine malformed-input errors) and having AOF.Replay convert that
// specific case into a DEL for the key instead of aborting — see
// persistence.handleExpiredKeyReplayError and its call site in AOF.Replay.
func TestRestartAfterTTLExpiration(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	in := startInstance(t, dataDir, constants.SyncAlways)
	c := in.dial()
	c.mustDo("SET", "persists", "yes")
	c.mustDo("SET", "expiring", "soon")
	c.mustDo("EXPIRE", "expiring", "1")

	waitUntilExpired(t, c, "expiring", 5*time.Second)

	in.sigkill()
	in.waitExit(5 * time.Second)

	fresh := startInstance(t, dataDir, constants.SyncAlways)
	fc := fresh.dial()
	if got := fc.mustDo("GET", "expiring"); got != "nil" {
		t.Errorf("expired key resurrected after restart: GET expiring = %q, want nil", got)
	}
	if got := fc.mustDo("GET", "persists"); got != "yes" {
		t.Errorf("GET persists = %q, want yes — unrelated key must be unaffected by sibling expiry", got)
	}
}

// waitUntilExpired polls TTL(key) until it reports -2 (gone), bounded by timeout.
func waitUntilExpired(t *testing.T, c *rawClient, key string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := c.mustDo("TTL", key); got == "-2" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("key %q did not expire within %s", key, timeout)
}

// Invariant: pub/sub state is purely in-memory and must not block or corrupt
// a restart. A subscriber connection open at crash time must simply be
// dropped; the fresh process must come up with a clean slate (zero active
// subscribers) and serve new subscriptions normally.
func TestRestartWithActivePubSubSubscriber(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	in := startInstance(t, dataDir, constants.SyncAlways)
	sub := in.dial()
	if _, err := sub.do("SUBSCRIBE", "topic-a"); err != nil {
		t.Fatalf("SUBSCRIBE: %v", err)
	}
	waitForMetricAtLeast(t, in.metricsAddr, "kv_store_active_subscribers", 1, 2*time.Second)

	in.sigkill() // subscriber connection dies with the process, uncleanly
	in.waitExit(5 * time.Second)

	fresh := startInstance(t, dataDir, constants.SyncAlways)
	if v, ok := metricValue(t, fresh.metricsAddr, "kv_store_active_subscribers"); ok && v != 0 {
		t.Errorf("fresh process active_subscribers = %v, want 0", v)
	}

	pub := fresh.dial()
	newSub := fresh.dial()
	if _, err := newSub.do("SUBSCRIBE", "topic-a"); err != nil {
		t.Fatalf("SUBSCRIBE after restart: %v", err)
	}
	if got := pub.mustDo("PUBLISH", "topic-a", "hello"); got != "1" {
		t.Errorf("PUBLISH after restart delivered to %s subscribers, want 1 — fresh subscription must work normally", got)
	}
}

// Invariant: under the weaker "everysec" durability policy, a write followed
// immediately by a hard kill MAY be lost (that's the documented tradeoff —
// fsync happens on a 1s ticker, not per-write) but must NEVER corrupt the
// journal or prevent the server from starting cleanly afterward. We
// deliberately do not assert whether the racing write survived (that
// outcome is a real race against the fsync ticker); we assert the two things
// that must hold unconditionally.
func TestSyncEverySecNeverCorruptsOnImmediateKill(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()

	in := startInstance(t, dataDir, constants.SyncEverySec)
	c := in.dial()
	c.mustDo("SET", "racing-write", "maybe-lost")
	in.sigkill() // immediately, without waiting for the 1s fsync ticker
	in.waitExit(5 * time.Second)

	fresh := startInstance(t, dataDir, constants.SyncEverySec)
	fc := fresh.dial()
	if got := fc.mustDo("PING"); got != "PONG" {
		t.Fatalf("PING after everysec crash-restart = %q, want PONG — journal must never be left unreadable", got)
	}
	// Durability continues to work normally post-restart.
	if got := fc.mustDo("SET", "after-restart", "value"); got != "OK" {
		t.Fatalf("SET after restart = %q, want OK", got)
	}
	if got := fc.mustDo("GET", "after-restart"); got != "value" {
		t.Errorf("GET after-restart = %q, want value", got)
	}
}
