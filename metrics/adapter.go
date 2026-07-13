package metrics

import (
	"github.com/priyanshu-s-rana/kv_store/persistence"
	"github.com/priyanshu-s-rana/kv_store/server"
	"github.com/priyanshu-s-rana/kv_store/store"
	"github.com/prometheus/client_golang/prometheus"
)

// Compile-time assertions that each adapter satisfies its subsystem's metrics
// interface, so interface drift fails the build instead of surfacing at runtime.
var (
	_ server.ServerMetrics           = (*PrometheusServerMetrics)(nil)
	_ store.StoreMetrics             = (*PrometheusStoreMetrics)(nil)
	_ persistence.PersistenceMetrics = (*PrometheusPersistenceMetrics)(nil)
)

type Manager struct {
	Server      server.ServerMetrics
	Store       store.StoreMetrics
	Persistence persistence.PersistenceMetrics

	Registry *prometheus.Registry
}

func New() *Manager {
	serverMetrics := NewPrometheusServerMetrics()
	storeMetrics := NewPrometheusStoreMetrics()
	persistenceMetrics := NewPrometheusPersistenceMetrics()

	registry := prometheus.NewRegistry()
	registry.MustRegister(serverMetrics.collectors()...)
	registry.MustRegister(storeMetrics.collectors()...)
	registry.MustRegister(persistenceMetrics.collectors()...)

	return &Manager{
		Server:      serverMetrics,
		Store:       storeMetrics,
		Persistence: persistenceMetrics,
		Registry:    registry,
	}
}
