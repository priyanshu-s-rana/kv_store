package sdk

import (
	"testing"
	"time"
)

// pollUntil repeatedly calls cond every interval until it returns true or
// timeout elapses. Used instead of a fixed sleep so TTL expiration tests are
// bounded but not tied to a hardcoded wait longer than necessary.
func pollUntil(t *testing.T, timeout, interval time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(interval)
	}
	return cond()
}

// ---- Expire / TTL ----

// TestSDKExpireMissingKeyReturnsZero verifies Expire on a key that does not
// exist reports 0 and does not create the key.
func TestSDKExpireMissingKeyReturnsZero(t *testing.T) {
	c := newTestClient(t)
	resp, err := c.Expire("does-not-exist", 10)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if resp != "0" {
		t.Errorf("Expire(missing) = %q, want 0", resp)
	}
	got, _ := c.Get("does-not-exist")
	if got != "nil" {
		t.Errorf("Expire on missing key should not create it: Get = %q", got)
	}
}

// TestSDKExpirePollingActualExpiration sets a 1-second TTL and polls Get
// until the key is lazily evicted, verifying real expiration behavior end to
// end (not just the TTL command's reported countdown). Bounded polling is
// used instead of a fixed sleep.
func TestSDKExpirePollingActualExpiration(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "v")
	resp, err := c.Expire("k", 1)
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if resp != "1" {
		t.Fatalf("Expire = %q, want 1", resp)
	}

	expired := pollUntil(t, 3*time.Second, 50*time.Millisecond, func() bool {
		got, err := c.Get("k")
		return err == nil && got == "nil"
	})
	if !expired {
		got, _ := c.Get("k")
		t.Errorf("key did not expire within timeout, Get = %q", got)
	}
}

// TestSDKExpireOverwritesPreviousExpiration verifies calling Expire a second
// time on the same key replaces the earlier TTL rather than stacking or
// being ignored.
func TestSDKExpireOverwritesPreviousExpiration(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "v")
	c.Expire("k", 100)

	ttl1, _ := c.TTL("k")
	if ttl1 != "100" && ttl1 != "99" {
		t.Fatalf("TTL after first Expire = %q, want ~100", ttl1)
	}

	resp, err := c.Expire("k", 1)
	if err != nil {
		t.Fatalf("second Expire: %v", err)
	}
	if resp != "1" {
		t.Fatalf("second Expire = %q, want 1", resp)
	}

	ttl2, _ := c.TTL("k")
	if ttl2 != "1" && ttl2 != "0" {
		t.Errorf("TTL after second Expire = %q, want ~1 (shortened, not 100)", ttl2)
	}

	expired := pollUntil(t, 3*time.Second, 50*time.Millisecond, func() bool {
		got, err := c.Get("k")
		return err == nil && got == "nil"
	})
	if !expired {
		t.Error("key did not expire on the shortened TTL within timeout")
	}
}

// TestSDKSetClearsExpirationOnOverwrite verifies overwriting an expiring key
// with a plain Set (no EX) drops the previous TTL entirely, since Set always
// stores a brand-new entry rather than mutating the existing one.
func TestSDKSetClearsExpirationOnOverwrite(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "v1", WithEX(100))

	ttlBefore, _ := c.TTL("k")
	if ttlBefore != "100" && ttlBefore != "99" {
		t.Fatalf("TTL before overwrite = %q, want ~100", ttlBefore)
	}

	if _, err := c.Set("k", "v2"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	ttlAfter, err := c.TTL("k")
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttlAfter != "-1" {
		t.Errorf("TTL after plain overwrite = %q, want -1 (expiration cleared)", ttlAfter)
	}
	got, _ := c.Get("k")
	if got != "v2" {
		t.Errorf("Get = %q, want v2", got)
	}
}

// TestSDKExpireThenGetBeforeExpiry verifies the key is still readable and
// unaffected while its TTL has not yet elapsed.
func TestSDKExpireThenGetBeforeExpiry(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "v")
	c.Expire("k", 30)

	got, err := c.Get("k")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "v" {
		t.Errorf("Get before expiry = %q, want v", got)
	}
}
