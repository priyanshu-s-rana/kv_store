package sdk

import (
	"fmt"
	"testing"
)

// ---- MSET / MGET ----

// TestSDKMSetOverwritesExistingValues verifies MSet replaces the current
// value of keys that already exist, atomically alongside any new keys in
// the same call.
func TestSDKMSetOverwritesExistingValues(t *testing.T) {
	c := newTestClient(t)
	c.Set("k1", "old1")
	c.Set("k2", "old2")

	if _, err := c.MSet("k1", "new1", "k2", "new2", "k3", "new3"); err != nil {
		t.Fatalf("MSet: %v", err)
	}

	vals, err := c.MGet("k1", "k2", "k3")
	if err != nil {
		t.Fatalf("MGet: %v", err)
	}
	want := []string{"new1", "new2", "new3"}
	for i, w := range want {
		if vals[i] != w {
			t.Errorf("vals[%d] = %q, want %q", i, vals[i], w)
		}
	}
}

// TestSDKMSetMGetLargeBatch verifies a large number of pairs are all set and
// retrievable, with ordering in the MGet response matching the requested
// key order (not insertion order or map iteration order).
func TestSDKMSetMGetLargeBatch(t *testing.T) {
	c := newTestClient(t)
	const n = 500

	pairs := make([]string, 0, n*2)
	keys := make([]string, 0, n)
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("batch-key-%04d", i)
		val := fmt.Sprintf("batch-val-%04d", i)
		pairs = append(pairs, key, val)
		keys = append(keys, key)
	}

	if _, err := c.MSet(pairs...); err != nil {
		t.Fatalf("MSet large batch: %v", err)
	}

	vals, err := c.MGet(keys...)
	if err != nil {
		t.Fatalf("MGet large batch: %v", err)
	}
	if len(vals) != n {
		t.Fatalf("MGet returned %d values, want %d", len(vals), n)
	}
	for i := 0; i < n; i++ {
		want := fmt.Sprintf("batch-val-%04d", i)
		if vals[i] != want {
			t.Errorf("vals[%d] = %q, want %q", i, vals[i], want)
		}
	}
}

// TestSDKMGetOrderingMatchesRequestedKeyOrder verifies MGet preserves the
// order of the requested keys even when that order does not match the order
// the keys were originally written in.
func TestSDKMGetOrderingMatchesRequestedKeyOrder(t *testing.T) {
	c := newTestClient(t)
	c.MSet("a", "1", "b", "2", "c", "3")

	vals, err := c.MGet("c", "a", "b")
	if err != nil {
		t.Fatalf("MGet: %v", err)
	}
	want := []string{"3", "1", "2"}
	for i, w := range want {
		if vals[i] != w {
			t.Errorf("vals[%d] = %q, want %q", i, vals[i], w)
		}
	}
}

// TestSDKMGetPartialMissingKeys verifies a mix of present and missing keys
// returns "" only for the missing ones, at the correct positions.
func TestSDKMGetPartialMissingKeys(t *testing.T) {
	c := newTestClient(t)
	c.Set("present1", "v1")
	c.Set("present2", "v2")

	vals, err := c.MGet("present1", "missing1", "present2", "missing2")
	if err != nil {
		t.Fatalf("MGet: %v", err)
	}
	want := []string{"v1", "", "v2", ""}
	for i, w := range want {
		if vals[i] != w {
			t.Errorf("vals[%d] = %q, want %q", i, vals[i], w)
		}
	}
}

// TestSDKMGetAllMissingKeys verifies requesting only missing keys returns an
// empty string for every position, none of which errors.
func TestSDKMGetAllMissingKeys(t *testing.T) {
	c := newTestClient(t)
	vals, err := c.MGet("nope1", "nope2", "nope3")
	if err != nil {
		t.Fatalf("MGet: %v", err)
	}
	if len(vals) != 3 {
		t.Fatalf("MGet returned %d values, want 3", len(vals))
	}
	for i, v := range vals {
		if v != "" {
			t.Errorf("vals[%d] = %q, want \"\"", i, v)
		}
	}
}

// TestSDKMGetNoKeysReturnsError verifies calling MGet with zero keys is
// rejected by the server rather than silently returning an empty slice.
func TestSDKMGetNoKeysReturnsError(t *testing.T) {
	c := newTestClient(t)
	_, err := c.MGet()
	if err == nil {
		t.Errorf("MGet with no keys should return an error")
	}
}

// TestSDKMSetSingleWholePair verifies the smallest valid MSet call (one pair)
// behaves the same as Set.
func TestSDKMSetSingleWholePair(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.MSet("solo", "value"); err != nil {
		t.Fatalf("MSet: %v", err)
	}
	got, err := c.Get("solo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "value" {
		t.Errorf("Get(solo) = %q, want value", got)
	}
}
