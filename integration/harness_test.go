//go:build integration

// Package integration runs the real kv-server binary as a subprocess and
// drives it over a real TCP connection, so that crash/restart/recovery
// behavior is exercised the way it actually happens in production: real OS
// process kill, real file descriptors, real config/env wiring — none of
// which an in-process test (constructing Store/Persistence directly) can
// observe. See persistence/persistence_test.go for the in-process
// complement to these tests (it already covers many recovery-ordering edge
// cases at the unit level); this package intentionally does not repeat
// those, focusing instead on real-process crash timing and OS-level
// durability.
//
// Run with: go test -tags=integration ./integration/...
package integration

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/parser"
)

// binPath is the compiled kv-server binary, built once in TestMain and
// shared read-only across every test in this package.
var binPath string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "kv-server-bin-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration TestMain: MkdirTemp:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration TestMain: repoRoot:", err)
		os.Exit(1)
	}

	binPath = filepath.Join(tmpDir, "kv-server-under-test")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/kv-server")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "integration TestMain: go build ./cmd/kv-server failed: %v\n%s\n", err, out)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

// repoRoot returns the repository root, derived from this file's own path
// so the harness works regardless of the directory `go test` is invoked from.
func repoRoot() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller failed")
	}
	// this file lives at <root>/integration/harness_test.go
	return filepath.Dir(filepath.Dir(thisFile)), nil
}

// ============================================================
// process lifecycle
// ============================================================

type instance struct {
	t           *testing.T
	cmd         *exec.Cmd
	addr        string
	metricsAddr string
	dataDir     string
	syncPolicy  string

	exited  chan struct{}
	exitErr error
}

// freePort asks the OS for an ephemeral port and immediately releases it.
// Small TOCTOU race in theory; standard practice for test harnesses and
// good enough here since each test uses its own pair of ports.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startInstance launches the kv-server binary against dataDir with the given
// journal sync policy, waits for it to accept connections, and registers
// cleanup (kill + wait) via t.Cleanup. dataDir is not removed so callers can
// restart against the same directory to exercise recovery.
func startInstance(t *testing.T, dataDir string, syncPolicy string) *instance {
	t.Helper()

	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}

	port := freePort(t)
	metricsPort := freePort(t)

	in := &instance{
		t:           t,
		addr:        fmt.Sprintf("127.0.0.1:%d", port),
		metricsAddr: fmt.Sprintf("127.0.0.1:%d", metricsPort),
		dataDir:     dataDir,
		syncPolicy:  syncPolicy,
		exited:      make(chan struct{}),
	}

	cmd := exec.Command(binPath)
	cmd.Dir = root // so viper's "./config" AddConfigPath resolves to the real config dir
	cmd.Env = append(os.Environ(),
		"SERVER_HOST=127.0.0.1",
		"SERVER_PORT="+strconv.Itoa(port),
		"METRICS_HOST=127.0.0.1",
		"METRICS_PORT="+strconv.Itoa(metricsPort),
		"PERSISTENCE_JOURNAL_PATH1="+filepath.Join(dataDir, "journal_0.aof"),
		"PERSISTENCE_JOURNAL_PATH2="+filepath.Join(dataDir, "journal_1.aof"),
		"PERSISTENCE_SNAPSHOT_PATH="+filepath.Join(dataDir, "dump.gob"),
		"PERSISTENCE_JOURNAL_POLICY="+syncPolicy,
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	in.cmd = cmd

	if err := cmd.Start(); err != nil {
		t.Fatalf("start kv-server: %v", err)
	}

	go func() {
		in.exitErr = cmd.Wait()
		close(in.exited)
	}()

	t.Cleanup(func() {
		// Checking in.cmd.ProcessState directly here would race with
		// cmd.Wait() (running in the goroutine above) writing it — select
		// on the already-synchronized in.exited channel instead.
		select {
		case <-in.exited:
		default:
			_ = in.cmd.Process.Kill()
		}
		select {
		case <-in.exited:
		case <-time.After(5 * time.Second):
			t.Logf("kv-server process did not exit during cleanup")
		}
		if t.Failed() {
			t.Logf("kv-server stdout:\n%s", stdout.String())
			t.Logf("kv-server stderr:\n%s", stderr.String())
		}
	})

	waitForPort(t, in.addr, 5*time.Second)
	return in
}

// waitForPort polls addr with bounded retries until a TCP connection
// succeeds. No fixed sleeps: returns as soon as the port is accepting.
func waitForPort(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to accept connections: %v", addr, lastErr)
}

// sigterm sends SIGTERM (graceful shutdown path: persist.Close() runs, a
// final checkpoint fires, then the process exits cleanly).
func (in *instance) sigterm() {
	in.t.Helper()
	if err := in.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		in.t.Fatalf("SIGTERM: %v", err)
	}
}

// sigkill hard-kills the process: no shutdown hooks run, simulating a real
// crash (power loss, OOM kill, panic in a goroutine that takes the process
// down, etc). This is the scenario the persistence engine's durability
// guarantees are supposed to survive.
func (in *instance) sigkill() {
	in.t.Helper()
	if err := in.cmd.Process.Kill(); err != nil {
		in.t.Fatalf("SIGKILL: %v", err)
	}
}

// waitExit blocks until the process has actually exited, bounded by timeout.
func (in *instance) waitExit(timeout time.Duration) {
	in.t.Helper()
	select {
	case <-in.exited:
	case <-time.After(timeout):
		in.t.Fatalf("process did not exit within %s", timeout)
	}
}

// ============================================================
// wire client — talks raw RESP so tests can issue any command, including
// internal ones (CHECKPOINT, REBASELINE) that the SDK doesn't expose.
// ============================================================

type rawClient struct {
	t      *testing.T
	conn   net.Conn
	reader *bufio.Reader
}

func (in *instance) dial() *rawClient {
	in.t.Helper()
	conn, err := net.DialTimeout("tcp", in.addr, 2*time.Second)
	if err != nil {
		in.t.Fatalf("dial %s: %v", in.addr, err)
	}
	c := &rawClient{t: in.t, conn: conn, reader: bufio.NewReader(conn)}
	in.t.Cleanup(func() { conn.Close() })
	return c
}

// do sends args as a RESP array command and returns the decoded response.
func (c *rawClient) do(args ...string) (string, error) {
	if _, err := c.conn.Write(parser.Array(args...)); err != nil {
		return "", err
	}
	return parser.ReadResponse(c.reader)
}

func (c *rawClient) mustDo(args ...string) string {
	c.t.Helper()
	resp, err := c.do(args...)
	if err != nil {
		c.t.Fatalf("%v -> error: %v", args, err)
	}
	return resp
}

func (c *rawClient) close() { c.conn.Close() }

// ============================================================
// metrics scraping — used to observe internal persistence state (checkpoint
// in progress, generation, sequence id) from outside the process, giving
// deterministic synchronization points without touching production code.
// ============================================================

func scrapeMetrics(t *testing.T, metricsAddr string) map[string]float64 {
	t.Helper()
	resp, err := http.Get("http://" + metricsAddr + "/metrics")
	if err != nil {
		t.Fatalf("scrape metrics: %v", err)
	}
	defer resp.Body.Close()

	out := make(map[string]float64)
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[0]
		if idx := strings.IndexByte(name, '{'); idx >= 0 {
			name = name[:idx]
		}
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			continue
		}
		out[name] = v
	}
	return out
}

func metricValue(t *testing.T, metricsAddr, name string) (float64, bool) {
	t.Helper()
	v, ok := scrapeMetrics(t, metricsAddr)[name]
	return v, ok
}

// waitForMetricAtLeast polls metricsAddr until name reaches at least want, or
// fails the test after timeout. Used to synchronize on checkpoint/rotation
// completion without fixed sleeps.
func waitForMetricAtLeast(t *testing.T, metricsAddr, name string, want float64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last float64
	var ok bool
	for time.Now().Before(deadline) {
		last, ok = metricValue(t, metricsAddr, name)
		if ok && last >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("metric %s did not reach %v within %s (last observed: %v, present: %v)", name, want, timeout, last, ok)
}

// checkpointAndWait triggers a CHECKPOINT and blocks until the persistence
// engine reports it complete (checkpoint_successes_total incremented),
// entirely via observable effects (RESP reply + metrics), never a sleep.
func (in *instance) checkpointAndWait(c *rawClient) {
	in.t.Helper()
	before, _ := metricValue(in.t, in.metricsAddr, "kv_persistence_checkpoint_successes_total")
	resp := c.mustDo("CHECKPOINT")
	if resp != "OK" {
		in.t.Fatalf("CHECKPOINT: got %q, want OK", resp)
	}
	waitForMetricAtLeast(in.t, in.metricsAddr, "kv_persistence_checkpoint_successes_total", before+1, 5*time.Second)
}
