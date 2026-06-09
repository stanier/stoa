package agentd

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stanier/stoa/internal/llm/cache"
)

// Metrics holds all Prometheus metric descriptors for agentd.
type Metrics struct {
	// Session lifecycle
	sessionsCreated *prometheus.CounterVec   // labels: source, persona
	activeSessions  *prometheus.GaugeVec     // labels: source
	activeWSConns   prometheus.Gauge

	// LLM calls
	llmRequestsTotal   *prometheus.CounterVec   // labels: provider, status (success|error)
	llmRequestDuration *prometheus.HistogramVec  // labels: provider
	llmInputTokens     *prometheus.CounterVec    // labels: provider
	llmOutputTokens    *prometheus.CounterVec    // labels: provider

	// Tool calls
	toolCallsTotal   *prometheus.CounterVec   // labels: tool, status (success|error|not_found)
	toolCallDuration *prometheus.HistogramVec  // labels: tool

	// Compaction
	compactionsTotal *prometheus.CounterVec // labels: trigger (auto|manual)
}

// NewMetrics creates and registers all agentd metrics with reg.
// Panics if registration fails (programmer error).
func NewMetrics(reg prometheus.Registerer, llmCache *cache.MemoryCache) *Metrics {
	m := &Metrics{
		sessionsCreated: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "agentd",
			Name:      "sessions_created_total",
			Help:      "Total sessions created, labelled by source client and persona.",
		}, []string{"source", "persona"}),

		activeSessions: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "agentd",
			Name:      "active_sessions",
			Help:      "Number of sessions currently in memory, labelled by source client.",
		}, []string{"source"}),

		activeWSConns: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "agentd",
			Name:      "active_websocket_connections",
			Help:      "Number of WebSocket connections currently open.",
		}),

		llmRequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "agentd",
			Name:      "llm_requests_total",
			Help:      "Total LLM stream calls, labelled by provider and outcome.",
		}, []string{"provider", "status"}),

		llmRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "agentd",
			Name:      "llm_request_duration_seconds",
			Help:      "Time from first StreamMessage call to channel drain, by provider.",
			Buckets:   []float64{.5, 1, 2.5, 5, 10, 20, 40, 60, 90, 120},
		}, []string{"provider"}),

		llmInputTokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "agentd",
			Name:      "llm_input_tokens_total",
			Help:      "Cumulative LLM input tokens consumed, by provider.",
		}, []string{"provider"}),

		llmOutputTokens: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "agentd",
			Name:      "llm_output_tokens_total",
			Help:      "Cumulative LLM output tokens generated, by provider.",
		}, []string{"provider"}),

		toolCallsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "agentd",
			Name:      "tool_calls_total",
			Help:      "Total tool invocations, labelled by tool name and outcome.",
		}, []string{"tool", "status"}),

		toolCallDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "agentd",
			Name:      "tool_call_duration_seconds",
			Help:      "Tool execution wall time, by tool name.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"tool"}),

		compactionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "agentd",
			Name:      "compactions_total",
			Help:      "Total compaction events, labelled by trigger (auto or manual).",
		}, []string{"trigger"}),
	}

	reg.MustRegister(
		m.sessionsCreated,
		m.activeSessions,
		m.activeWSConns,
		m.llmRequestsTotal,
		m.llmRequestDuration,
		m.llmInputTokens,
		m.llmOutputTokens,
		m.toolCallsTotal,
		m.toolCallDuration,
		m.compactionsTotal,
	)

	if llmCache != nil {
		reg.MustRegister(newCacheCollector(llmCache))
	}

	return m
}

// cacheCollector adapts cache.MemoryCache stats to a Prometheus Collector so
// hit/miss counts are read at scrape time rather than being pushed on every call.
type cacheCollector struct {
	c       *cache.MemoryCache
	hitsDesc   *prometheus.Desc
	missesDesc *prometheus.Desc
}

func newCacheCollector(c *cache.MemoryCache) *cacheCollector {
	return &cacheCollector{
		c: c,
		hitsDesc: prometheus.NewDesc(
			"agentd_llm_cache_hits_total",
			"LLM response cache hits since startup.",
			nil, nil,
		),
		missesDesc: prometheus.NewDesc(
			"agentd_llm_cache_misses_total",
			"LLM response cache misses since startup.",
			nil, nil,
		),
	}
}

func (cc *cacheCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- cc.hitsDesc
	ch <- cc.missesDesc
}

func (cc *cacheCollector) Collect(ch chan<- prometheus.Metric) {
	st := cc.c.Stats()
	ch <- prometheus.MustNewConstMetric(cc.hitsDesc, prometheus.CounterValue, float64(st.Hits))
	ch <- prometheus.MustNewConstMetric(cc.missesDesc, prometheus.CounterValue, float64(st.Misses))
}
