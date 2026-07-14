package sdk

import (
	"strings"
	"testing"
)

// ---- Connection Lifecycle ----

func TestSDKConnectSuccess(t *testing.T) {
	c, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	if _, err := c.Ping(); err != nil {
		t.Fatalf("Ping after connect: %v", err)
	}
}

// TestSDKConnectInvalidAddress covers addresses that fail during address
// parsing/dialing itself, before any handshake with a server occurs.
func TestSDKConnectInvalidAddress(t *testing.T) {
	cases := []struct {
		name string
		addr string
	}{
		{"empty address", ""},
		{"missing port", "localhost"},
		{"too many colons", "::::"},
		{"port out of range", "localhost:999999"},
		{"negative port", "localhost:-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewClient(tc.addr)
			if err == nil {
				c.Close()
				t.Fatalf("NewClient(%q) succeeded, want error", tc.addr)
			}
		})
	}
}

// TestSDKConnectRefused dials a port with no listener (as opposed to an
// unparseable address) to verify the SDK surfaces a connection-refused error.
func TestSDKConnectRefused(t *testing.T) {
	_, err := NewClient("localhost:1")
	if err == nil {
		t.Fatalf("NewClient to a port with no listener should fail")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Errorf("error = %q, want it to mention connection refused", err.Error())
	}
}

// TestSDKMultipleClientsIndependent verifies two separate SDK clients each
// see the same server state (writes from one are visible to the other),
// while remaining independent connections.
func TestSDKMultipleClientsIndependent(t *testing.T) {
	c1 := newTestClient(t)
	c2, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient c2: %v", err)
	}
	defer c2.Close()

	if _, err := c1.Set("k1", "v1"); err != nil {
		t.Fatalf("c1 Set: %v", err)
	}
	got, err := c2.Get("k1")
	if err != nil {
		t.Fatalf("c2 Get: %v", err)
	}
	if got != "v1" {
		t.Errorf("c2 Get(k1) = %q, want v1", got)
	}
}

func TestSDKClose(t *testing.T) {
	c, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// TestSDKCloseTwice verifies a second Close() call does not panic. The SDK
// returns whatever the underlying net.Conn.Close() reports (typically a
// "use of closed network connection" error) rather than swallowing it.
func TestSDKCloseTwice(t *testing.T) {
	c, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	_ = c.Close() // must not panic; error (if any) is not asserted here
}

// TestSDKOperationsAfterClose verifies every command returns an error
// instead of panicking or hanging once the connection is closed.
func TestSDKOperationsAfterClose(t *testing.T) {
	c, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := c.Ping(); err == nil {
		t.Errorf("Ping after Close should return an error")
	}
	if _, err := c.Set("k", "v"); err == nil {
		t.Errorf("Set after Close should return an error")
	}
	if _, err := c.Get("k"); err == nil {
		t.Errorf("Get after Close should return an error")
	}
	if _, err := c.Del("k"); err == nil {
		t.Errorf("Del after Close should return an error")
	}
	if _, err := c.MGet("k"); err == nil {
		t.Errorf("MGet after Close should return an error")
	}
}

// TestSDKOperationsAfterCloseErrorMentionsConnection checks the returned
// error is meaningful (mentions the connection is closed) rather than an
// opaque failure, per the "Error Propagation" requirement.
func TestSDKOperationsAfterCloseErrorMentionsConnection(t *testing.T) {
	c, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	c.Close()

	_, err = c.Ping()
	if err == nil {
		t.Fatal("expected error after Close")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("error after Close = %q, want it to mention the connection is closed", err.Error())
	}
}

// TestSDKNewClientAfterClosingAnother verifies closing one client has no
// effect on the ability to open a fresh one to the same server.
func TestSDKNewClientAfterClosingAnother(t *testing.T) {
	first, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient first: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close first: %v", err)
	}

	second, err := NewClient(testAddr)
	if err != nil {
		t.Fatalf("NewClient second: %v", err)
	}
	defer second.Close()

	if _, err := second.Ping(); err != nil {
		t.Fatalf("Ping on second client: %v", err)
	}
}
