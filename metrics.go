package sentry_transport

import (
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	namespace = "rr_sentry_transport"
)

// metricsCollector implements prometheus.Collector interface
type metricsCollector struct {
	// Atomic counters for thread-safe metric updates
	deadLetterEvents  *uint64 // Total events moved to dead letter queue
	droppedEvents     *uint64 // Total dropped events (queue full, etc.)
	failedEvents      *uint64 // Total failed events (HTTP errors, etc.)
	successfulEvents  *uint64 // Total successfully sent events

	// Prometheus metric descriptors
	deadLetterEventsDesc  *prometheus.Desc
	droppedEventsDesc     *prometheus.Desc
	failedEventsDesc      *prometheus.Desc
	successfulEventsDesc  *prometheus.Desc

	// Vector metric for events by type
	eventsByType *prometheus.CounterVec
}

// newMetricsCollector creates a new metrics collector
func newMetricsCollector() *metricsCollector {
	return &metricsCollector{
		// Initialize atomic counters
		deadLetterEvents:  ptrTo(uint64(0)),
		droppedEvents:     ptrTo(uint64(0)),
		failedEvents:      ptrTo(uint64(0)),
		successfulEvents:  ptrTo(uint64(0)),

		// Create metric descriptors
		deadLetterEventsDesc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "dead_letter_events_total"),
			"Total number of events moved to dead letter queue",
			nil, nil),

		droppedEventsDesc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "dropped_events_total"),  
			"Total number of dropped Sentry events",
			nil, nil),

		failedEventsDesc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "failed_events_total"),
			"Total number of failed Sentry events",
			nil, nil),

		successfulEventsDesc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "", "successful_events_total"),
			"Total number of successfully sent events",
			nil, nil),

		// Vector metric with event type label
		eventsByType: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: prometheus.BuildFQName(namespace, "", "events_by_type_total"),
				Help: "Total number of events by event type",
			},
			[]string{"event_type"}),
	}
}

// Public methods for updating metrics (called from business logic)

// IncDeadLetterEvents increments dead letter events counter
func (mc *metricsCollector) IncDeadLetterEvents() {
	atomic.AddUint64(mc.deadLetterEvents, 1)
}

// IncDroppedEvents increments dropped events counter
func (mc *metricsCollector) IncDroppedEvents() {
	atomic.AddUint64(mc.droppedEvents, 1)
}

// IncFailedEvents increments failed events counter
func (mc *metricsCollector) IncFailedEvents() {
	atomic.AddUint64(mc.failedEvents, 1)
}

// IncSuccessfulEvents increments successful events counter
func (mc *metricsCollector) IncSuccessfulEvents() {
	atomic.AddUint64(mc.successfulEvents, 1)
}

// IncEventsByType increments events counter for specific event type
func (mc *metricsCollector) IncEventsByType(eventType string) {
	mc.eventsByType.WithLabelValues(eventType).Inc()
}

// Implement prometheus.Collector interface

// Describe sends all metric descriptions to Prometheus
func (mc *metricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- mc.deadLetterEventsDesc
	ch <- mc.droppedEventsDesc
	ch <- mc.failedEventsDesc
	ch <- mc.successfulEventsDesc

	// Vector metric handles its own description
	mc.eventsByType.Describe(ch)
}

// Collect sends current metric values to Prometheus
func (mc *metricsCollector) Collect(ch chan<- prometheus.Metric) {
	// Send current values of atomic counters
	ch <- prometheus.MustNewConstMetric(
		mc.deadLetterEventsDesc,
		prometheus.CounterValue,
		float64(atomic.LoadUint64(mc.deadLetterEvents)))

	ch <- prometheus.MustNewConstMetric(
		mc.droppedEventsDesc,
		prometheus.CounterValue,
		float64(atomic.LoadUint64(mc.droppedEvents)))

	ch <- prometheus.MustNewConstMetric(
		mc.failedEventsDesc,
		prometheus.CounterValue,
		float64(atomic.LoadUint64(mc.failedEvents)))

	ch <- prometheus.MustNewConstMetric(
		mc.successfulEventsDesc,
		prometheus.CounterValue,
		float64(atomic.LoadUint64(mc.successfulEvents)))

	// Vector metric collects itself
	mc.eventsByType.Collect(ch)
}

// Helper function for pointer creation
func ptrTo[T any](v T) *T {
	return &v
}
