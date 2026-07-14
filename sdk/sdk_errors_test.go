package sdk

import (
	"net"
	"testing"
	"time"
)

// ---- Error propagation ----

// TestSDKServerErrorMessagesAreMeaningful verifies that a variety of
// server-rejected commands surface non-empty, descriptive errors through the
// SDK rather than a bare "error" or a swallowed nil.
func TestSDKServerErrorMessagesAreMeaningful(t *testing.T) {
	c := newTestClient(t)
	c.Set("nonint", "abc")

	cases := []struct {
		name string
		call func() (string, error)
	}{
		{"Incr on non-integer", func() (string, error) { return c.Incr("nonint") }},
		{"Decr on non-integer", func() (string, error) { return c.Decr("nonint") }},
		{"MSet odd args", func() (string, error) { return c.MSet("k1", "v1", "k2") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.call()
			if err == nil {
				t.Fatalf("%s: expected an error", tc.name)
			}
			if err.Error() == "" {
				t.Errorf("%s: error message is empty", tc.name)
			}
		})
	}

	_, err := c.Subscribe()
	if err == nil {
		t.Fatal("Subscribe with no topics: expected an error")
	}
	if err.Error() == "" {
		t.Error("Subscribe with no topics: error message is empty")
	}
}

// TestSDKConnectionUsableAfterServerError verifies that receiving a RESP
// error response for one command does not desynchronize the connection —
// the next command on the same client must still get its own correct
// response, not leftover bytes from the error.
func TestSDKConnectionUsableAfterServerError(t *testing.T) {
	c := newTestClient(t)
	c.Set("nonint", "abc")

	if _, err := c.Incr("nonint"); err == nil {
		t.Fatal("expected Incr on non-integer to error")
	}

	// The connection must still be perfectly usable afterward.
	if resp, err := c.Ping(); err != nil || resp != "PONG" {
		t.Fatalf("Ping after server error = (%q, %v), want (PONG, nil)", resp, err)
	}
	if _, err := c.Set("k", "v"); err != nil {
		t.Fatalf("Set after server error: %v", err)
	}
	got, err := c.Get("k")
	if err != nil || got != "v" {
		t.Fatalf("Get after server error = (%q, %v), want (v, nil)", got, err)
	}
}

// TestSDKNetworkFailureMidRequest simulates an abrupt server-side connection
// drop (distinct from a client-initiated Close): a bare TCP listener accepts
// the connection and closes it without ever writing a response, mimicking a
// crashed or misbehaving peer. The SDK must surface an error rather than
// hang or panic.
func TestSDKNetworkFailureMidRequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake listener: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Read whatever the client sends, then hang up without responding.
		buf := make([]byte, 256)
		conn.Read(buf)
		conn.Close()
	}()

	c, err := NewClient(ln.Addr().String())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer c.Close()

	done := make(chan struct{})
	var pingErr error
	go func() {
		_, pingErr = c.Ping()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Ping did not return after the peer dropped the connection mid-request")
	}

	if pingErr == nil {
		t.Error("Ping should return an error when the peer closes without responding")
	}
}
