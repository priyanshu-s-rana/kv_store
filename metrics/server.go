package metrics

import (
	"time"

	"github.com/priyanshu-s-rana/kv_store/constants"
	"github.com/prometheus/client_golang/prometheus"
)

type PrometheusServerMetrics struct {
	// Counters
	connectionsAccepted prometheus.Counter
	connectionsClosed   prometheus.Counter
	bytesSent           prometheus.Counter
	bytesReceived       prometheus.Counter
	parserErrors        prometheus.Counter
	commandsReceived    *prometheus.CounterVec
	commandsFailed      *prometheus.CounterVec

	// Gauges
	activeConnections prometheus.Gauge

	// Histograms
	commandDuration       *prometheus.HistogramVec
	responseWriteDuration prometheus.Histogram
}

func NewPrometheusServerMetrics() *PrometheusServerMetrics {
	return &PrometheusServerMetrics{
		connectionsAccepted: newCounter(constants.MetricsSubsystemServer, "connections_accepted_total", "Total number of accepted connections."),
		connectionsClosed:   newCounter(constants.MetricsSubsystemServer, "connections_closed_total", "Total number of closed connections."),
		bytesSent:           newCounter(constants.MetricsSubsystemServer, "bytes_sent_total", "Total number of bytes sent to clients."),
		bytesReceived:       newCounter(constants.MetricsSubsystemServer, "bytes_received_total", "Total number of bytes received from clients."),
		parserErrors:        newCounter(constants.MetricsSubsystemServer, "parser_errors_total", "Total number of parser errors."),
		commandsReceived:    newCounterVec(constants.MetricsSubsystemServer, "commands_received_total", "Total number of commands received by the server.", []string{"command"}),
		commandsFailed:      newCounterVec(constants.MetricsSubsystemServer, "commands_failed_total", "Total number of commands that returned an error response, by command.", []string{"command"}),

		activeConnections: newGauge(constants.MetricsSubsystemServer, "active_connections", "Current number of active connections."),

		commandDuration:       newHistogramVec(constants.MetricsSubsystemServer, "command_duration_seconds", "Duration of a command's full round trip through the server, in seconds.", fastDurationBuckets, []string{"command"}),
		responseWriteDuration: newHistogram(constants.MetricsSubsystemServer, "response_write_duration_seconds", "Duration of writing a response to a client, in seconds.", fastDurationBuckets),
	}
}

func (svm *PrometheusServerMetrics) IncConnectionsAccepted() {
	svm.connectionsAccepted.Inc()
}

func (svm *PrometheusServerMetrics) IncConnectionsClosed() {
	svm.connectionsClosed.Inc()
}

func (svm *PrometheusServerMetrics) IncBytesSent(n int64) {
	svm.bytesSent.Add(float64(n))
}

func (svm *PrometheusServerMetrics) IncBytesReceived(n int64) {
	svm.bytesReceived.Add(float64(n))
}

func (svm *PrometheusServerMetrics) IncParserErrors() {
	svm.parserErrors.Inc()
}

func (svm *PrometheusServerMetrics) IncCommandsReceived(cmd constants.CmdName) {
	svm.commandsReceived.WithLabelValues(string(cmd)).Inc()
}

func (svm *PrometheusServerMetrics) IncFailedCommands(cmd constants.CmdName) {
	svm.commandsFailed.WithLabelValues(string(cmd)).Inc()
}

func (svm *PrometheusServerMetrics) SetActiveConnections(count int64) {
	svm.activeConnections.Set(float64(count))
}

func (svm *PrometheusServerMetrics) ObserveCommandDuration(cmd constants.CmdName, duration time.Duration) {
	svm.commandDuration.WithLabelValues(string(cmd)).Observe(duration.Seconds())
}

func (svm *PrometheusServerMetrics) ObserveResponseWriteDuration(duration time.Duration) {
	svm.responseWriteDuration.Observe(duration.Seconds())
}

// collectors returns every metric owned by PrometheusServerMetrics, for registration
// against a prometheus.Registry.
func (svm *PrometheusServerMetrics) collectors() []prometheus.Collector {
	return []prometheus.Collector{
		svm.connectionsAccepted,
		svm.connectionsClosed,
		svm.bytesSent,
		svm.bytesReceived,
		svm.parserErrors,
		svm.commandsReceived,
		svm.commandsFailed,
		svm.activeConnections,
		svm.commandDuration,
		svm.responseWriteDuration,
	}
}
