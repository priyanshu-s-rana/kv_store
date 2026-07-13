package metrics

import (
	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/prometheus/client_golang/prometheus"
)

// Reusable latency bucket profiles. Each subsystem's histograms pick the
// profile matching their real-world latency scale rather than sharing one
// generic set of buckets that under-resolves fast operations and
// over-resolves slow ones.

// ultraFastDurationBuckets covers ~1µs-1ms: in-memory Store operations
// (command execution, TTL expiry sweeps, publish fan-out) that never touch
// disk or the network and are expected to complete in low microseconds.
var ultraFastDurationBuckets = []float64{
	0.000001, // 1µs
	0.000002, // 2µs
	0.000005, // 5µs
	0.00001,  // 10µs
	0.00002,  // 20µs
	0.00005,  // 50µs
	0.0001,   // 100µs
	0.00025,  // 250µs
	0.0005,   // 500µs
	0.001,    // 1ms
}

// fastDurationBuckets covers ~50µs-50ms: Server operations that add network
// I/O and Store round-trip overhead on top of an in-memory command.
var fastDurationBuckets = []float64{
	0.00005, // 50µs
	0.0001,  // 100µs
	0.00025, // 250µs
	0.0005,  // 500µs
	0.001,   // 1ms
	0.002,   // 2ms
	0.005,   // 5ms
	0.01,    // 10ms
	0.02,    // 20ms
	0.05,    // 50ms
}

// ioDurationBuckets covers ~100µs-1s: Persistence operations bottlenecked on
// disk I/O (snapshot save/load, journal replay, recovery, rebaseline, AOF
// fsync), which run one to several orders of magnitude slower than in-memory
// Store operations.
var ioDurationBuckets = []float64{
	0.0001, // 100µs
	0.0005, // 500µs
	0.001,  // 1ms
	0.002,  // 2ms
	0.005,  // 5ms
	0.01,   // 10ms
	0.02,   // 20ms
	0.05,   // 50ms
	0.1,    // 100ms
	0.5,    // 500ms
	1,      // 1s
}

func newCounter(subsystem, name, help string) prometheus.Counter {
	return prometheus.NewCounter(prometheus.CounterOpts{
		Namespace: constants.MetricsNamespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
	})
}

func newCounterVec(subsystem, name, help string, labels []string) *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: constants.MetricsNamespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
	}, labels)
}

func newGauge(subsystem, name, help string) prometheus.Gauge {
	return prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: constants.MetricsNamespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
	})
}

func newHistogram(subsystem, name, help string, buckets []float64) prometheus.Histogram {
	return prometheus.NewHistogram(prometheus.HistogramOpts{
		Namespace: constants.MetricsNamespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
		Buckets:   buckets,
	})
}

func newHistogramVec(subsystem, name, help string, buckets []float64, labels []string) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: constants.MetricsNamespace,
		Subsystem: subsystem,
		Name:      name,
		Help:      help,
		Buckets:   buckets,
	}, labels)
}

// boolToFloat64 converts a boolean state into the 0/1 convention Prometheus
// gauges use to represent flags.
func boolToFloat64(val bool) float64 {
	if val {
		return 1
	}
	return 0
}
