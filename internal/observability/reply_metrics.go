package observability

import (
	"context"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

const replyMetricPrefix = "omni_agent_customer_service_ai_reply_"

// PrometheusReplyObserver records model-generation attempts, not HTTP requests.
// Validation failures and replies served from storage never enter this lifecycle.
// It owns its collectors but does not register with the global default registry.
type PrometheusReplyObserver struct {
	started       *prometheus.CounterVec
	rejected      *prometheus.CounterVec
	finished      *prometheus.CounterVec
	modelDuration *prometheus.HistogramVec
	totalDuration *prometheus.HistogramVec
	collectors    []prometheus.Collector
}

func NewPrometheusReplyObserver() *PrometheusReplyObserver {
	counter := func(name, help string, labels ...string) *prometheus.CounterVec {
		return prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: replyMetricPrefix + name,
			Help: help,
		}, labels)
	}
	histogram := func(name, help string) *prometheus.HistogramVec {
		return prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: replyMetricPrefix + name,
			Help: help,
			// Cover interactive replies and the server's 75-second model timeout.
			Buckets: []float64{0.1, 0.5, 1, 2.5, 5, 10, 20, 30, 60, 75, 90, 120},
		}, []string{"mode"})
	}
	m := &PrometheusReplyObserver{
		started:       counter("started_total", "Model-generation attempts admitted by the capacity limiter.", "mode"),
		rejected:      counter("rejected_total", "Model-generation attempts rejected because capacity was exhausted.", "mode"),
		finished:      counter("finished_total", "Terminal model-generation attempts by application outcome.", "mode", "outcome"),
		modelDuration: histogram("model_duration_seconds", "Model-generation duration, including stream consumption and cleanup, recorded at attempt completion."),
		totalDuration: histogram("total_duration_seconds", "Admitted reply attempt duration through its terminal outcome, including persistence but excluding initial preparation."),
	}
	m.collectors = []prometheus.Collector{m.started, m.rejected, m.finished, m.modelDuration, m.totalDuration}
	// Publish the known series at zero even before the first reply. Unknown enum
	// values use one bounded fallback series rather than becoming arbitrary labels.
	for _, mode := range []customerservice.ReplyMode{customerservice.ReplyModeGenerate, customerservice.ReplyModeStream} {
		label := string(mode)
		m.started.WithLabelValues(label)
		m.rejected.WithLabelValues(label)
		m.modelDuration.WithLabelValues(label)
		m.totalDuration.WithLabelValues(label)
		for _, outcome := range replyMetricOutcomes {
			m.finished.WithLabelValues(label, string(outcome))
		}
	}
	return m
}

func (m *PrometheusReplyObserver) ReplyStarted(_ context.Context, event customerservice.ReplyStartedEvent) {
	m.started.WithLabelValues(replyModeLabel(event.Operation.Mode)).Inc()
}

func (m *PrometheusReplyObserver) ReplyRejected(_ context.Context, event customerservice.ReplyRejectedEvent) {
	m.rejected.WithLabelValues(replyModeLabel(event.Operation.Mode)).Inc()
}

func (m *PrometheusReplyObserver) ReplyFinished(_ context.Context, event customerservice.ReplyFinishedEvent) {
	mode := replyModeLabel(event.Operation.Mode)
	m.finished.WithLabelValues(mode, replyOutcomeLabel(event.Outcome)).Inc()
	m.modelDuration.WithLabelValues(mode).Observe(event.ModelDuration.Seconds())
	m.totalDuration.WithLabelValues(mode).Observe(event.TotalDuration.Seconds())
}

func (m *PrometheusReplyObserver) Describe(ch chan<- *prometheus.Desc) {
	for _, collector := range m.collectors {
		collector.Describe(ch)
	}
}

func (m *PrometheusReplyObserver) Collect(ch chan<- prometheus.Metric) {
	for _, collector := range m.collectors {
		collector.Collect(ch)
	}
}

var replyMetricOutcomes = [...]customerservice.ReplyOutcome{
	customerservice.ReplyOutcomeCompleted,
	customerservice.ReplyOutcomeSuperseded,
	customerservice.ReplyOutcomeEmpty,
	customerservice.ReplyOutcomeGenerationFailed,
	customerservice.ReplyOutcomePersistenceFailed,
	customerservice.ReplyOutcomeCanceled,
	customerservice.ReplyOutcomeDeadlineExceeded,
	customerservice.ReplyOutcomeAbandoned,
}

func replyModeLabel(mode customerservice.ReplyMode) string {
	switch mode {
	case customerservice.ReplyModeGenerate, customerservice.ReplyModeStream:
		return string(mode)
	default:
		return "unknown"
	}
}

func replyOutcomeLabel(outcome customerservice.ReplyOutcome) string {
	for _, known := range replyMetricOutcomes {
		if outcome == known {
			return string(outcome)
		}
	}
	return "unknown"
}

// ReplyCapacityCollector reads the authoritative limiter at scrape time.
// Observer snapshots can arrive out of order, and a Finished event can arrive
// long after its model slot was released while the reply was being persisted.
type ReplyCapacityCollector struct {
	snapshot func() customerservice.ReplyCapacitySnapshot
	active   *prometheus.Desc
	limit    *prometheus.Desc
}

// NewReplyCapacityCollector requires a prompt, concurrency-safe snapshot source.
// Bind service.ReplyCapacity after creating the Service, before serving requests.
func NewReplyCapacityCollector(snapshot func() customerservice.ReplyCapacitySnapshot) (*ReplyCapacityCollector, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("%w: reply capacity snapshot source is nil", errInvalidDependency)
	}
	return &ReplyCapacityCollector{
		snapshot: snapshot,
		active:   prometheus.NewDesc(replyMetricPrefix+"active", "Currently occupied process-local model-generation slots; untracked when limit is zero.", nil, nil),
		limit:    prometheus.NewDesc(replyMetricPrefix+"limit", "Process-local model-generation capacity; zero means no configured limiter and no active tracking.", nil, nil),
	}, nil
}

func (c *ReplyCapacityCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.active
	ch <- c.limit
}

func (c *ReplyCapacityCollector) Collect(ch chan<- prometheus.Metric) {
	// One read keeps both values in the same limiter snapshot.
	capacity := c.snapshot()
	ch <- prometheus.MustNewConstMetric(c.active, prometheus.GaugeValue, float64(capacity.Active))
	ch <- prometheus.MustNewConstMetric(c.limit, prometheus.GaugeValue, float64(capacity.Limit))
}

var (
	_ customerservice.ReplyObserver = (*PrometheusReplyObserver)(nil)
	_ prometheus.Collector          = (*PrometheusReplyObserver)(nil)
	_ prometheus.Collector          = (*ReplyCapacityCollector)(nil)
)
