package metrics

import (
	"testing"
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPrometheusStoreMetricsCommandLabels(t *testing.T) {
	m := NewPrometheusStoreMetrics()

	m.IncCommandsExecuted(constants.Set)
	m.IncCommandsExecuted(constants.Set)
	m.IncCommandsExecuted(constants.Get)
	if got := testutil.ToFloat64(m.commandsExecuted.WithLabelValues(string(constants.Set))); got != 2 {
		t.Errorf("commandsExecuted[SET] = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.commandsExecuted.WithLabelValues(string(constants.Get))); got != 1 {
		t.Errorf("commandsExecuted[GET] = %v, want 1", got)
	}

	m.IncCommandFailures(constants.Set)
	if got := testutil.ToFloat64(m.commandFailures.WithLabelValues(string(constants.Set))); got != 1 {
		t.Errorf("commandFailures[SET] = %v, want 1", got)
	}

	m.ObserveCommandDuration(constants.Set, 2*time.Millisecond)
	got := m.commandDuration.WithLabelValues(string(constants.Set)).(prometheus.Metric)
	if count := histogramSampleCount(t, got); count != 1 {
		t.Errorf("commandDuration[SET] sample count = %d, want 1", count)
	}
}

func TestPrometheusStoreMetricsMemoryGauges(t *testing.T) {
	m := NewPrometheusStoreMetrics()

	m.SetCurrentMemoryBytes(100)
	m.SetPeakMemoryBytes(200)
	m.SetMaxMemoryBytes(1000)
	m.SetMemoryUtilization(10.5)
	m.SetKeyCount(3)
	m.SetKeyBytes(30)
	m.SetValueBytes(70)
	m.SetTTLBytes(5)
	m.SetLRUBytes(15)
	m.SetPubSubBytes(0)

	cases := []struct {
		name string
		got  prometheus.Gauge
		want float64
	}{
		{"currentMemoryBytes", m.currentMemoryBytes, 100},
		{"peakMemoryBytes", m.peakMemoryBytes, 200},
		{"maxMemoryBytes", m.maxMemoryBytes, 1000},
		{"memoryUtilization", m.memoryUtilization, 10.5},
		{"keyCount", m.keyCount, 3},
		{"keyBytes", m.keyBytes, 30},
		{"valueBytes", m.valueBytes, 70},
		{"ttlBytes", m.ttlBytes, 5},
		{"lruBytes", m.lruBytes, 15},
		{"pubsubBytes", m.pubsubBytes, 0},
	}
	for _, c := range cases {
		if got := testutil.ToFloat64(c.got); got != c.want {
			t.Errorf("%s = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPrometheusStoreMetricsTTL(t *testing.T) {
	m := NewPrometheusStoreMetrics()

	m.IncExpiredKeys()
	m.IncExpiredKeys()
	if got := testutil.ToFloat64(m.expiredKeys); got != 2 {
		t.Errorf("expiredKeys = %v, want 2", got)
	}

	m.ObserveTTLExpiryDuration(time.Millisecond)
	if count := histogramSampleCount(t, m.ttlExpiryDuration); count != 1 {
		t.Errorf("ttlExpiryDuration sample count = %d, want 1", count)
	}
}

func TestPrometheusStoreMetricsPubSub(t *testing.T) {
	m := NewPrometheusStoreMetrics()

	m.SetActiveTopics(4)
	m.SetActiveSubscribers(9)
	if got := testutil.ToFloat64(m.activeTopics); got != 4 {
		t.Errorf("activeTopics = %v, want 4", got)
	}
	if got := testutil.ToFloat64(m.activeSubscribers); got != 9 {
		t.Errorf("activeSubscribers = %v, want 9", got)
	}

	m.IncMessagesPublished()
	m.IncMessagesPublished()
	m.IncMessagesPublished()
	if got := testutil.ToFloat64(m.messagesPublished); got != 3 {
		t.Errorf("messagesPublished = %v, want 3", got)
	}

	m.ObservePublishDuration(time.Millisecond)
	if count := histogramSampleCount(t, m.publishDuration); count != 1 {
		t.Errorf("publishDuration sample count = %d, want 1", count)
	}
}

func TestPrometheusStoreMetricsCollectorsRegisterCleanly(t *testing.T) {
	m := NewPrometheusStoreMetrics()
	registry := prometheus.NewRegistry()

	registry.MustRegister(m.collectors()...)

	// Touch every Vec collector so it contributes a metric family below.
	m.IncCommandsExecuted(constants.Set)
	m.IncCommandFailures(constants.Set)
	m.ObserveCommandDuration(constants.Set, time.Millisecond)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(families) != len(m.collectors()) {
		t.Errorf("gathered %d metric families, want %d (one per collector)", len(families), len(m.collectors()))
	}
}
