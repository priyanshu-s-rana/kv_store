package parser

import (
	"strings"
	"testing"
)

// assertEncoded checks the encoder output matches the exact expected bytes.
func assertEncoded(t *testing.T, got []byte, want string) {
	t.Helper()
	if string(got) != want {
		t.Errorf("= %q, want %q", got, want)
	}
}

// ---- Integer ----

// Verifies a positive integer encodes as a RESP integer.
// Example: Integer(1) → ":1\r\n"
func TestIntegerPositive(t *testing.T) {
	assertEncoded(t, Integer(1), ":1\r\n")
}

// Verifies zero encodes correctly.
// Example: Integer(0) → ":0\r\n"
func TestIntegerZero(t *testing.T) {
	assertEncoded(t, Integer(0), ":0\r\n")
}

// Verifies a negative integer keeps its sign (used by TTL -1 / -2).
// Example: Integer(-2) → ":-2\r\n"
func TestIntegerNegative(t *testing.T) {
	assertEncoded(t, Integer(-2), ":-2\r\n")
}

// Verifies a large integer is rendered in full.
// Example: Integer(1234567) → ":1234567\r\n"
func TestIntegerLarge(t *testing.T) {
	assertEncoded(t, Integer(1234567), ":1234567\r\n")
}

// ---- BulkString ----

// Verifies a basic bulk string with a correct length prefix.
// Example: BulkString("foo") → "$3\r\nfoo\r\n"
func TestBulkStringBasic(t *testing.T) {
	assertEncoded(t, BulkString("foo"), "$3\r\nfoo\r\n")
}

// Verifies an empty string encodes as a zero-length bulk string.
// Example: BulkString("") → "$0\r\n\r\n"
func TestBulkStringEmpty(t *testing.T) {
	assertEncoded(t, BulkString(""), "$0\r\n\r\n")
}

// Verifies inner spaces are preserved and counted in the length.
// Example: BulkString("hello world") → "$11\r\nhello world\r\n"
func TestBulkStringWithSpaces(t *testing.T) {
	assertEncoded(t, BulkString("hello world"), "$11\r\nhello world\r\n")
}

// Verifies null bytes inside the payload are preserved (binary-safe).
// Example: BulkString("a\x00b") → "$3\r\na\x00b\r\n"
func TestBulkStringBinarySafe(t *testing.T) {
	assertEncoded(t, BulkString("a\x00b"), "$3\r\na\x00b\r\n")
}

// Verifies CRLF inside the payload is preserved and counted, not treated as
// a terminator.
// Example: BulkString("a\r\nb") → "$4\r\na\r\nb\r\n"
func TestBulkStringWithCRLF(t *testing.T) {
	assertEncoded(t, BulkString("a\r\nb"), "$4\r\na\r\nb\r\n")
}

// Verifies the length prefix counts bytes, not runes, for multibyte input.
// "héllo" is 6 bytes (é is 2 bytes in UTF-8).
// Example: BulkString("héllo") → "$6\r\nhéllo\r\n"
func TestBulkStringMultibyte(t *testing.T) {
	assertEncoded(t, BulkString("héllo"), "$6\r\nhéllo\r\n")
}

// ---- Array ----

// Verifies an empty array encodes just the header.
// Example: Array() → "*0\r\n"
func TestArrayEmpty(t *testing.T) {
	assertEncoded(t, Array(), "*0\r\n")
}

// Verifies a single-element array.
// Example: Array("PING") → "*1\r\n$4\r\nPING\r\n"
func TestArraySingle(t *testing.T) {
	assertEncoded(t, Array("PING"), "*1\r\n$4\r\nPING\r\n")
}

// Verifies a multi-element array nests bulk strings in order.
// Example: Array("GET", "foo") → "*2\r\n$3\r\nGET\r\n$3\r\nfoo\r\n"
func TestArrayMultiple(t *testing.T) {
	assertEncoded(t, Array("GET", "foo"), "*2\r\n$3\r\nGET\r\n$3\r\nfoo\r\n")
}

// Verifies an empty-string element is encoded as a zero-length bulk string.
// Example: Array("SET", "k", "") → "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$0\r\n\r\n"
func TestArrayWithEmptyElement(t *testing.T) {
	assertEncoded(t, Array("SET", "k", ""), "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$0\r\n\r\n")
}

// ---- Error / SimpleString ----

// Verifies an error is prefixed with "-ERR ".
// Example: Error("bad thing") → "-ERR bad thing\r\n"
func TestError(t *testing.T) {
	assertEncoded(t, Error("bad thing"), "-ERR bad thing\r\n")
}

// Verifies a simple string is prefixed with "+".
// Example: SimpleString("OK") → "+OK\r\n"
func TestSimpleString(t *testing.T) {
	assertEncoded(t, SimpleString("OK"), "+OK\r\n")
}

// ---- Null encodings ----

// Verifies the null bulk string sentinel.
// Example: NullBulkString() → "$-1\r\n"
func TestNullBulkString(t *testing.T) {
	assertEncoded(t, NullBulkString(), "$-1\r\n")
}

// Verifies the null array sentinel.
// Example: NullArray() → "*-1\r\n"
func TestNullArray(t *testing.T) {
	assertEncoded(t, NullArray(), "*-1\r\n")
}

// ---- Round trip (encoder ↔ parser) ----

// Verifies an array encoded here decodes back to the same command through
// the parser — encoder and parser are inverses.
func TestArrayRoundTripsThroughParser(t *testing.T) {
	encoded := Array("SET", "key", "val")

	cmd, err := New(strings.NewReader(string(encoded))).ReadCommand()
	if err != nil {
		t.Fatalf("ReadCommand: %v", err)
	}
	assertCmd(t, cmd, "SET", "key", "val")
}

// Verifies binary-safe payloads survive an encode → decode round trip.
func TestArrayRoundTripBinarySafe(t *testing.T) {
	encoded := Array("SET", "k", "a\r\nb\x00c")

	cmd, err := New(strings.NewReader(string(encoded))).ReadCommand()
	if err != nil {
		t.Fatalf("ReadCommand: %v", err)
	}
	assertCmd(t, cmd, "SET", "k", "a\r\nb\x00c")
}
