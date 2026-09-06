package observability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	kitstream "github.com/qiuyier/omni-agent-kit/stream"
)

func TestReplyMetricsCountsOutcomesAndDurationsInSeconds(t *testing.T) {
	observer := NewPrometheusReplyObserver()
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(observer)
	ctx := context.Background()
	for _, mode := range []customerservice.ReplyMode{customerservice.ReplyModeGenerate, customerservice.ReplyModeStream} {
		operation := customerservice.ReplyOperation{Mode: mode}
		observer.ReplyRejected(ctx, customerservice.ReplyRejectedEvent{Operation: operation})
		for _, outcome := range replyMetricOutcomes {
			observer.ReplyStarted(ctx, customerservice.ReplyStartedEvent{Operation: operation})
			observer.ReplyFinished(ctx, customerservice.ReplyFinishedEvent{
				Operation: operation, Outcome: outcome,
				ModelDuration: 1500 * time.Millisecond, TotalDuration: 2500 * time.Millisecond,
			})
			assertMetricCounter(t, registry, "finished_total", map[string]string{"mode": string(mode), "outcome": string(outcome)}, 1)
		}
		labels := map[string]string{"mode": string(mode)}
		assertMetricCounter(t, registry, "started_total", labels, 8)
		assertMetricCounter(t, registry, "rejected_total", labels, 1)
		for _, test := range []struct {
			name string
			sum  float64
		}{{"model_duration_seconds", 12}, {"total_duration_seconds", 20}} {
			histogram := findReplyMetric(t, registry, test.name, labels).GetHistogram()
			if histogram.GetSampleCount() != 8 || histogram.GetSampleSum() != test.sum {
				t.Fatalf("%s = %v, want count 8 sum %v", test.name, histogram, test.sum)
			}
		}
	}
}

func TestReplyMetricsNeverUseIdentifiersOrUnknownValuesAsLabels(t *testing.T) {
	observer := NewPrometheusReplyObserver()
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(observer)
	ctx := context.WithValue(context.Background(), struct{}{}, "secret-context")
	for index := range 50 {
		operation := customerservice.ReplyOperation{
			Mode:           customerservice.ReplyMode(fmt.Sprintf("unbounded-mode-%d", index)),
			ConversationID: "private-conversation", TriggerMessageID: "private-trigger",
		}
		observer.ReplyStarted(ctx, customerservice.ReplyStartedEvent{Operation: operation})
		observer.ReplyRejected(ctx, customerservice.ReplyRejectedEvent{Operation: operation})
		observer.ReplyFinished(ctx, customerservice.ReplyFinishedEvent{
			Operation: operation, Outcome: customerservice.ReplyOutcome(fmt.Sprintf("raw-error-%d", index)),
		})
	}
	assertMetricCounter(t, registry, "finished_total", map[string]string{"mode": "unknown", "outcome": "unknown"}, 50)
	response := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	for _, forbidden := range []string{"secret-context", "private-conversation", "private-trigger", "unbounded-mode", "raw-error", "conversation_id", "request_id"} {
		if strings.Contains(response.Body.String(), forbidden) {
			t.Errorf("metrics expose forbidden value %q", forbidden)
		}
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if label.GetName() != "mode" && label.GetName() != "outcome" {
					t.Errorf("unexpected label %q", label.GetName())
				}
			}
		}
	}
}

func TestReplyMetricsConcurrentNotificationsAndIndependentRegistries(t *testing.T) {
	observer := NewPrometheusReplyObserver()
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(observer)
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 25 {
				operation := customerservice.ReplyOperation{Mode: customerservice.ReplyModeStream}
				observer.ReplyStarted(context.Background(), customerservice.ReplyStartedEvent{Operation: operation})
				observer.ReplyFinished(context.Background(), customerservice.ReplyFinishedEvent{Operation: operation, Outcome: customerservice.ReplyOutcomeCompleted})
				if _, err := registry.Gather(); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	workers.Wait()
	labels := map[string]string{"mode": "stream"}
	assertMetricCounter(t, registry, "started_total", labels, 200)
	other := prometheus.NewPedanticRegistry()
	other.MustRegister(NewPrometheusReplyObserver())
	assertMetricCounter(t, other, "started_total", labels, 0)
}

func TestReplyCapacityCollectorReadsOneFreshSnapshotPerScrape(t *testing.T) {
	if collector, err := NewReplyCapacityCollector(nil); !errors.Is(err, errInvalidDependency) || collector != nil {
		t.Fatalf("nil snapshot constructor = (%v, %v)", collector, err)
	}
	var calls int
	capacity := customerservice.ReplyCapacitySnapshot{Active: 2, Limit: 8}
	collector, err := NewReplyCapacityCollector(func() customerservice.ReplyCapacitySnapshot { calls++; return capacity })
	if err != nil {
		t.Fatal(err)
	}
	observer := NewPrometheusReplyObserver()
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector, observer)
	if calls != 0 {
		t.Fatal("Describe read capacity during registration")
	}
	for _, active := range []int{2, 0, 3} {
		capacity.Active = active
		// Delayed notifications from previous attempts must not overwrite gauges.
		observer.ReplyStarted(context.Background(), customerservice.ReplyStartedEvent{Capacity: customerservice.ReplyCapacitySnapshot{Active: 7, Limit: 8}})
		observer.ReplyFinished(context.Background(), customerservice.ReplyFinishedEvent{Capacity: customerservice.ReplyCapacitySnapshot{Limit: 8}})
		before := calls
		families, err := registry.Gather()
		if err != nil {
			t.Fatal(err)
		}
		if calls != before+1 {
			t.Fatalf("snapshot calls = %d, want %d", calls, before+1)
		}
		for _, family := range families {
			switch family.GetName() {
			case replyMetricPrefix + "active":
				if got := family.Metric[0].GetGauge().GetValue(); got != float64(active) {
					t.Errorf("active = %v, want %d", got, active)
				}
			case replyMetricPrefix + "limit":
				if got := family.Metric[0].GetGauge().GetValue(); got != 8 {
					t.Errorf("limit = %v, want 8", got)
				}
			}
		}
	}
}

func TestReplyMetricsRealServiceCountsGenerationNotReplayOrStreamChunks(t *testing.T) {
	observer := NewPrometheusReplyObserver()
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(observer)
	var ids atomic.Int64
	service, err := customerservice.New(conversation.NewMemoryRepository(),
		customerservice.MessageIDGeneratorFunc(func() (string, error) { return fmt.Sprint(ids.Add(1)), nil }),
		customerservice.ClockFunc(time.Now), customerservice.WithMaxConcurrentReplies(1), customerservice.WithReplyObserver(observer),
		customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(func(context.Context, customerservice.ReplyRequest) (customerservice.Reply, error) {
			return customerservice.Reply{Content: "answer"}, nil
		})),
		customerservice.WithReplyStreamGenerator(customerservice.ReplyStreamGeneratorFunc(func(context.Context, customerservice.ReplyRequest) (kitstream.Reader[customerservice.ReplyDelta], error) {
			return kitstream.FromSlice([]customerservice.ReplyDelta{{Content: "an"}, {Content: "swer"}}), nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	capacity, err := NewReplyCapacityCollector(service.ReplyCapacity)
	if err != nil {
		t.Fatal(err)
	}
	registry.MustRegister(capacity)
	ctx := context.Background()
	for _, mode := range []string{"generate", "stream"} {
		received, err := service.ReceiveMessage(ctx, customerservice.ReceiveMessageCommand{
			ConversationID: mode, CustomerID: "customer", Channel: "test", ExternalMessageID: mode, Content: "question",
		})
		if err != nil {
			t.Fatal(err)
		}
		command := customerservice.GenerateBotReplyCommand{ConversationID: mode, TriggerMessageID: received.MessageID}
		for attempt := range 2 { // Second call replays the stored reply, without a model attempt.
			if mode == "generate" {
				if _, err := service.GenerateBotReply(ctx, command); err != nil {
					t.Fatal(err)
				}
			} else {
				reader, err := service.StreamBotReply(ctx, command)
				if err != nil {
					t.Fatal(err)
				}
				if attempt == 0 {
					if got := findReplyMetric(t, registry, "active", nil).GetGauge().GetValue(); got != 1 {
						t.Fatalf("active during stream = %v, want 1", got)
					}
					if _, err := service.GenerateBotReply(ctx, command); !errors.Is(err, customerservice.ErrReplyCapacityExceeded) {
						t.Fatalf("generation while stream holds capacity = %v", err)
					}
					assertMetricCounter(t, registry, "finished_total", map[string]string{"mode": "stream", "outcome": "completed"}, 0)
				}
				for {
					_, err := reader.Recv()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						_ = reader.Close()
						t.Fatal(err)
					}
				}
				for range 2 {
					if err := reader.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		assertMetricCounter(t, registry, "started_total", map[string]string{"mode": mode}, 1)
		assertMetricCounter(t, registry, "finished_total", map[string]string{"mode": mode, "outcome": "completed"}, 1)
	}
	assertMetricCounter(t, registry, "rejected_total", map[string]string{"mode": "generate"}, 1)
	if got := findReplyMetric(t, registry, "active", nil).GetGauge().GetValue(); got != 0 {
		t.Fatalf("active = %v, want 0", got)
	}
}

func assertMetricCounter(t *testing.T, registry *prometheus.Registry, name string, labels map[string]string, want float64) {
	t.Helper()
	if got := findReplyMetric(t, registry, name, labels).GetCounter().GetValue(); got != want {
		t.Fatalf("%s %v = %v, want %v", name, labels, got, want)
	}
}

func findReplyMetric(t *testing.T, registry *prometheus.Registry, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != replyMetricPrefix+name {
			continue
		}
		for _, metric := range family.Metric {
			if len(metric.Label) != len(labels) {
				continue
			}
			matches := true
			for _, label := range metric.Label {
				if value, exists := labels[label.GetName()]; !exists || value != label.GetValue() {
					matches = false
					break
				}
			}
			if matches {
				return metric
			}
		}
	}
	t.Fatalf("missing metric %s with labels %v", name, labels)
	return nil
}
