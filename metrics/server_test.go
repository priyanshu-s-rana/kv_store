package metrics

import (
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPrometheusServerMetricsCounters(t *testing.T) {
	m := NewPrometheusServerMetrics()

	m.IncConnectionsAccepted()
	m.IncConnectionsAccepted()
	if got := testutil.ToFloat64(m.connectionsAccepted); got != 2 {
		t.Errorf("connectionsAccepted = %v, want 2", got)
	}

	m.IncConnectionsClosed()
	if got := testutil.ToFloat64(m.connectionsClosed); got != 1 {
		t.Errorf("connectionsClosed = %v, want 1", got)
	}

	m.IncBytesSent(42)
	m.IncBytesSent(8)
	if got := testutil.ToFloat64(m.bytesSent); got != 50 {
		t.Errorf("bytesSent = %v, want 50", got)
	}

	m.IncBytesReceived(10)
	if got := testutil.ToFloat64(m.bytesReceived); got != 10 {
		t.Errorf("bytesReceived = %v, want 10", got)
	}

	m.IncParserErrors()
	if got := testutil.ToFloat64(m.parserErrors); got != 1 {
		t.Errorf("parserErrors = %v, want 1", got)
	}
}

func TestPrometheusServerMetricsCommandLabels(t *testing.T) {
	m := NewPrometheusServerMetrics()

	m.IncCommandsReceived(constants.Get)
	m.IncCommandsReceived(constants.Get)
	m.IncCommandsReceived(constants.Set)
	if got := testutil.ToFloat64(m.commandsReceived.WithLabelValues(string(constants.Get))); got != 2 {
		t.Errorf("commandsReceived[GET] = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.commandsReceived.WithLabelValues(string(constants.Set))); got != 1 {
		t.Errorf("commandsReceived[SET] = %v, want 1", got)
	}

	m.IncFailedCommands(constants.Set)
	if got := testutil.ToFloat64(m.commandsFailed.WithLabelValues(string(constants.Set))); got != 1 {
		t.Errorf("commandsFailed[SET] = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.commandsFailed.WithLabelValues(string(constants.Get))); got != 0 {
		t.Errorf("commandsFailed[GET] = %v, want 0 (never failed)", got)
	}
}

func TestPrometheusServerMetricsGauge(t *testing.T) {
	m := NewPrometheusServerMetrics()

	m.SetActiveConnections(5)
	if got := testutil.ToFloat64(m.activeConnections); got != 5 {
		t.Errorf("activeConnections = %v, want 5", got)
	}

	m.SetActiveConnections(2)
	if got := testutil.ToFloat64(m.activeConnections); got != 2 {
		t.Errorf("activeConnections = %v, want 2 (Set overwrites, not accumulates)", got)
	}
}

func TestPrometheusServerMetricsHistograms(t *testing.T) {
	m := NewPrometheusServerMetrics()

	m.ObserveCommandDuration(constants.Get, 5*time.Millisecond)
	m.ObserveCommandDuration(constants.Get, 10*time.Millisecond)
	got := m.commandDuration.WithLabelValues(string(constants.Get)).(prometheus.Metric)
	if count := histogramSampleCount(t, got); count != 2 {
		t.Errorf("commandDuration[GET] sample count = %d, want 2", count)
	}

	m.ObserveResponseWriteDuration(time.Microsecond)
	if count := histogramSampleCount(t, m.responseWriteDuration); count != 1 {
		t.Errorf("responseWriteDuration sample count = %d, want 1", count)
	}
}

// TestPrometheusServerMetricsCollectorsRegisterCleanly guards against a
// collectors() entry being nil or duplicated: either would make registration
// panic (nil collector) or fail (duplicate/colliding descriptor).
func TestPrometheusServerMetricsCollectorsRegisterCleanly(t *testing.T) {
	m := NewPrometheusServerMetrics()
	registry := prometheus.NewRegistry()

	registry.MustRegister(m.collectors()...)

	// CounterVec/HistogramVec collectors only produce a metric family once a
	// label combination has been observed; touch each so every collector
	// contributes exactly one family below.
	m.IncCommandsReceived(constants.Get)
	m.IncFailedCommands(constants.Get)
	m.ObserveCommandDuration(constants.Get, time.Millisecond)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(families) != len(m.collectors()) {
		t.Errorf("gathered %d metric families, want %d (one per collector)", len(families), len(m.collectors()))
	}
}
