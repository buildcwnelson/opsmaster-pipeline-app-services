package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HttpRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "opsmaster",
			Subsystem: "app_engine",
			Name:      "http_requests_total",
			Help:      "Total HTTP requests handled by endpoint and status.",
		},
		[]string{"method", "path", "status"},
	)

	HttpRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "opsmaster",
			Subsystem: "app_engine",
			Name:      "http_request_duration_seconds",
			Help:      "Latency histogram for HTTP requests.",
			Buckets:   []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5},
		},
		[]string{"method", "path"},
	)

	VitalsIngestedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "opsmaster",
			Subsystem: "app_engine",
			Name:      "vitals_ingested_total",
			Help:      "Total sensor vitals records ingested.",
		},
		[]string{"metric_type", "protocol", "status"},
	)

	AnomaliesDetectedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "opsmaster",
			Subsystem: "app_engine",
			Name:      "anomalies_detected_total",
			Help:      "Total medical anomalies flagged by in-memory rules worker.",
		},
		[]string{"anomaly_type", "severity"},
	)

	ActivePatientsGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "opsmaster",
			Subsystem: "app_engine",
			Name:      "active_patients_count",
			Help:      "Current patients tracked in sliding window anomaly evaluator.",
		},
	)

	ActiveSessionsGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "opsmaster",
			Subsystem: "app_engine",
			Name:      "active_telehealth_sessions_count",
			Help:      "Current active doctor-patient WebRTC telehealth sessions.",
		},
	)

	ActivePeersGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "opsmaster",
			Subsystem: "app_engine",
			Name:      "active_webrtc_peers_count",
			Help:      "Current WebRTC WebSocket peers connected.",
		},
	)

	SignalingMessagesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "opsmaster",
			Subsystem: "app_engine",
			Name:      "signaling_messages_total",
			Help:      "Total WebRTC signaling messages processed.",
		},
		[]string{"type", "status"},
	)

	QueueLatency = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "opsmaster",
			Subsystem: "app_engine",
			Name:      "queue_latency_seconds",
			Help:      "Redis stream dispatch latency.",
			Buckets:   []float64{0.0005, 0.001, 0.005, 0.01, 0.05},
		},
	)
)

type metricsResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *metricsResponseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &metricsResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		duration := time.Since(start).Seconds()
		path := r.URL.Path
		status := strconv.Itoa(wrapped.statusCode)

		HttpRequestsTotal.WithLabelValues(r.Method, path, status).Inc()
		HttpRequestDuration.WithLabelValues(r.Method, path).Observe(duration)
	})
}
