package sdk

import "testing"

// ---- SET options (NX / XX / EX) ----
//
// The server does not implement Redis semantics exactly; behavior here is
// verified against and documents this server's actual implementation
// (store/helper.go setWithModifiers), not assumed Redis compatibility.

// TestSDKSetWithXXAndEX verifies XX+EX together set the TTL when the key
// already exists.
func TestSDKSetWithXXAndEX(t *testing.T) {
	c := newTestClient(t)
	c.Set("k", "original")

	resp, err := c.Set("k", "updated", WithXX(), WithEX(30))
	if err != nil {
		t.Fatalf("Set WithXX+WithEX: %v", err)
	}
	if resp != "OK" {
		t.Errorf("Set WithXX+WithEX on existing key = %q, want OK", resp)
	}
	got, _ := c.Get("k")
	if got != "updated" {
		t.Errorf("value = %q, want updated", got)
	}
	ttl, _ := c.TTL("k")
	if ttl != "30" && ttl != "29" {
		t.Errorf("TTL after Set WithXX+WithEX = %q, want ~30", ttl)
	}
}

// TestSDKSetWithXXAndEXKeyAbsent verifies XX blocks the write (and therefore
// the EX modifier never applies) when the key does not exist.
func TestSDKSetWithXXAndEXKeyAbsent(t *testing.T) {
	c := newTestClient(t)
	resp, err := c.Set("k", "v", WithXX(), WithEX(30))
	if err != nil {
		t.Fatalf("Set WithXX+WithEX: %v", err)
	}
	if resp != "nil" {
		t.Errorf("Set WithXX+WithEX on absent key = %q, want nil", resp)
	}
	got, _ := c.Get("k")
	if got != "nil" {
		t.Errorf("key should not exist after XX blocked write: got %q", got)
	}
}

// TestSDKSetWithNXAndXXTogetherAlwaysBlocked documents an unsupported
// combination: requesting NX and XX in the same call always results in the
// write being blocked ("nil"), regardless of whether the key exists.
//
// Root cause: store/helper.go's setWithModifiers walks the modifier args in
// order and returns as soon as any single modifier's condition fails. Since
// SDK's buildSetArgs always emits NX before XX, the two conditions are
// mutually exclusive by construction (a key cannot simultaneously "not
// exist" and "exist"), so one of them always fails and no write ever
// succeeds. This is unlikely to be intentional but is the current,
// consistent behavior. Classification: undefined/unsupported behavior — the
// SDK does not prevent constructing this combination, and the server has no
// dedicated validation for it.
func TestSDKSetWithNXAndXXTogetherAlwaysBlocked(t *testing.T) {
	c := newTestClient(t)

	t.Run("key absent", func(t *testing.T) {
		resp, err := c.Set("nx-xx-absent", "v", WithNX(), WithXX())
		if err != nil {
			t.Fatalf("Set WithNX+WithXX: %v", err)
		}
		if resp != "nil" {
			t.Errorf("Set WithNX+WithXX on absent key = %q, want nil", resp)
		}
		got, _ := c.Get("nx-xx-absent")
		if got != "nil" {
			t.Errorf("key should not exist: got %q", got)
		}
	})

	t.Run("key exists", func(t *testing.T) {
		c.Set("nx-xx-exists", "original")
		resp, err := c.Set("nx-xx-exists", "attempted-update", WithNX(), WithXX())
		if err != nil {
			t.Fatalf("Set WithNX+WithXX: %v", err)
		}
		if resp != "nil" {
			t.Errorf("Set WithNX+WithXX on existing key = %q, want nil", resp)
		}
		got, _ := c.Get("nx-xx-exists")
		if got != "original" {
			t.Errorf("value changed despite blocked write: got %q, want original", got)
		}
	})
}

// TestSDKSetWithEXNonPositiveIsSilentlyIgnored documents an SDK API
// inconsistency: WithEX(seconds) only appends the EX modifier to the wire
// command when seconds > 0 (see sdk_helper.go buildSetArgs). Passing 0 or a
// negative value does not raise an error and does not clear any existing
// TTL — it silently behaves as if WithEX had not been passed at all, which
// could surprise a caller expecting either an immediate expiry (0) or a
// validation error (negative).
// Classification: SDK API inconsistency (silent no-op instead of error).
func TestSDKSetWithEXNonPositiveIsSilentlyIgnored(t *testing.T) {
	c := newTestClient(t)

	cases := []struct {
		name    string
		seconds int
	}{
		{"zero", 0},
		{"negative", -5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := "ex-nonpositive-" + tc.name
			resp, err := c.Set(key, "v", WithEX(tc.seconds))
			if err != nil {
				t.Fatalf("Set WithEX(%d): %v", tc.seconds, err)
			}
			if resp != "OK" {
				t.Errorf("Set WithEX(%d) = %q, want OK", tc.seconds, resp)
			}
			ttl, err := c.TTL(key)
			if err != nil {
				t.Fatalf("TTL: %v", err)
			}
			if ttl != "-1" {
				t.Errorf("TTL after WithEX(%d) = %q, want -1 (no expiry — EX modifier was silently dropped)", tc.seconds, ttl)
			}
		})
	}
}

// TestSDKSetWithNXRepeated verifies only the first NX write of a sequence
// succeeds; subsequent NX writes against the same key are all blocked.
func TestSDKSetWithNXRepeated(t *testing.T) {
	c := newTestClient(t)

	resp1, _ := c.Set("k-nx-repeat", "first", WithNX())
	if resp1 != "OK" {
		t.Fatalf("first NX Set = %q, want OK", resp1)
	}
	for i := 0; i < 3; i++ {
		resp, err := c.Set("k-nx-repeat", "attempt", WithNX())
		if err != nil {
			t.Fatalf("NX Set #%d: %v", i, err)
		}
		if resp != "nil" {
			t.Errorf("NX Set #%d on existing key = %q, want nil", i, resp)
		}
	}
	got, _ := c.Get("k-nx-repeat")
	if got != "first" {
		t.Errorf("value = %q, want first (unchanged by blocked NX writes)", got)
	}
}
