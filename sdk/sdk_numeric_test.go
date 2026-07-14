package sdk

import (
	"strconv"
	"testing"
)

// ---- Numeric commands (INCR / DECR) ----

// TestSDKIncrRepeated verifies N sequential Incr calls land on N, confirming
// each call is applied against the previous result rather than some cached
// or stale value.
func TestSDKIncrRepeated(t *testing.T) {
	c := newTestClient(t)
	const n = 25
	var last string
	for i := 1; i <= n; i++ {
		resp, err := c.Incr("counter")
		if err != nil {
			t.Fatalf("Incr #%d: %v", i, err)
		}
		want := strconv.Itoa(i)
		if resp != want {
			t.Fatalf("Incr #%d = %q, want %q", i, resp, want)
		}
		last = resp
	}
	got, _ := c.Get("counter")
	if got != last {
		t.Errorf("final Get(counter) = %q, want %q", got, last)
	}
}

// TestSDKDecrRepeated verifies N sequential Decr calls land on -N.
func TestSDKDecrRepeated(t *testing.T) {
	c := newTestClient(t)
	const n = 25
	var last string
	for i := 1; i <= n; i++ {
		resp, err := c.Decr("counter")
		if err != nil {
			t.Fatalf("Decr #%d: %v", i, err)
		}
		want := strconv.Itoa(-i)
		if resp != want {
			t.Fatalf("Decr #%d = %q, want %q", i, resp, want)
		}
		last = resp
	}
	got, _ := c.Get("counter")
	if got != last {
		t.Errorf("final Get(counter) = %q, want %q", got, last)
	}
}

// TestSDKIncrFromNegativeValue verifies Incr correctly walks a negative
// starting value up through zero.
func TestSDKIncrFromNegativeValue(t *testing.T) {
	c := newTestClient(t)
	c.Set("n", "-3")

	want := []string{"-2", "-1", "0", "1", "2"}
	for i, w := range want {
		resp, err := c.Incr("n")
		if err != nil {
			t.Fatalf("Incr #%d: %v", i, err)
		}
		if resp != w {
			t.Errorf("Incr #%d = %q, want %q", i, resp, w)
		}
	}
}

// TestSDKDecrIntoNegativeValue verifies Decr correctly walks a positive
// starting value down through zero into negative numbers.
func TestSDKDecrIntoNegativeValue(t *testing.T) {
	c := newTestClient(t)
	c.Set("n", "2")

	want := []string{"1", "0", "-1", "-2", "-3"}
	for i, w := range want {
		resp, err := c.Decr("n")
		if err != nil {
			t.Fatalf("Decr #%d: %v", i, err)
		}
		if resp != w {
			t.Errorf("Decr #%d = %q, want %q", i, resp, w)
		}
	}
}

// TestSDKIncrDecrMixedSequence interleaves Incr and Decr against the same
// key and verifies the running total matches the expected net delta at
// every step.
func TestSDKIncrDecrMixedSequence(t *testing.T) {
	c := newTestClient(t)
	ops := []struct {
		incr bool
		want string
	}{
		{true, "1"},
		{true, "2"},
		{false, "1"},
		{false, "0"},
		{false, "-1"},
		{true, "0"},
	}
	for i, op := range ops {
		var resp string
		var err error
		if op.incr {
			resp, err = c.Incr("mixed")
		} else {
			resp, err = c.Decr("mixed")
		}
		if err != nil {
			t.Fatalf("op #%d: %v", i, err)
		}
		if resp != op.want {
			t.Errorf("op #%d = %q, want %q", i, resp, op.want)
		}
	}
}

// TestSDKDecrNonIntegerReturnsError mirrors the existing Incr non-integer
// coverage for Decr, since the two share the same underlying validation.
func TestSDKDecrNonIntegerReturnsError(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "notanint")
	_, err := c.Decr("k")
	if err == nil {
		t.Errorf("Decr on non-integer should return error")
	}
	// The stored value must be left untouched by the rejected operation.
	got, _ := c.Get("k")
	if got != "notanint" {
		t.Errorf("value after failed Decr = %q, want unchanged notanint", got)
	}
}

// TestSDKIncrNonIntegerLeavesValueUnchanged extends the existing
// TestSDKIncrNonIntegerReturnsError test by asserting the rejected Incr does
// not mutate server state.
func TestSDKIncrNonIntegerLeavesValueUnchanged(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "abc")
	if _, err := c.Incr("k"); err == nil {
		t.Fatal("Incr on non-integer should return error")
	}
	got, _ := c.Get("k")
	if got != "abc" {
		t.Errorf("value after failed Incr = %q, want unchanged abc", got)
	}
}
