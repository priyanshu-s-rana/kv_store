package parser

import (
	"io"
	"strings"
	"testing"
)

// parse wraps input in a strings.Reader, builds a Parser, and reads one
// command. Shorthand used by every single-command test.
func parse(t *testing.T, input string) (*Command, error) {
	t.Helper()
	return New(strings.NewReader(input)).ReadCommand()
}

// assertCmd checks that got is non-nil and matches the expected name + args.
// Fails the test on mismatch with a descriptive message.
func assertCmd(t *testing.T, got *Command, name string, args ...string) {
	t.Helper()
	if got == nil {
		t.Fatalf("Command is nil")
	}
	if got.Name != name {
		t.Errorf("Name = %q, want %q", got.Name, name)
	}
	if len(got.Args) != len(args) {
		t.Fatalf("Args = %v (len %d), want %v (len %d)", got.Args, len(got.Args), args, len(args))
	}
	for i, want := range args {
		if got.Args[i] != want {
			t.Errorf("Args[%d] = %q, want %q", i, got.Args[i], want)
		}
	}
}

// ---- INLINE COMMANDS ----

// Verifies a bare command name with no args parses correctly.
// Example: "PING\r\n" → {Name: "PING", Args: []}
func TestInlineSimple(t *testing.T) {
	cmd, err := parse(t, "PING\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "PING")
}

// Verifies a command with a single argument parses correctly.
// Example: "GET foo\r\n" → {Name: "GET", Args: ["foo"]}
func TestInlineWithArgs(t *testing.T) {
	cmd, err := parse(t, "GET foo\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "GET", "foo")
}

// Verifies a command with multiple whitespace-separated args parses correctly.
// Example: "SET key value EX 10\r\n" → {Name: "SET", Args: ["key", "value", "EX", "10"]}
func TestInlineMultiArg(t *testing.T) {
	cmd, err := parse(t, "SET key value EX 10\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "SET", "key", "value", "EX", "10")
}

// Verifies lowercase verbs are normalized to uppercase.
// Example: "get foo\r\n" → {Name: "GET", Args: ["foo"]}
func TestInlineLowercaseUppercased(t *testing.T) {
	cmd, err := parse(t, "get foo\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "GET", "foo")
}

// Verifies mixed-case verbs are normalized to uppercase.
// Example: "SeT k v\r\n" → {Name: "SET", Args: ["k", "v"]}
func TestInlineMixedCase(t *testing.T) {
	cmd, err := parse(t, "SeT k v\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "SET", "k", "v")
}

// Verifies runs of multiple spaces between tokens are collapsed.
// Example: "GET    foo\r\n" → {Name: "GET", Args: ["foo"]}
func TestInlineCollapsesMultipleSpaces(t *testing.T) {
	cmd, err := parse(t, "GET    foo\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "GET", "foo")
}

// Verifies LF-only line endings (no CR) are accepted.
// Example: "PING\n" → {Name: "PING", Args: []}
func TestInlineLFOnlyLineEnding(t *testing.T) {
	cmd, err := parse(t, "PING\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "PING")
}

// Verifies leading and trailing spaces around a command are ignored.
// Example: "  GET foo  \r\n" → {Name: "GET", Args: ["foo"]}
func TestInlineLeadingTrailingSpaces(t *testing.T) {
	cmd, err := parse(t, "  GET foo  \r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "GET", "foo")
}

// Verifies tabs act as token separators like spaces.
// Example: "GET\tfoo\r\n" → {Name: "GET", Args: ["foo"]}
func TestInlineTabSeparator(t *testing.T) {
	cmd, err := parse(t, "GET\tfoo\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "GET", "foo")
}

// Verifies a mix of tabs and spaces (leading, inner, trailing) is collapsed.
// Example: "\t SET \t k   v \t\r\n" → {Name: "SET", Args: ["k", "v"]}
func TestInlineMixedTabsAndSpaces(t *testing.T) {
	cmd, err := parse(t, "\t SET \t k   v \t\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "SET", "k", "v")
}

// ---- BLANK LINE HANDLING ----

// Verifies a leading blank line is skipped and the next command is parsed.
// Example: "\r\nPING\r\n" → {Name: "PING", Args: []}
func TestBlankLineSkipped(t *testing.T) {
	cmd, err := parse(t, "\r\nPING\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "PING")
}

// Verifies a whitespace-only line is skipped, same as a truly-blank line.
// Example: "   \r\nPING\r\n" → {Name: "PING", Args: []}
func TestWhitespaceOnlyLineSkipped(t *testing.T) {
	cmd, err := parse(t, "   \r\nPING\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "PING")
}

// Verifies a tab-only line is skipped (tabs are whitespace too).
// Example: "\t\t\r\nPING\r\n" → {Name: "PING", Args: []}
func TestTabOnlyLineSkipped(t *testing.T) {
	cmd, err := parse(t, "\t\t\r\nPING\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "PING")
}

// Verifies a run of mixed blank and whitespace-only lines is fully skipped.
// Example: "\r\n   \r\n\t\r\nGET foo\r\n" → {Name: "GET", Args: ["foo"]}
func TestMixedBlankAndWhitespaceLinesSkipped(t *testing.T) {
	cmd, err := parse(t, "\r\n   \r\n\t\r\nGET foo\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "GET", "foo")
}

// Verifies a lone whitespace-only line with nothing after it returns io.EOF —
// the line is skipped, then the stream is exhausted.
// Example: "   \r\n" → io.EOF
func TestWhitespaceOnlyLineThenEOF(t *testing.T) {
	_, err := parse(t, "   \r\n")
	if err != io.EOF {
		t.Errorf("expected io.EOF after lone whitespace line, got %v", err)
	}
}

// Verifies several consecutive blank lines are all skipped.
// Example: "\r\n\r\n\r\nGET foo\r\n" → {Name: "GET", Args: ["foo"]}
func TestMultipleBlankLinesSkipped(t *testing.T) {
	cmd, err := parse(t, "\r\n\r\n\r\nGET foo\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "GET", "foo")
}

// Verifies a blank line skipped before a RESP array still parses the array.
// Example: "\r\n*1\r\n$4\r\nPING\r\n" → {Name: "PING", Args: []}
func TestBlankLineSkippedBeforeResp(t *testing.T) {
	cmd, err := parse(t, "\r\n*1\r\n$4\r\nPING\r\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "PING")
}

// Verifies a lone blank line with nothing after it returns io.EOF — the
// blank is skipped, then the stream is exhausted.
// Example: "\r\n" → io.EOF
func TestBlankLineThenEOF(t *testing.T) {
	_, err := parse(t, "\r\n")
	if err != io.EOF {
		t.Errorf("expected io.EOF after lone blank line, got %v", err)
	}
}

// Verifies blank lines between two commands are skipped during sequential
// reads — the second ReadCommand returns the command after the blanks.
// Example: "PING\r\n\r\n\r\nGET foo\r\n" → PING, then GET foo
func TestBlankLinesBetweenCommands(t *testing.T) {
	p := New(strings.NewReader("PING\r\n\r\n\r\nGET foo\r\n"))

	c1, err := p.ReadCommand()
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	assertCmd(t, c1, "PING")

	c2, err := p.ReadCommand()
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	assertCmd(t, c2, "GET", "foo")
}

// ---- RESP ARRAY COMMANDS ----

// Verifies a basic two-element RESP array parses into name + one arg.
// Example: "*2\r\n$3\r\nGET\r\n$3\r\nfoo\r\n" → {Name: "GET", Args: ["foo"]}
func TestRespArraySimple(t *testing.T) {
	input := "*2\r\n$3\r\nGET\r\n$3\r\nfoo\r\n"
	cmd, err := parse(t, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "GET", "foo")
}

// Verifies a single-element RESP array parses into name with no args.
// Example: "*1\r\n$4\r\nPING\r\n" → {Name: "PING", Args: []}
func TestRespArraySingleElement(t *testing.T) {
	input := "*1\r\n$4\r\nPING\r\n"
	cmd, err := parse(t, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "PING")
}

// Verifies a four-element RESP array parses into name + three args.
// Example: "*4\r\n$3\r\nSET\r\n$3\r\nkey\r\n$3\r\nval\r\n$2\r\nEX\r\n"
//
//	→ {Name: "SET", Args: ["key", "val", "EX"]}
func TestRespArrayMultipleArgs(t *testing.T) {
	input := "*4\r\n$3\r\nSET\r\n$3\r\nkey\r\n$3\r\nval\r\n$2\r\nEX\r\n"
	cmd, err := parse(t, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "SET", "key", "val", "EX")
}

// Verifies the command name is uppercased even when sent via RESP.
// Example: "*1\r\n$4\r\nping\r\n" → {Name: "PING", Args: []}
func TestRespArrayUppercasesName(t *testing.T) {
	input := "*1\r\n$4\r\nping\r\n"
	cmd, err := parse(t, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "PING")
}

// Verifies argument case is preserved (only the verb is normalized).
// Example: "*2\r\n$3\r\nGET\r\n$5\r\nMyKey\r\n" → {Name: "GET", Args: ["MyKey"]}
func TestRespArrayPreservesArgCase(t *testing.T) {
	input := "*2\r\n$3\r\nGET\r\n$5\r\nMyKey\r\n"
	cmd, err := parse(t, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "GET", "MyKey")
}

// Verifies a zero-length bulk string ("$0") parses as an empty argument.
// Example: "*2\r\n$3\r\nGET\r\n$0\r\n\r\n" → {Name: "GET", Args: [""]}
func TestRespArrayEmptyBulkString(t *testing.T) {
	input := "*2\r\n$3\r\nGET\r\n$0\r\n\r\n"
	cmd, err := parse(t, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "GET", "")
}

// Verifies null bytes inside a bulk string payload are preserved.
// Example: "$4\r\nab\x00d\r\n" inside a SET → Args: ["k", "ab\x00d"]
func TestRespArrayBinarySafeValue(t *testing.T) {
	// $4 bulk string contains a null byte
	input := "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$4\r\nab\x00d\r\n"
	cmd, err := parse(t, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "SET", "k", "ab\x00d")
}

// Verifies CRLF inside a bulk string payload is preserved (length-prefixed,
// not delimiter-scanned).
// Example: "$6\r\nab\r\ncd\r\n" inside a SET → Args: ["k", "ab\r\ncd"]
func TestRespArrayValueWithCRLF(t *testing.T) {
	// Bulk string explicitly carries \r\n inside the payload
	input := "*3\r\n$3\r\nSET\r\n$1\r\nk\r\n$6\r\nab\r\ncd\r\n"
	cmd, err := parse(t, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCmd(t, cmd, "SET", "k", "ab\r\ncd")
}

// ---- RESP ARRAY ERRORS ----

// Verifies a non-numeric array length returns an error.
// Example: "*abc\r\n" → error
func TestRespInvalidArrayLength(t *testing.T) {
	_, err := parse(t, "*abc\r\n")
	if err == nil {
		t.Errorf("expected error for non-numeric array length")
	}
}

// Verifies a zero-length array returns an error.
// Example: "*0\r\n" → error
func TestRespZeroArrayLength(t *testing.T) {
	_, err := parse(t, "*0\r\n")
	if err == nil {
		t.Errorf("expected error for zero-length array")
	}
}

// Verifies a negative array length returns an error.
// Example: "*-1\r\n" → error
func TestRespNegativeArrayLength(t *testing.T) {
	_, err := parse(t, "*-1\r\n")
	if err == nil {
		t.Errorf("expected error for negative array length")
	}
}

// Verifies an element line missing the '$' prefix returns an error.
// Example: "*1\r\nFOO\r\n" → error (FOO missing $)
func TestRespMissingBulkStringMarker(t *testing.T) {
	// Array claims 1 element but next line lacks `$`
	_, err := parse(t, "*1\r\nFOO\r\n")
	if err == nil {
		t.Errorf("expected error for missing $ marker on bulk string")
	}
}

// Verifies a non-numeric bulk string length returns an error.
// Example: "*1\r\n$xyz\r\nFOO\r\n" → error
func TestRespInvalidBulkLength(t *testing.T) {
	_, err := parse(t, "*1\r\n$xyz\r\nFOO\r\n")
	if err == nil {
		t.Errorf("expected error for non-numeric bulk string length")
	}
}

// Verifies stream ending right after array header returns an error.
// Example: "*2\r\n" (no elements follow) → error
func TestRespEOFAfterArrayHeader(t *testing.T) {
	// Array claims 2 elements but stream ends
	_, err := parse(t, "*2\r\n")
	if err == nil {
		t.Errorf("expected error when stream ends mid-array")
	}
}

// Verifies a truncated bulk string payload returns an error.
// Example: "*1\r\n$10\r\nabc" (claims 10 bytes, gets 3) → error
func TestRespEOFMidBulkString(t *testing.T) {
	// Bulk says 10 bytes but only 3 provided
	_, err := parse(t, "*1\r\n$10\r\nabc")
	if err == nil {
		t.Errorf("expected error when bulk string truncated")
	}
}

// ---- IO ERRORS ----

// Verifies parsing empty input returns io.EOF.
// Example: "" → io.EOF
func TestEmptyInput(t *testing.T) {
	_, err := parse(t, "")
	if err != io.EOF {
		t.Errorf("expected io.EOF on empty input, got %v", err)
	}
}

// Verifies input without a line terminator returns an error.
// Example: "PING" (no \r\n) → error
func TestNoTerminator(t *testing.T) {
	// No \r\n — bufio.Reader returns io.EOF
	_, err := parse(t, "PING")
	if err == nil {
		t.Errorf("expected error for input without terminator")
	}
}

// ---- SEQUENTIAL READS ----

// Verifies three back-to-back inline commands parse correctly from one
// parser, and that the stream returns io.EOF after the last command.
// Example: "PING\r\nGET foo\r\nSET k v\r\n" → PING, GET foo, SET k v, then io.EOF
func TestSequentialInlineCommands(t *testing.T) {
	input := "PING\r\nGET foo\r\nSET k v\r\n"
	p := New(strings.NewReader(input))

	c1, err := p.ReadCommand()
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	assertCmd(t, c1, "PING")

	c2, err := p.ReadCommand()
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	assertCmd(t, c2, "GET", "foo")

	c3, err := p.ReadCommand()
	if err != nil {
		t.Fatalf("read 3: %v", err)
	}
	assertCmd(t, c3, "SET", "k", "v")

	_, err = p.ReadCommand()
	if err != io.EOF {
		t.Errorf("expected io.EOF after last command, got %v", err)
	}
}

// Verifies two back-to-back RESP commands parse correctly from one parser.
// Example: "*1\r\n$4\r\nPING\r\n*2\r\n$3\r\nGET\r\n$3\r\nfoo\r\n" → PING, GET foo
func TestSequentialRespCommands(t *testing.T) {
	input := "*1\r\n$4\r\nPING\r\n*2\r\n$3\r\nGET\r\n$3\r\nfoo\r\n"
	p := New(strings.NewReader(input))

	c1, err := p.ReadCommand()
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	assertCmd(t, c1, "PING")

	c2, err := p.ReadCommand()
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	assertCmd(t, c2, "GET", "foo")
}

// Verifies the parser auto-detects format per command — inline followed
// by RESP works through the same parser instance.
// Example: "PING\r\n*2\r\n$3\r\nGET\r\n$3\r\nfoo\r\n" → PING (inline), GET foo (RESP)
func TestMixedInlineAndResp(t *testing.T) {
	input := "PING\r\n*2\r\n$3\r\nGET\r\n$3\r\nfoo\r\n"
	p := New(strings.NewReader(input))

	c1, err := p.ReadCommand()
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	assertCmd(t, c1, "PING")

	c2, err := p.ReadCommand()
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	assertCmd(t, c2, "GET", "foo")
}
