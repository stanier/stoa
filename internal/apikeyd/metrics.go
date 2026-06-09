package apikeyd

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// apikeydMetrics holds Prometheus metric descriptors for apikeyd.
type apikeydMetrics struct {
	httpRequestsTotal   *prometheus.CounterVec
	httpRequestDuration *prometheus.HistogramVec
}

func newAPIKeydMetrics(reg prometheus.Registerer) *apikeydMetrics {
	m := &apikeydMetrics{
		httpRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "apikeyd",
			Name:      "http_requests_total",
			Help:      "Total HTTP requests handled by apikeyd, by method, path, and status code.",
		}, []string{"method", "path", "status"}),

		httpRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "apikeyd",
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request latency, by method and path.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method", "path"}),
	}
	reg.MustRegister(m.httpRequestsTotal, m.httpRequestDuration)
	return m
}

// apikeydStatusRecorder captures the HTTP status code written to the response.
type apikeydStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *apikeydStatusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// normalizeAPIKeydPath replaces dynamic path segments with placeholders to
// avoid high label cardinality (e.g. /api/keys/abc123 → /api/keys/{id}).
func normalizeAPIKeydPath(path string) string {
	if strings.HasPrefix(path, "/api/keys/") && len(path) > len("/api/keys/") {
		return "/api/keys/{id}"
	}
	if strings.HasPrefix(path, "/admin/users/") && len(path) > len("/admin/users/") {
		return "/admin/users/{user_id}"
	}
	return path
}

// middleware wraps next with request count and latency instrumentation.
// /healthz and /metrics are skipped to avoid low-value noise.
func (m *apikeydMetrics) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/healthz" || path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &apikeydStatusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		normalized := normalizeAPIKeydPath(path)
		m.httpRequestsTotal.WithLabelValues(r.Method, normalized, strconv.Itoa(rec.status)).Inc()
		m.httpRequestDuration.WithLabelValues(r.Method, normalized).Observe(time.Since(start).Seconds())
	})
}
