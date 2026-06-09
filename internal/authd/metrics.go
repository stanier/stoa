package authd

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// authdMetrics holds Prometheus metric descriptors for authd.
type authdMetrics struct {
	httpRequestsTotal   *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec
}

func newAuthdMetrics(reg prometheus.Registerer) *authdMetrics {
	m := &authdMetrics{
		httpRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "authd",
			Name:      "http_requests_total",
			Help:      "Total HTTP requests handled by authd, by method, path, and status code.",
		}, []string{"method", "path", "status"}),

		httpRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "authd",
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request latency, by method and path.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method", "path"}),
	}
	reg.MustRegister(m.httpRequestsTotal, m.httpRequestDuration)
	return m
}

// authdStatusRecorder captures the HTTP status code written to the response.
type authdStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *authdStatusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// middleware wraps next with request count and latency instrumentation.
// /healthz and /metrics are skipped to avoid low-value noise.
func (m *authdMetrics) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/healthz" || path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &authdStatusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		m.httpRequestsTotal.WithLabelValues(r.Method, path, strconv.Itoa(rec.status)).Inc()
		m.httpRequestDuration.WithLabelValues(r.Method, path).Observe(time.Since(start).Seconds())
	})
}
