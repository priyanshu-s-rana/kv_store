package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// histogramSampleCount returns the number of observations recorded by m,
// for tests that need to verify an Observe call landed without asserting
// on the exact bucket distribution. m must be a Histogram, or an Observer
// returned by a HistogramVec.WithLabelValues call (which is one under the
// hood).
func histogramSampleCount(t *testing.T, m prometheus.Metric) uint64 {
	t.Helper()
	var pb dto.Metric
	if err := m.Write(&pb); err != nil {
		t.Fatalf("Write metric: %v", err)
	}
	return pb.GetHistogram().GetSampleCount()
}
