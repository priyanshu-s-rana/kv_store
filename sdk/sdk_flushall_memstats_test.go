package sdk

import (
	"strconv"
	"strings"
	"testing"
)

// memStat extracts the integer value for a "label: <n>[ B]" line out of a
// MemoryStats response. Fails the test if the label is not present or the
// value is not parseable, since MemoryStats' response shape is a documented
// contract of the command, not an edge case under test here.
func memStat(t *testing.T, resp string, label string) int64 {
	t.Helper()
	for _, line := range strings.Split(resp, "\n") {
		name, val, found := strings.Cut(line, ": ")
		if !found || name != label {
			continue
		}
		val = strings.TrimSuffix(val, " B")
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			t.Fatalf("MemoryStats label %q has non-numeric value %q: %v", label, val, err)
		}
		return n
	}
	t.Fatalf("MemoryStats response missing label %q; full response: %q", label, resp)
	return 0
}

// ---- FlushAll ----

// TestSDKFlushAllEmptyDatabase verifies flushing an already-empty database
// is a safe no-op that still reports OK.
func TestSDKFlushAllEmptyDatabase(t *testing.T) {
	c := newTestClient(t)
	resp, err := c.FlushAll()
	if err != nil {
		t.Fatalf("FlushAll: %v", err)
	}
	if resp != "OK" {
		t.Errorf("FlushAll = %q, want OK", resp)
	}
	keysResp, _ := c.Keys("*")
	if keysResp != "" {
		t.Errorf("Keys(*) after FlushAll on empty db = %q, want \"\"", keysResp)
	}
}

// TestSDKFlushAllPopulatedDatabase verifies every key, including ones with
// an active TTL, is gone after FlushAll.
func TestSDKFlushAllPopulatedDatabase(t *testing.T) {
	c := newTestClient(t)
	c.Set("k1", "v1")
	c.Set("k2", "v2")
	c.Set("k3", "v3", WithEX(100))

	resp, err := c.FlushAll()
	if err != nil {
		t.Fatalf("FlushAll: %v", err)
	}
	if resp != "OK" {
		t.Fatalf("FlushAll = %q, want OK", resp)
	}

	for _, k := range []string{"k1", "k2", "k3"} {
		got, err := c.Get(k)
		if err != nil {
			t.Fatalf("Get(%s): %v", k, err)
		}
		if got != "nil" {
			t.Errorf("Get(%s) after FlushAll = %q, want nil", k, got)
		}
	}
	keysResp, _ := c.Keys("*")
	if keysResp != "" {
		t.Errorf("Keys(*) after FlushAll = %q, want \"\"", keysResp)
	}
}

// TestSDKFlushAllTwiceInARow verifies a second consecutive FlushAll on an
// already-flushed database behaves identically to the first.
func TestSDKFlushAllTwiceInARow(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "v")
	c.FlushAll()
	resp, err := c.FlushAll()
	if err != nil {
		t.Fatalf("second FlushAll: %v", err)
	}
	if resp != "OK" {
		t.Errorf("second FlushAll = %q, want OK", resp)
	}
}

// ---- MemoryStats ----

// TestSDKMemoryStatsEmptyDatabase verifies keyCount is 0 immediately after
// FlushAll and that every expected stat label is present.
func TestSDKMemoryStatsEmptyDatabase(t *testing.T) {
	c := newTestClient(t)
	resp, err := c.MemoryStats()
	if err != nil {
		t.Fatalf("MemoryStats: %v", err)
	}
	for _, label := range []string{"currentSize", "peakSize", "maxSize", "keyCount", "keySize", "valueSize", "ttlSize", "lruSize", "pubsubSize"} {
		memStat(t, resp, label) // fails the test if missing/non-numeric
	}
	if got := memStat(t, resp, "keyCount"); got != 0 {
		t.Errorf("keyCount on empty db = %d, want 0", got)
	}
}

// TestSDKMemoryStatsAfterInserts verifies keyCount and keySize/valueSize
// grow monotonically as keys are added, validating internal consistency
// rather than pinning exact byte counts (which are an implementation
// detail of the memory accounting formula).
func TestSDKMemoryStatsAfterInserts(t *testing.T) {
	c := newTestClient(t)

	before, err := c.MemoryStats()
	if err != nil {
		t.Fatalf("MemoryStats: %v", err)
	}
	keyCountBefore := memStat(t, before, "keyCount")
	valueSizeBefore := memStat(t, before, "valueSize")

	for i := 0; i < 10; i++ {
		c.Set(strconv.Itoa(i), "some-value")
	}

	after, err := c.MemoryStats()
	if err != nil {
		t.Fatalf("MemoryStats: %v", err)
	}
	keyCountAfter := memStat(t, after, "keyCount")
	valueSizeAfter := memStat(t, after, "valueSize")

	if keyCountAfter != keyCountBefore+10 {
		t.Errorf("keyCount after 10 inserts = %d, want %d", keyCountAfter, keyCountBefore+10)
	}
	if valueSizeAfter <= valueSizeBefore {
		t.Errorf("valueSize after inserts = %d, want > %d (before)", valueSizeAfter, valueSizeBefore)
	}
}

// TestSDKMemoryStatsAfterDeletes verifies keyCount shrinks back down as keys
// are removed, and returns to its pre-insert value once all inserted keys
// are deleted.
func TestSDKMemoryStatsAfterDeletes(t *testing.T) {
	c := newTestClient(t)

	before, err := c.MemoryStats()
	if err != nil {
		t.Fatalf("MemoryStats: %v", err)
	}
	keyCountBefore := memStat(t, before, "keyCount")

	keys := []string{"m1", "m2", "m3"}
	for _, k := range keys {
		c.Set(k, "v")
	}
	for _, k := range keys {
		c.Del(k)
	}

	after, err := c.MemoryStats()
	if err != nil {
		t.Fatalf("MemoryStats: %v", err)
	}
	keyCountAfter := memStat(t, after, "keyCount")

	if keyCountAfter != keyCountBefore {
		t.Errorf("keyCount after inserting and deleting the same keys = %d, want %d (back to baseline)", keyCountAfter, keyCountBefore)
	}
}

// TestSDKMemoryStatsAfterFlushAllResetsToBaseline verifies FlushAll resets
// every dynamic accounting field to zero, not just clearing the key map.
func TestSDKMemoryStatsAfterFlushAllResetsToBaseline(t *testing.T) {
	c := newTestClient(t)
	for i := 0; i < 5; i++ {
		c.Set(strconv.Itoa(i), "value")
	}
	c.FlushAll()

	resp, err := c.MemoryStats()
	if err != nil {
		t.Fatalf("MemoryStats: %v", err)
	}
	for _, label := range []string{"keyCount", "keySize", "valueSize", "ttlSize", "lruSize"} {
		if got := memStat(t, resp, label); got != 0 {
			t.Errorf("%s after FlushAll = %d, want 0", label, got)
		}
	}
}
