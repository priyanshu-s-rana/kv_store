package sdk

import "testing"

// ---- DELETE ----

// TestSDKDelAlreadyDeletedKey verifies deleting a key a second time reports
// 0 (nothing to delete) rather than erroring or re-reporting 1.
func TestSDKDelAlreadyDeletedKey(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "v")

	first, err := c.Del("k")
	if err != nil {
		t.Fatalf("first Del: %v", err)
	}
	if first != "1" {
		t.Fatalf("first Del = %q, want 1", first)
	}

	second, err := c.Del("k")
	if err != nil {
		t.Fatalf("second Del: %v", err)
	}
	if second != "0" {
		t.Errorf("second Del (already deleted) = %q, want 0", second)
	}
}

// TestSDKDelMultipleKeysSequentially deletes several distinct keys one at a
// time and verifies each is actually removed from server state.
func TestSDKDelMultipleKeysSequentially(t *testing.T) {
	c := newTestClient(t)
	keys := []string{"d1", "d2", "d3", "d4"}
	for _, k := range keys {
		if _, err := c.Set(k, "v-"+k); err != nil {
			t.Fatalf("Set(%s): %v", k, err)
		}
	}

	for _, k := range keys {
		resp, err := c.Del(k)
		if err != nil {
			t.Fatalf("Del(%s): %v", k, err)
		}
		if resp != "1" {
			t.Errorf("Del(%s) = %q, want 1", k, resp)
		}
		got, err := c.Get(k)
		if err != nil {
			t.Fatalf("Get(%s) after Del: %v", k, err)
		}
		if got != "nil" {
			t.Errorf("Get(%s) after Del = %q, want nil", k, got)
		}
	}

	// Keys deleted earlier in the loop must still be gone at the end —
	// deleting d2 must not resurrect or affect d1's already-deleted state.
	for _, k := range keys {
		got, _ := c.Get(k)
		if got != "nil" {
			t.Errorf("Get(%s) at end = %q, want nil", k, got)
		}
	}
}

// TestSDKDelDoesNotAffectOtherKeys verifies deleting one key leaves sibling
// keys untouched.
func TestSDKDelDoesNotAffectOtherKeys(t *testing.T) {
	c := newTestClient(t)
	c.Set("keep-1", "a")
	c.Set("keep-2", "b")
	c.Set("remove-me", "c")

	if _, err := c.Del("remove-me"); err != nil {
		t.Fatalf("Del: %v", err)
	}

	got1, _ := c.Get("keep-1")
	got2, _ := c.Get("keep-2")
	if got1 != "a" {
		t.Errorf("Get(keep-1) = %q, want a", got1)
	}
	if got2 != "b" {
		t.Errorf("Get(keep-2) = %q, want b", got2)
	}
}
