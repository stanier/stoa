package inferencegateway

import (
	"github.com/prometheus/client_golang/prometheus"
)

// gwMetrics holds Prometheus metric descriptors for inference-gateway.
type gwMetrics struct {
	completionsTotal   *prometheus.CounterVec
	inputTokens        *prometheus.CounterVec
	outputTokens       *prometheus.CounterVec
	upstreamTotal      *prometheus.CounterVec
	upstreamDuration   *prometheus.HistogramVec
}

func newGWMetrics(reg prometheus.Registerer) *gwMetrics {
	m := &gwMetrics{
		completionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "infgw",
			Name:      "completions_total",
			Help:      "Total LLM completion requests, by model and outcome.",
		}, []string{"model", "status"}),

		inputTokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "infgw",
			Name:      "input_tokens_total",
			Help:      "Cumulative input tokens consumed, by model.",
		}, []string{"model"}),

		outputTokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "infgw",
			Name:      "output_tokens_total",
			Help:      "Cumulative output tokens generated, by model.",
		}, []string{"model"}),

		upstreamTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "infgw",
			Name:      "upstream_requests_total",
			Help:      "Total requests forwarded to each upstream provider.",
		}, []string{"upstream", "status"}),

		upstreamDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "infgw",
			Name:      "upstream_duration_seconds",
			Help:      "Time from first upstream request to stream close, by upstream.",
			Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
		}, []string{"upstream"}),
	}
	reg.MustRegister(
		m.completionsTotal,
		m.inputTokens,
		m.outputTokens,
		m.upstreamTotal,
		m.upstreamDuration,
	)
	return m
}
