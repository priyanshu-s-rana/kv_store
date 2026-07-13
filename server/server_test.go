package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/priyanshu-s-rana/kv_store/store"
)

// fakePersistence is a no-op store.Persistence used to run a real store
// event loop in tests without touching disk.
type fakePersistence struct{}

func (fakePersistence) Append(constants.CmdName, []string) error        { return nil }
func (fakePersistence) Checkpoint(map[string]store.SnapshotEntry) error { return nil }
func (fakePersistence) CheckpointSuccess() error                        { return nil }
func (fakePersistence) Rebaseline(map[string]store.SnapshotEntry) error { return nil }

// noopStoreMetrics is a no-op store.StoreMetrics used to build a real Store
// in server tests that don't inspect store-level metrics.
type noopStoreMetrics struct{}

func (noopStoreMetrics) IncCommandsExecuted(constants.CmdName)                   {}
func (noopStoreMetrics) IncCommandFailures(constants.CmdName)                    {}
func (noopStoreMetrics) ObserveCommandDuration(constants.CmdName, time.Duration) {}
func (noopStoreMetrics) SetCurrentMemoryBytes(int64)                             {}
func (noopStoreMetrics) SetPeakMemoryBytes(int64)                                {}
func (noopStoreMetrics) SetMaxMemoryBytes(int64)                                 {}
func (noopStoreMetrics) SetMemoryUtilization(float32)                            {}
func (noopStoreMetrics) SetKeyCount(int64)                                       {}
func (noopStoreMetrics) SetKeyBytes(int64)                                       {}
func (noopStoreMetrics) SetValueBytes(int64)                                     {}
func (noopStoreMetrics) SetTTLBytes(int64)                                       {}
func (noopStoreMetrics) SetLRUBytes(int64)                                       {}
func (noopStoreMetrics) SetPubSubBytes(int64)                                    {}
func (noopStoreMetrics) IncExpiredKeys()                                         {}
func (noopStoreMetrics) ObserveTTLExpiryDuration(time.Duration)                  {}
func (noopStoreMetrics) SetActiveTopics(int64)                                   {}
func (noopStoreMetrics) SetActiveSubscribers(int64)                              {}
func (noopStoreMetrics) IncMessagesPublished()                                   {}
func (noopStoreMetrics) ObservePublishDuration(time.Duration)                    {}

var _ store.StoreMetrics = noopStoreMetrics{}

// spyServerMetrics is a ServerMetrics test double that records every call, so
// tests can assert on server-level metrics that no longer have a public
// GetStats-style accessor.
type spyServerMetrics struct {
	mu sync.Mutex

	connectionsAccepted int
	connectionsClosed   int
	activeConnections   int64
	bytesSent           int64
	bytesReceived       int64
	parserErrors        int
	commandsReceived    map[constants.CmdName]int
	failedCommands      map[constants.CmdName]int
}

func newSpyServerMetrics() *spyServerMetrics {
	return &spyServerMetrics{
		commandsReceived: make(map[constants.CmdName]int),
		failedCommands:   make(map[constants.CmdName]int),
	}
}

func (m *spyServerMetrics) IncConnectionsAccepted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectionsAccepted++
}

func (m *spyServerMetrics) IncConnectionsClosed() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectionsClosed++
}

func (m *spyServerMetrics) SetActiveConnections(count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeConnections = count
}

func (m *spyServerMetrics) IncBytesSent(n int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bytesSent += n
}

func (m *spyServerMetrics) IncBytesReceived(n int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bytesReceived += n
}

func (m *spyServerMetrics) IncParserErrors() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.parserErrors++
}

func (m *spyServerMetrics) IncCommandsReceived(cmd constants.CmdName) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commandsReceived[cmd]++
}

func (m *spyServerMetrics) IncFailedCommands(cmd constants.CmdName) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failedCommands[cmd]++
}

func (m *spyServerMetrics) ObserveCommandDuration(constants.CmdName, time.Duration) {}
func (m *spyServerMetrics) ObserveResponseWriteDuration(time.Duration)              {}

func (m *spyServerMetrics) TotalConnectionsAccepted() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connectionsAccepted
}

func (m *spyServerMetrics) TotalCommandsReceived() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := 0
	for _, n := range m.commandsReceived {
		total += n
	}
	return total
}

func (m *spyServerMetrics) TotalFailedCommands() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := 0
	for _, n := range m.failedCommands {
		total += n
	}
	return total
}

func (m *spyServerMetrics) BytesReceived() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bytesReceived
}

func (m *spyServerMetrics) BytesSent() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bytesSent
}

var _ ServerMetrics = (*spyServerMetrics)(nil)

// newTestStore builds a Store with its event loop running against a fresh
// command channel wired to a no-op Persistence.
func newTestStore() (*store.Store, chan store.Command) {
	cmdChan := make(chan store.Command)
	st := store.New(0, cmdChan, fakePersistence{}, noopStoreMetrics{})
	st.Start()
	return st, cmdChan
}

// testConn creates an in-memory server/client pair using net.Pipe.
// handleConnection runs in a goroutine on the server side.
// t.Cleanup closes the client connection.
func testConn(t *testing.T) (net.Conn, *bufio.Reader) {
	t.Helper()
	st, cmdChan := newTestStore()
	s := New("", cmdChan, st, newSpyServerMetrics())
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
	st, cmdChan := newTestStore()
	s := New("", cmdChan, st, newSpyServerMetrics())

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

// readFullBulkResp reads a RESP bulk string including multi-line content.
// Regular readResp stops at the first \n inside the payload, so MGET (which
// returns a numbered bulk string with embedded newlines) needs this helper.
func readFullBulkResp(t *testing.T, r *bufio.Reader, conn net.Conn) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	header, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("readFullBulkResp header: %v", err)
	}
	if header == "$-1\r\n" || len(header) == 0 || header[0] != '$' {
		return header
	}
	n, err := strconv.Atoi(strings.TrimRight(header[1:], "\r\n"))
	if err != nil {
		t.Fatalf("readFullBulkResp parse length %q: %v", header, err)
	}
	buf := make([]byte, n+2) // +2 for trailing \r\n
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("readFullBulkResp payload: %v", err)
	}
	return header + string(buf)
}

// ---- MSET ----

func TestMSET(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "MSET k1 v1 k2 v2")
	if got := readResp(t, r, conn); got != "+OK\r\n" {
		t.Errorf("MSET = %q, want +OK\\r\\n", got)
	}
	writeLine(t, conn, "GET k1")
	if got := readResp(t, r, conn); got != "$2\r\nv1\r\n" {
		t.Errorf("GET k1 after MSET = %q, want $2\\r\\nv1\\r\\n", got)
	}
	writeLine(t, conn, "GET k2")
	if got := readResp(t, r, conn); got != "$2\r\nv2\r\n" {
		t.Errorf("GET k2 after MSET = %q, want $2\\r\\nv2\\r\\n", got)
	}
}

func TestMSETWrongArgs(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "MSET k1")
	got := readResp(t, r, conn)
	if len(got) == 0 || got[0] != '-' {
		t.Errorf("MSET odd args = %q, want error", got)
	}
}

// ---- MGET ----

func TestMGET(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "MSET k1 v1 k2 v2")
	readResp(t, r, conn)

	writeLine(t, conn, "MGET k1 k2")
	got := readFullBulkResp(t, r, conn)
	if !strings.Contains(got, "v1") {
		t.Errorf("MGET missing v1: %q", got)
	}
	if !strings.Contains(got, "v2") {
		t.Errorf("MGET missing v2: %q", got)
	}
}

func TestMGETMissingKey(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "SET k v")
	readResp(t, r, conn)

	writeLine(t, conn, "MGET k missing")
	got := readFullBulkResp(t, r, conn)
	if !strings.Contains(got, "v") {
		t.Errorf("MGET missing existing value: %q", got)
	}
	if !strings.Contains(got, constants.NIL_DISPLAY) {
		t.Errorf("MGET missing (nil) for missing key: %q", got)
	}
}

func TestMGETWrongArgs(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "MGET")
	got := readResp(t, r, conn)
	if len(got) == 0 || got[0] != '-' {
		t.Errorf("MGET no args = %q, want error", got)
	}
}

// ---- INCR ----

func TestINCRNewKey(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "INCR counter")
	if got := readResp(t, r, conn); got != ":1\r\n" {
		t.Errorf("INCR new key = %q, want :1\\r\\n", got)
	}
}

func TestINCRExistingKey(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "SET n 5")
	readResp(t, r, conn)
	writeLine(t, conn, "INCR n")
	if got := readResp(t, r, conn); got != ":6\r\n" {
		t.Errorf("INCR existing = %q, want :6\\r\\n", got)
	}
}

func TestINCRNonInteger(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "SET k notanint")
	readResp(t, r, conn)
	writeLine(t, conn, "INCR k")
	got := readResp(t, r, conn)
	if len(got) == 0 || got[0] != '-' {
		t.Errorf("INCR non-integer = %q, want error", got)
	}
}

// ---- DECR ----

func TestDECRNewKey(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "DECR counter")
	if got := readResp(t, r, conn); got != ":-1\r\n" {
		t.Errorf("DECR new key = %q, want :-1\\r\\n", got)
	}
}

func TestDECRExistingKey(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "SET n 5")
	readResp(t, r, conn)
	writeLine(t, conn, "DECR n")
	if got := readResp(t, r, conn); got != ":4\r\n" {
		t.Errorf("DECR existing = %q, want :4\\r\\n", got)
	}
}

func TestDECRNonInteger(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "SET k notanint")
	readResp(t, r, conn)
	writeLine(t, conn, "DECR k")
	got := readResp(t, r, conn)
	if len(got) == 0 || got[0] != '-' {
		t.Errorf("DECR non-integer = %q, want error", got)
	}
}

// ---- EXPIRE / TTL ----

func TestExpireAndTTL(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "SET k v")
	readResp(t, r, conn)

	writeLine(t, conn, "EXPIRE k 30")
	if got := readResp(t, r, conn); got != ":1\r\n" {
		t.Errorf("EXPIRE = %q, want :1\\r\\n", got)
	}

	writeLine(t, conn, "TTL k")
	got := readResp(t, r, conn)
	if got != ":30\r\n" && got != ":29\r\n" {
		t.Errorf("TTL after EXPIRE = %q, want ~:30\\r\\n", got)
	}
}

func TestTTLNoExpiry(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "SET k v")
	readResp(t, r, conn)

	writeLine(t, conn, "TTL k")
	if got := readResp(t, r, conn); got != ":-1\r\n" {
		t.Errorf("TTL no expiry = %q, want :-1\\r\\n", got)
	}
}

func TestTTLMissingKey(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "TTL nosuchkey")
	if got := readResp(t, r, conn); got != ":-2\r\n" {
		t.Errorf("TTL missing = %q, want :-2\\r\\n", got)
	}
}

func TestExpireWrongArgs(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "EXPIRE k")
	got := readResp(t, r, conn)
	if len(got) == 0 || got[0] != '-' {
		t.Errorf("EXPIRE wrong args = %q, want error", got)
	}
}

// ---- KEYS ----

func TestKEYS(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "SET user:1 a")
	readResp(t, r, conn)
	writeLine(t, conn, "SET user:2 b")
	readResp(t, r, conn)
	writeLine(t, conn, "SET session:1 c")
	readResp(t, r, conn)

	writeLine(t, conn, "KEYS user:*")
	got := readFullBulkResp(t, r, conn)
	if !strings.Contains(got, "user:1") {
		t.Errorf("KEYS user:* missing user:1: %q", got)
	}
	if !strings.Contains(got, "user:2") {
		t.Errorf("KEYS user:* missing user:2: %q", got)
	}
	if strings.Contains(got, "session:1") {
		t.Errorf("KEYS user:* should not contain session:1: %q", got)
	}
}

func TestKEYSAll(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "SET a 1")
	readResp(t, r, conn)
	writeLine(t, conn, "SET b 2")
	readResp(t, r, conn)

	writeLine(t, conn, "KEYS *")
	got := readFullBulkResp(t, r, conn)
	if !strings.Contains(got, "a") || !strings.Contains(got, "b") {
		t.Errorf("KEYS * = %q, want both a and b", got)
	}
}

func TestKEYSWrongArgs(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "KEYS")
	got := readResp(t, r, conn)
	if len(got) == 0 || got[0] != '-' {
		t.Errorf("KEYS no args = %q, want error", got)
	}
}

// ---- FLUSHALL ----

func TestFLUSHALL(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "SET k1 v1")
	readResp(t, r, conn)
	writeLine(t, conn, "SET k2 v2")
	readResp(t, r, conn)

	writeLine(t, conn, "FLUSHALL")
	if got := readResp(t, r, conn); got != "+OK\r\n" {
		t.Errorf("FLUSHALL = %q, want +OK\\r\\n", got)
	}

	writeLine(t, conn, "GET k1")
	if got := readResp(t, r, conn); got != "$-1\r\n" {
		t.Errorf("GET after FLUSHALL = %q, want $-1\\r\\n", got)
	}
}

// ---- MEMORYSTATS ----

func TestMEMORYSTATS(t *testing.T) {
	conn, r := testConn(t)
	writeLine(t, conn, "SET k v")
	readResp(t, r, conn)

	writeLine(t, conn, "MEMORYSTATS")
	got := readFullBulkResp(t, r, conn)
	if !strings.Contains(got, "currentSize") {
		t.Errorf("MEMORYSTATS missing currentSize: %q", got)
	}
}

// ---- metrics ----

// testServer creates an in-memory server and returns the connection, reader, and the metrics spy it reports to.
func testServer(t *testing.T) (net.Conn, *bufio.Reader, *spyServerMetrics) {
	t.Helper()
	cmdChan := make(chan store.Command)
	st := store.New(0, cmdChan, fakePersistence{}, noopStoreMetrics{})
	st.Start()
	metrics := newSpyServerMetrics()
	s := New("", cmdChan, st, metrics)
	client, srv := net.Pipe()
	t.Cleanup(func() { client.Close() })
	go s.handleConnection(srv)
	return client, bufio.NewReader(client), metrics
}

func TestMetricsTotalConnectionsAccepted(t *testing.T) {
	cmdChan := make(chan store.Command)
	st := store.New(0, cmdChan, fakePersistence{}, noopStoreMetrics{})
	st.Start()
	metrics := newSpyServerMetrics()
	s := New("", cmdChan, st, metrics)

	c1, srv1 := net.Pipe()
	c2, srv2 := net.Pipe()
	t.Cleanup(func() { c1.Close(); c2.Close() })
	go s.handleConnection(srv1)
	go s.handleConnection(srv2)

	r1 := bufio.NewReader(c1)
	r2 := bufio.NewReader(c2)
	writeLine(t, c1, "PING")
	readResp(t, r1, c1)
	writeLine(t, c2, "PING")
	readResp(t, r2, c2)

	if got := metrics.TotalConnectionsAccepted(); got < 2 {
		t.Errorf("TotalConnectionsAccepted = %d, want >= 2", got)
	}
}

func TestMetricsCommandsReceived(t *testing.T) {
	conn, r, metrics := testServer(t)

	writeLine(t, conn, "PING")
	readResp(t, r, conn)
	writeLine(t, conn, "PING")
	readResp(t, r, conn)
	writeLine(t, conn, "PING")
	readResp(t, r, conn)

	if got := metrics.TotalCommandsReceived(); got < 3 {
		t.Errorf("CommandsReceived = %d, want >= 3", got)
	}
}

func TestMetricsBytesReceived(t *testing.T) {
	conn, r, metrics := testServer(t)

	writeLine(t, conn, "PING")
	readResp(t, r, conn)

	if got := metrics.BytesReceived(); got <= 0 {
		t.Errorf("BytesReceived = %d, want > 0", got)
	}
}

func TestMetricsBytesSent(t *testing.T) {
	conn, r, metrics := testServer(t)

	writeLine(t, conn, "PING")
	readResp(t, r, conn)
	// Send a second command so the server goroutine has definitely finished
	// writing (and accounting for) the first response before we read stats.
	// bytesSent is updated after conn.Write returns, which races with readResp
	// on net.Pipe; the extra round-trip makes that race impossible.
	writeLine(t, conn, "PING")
	readResp(t, r, conn)

	if got := metrics.BytesSent(); got <= 0 {
		t.Errorf("BytesSent = %d, want > 0", got)
	}
}

func TestMetricsFailedCommands(t *testing.T) {
	conn, r, metrics := testServer(t)

	// SET with missing value returns a RESP error, which increments FailedCommands.
	writeLine(t, conn, "SET onlykey")
	got := readResp(t, r, conn)
	if len(got) == 0 || got[0] != '-' {
		t.Fatalf("expected error response, got %q", got)
	}

	if got := metrics.TotalFailedCommands(); got < 1 {
		t.Errorf("FailedCommands = %d, want >= 1", got)
	}
}
