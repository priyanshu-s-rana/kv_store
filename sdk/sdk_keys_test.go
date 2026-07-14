package sdk

import (
	"sort"
	"strings"
	"testing"
)

// parseKeysResponse turns the KEYS/MGET-style numbered response
// ("1) foo\n2) bar") into a sorted slice of the raw values, so tests can
// assert on set membership without depending on server-side map iteration
// order.
func parseKeysResponse(resp string) []string {
	if resp == "" || resp == "nil" {
		return nil
	}
	lines := strings.Split(resp, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		_, val, found := strings.Cut(line, ") ")
		if found {
			out = append(out, val)
		}
	}
	sort.Strings(out)
	return out
}

func assertKeySet(t *testing.T, got []string, want []string) {
	t.Helper()
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("keys = %v, want %v", got, want)
			return
		}
	}
}

// ---- Keys ----

// TestSDKKeysPatterns covers the four pattern shapes the server's glob
// matcher supports: exact, prefix* (suffix match), *suffix (prefix match),
// and *contains*.
func TestSDKKeysPatterns(t *testing.T) {
	c := newTestClient(t)
	c.Set("user:1", "a")
	c.Set("user:2", "b")
	c.Set("session:1", "c")
	c.Set("session:2", "d")

	cases := []struct {
		name    string
		pattern string
		want    []string
	}{
		{"suffix wildcard matches prefix", "user:*", []string{"user:1", "user:2"}},
		{"prefix wildcard matches suffix", "*:1", []string{"user:1", "session:1"}},
		{"contains wildcard", "*ssion*", []string{"session:1", "session:2"}},
		{"exact match", "user:1", []string{"user:1"}},
		{"match all", "*", []string{"user:1", "user:2", "session:1", "session:2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := c.Keys(tc.pattern)
			if err != nil {
				t.Fatalf("Keys(%q): %v", tc.pattern, err)
			}
			got := parseKeysResponse(resp)
			assertKeySet(t, got, tc.want)
		})
	}
}

// TestSDKKeysNoMatches documents that a pattern matching nothing returns the
// empty string, not the SDK's "nil" sentinel — KEYS never sends a real RESP
// null for "no matches", it always sends a genuine (possibly empty)
// BulkString, and the parser now correctly distinguishes that from a null
// bulk string.
func TestSDKKeysNoMatches(t *testing.T) {
	c := newTestClient(t)
	c.Set("user:1", "a")

	resp, err := c.Keys("nonexistent-prefix:*")
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if resp != "" {
		t.Errorf("Keys(no matches) = %q, want \"\"", resp)
	}
}

// TestSDKKeysEmptyDatabase verifies Keys("*") against an empty store returns
// the same empty string as a non-matching pattern.
func TestSDKKeysEmptyDatabase(t *testing.T) {
	c := newTestClient(t)
	resp, err := c.Keys("*")
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if resp != "" {
		t.Errorf("Keys(*) on empty database = %q, want \"\"", resp)
	}
}

// TestSDKKeysAfterDelete verifies a deleted key no longer appears in Keys
// results.
func TestSDKKeysAfterDelete(t *testing.T) {
	c := newTestClient(t)
	c.Set("del:1", "a")
	c.Set("del:2", "b")
	c.Del("del:1")

	resp, err := c.Keys("del:*")
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	got := parseKeysResponse(resp)
	assertKeySet(t, got, []string{"del:2"})
}
