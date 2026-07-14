package sdk

import (
	"strings"
	"testing"
)

// ---- SET / GET edge cases ----

// TestSDKSetGetEdgeCaseValues verifies exact round-trip correctness of Set/Get
// for a variety of non-trivial payloads. Binary-safety (CRLF, tabs, unicode)
// is guaranteed by RESP's length-prefixed bulk strings, not by scanning for
// delimiters, so these must all survive intact.
func TestSDKSetGetEdgeCaseValues(t *testing.T) {
	c := newTestClient(t)
	cases := []struct {
		name  string
		key   string
		value string
	}{
		{"unicode", "k-unicode", "héllo wörld 你好"},
		{"emoji", "k-emoji", "🚀🔥💯"},
		{"leading and trailing whitespace", "k-ws", "   padded value   "},
		{"internal newline", "k-newline", "line1\nline2\nline3"},
		{"internal CRLF", "k-crlf", "line1\r\nline2"},
		{"tab characters", "k-tab", "a\tb\tc"},
		{"single space value", "k-space", " "},
		{"long key", strings.Repeat("k", 500), "v"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.Set(tc.key, tc.value); err != nil {
				t.Fatalf("Set: %v", err)
			}
			got, err := c.Get(tc.key)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got != tc.value {
				t.Errorf("Get(%q) = %q, want %q", tc.key, got, tc.value)
			}
		})
	}
}

// TestSDKSetGetLargeValue verifies a several-hundred-KB value round-trips
// exactly, exercising buffered reads across multiple underlying TCP reads.
func TestSDKSetGetLargeValue(t *testing.T) {
	c := newTestClient(t)
	large := strings.Repeat("abcdefghij", 30000) // 300,000 bytes
	if _, err := c.Set("k-large", large); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := c.Get("k-large")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got) != len(large) {
		t.Fatalf("Get large value len = %d, want %d", len(got), len(large))
	}
	if got != large {
		t.Errorf("Get large value did not round-trip exactly")
	}
}

// TestSDKSetOverwriteMultipleTimes verifies repeated overwrites of the same
// key always reflect the most recent write, with no stale reads.
func TestSDKSetOverwriteMultipleTimes(t *testing.T) {
	c := newTestClient(t)
	for i := 0; i < 10; i++ {
		val := strings.Repeat("v", i+1)
		if _, err := c.Set("k-overwrite", val); err != nil {
			t.Fatalf("Set #%d: %v", i, err)
		}
		got, err := c.Get("k-overwrite")
		if err != nil {
			t.Fatalf("Get #%d: %v", i, err)
		}
		if got != val {
			t.Errorf("Get after overwrite #%d = %q, want %q", i, got, val)
		}
	}
}

// TestSDKGetMissingKeyReturnsNilSentinel documents that a never-set key is
// reported as the string "nil" (not a Go error, not an empty string).
func TestSDKGetMissingKeyReturnsNilSentinel(t *testing.T) {
	c := newTestClient(t)
	got, err := c.Get("truly-missing-key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "nil" {
		t.Errorf("Get(missing) = %q, want \"nil\"", got)
	}
}

// TestSDKSetGetEmptyValueRoundTrips verifies a key set to the empty string
// round-trips as "" and stays distinguishable from a missing key ("nil").
// This previously regressed: parser.DecodeBulkString mapped both the null
// bulk string ("$-1\r\n") and a valid empty bulk string ("$0\r\n\r\n") to the
// literal "nil", so the SDK had no way to tell "key holds an empty string"
// apart from "key does not exist". Fixed in parser.Parser.readBulkString by
// reporting null-ness explicitly instead of collapsing it into an empty
// string.
func TestSDKSetGetEmptyValueRoundTrips(t *testing.T) {
	c := newTestClient(t)
	if _, err := c.Set("k-empty", ""); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := c.Get("k-empty")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "" {
		t.Errorf("Get(empty-string value) = %q, want \"\" (must be distinguishable from a missing key)", got)
	}

	missing, err := c.Get("k-truly-missing")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if missing != "nil" {
		t.Errorf("Get(missing) = %q, want \"nil\"", missing)
	}
	if got == missing {
		t.Errorf("empty-string value and missing key are indistinguishable: both = %q", got)
	}
}
