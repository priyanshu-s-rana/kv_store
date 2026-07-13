package metrics

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/priyanshu-s-rana/kv_store/constants"
)

func TestNewRegistersAllSubsystems(t *testing.T) {
	m := New()

	if m.Server == nil {
		t.Fatal("Server metrics not initialized")
	}
	if m.Store == nil {
		t.Fatal("Store metrics not initialized")
	}
	if m.Persistence == nil {
		t.Fatal("Persistence metrics not initialized")
	}
	if m.Registry == nil {
		t.Fatal("Registry not initialized")
	}

	if _, err := m.Registry.Gather(); err != nil {
		t.Fatalf("Gather: %v", err)
	}
}

// TestNewDoesNotPanicOnRepeatedConstruction guards against collectors being
// registered against a shared/global registry: each Manager owns its own
// private *prometheus.Registry, so building several must never panic with a
// duplicate-registration error, unlike prometheus.MustRegister on the default
// global registry would if called twice for the same metric.
func TestNewDoesNotPanicOnRepeatedConstruction(t *testing.T) {
	for range 3 {
		New()
	}
}

func TestManagerHandlerServesExpositionFormat(t *testing.T) {
	m := New()
	m.Server.IncConnectionsAccepted()
	m.Store.IncCommandsExecuted(constants.Get)
	m.Persistence.SetPersistenceEnabled(true)

	srv := httptest.NewServer(m.Handler())
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	text := string(body)

	for _, want := range []string{
		"kv_server_connections_accepted_total 1",
		`kv_store_commands_executed_total{command="GET"} 1`,
		"kv_persistence_enabled 1",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("response missing %q\nfull body:\n%s", want, text)
		}
	}
}
