package metrics

import (
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/prometheus/client_golang/prometheus"
)

type PrometheusStoreMetrics struct {
	// Commands
	commandsExecuted *prometheus.CounterVec
	commandFailures  *prometheus.CounterVec
	commandDuration  *prometheus.HistogramVec

	// Memory
	currentMemoryBytes prometheus.Gauge
	peakMemoryBytes    prometheus.Gauge
	maxMemoryBytes     prometheus.Gauge
	memoryUtilization  prometheus.Gauge
	keyCount           prometheus.Gauge
	keyBytes           prometheus.Gauge
	valueBytes         prometheus.Gauge
	ttlBytes           prometheus.Gauge
	lruBytes           prometheus.Gauge
	pubsubBytes        prometheus.Gauge

	// TTL
	expiredKeys       prometheus.Counter
	ttlExpiryDuration prometheus.Histogram

	// Pub/Sub
	messagesPublished prometheus.Counter
	activeTopics      prometheus.Gauge
	activeSubscribers prometheus.Gauge
	publishDuration   prometheus.Histogram
}

func NewPrometheusStoreMetrics() *PrometheusStoreMetrics {
	return &PrometheusStoreMetrics{
		commandsExecuted: newCounterVec(constants.MetricsSubsystemStore, "commands_executed_total", "Total number of commands executed by the store.", []string{"command"}),
		commandFailures:  newCounterVec(constants.MetricsSubsystemStore, "command_failures_total", "Total number of commands that returned an error response during execution, by command.", []string{"command"}),
		commandDuration:  newHistogramVec(constants.MetricsSubsystemStore, "command_duration_seconds", "Duration of executing a command inside the store, in seconds.", ultraFastDurationBuckets, []string{"command"}),

		currentMemoryBytes: newGauge(constants.MetricsSubsystemStore, "current_memory_bytes", "Current tracked memory usage, in bytes."),
		peakMemoryBytes:    newGauge(constants.MetricsSubsystemStore, "peak_memory_bytes", "Peak tracked memory usage, in bytes."),
		maxMemoryBytes:     newGauge(constants.MetricsSubsystemStore, "max_memory_bytes", "Configured memory limit, in bytes (0 means unlimited)."),
		memoryUtilization:  newGauge(constants.MetricsSubsystemStore, "memory_utilization_percent", "Current memory usage as a percentage of the configured limit, ranging from 0-100."),
		keyCount:           newGauge(constants.MetricsSubsystemStore, "key_count", "Current number of keys held in the store."),
		keyBytes:           newGauge(constants.MetricsSubsystemStore, "key_bytes", "Memory used by keys, in bytes."),
		valueBytes:         newGauge(constants.MetricsSubsystemStore, "value_bytes", "Memory used by values, in bytes."),
		ttlBytes:           newGauge(constants.MetricsSubsystemStore, "ttl_bytes", "Memory used by TTL tracking, in bytes."),
		lruBytes:           newGauge(constants.MetricsSubsystemStore, "lru_bytes", "Memory used by the LRU index, in bytes."),
		pubsubBytes:        newGauge(constants.MetricsSubsystemStore, "pubsub_bytes", "Memory used by pub/sub state, in bytes."),

		expiredKeys:       newCounter(constants.MetricsSubsystemStore, "expired_keys_total", "Total number of keys removed by TTL expiry."),
		ttlExpiryDuration: newHistogram(constants.MetricsSubsystemStore, "ttl_expiry_duration_seconds", "Duration of a TTL expiry sweep, in seconds.", ultraFastDurationBuckets),

		// Named "delivered" rather than "published": this increments once per
		// subscriber a message is fanned out to, not once per PUBLISH command
		// (that call count is already covered by commands_executed_total{command="PUBLISH"}).
		messagesPublished: newCounter(constants.MetricsSubsystemStore, "messages_delivered_total", "Total number of messages delivered to subscribers (incremented once per recipient, not once per publish)."),
		activeTopics:      newGauge(constants.MetricsSubsystemStore, "active_topics", "Current number of pub/sub topics with at least one subscriber."),
		activeSubscribers: newGauge(constants.MetricsSubsystemStore, "active_subscribers", "Current number of pub/sub subscribers."),
		publishDuration:   newHistogram(constants.MetricsSubsystemStore, "publish_duration_seconds", "Duration of publishing a message to subscribers, in seconds.", ultraFastDurationBuckets),
	}
}

// Commands

func (sm *PrometheusStoreMetrics) IncCommandsExecuted(cmd constants.CmdName) {
	sm.commandsExecuted.WithLabelValues(string(cmd)).Inc()
}

func (sm *PrometheusStoreMetrics) IncCommandFailures(cmd constants.CmdName) {
	sm.commandFailures.WithLabelValues(string(cmd)).Inc()
}

func (sm *PrometheusStoreMetrics) ObserveCommandDuration(cmd constants.CmdName, duration time.Duration) {
	sm.commandDuration.WithLabelValues(string(cmd)).Observe(duration.Seconds())
}

// Memory

func (sm *PrometheusStoreMetrics) SetCurrentMemoryBytes(bytes int64) {
	sm.currentMemoryBytes.Set(float64(bytes))
}

func (sm *PrometheusStoreMetrics) SetPeakMemoryBytes(bytes int64) {
	sm.peakMemoryBytes.Set(float64(bytes))
}

func (sm *PrometheusStoreMetrics) SetMaxMemoryBytes(bytes int64) {
	sm.maxMemoryBytes.Set(float64(bytes))
}

func (sm *PrometheusStoreMetrics) SetMemoryUtilization(percent float32) {
	sm.memoryUtilization.Set(float64(percent))
}

func (sm *PrometheusStoreMetrics) SetKeyCount(count int64) {
	sm.keyCount.Set(float64(count))
}

func (sm *PrometheusStoreMetrics) SetKeyBytes(bytes int64) {
	sm.keyBytes.Set(float64(bytes))
}

func (sm *PrometheusStoreMetrics) SetValueBytes(bytes int64) {
	sm.valueBytes.Set(float64(bytes))
}

func (sm *PrometheusStoreMetrics) SetTTLBytes(bytes int64) {
	sm.ttlBytes.Set(float64(bytes))
}

func (sm *PrometheusStoreMetrics) SetLRUBytes(bytes int64) {
	sm.lruBytes.Set(float64(bytes))
}

func (sm *PrometheusStoreMetrics) SetPubSubBytes(bytes int64) {
	sm.pubsubBytes.Set(float64(bytes))
}

// TTL

func (sm *PrometheusStoreMetrics) IncExpiredKeys() {
	sm.expiredKeys.Inc()
}

func (sm *PrometheusStoreMetrics) ObserveTTLExpiryDuration(duration time.Duration) {
	sm.ttlExpiryDuration.Observe(duration.Seconds())
}

// Pub/Sub

func (sm *PrometheusStoreMetrics) SetActiveTopics(count int64) {
	sm.activeTopics.Set(float64(count))
}

func (sm *PrometheusStoreMetrics) SetActiveSubscribers(count int64) {
	sm.activeSubscribers.Set(float64(count))
}

func (sm *PrometheusStoreMetrics) IncMessagesPublished() {
	sm.messagesPublished.Inc()
}

func (sm *PrometheusStoreMetrics) ObservePublishDuration(duration time.Duration) {
	sm.publishDuration.Observe(duration.Seconds())
}

// collectors returns every metric owned by PrometheusStoreMetrics, for registration
// against a prometheus.Registry.
func (sm *PrometheusStoreMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		sm.commandsExecuted,
		sm.commandFailures,
		sm.commandDuration,
		sm.currentMemoryBytes,
		sm.peakMemoryBytes,
		sm.maxMemoryBytes,
		sm.memoryUtilization,
		sm.keyCount,
		sm.keyBytes,
		sm.valueBytes,
		sm.ttlBytes,
		sm.lruBytes,
		sm.pubsubBytes,
		sm.expiredKeys,
		sm.ttlExpiryDuration,
		sm.messagesPublished,
		sm.activeTopics,
		sm.activeSubscribers,
		sm.publishDuration,
	}
}
