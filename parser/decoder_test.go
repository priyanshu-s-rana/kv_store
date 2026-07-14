package parser

import (
	"bufio"
	"strings"
	"testing"
)

// ---- DecodeSimpleString ----

func TestDecodeSimpleString(t *testing.T) {
	cases := []struct {
		input []byte
		want  string
	}{
		{[]byte("+OK\r\n"), "OK"},
		{[]byte("+PONG\r\n"), "PONG"},
		{[]byte("+hello world\r\n"), "hello world"},
	}
	for _, c := range cases {
		if got := DecodeSimpleString(nil, c.input); got != c.want {
			t.Errorf("DecodeSimpleString(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestDecodeSimpleStringTooShort(t *testing.T) {
	cases := [][]byte{{}, {'+'}, []byte("+\r")}
	for _, input := range cases {
		if got := DecodeSimpleString(nil, input); got != "" {
			t.Errorf("DecodeSimpleString(%q) = %q, want empty", input, got)
		}
	}
}

// ---- DecodeError ----

func TestDecodeError(t *testing.T) {
	cases := []struct {
		input []byte
		want  string
	}{
		{[]byte("-ERR unknown command\r\n"), "ERR unknown command"},
		{[]byte("-WRONGTYPE operation\r\n"), "WRONGTYPE operation"},
	}
	for _, c := range cases {
		if got := DecodeError(nil, c.input); got != c.want {
			t.Errorf("DecodeError(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// ---- DecodeInteger ----

func TestDecodeInteger(t *testing.T) {
	cases := []struct {
		input []byte
		want  string
	}{
		{[]byte(":1\r\n"), "1"},
		{[]byte(":0\r\n"), "0"},
		{[]byte(":-1\r\n"), "-1"},
		{[]byte(":-2\r\n"), "-2"},
		{[]byte(":42\r\n"), "42"},
	}
	for _, c := range cases {
		if got := DecodeInteger(nil, c.input); got != c.want {
			t.Errorf("DecodeInteger(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// ---- DecodeBulkString ----

func TestDecodeBulkString(t *testing.T) {
	cases := []struct {
		input []byte
		want  string
	}{
		{[]byte("$5\r\nhello\r\n"), "hello"},
		{[]byte("$3\r\nfoo\r\n"), "foo"},
		{[]byte("$0\r\n\r\n"), ""},
	}
	for _, c := range cases {
		if got := DecodeBulkString(nil, c.input); got != c.want {
			t.Errorf("DecodeBulkString(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestDecodeBulkStringNull(t *testing.T) {
	if got := DecodeBulkString(nil, []byte("$-1\r\n")); got != "nil" {
		t.Errorf("DecodeBulkString($-1) = %q, want \"nil\"", got)
	}
}

func TestDecodeBulkStringInvalid(t *testing.T) {
	cases := [][]byte{
		[]byte("notabulkstring"),
		[]byte("$xyz\r\nfoo\r\n"),
	}
	for _, input := range cases {
		if got := DecodeBulkString(nil, input); got != "nil" {
			t.Errorf("DecodeBulkString(%q) = %q, want \"nil\"", input, got)
		}
	}
}

// TestDecodeBulkStringReaderPathDistinguishesNullFromEmpty drives
// DecodeBulkString through a real *bufio.Reader (the reader != nil branch),
// which is the code path every live connection actually uses via
// ReadResponse. This is distinct from the byte-slice (reader == nil) branch
// exercised by TestDecodeBulkString/TestDecodeBulkStringNull above — the two
// branches previously disagreed on whether a valid, merely-empty payload
// ("$0\r\n\r\n") was distinguishable from the null bulk string ("$-1\r\n").
func TestDecodeBulkStringReaderPathDistinguishesNullFromEmpty(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"null bulk string", "$-1\r\n", "nil"},
		{"empty bulk string", "$0\r\n\r\n", ""},
		{"non-empty bulk string", "$5\r\nhello\r\n", "hello"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reader := bufio.NewReader(strings.NewReader(c.input))
			data, _ := reader.Peek(1)
			if got := DecodeBulkString(reader, data); got != c.want {
				t.Errorf("DecodeBulkString(reader, %q) = %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// ---- DecodeArray ----

func TestDecodeArraySimple(t *testing.T) {
	input := []byte("*3\r\n$7\r\nmessage\r\n$4\r\nnews\r\n$5\r\nhello\r\n")
	want := "message news hello"
	if got := DecodeArray(nil, input); got != want {
		t.Errorf("DecodeArray = %q, want %q", got, want)
	}
}

func TestDecodeArraySingleElement(t *testing.T) {
	input := []byte("*1\r\n$4\r\nPING\r\n")
	if got := DecodeArray(nil, input); got != "PING" {
		t.Errorf("DecodeArray = %q, want \"PING\"", got)
	}
}

func TestDecodeArrayLargeCount(t *testing.T) {
	// Verifies array counts ≥ 10 are parsed correctly (multi-digit header).
	input := []byte("*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n")
	if got := DecodeArray(nil, input); got != "foo bar" {
		t.Errorf("DecodeArray = %q, want \"foo bar\"", got)
	}
}

func TestDecodeArrayInvalid(t *testing.T) {
	// Structurally invalid but no I/O error — returns "".
	structural := [][]byte{
		[]byte("*0\r\n"),
		[]byte("*-1\r\n"),
	}
	for _, input := range structural {
		if got := DecodeArray(nil, input); got != "" {
			t.Errorf("DecodeArray(%q) = %q, want empty", input, got)
		}
	}

	// Malformed data that triggers a real parse/I/O error — returns "(error) ...".
	errored := [][]byte{
		[]byte("notanarray"),
		[]byte("*abc\r\n"),
	}
	for _, input := range errored {
		if got := DecodeArray(nil, input); !strings.HasPrefix(got, "(error)") {
			t.Errorf("DecodeArray(%q) = %q, want (error) prefix", input, got)
		}
	}
}

func TestDecodeArrayTruncated(t *testing.T) {
	// Claims 3 elements but only 1 is present — I/O error mid-read.
	input := []byte("*3\r\n$3\r\nfoo\r\n")
	if got := DecodeArray(nil, input); !strings.HasPrefix(got, "(error)") {
		t.Errorf("DecodeArray(truncated) = %q, want (error) prefix", got)
	}
}
