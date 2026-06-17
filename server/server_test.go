package server

import (
	"bufio"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/store"
)

// testConn creates an in-memory server/client pair using net.Pipe.
// handleConnection runs in a goroutine on the server side.
// t.Cleanup closes the client connection.
func testConn(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()
	st := store.New()
	s := New("", st)
	client, srv := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go s.handleConnection(srv)
	return client, bufio.NewReader(client)
}

func writeLine(t *testing.T, conn net.Conn, line string) {
	t.Helper()
	conn.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := fmt.Fprintf(conn, "%s\r\n", line); err != nil {
		t.Fatalf("write %q: %v", line, err)
	}
}

func readLine(t *testing.T, r *bufio.Reader, conn net.Conn) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("readLine: %v", err)
	}
	return line
}

// readResp reads one complete RESP value (handles bulk strings).
func readResp(t *testing.T, r *bufio.Reader, conn net.Conn) string {
	t.Helper()
	line := readLine(t, r, conn)
	if len(line) > 1 && line[0] == '$' && line != "$-1\r\n" {
		data := readLine(t, r, conn)
		return line + data
	}
	return line
}

func readNLines(t *testing.T, r *bufio.Reader, conn net.Conn, n int) []string {
	t.Helper()
	lines := make([]string, n)
	for i := range n {
		lines[i] = readLine(t, r, conn)
	}
	return lines
}

// ---- PING ----

func TestPING(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "PING")
	if got := readResp(t, r, conn); got != "+PONG\r\n" {
		t.Errorf("PING = %q, want +PONG\\r\\n", got)
	}
}

// ---- SET / GET ----

func TestSETAndGET(t *testing.T) {
	conn, r := testConn(t)

	writeLine(t, conn, "SET foo bar")
	if got := readResp(t, r, conn); got != "+OK\r\n" {
		t.Errorf("SET = %q, want +OK\\r\\n", got)
	}

	writeLine(t, conn, "GET foo")
	if got := readResp(t, r, conn); got != "$3\r\nbar\r\n" {
		t.Errorf("GET = %q, want $3\\r\\nbar\\r\\n", got)
	}
}

func TestGETMissingKey(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "GET nosuchkey")
	if got := readResp(t, r, conn); got != "$-1\r\n" {
		t.Errorf("GET missing = %q, want $-1\\r\\n", got)
	}
}

func TestSETWrongArgs(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "SET onlykey")
	got := readResp(t, r, conn)
	if len(got) == 0 || got[0] != '-' {
		t.Errorf("SET with missing value = %q, want error", got)
	}
}

// ---- DEL ----

func TestDELExistingKey(t *testing.T) {
	conn, r := testConn(t)

	writeLine(t, conn, "SET k v")
	readResp(t, r, conn)

	writeLine(t, conn, "DEL k")
	if got := readResp(t, r, conn); got != ":1\r\n" {
		t.Errorf("DEL existing = %q, want :1\\r\\n", got)
	}

	writeLine(t, conn, "GET k")
	if got := readResp(t, r, conn); got != "$-1\r\n" {
		t.Errorf("GET after DEL = %q, want $-1\\r\\n", got)
	}
}

func TestDELMissingKey(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "DEL nosuchkey")
	if got := readResp(t, r, conn); got != ":0\r\n" {
		t.Errorf("DEL missing = %q, want :0\\r\\n", got)
	}
}

// ---- UNKNOWN COMMAND ----

func TestUnknownCommand(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "FOOBAR")
	want := "-ERR unknown command FOOBAR\r\n"
	if got := readResp(t, r, conn); got != want {
		t.Errorf("unknown cmd = %q, want %q", got, want)
	}
}

// ---- SEQUENTIAL COMMANDS ----

func TestSequentialCommands(t *testing.T) {
	conn, r := testConn(t)

	steps := []struct {
		cmd  string
		want string
	}{
		{"PING", "+PONG\r\n"},
		{"SET x 42", "+OK\r\n"},
		{"GET x", "$2\r\n42\r\n"},
		{"DEL x", ":1\r\n"},
		{"GET x", "$-1\r\n"},
	}

	for _, tc := range steps {
		writeLine(t, conn, tc.cmd)
		if got := readResp(t, r, conn); got != tc.want {
			t.Errorf("%q → %q, want %q", tc.cmd, got, tc.want)
		}
	}
}

// ---- RESP WIRE FORMAT ----

func TestRESPProtocol(t *testing.T) {
	conn, r := testConn(t)
	conn.SetWriteDeadline(time.Now().Add(time.Second))

	fmt.Fprintf(conn, "*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$3\r\nval\r\n")
	if got := readResp(t, r, conn); got != "+OK\r\n" {
		t.Errorf("RESP SET = %q, want +OK\\r\\n", got)
	}

	fmt.Fprintf(conn, "*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n")
	if got := readResp(t, r, conn); got != "$3\r\nval\r\n" {
		t.Errorf("RESP GET = %q, want $3\\r\\nval\\r\\n", got)
	}
}

// ---- CONNECTION CLOSE ----

func TestClientDisconnect(t *testing.T) {
	conn, _ := testConn(t)
	// Close immediately — handleConnection must exit cleanly without panic.
	conn.Close()
	time.Sleep(10 * time.Millisecond)
}

// ---- PUB/SUB ----

func TestSubscribeAndPublish(t *testing.T) {
	st := store.New()
	s := New("", st)

	subConn, subSrv := net.Pipe()
	t.Cleanup(func() { subConn.Close() })
	go s.handleConnection(subSrv)

	pubConn, pubSrv := net.Pipe()
	t.Cleanup(func() { pubConn.Close() })
	go s.handleConnection(pubSrv)

	subR := bufio.NewReader(subConn)
	pubR := bufio.NewReader(pubConn)

	// Subscribe to "news"
	subConn.SetWriteDeadline(time.Now().Add(time.Second))
	fmt.Fprintf(subConn, "SUBSCRIBE news\r\n")

	// Confirmation: *3\r\n$9\r\nsubscribe\r\n$4\r\nnews\r\n$1\r\n1\r\n (7 lines)
	readNLines(t, subR, subConn, 7)

	// Publish "hello" to "news"
	pubConn.SetWriteDeadline(time.Now().Add(time.Second))
	fmt.Fprintf(pubConn, "PUBLISH news hello\r\n")

	// Publisher response: :1\r\n (1 subscriber received it)
	pubConn.SetReadDeadline(time.Now().Add(time.Second))
	if got := readLine(t, pubR, pubConn); got != ":1\r\n" {
		t.Errorf("PUBLISH response = %q, want :1\\r\\n", got)
	}

	// Subscriber receives: *3\r\n$7\r\nmessage\r\n$4\r\nnews\r\n$5\r\nhello\r\n (7 lines)
	lines := readNLines(t, subR, subConn, 7)
	if lines[0] != "*3\r\n" {
		t.Errorf("message header = %q, want *3\\r\\n", lines[0])
	}
	if lines[6] != "hello\r\n" {
		t.Errorf("message payload = %q, want hello\\r\\n", lines[6])
	}
}

// TestTTLCountdown samples TTL every second over the wire to catch any server-layer expiry bugs.
// Run with: go test -v -run TestTTLCountdown ./server/
func TestTTLCountdown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping TTL countdown test in -short mode")
	}
	conn, r := testConn(t)

	writeLine(t, conn, "SET countdown val EX 10")
	readResp(t, r, conn) // discard +OK

	for tick := range 13 {
		writeLine(t, conn, "TTL countdown")
		got := readResp(t, r, conn)
		t.Logf("t+%2ds → TTL = %s", tick, got[:len(got)-2]) // strip \r\n for readability
		time.Sleep(time.Second)
	}
}

func TestSubscribeNoTopics(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "SUBSCRIBE")
	got := readResp(t, r, conn)
	if len(got) == 0 || got[0] != '-' {
		t.Errorf("SUBSCRIBE with no topics = %q, want error", got)
	}
}
