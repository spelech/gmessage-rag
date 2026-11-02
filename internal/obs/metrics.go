package obs

import (
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metric name constants according to OBS-01 specification.
const (
	MetricMessagesIngestedTotal      = "gmessages_messages_ingested_total"
	MetricMediaBytesDecryptedTotal   = "gmessages_media_bytes_decrypted_total"
	MetricActiveWebsocketConnections = "gmessages_active_websocket_connections"
	MetricSessionChunksCreatedTotal  = "gmessages_session_chunks_created_total"
)

// Label value constants.
const (
	DirectionInbound  = "inbound"
	DirectionOutbound = "outbound"

	ProtocolSMS = "sms"
	ProtocolMMS = "mms"
	ProtocolRCS = "rcs"
)

// Metrics holds the Prometheus metric collectors for gmessages-rag.
type Metrics struct {
	reg *prometheus.Registry

	messagesIngestedTotal      *prometheus.CounterVec
	mediaBytesDecryptedTotal   prometheus.Counter
	activeWebsocketConnections prometheus.Gauge
	sessionChunksCreatedTotal  prometheus.Counter
}

var (
	defaultMetricsInstance *Metrics
	defaultMetricsOnce     sync.Once
)

// NewMetrics initializes and registers standardized metrics with a new Prometheus registry.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()

	messagesIngestedTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: MetricMessagesIngestedTotal,
			Help: "Total count of messages ingested by direction and protocol.",
		},
		[]string{"direction", "protocol"},
	)

	mediaBytesDecryptedTotal := prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: MetricMediaBytesDecryptedTotal,
			Help: "Total number of media bytes decrypted.",
		},
	)

	activeWebsocketConnections := prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: MetricActiveWebsocketConnections,
			Help: "Current count of active Google Messages WebSocket connections.",
		},
	)

	sessionChunksCreatedTotal := prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: MetricSessionChunksCreatedTotal,
			Help: "Total number of 2-hour session transcript chunks created.",
		},
	)

	reg.MustRegister(
		messagesIngestedTotal,
		mediaBytesDecryptedTotal,
		activeWebsocketConnections,
		sessionChunksCreatedTotal,
	)

	return &Metrics{
		reg:                        reg,
		messagesIngestedTotal:      messagesIngestedTotal,
		mediaBytesDecryptedTotal:   mediaBytesDecryptedTotal,
		activeWebsocketConnections: activeWebsocketConnections,
		sessionChunksCreatedTotal:  sessionChunksCreatedTotal,
	}
}

// DefaultMetrics returns the singleton default Metrics instance.
func DefaultMetrics() *Metrics {
	defaultMetricsOnce.Do(func() {
		defaultMetricsInstance = NewMetrics()
	})
	return defaultMetricsInstance
}

// Registry returns the Prometheus collector registry.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.reg
}

// MessagesIngestedTotal returns the counter vector for ingested messages.
func (m *Metrics) MessagesIngestedTotal() *prometheus.CounterVec {
	return m.messagesIngestedTotal
}

// MediaBytesDecryptedTotal returns the counter for media bytes decrypted.
func (m *Metrics) MediaBytesDecryptedTotal() prometheus.Counter {
	return m.mediaBytesDecryptedTotal
}

// ActiveWebsocketConnections returns the gauge for active websocket connections.
func (m *Metrics) ActiveWebsocketConnections() prometheus.Gauge {
	return m.activeWebsocketConnections
}

// SessionChunksCreatedTotal returns the counter for session chunks created.
func (m *Metrics) SessionChunksCreatedTotal() prometheus.Counter {
	return m.sessionChunksCreatedTotal
}

// Handler returns an http.Handler that serves Prometheus metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

// RecordMessageIngested increments gmessages_messages_ingested_total for the given direction and protocol.
func (m *Metrics) RecordMessageIngested(direction, protocol string) {
	if m == nil || m.messagesIngestedTotal == nil {
		return
	}
	m.messagesIngestedTotal.WithLabelValues(direction, protocol).Inc()
}

// RecordMediaBytesDecrypted adds the given number of decrypted bytes to gmessages_media_bytes_decrypted_total.
func (m *Metrics) RecordMediaBytesDecrypted(bytes float64) {
	if m == nil || m.mediaBytesDecryptedTotal == nil || bytes <= 0 {
		return
	}
	m.mediaBytesDecryptedTotal.Add(bytes)
}

// SetActiveWebsocketConnections sets the current number of active WebSocket connections.
func (m *Metrics) SetActiveWebsocketConnections(count float64) {
	if m == nil || m.activeWebsocketConnections == nil {
		return
	}
	m.activeWebsocketConnections.Set(count)
}

// IncActiveWebsocketConnections increments the active WebSocket connection count.
func (m *Metrics) IncActiveWebsocketConnections() {
	if m == nil || m.activeWebsocketConnections == nil {
		return
	}
	m.activeWebsocketConnections.Inc()
}

// DecActiveWebsocketConnections decrements the active WebSocket connection count.
func (m *Metrics) DecActiveWebsocketConnections() {
	if m == nil || m.activeWebsocketConnections == nil {
		return
	}
	m.activeWebsocketConnections.Dec()
}

// RecordSessionChunkCreated increments gmessages_session_chunks_created_total.
func (m *Metrics) RecordSessionChunkCreated() {
	if m == nil || m.sessionChunksCreatedTotal == nil {
		return
	}
	m.sessionChunksCreatedTotal.Inc()
}
