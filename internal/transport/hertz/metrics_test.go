package hertz

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const testMetricsAuthToken = "metrics-read-only-test-token-0123456789"

func TestRegisterMetricsRouteRejectsInvalidDependencies(t *testing.T) {
	engine := server.New().Engine
	registry := prometheus.NewRegistry()
	security := newTestSecurityMiddleware(t)
	for _, test := range []struct {
		name     string
		engine   *route.Engine
		gatherer prometheus.Gatherer
		security *SecurityMiddleware
	}{
		{name: "engine", gatherer: registry, security: security},
		{name: "gatherer", engine: engine, security: security},
		{name: "security", engine: engine, gatherer: registry},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := RegisterMetricsRoute(test.engine, test.gatherer, test.security); !errors.Is(err, errInvalidDependency) {
				t.Fatalf("RegisterMetricsRoute() error = %v, want invalid dependency", err)
			}
		})
	}
}

func TestMetricsRequiresDedicatedCredentialAndRetainsRequestObservability(t *testing.T) {
	harness := newMetricsHTTPTestServer(t, newMetricsTestRegistry(t), true)
	for _, token := range []string{"", "wrong-token", testAPIAuthToken} {
		response, body := harness.request(t, http.MethodGet, metricsPath, token)
		if response.StatusCode != http.StatusUnauthorized || response.Header.Get("WWW-Authenticate") == "" {
			t.Fatalf("metrics authentication status/challenge = %d/%q", response.StatusCode, response.Header.Get("WWW-Authenticate"))
		}
		if strings.Contains(body, testAPIAuthToken) || strings.Contains(body, testMetricsAuthToken) {
			t.Fatalf("authentication response leaked a credential: %s", body)
		}
	}
	response, _ := harness.request(t, http.MethodPost, "/v1/conversations/conversation-1/messages", testMetricsAuthToken)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("metrics token on business route status = %d, want 401", response.StatusCode)
	}

	harness.logs.TakeAll()
	response, body := harness.request(t, http.MethodGet, metricsPath, testMetricsAuthToken)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d, body = %s", response.StatusCode, body)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain;") || !strings.Contains(contentType, "version=0.0.4") {
		t.Fatalf("metrics Content-Type = %q, want Prometheus text exposition", contentType)
	}
	if !strings.Contains(body, "# TYPE test_ai_replies_total counter\n") || !strings.Contains(body, "test_ai_replies_total 3\n") {
		t.Fatalf("metrics exposition = %q, want registered counter value", body)
	}
	if got := response.Header.Get(RequestIDHeader); got != "metrics-request-123" {
		t.Fatalf("metrics response request ID = %q", got)
	}
	entry := takeSingleAccessLog(t, harness.logs)
	fields := entry.ContextMap()
	assertAccessLogField(t, fields, "http_method", http.MethodGet)
	assertAccessLogField(t, fields, "http_route", metricsPath)
	assertAccessLogField(t, fields, "http_status", int64(http.StatusOK))
	assertAccessLogField(t, fields, "request_id", "metrics-request-123")
	assertLogOmits(t, entry, testMetricsAuthToken, testAPIAuthToken)
}

func TestMetricsDoesNotShareBusinessRateLimitOrReadiness(t *testing.T) {
	harness := newMetricsHTTPTestServer(t, newMetricsTestRegistry(t), true)
	scrape := func() {
		t.Helper()
		response, body := harness.request(t, http.MethodGet, metricsPath, testMetricsAuthToken)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("metrics status = %d, body = %s", response.StatusCode, body)
		}
	}
	for range 3 {
		scrape()
	}
	const businessPath = "/v1/conversations/conversation-1/messages"
	response, body := harness.request(t, http.MethodPost, businessPath, testAPIAuthToken)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("first business request after scrapes status = %d, body = %s", response.StatusCode, body)
	}
	response, _ = harness.request(t, http.MethodPost, businessPath, testAPIAuthToken)
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("exhausted business quota status = %d, want 429", response.StatusCode)
	}
	scrape()
	harness.availability.BeginDraining()
	response, _ = harness.request(t, http.MethodGet, readinessPath, "")
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("draining readiness status = %d, want 503", response.StatusCode)
	}
	// Only admission changes here; shutting the real listener would naturally
	// make every route unreachable, including metrics.
	scrape()
}

func TestMetricsGatherFailureReturnsSanitized500(t *testing.T) {
	const privateError = "collector failed: api_key=sk-private prompt=private-customer-text"
	gatherer := prometheus.GathererFunc(func() ([]*dto.MetricFamily, error) {
		return nil, errors.New(privateError)
	})
	harness := newMetricsHTTPTestServer(t, gatherer, true)
	response, body := harness.request(t, http.MethodGet, metricsPath, testMetricsAuthToken)
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("gather failure status = %d, want 500; body = %s", response.StatusCode, body)
	}
	if strings.Contains(body, "sk-private") || strings.Contains(body, "private-customer-text") {
		t.Fatalf("gather failure exposed private error: %q", body)
	}
	entry := takeSingleAccessLog(t, harness.logs)
	assertAccessLogField(t, entry.ContextMap(), "http_status", int64(http.StatusInternalServerError))
	assertAccessLogField(t, entry.ContextMap(), "panicked", true)
	assertLogOmits(t, entry, privateError, "sk-private", "private-customer-text")
}

func TestMetricsRouteIsAbsentWhenNotRegistered(t *testing.T) {
	harness := newMetricsHTTPTestServer(t, nil, false)
	response, _ := harness.request(t, http.MethodGet, metricsPath, testMetricsAuthToken)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unregistered metrics route status = %d, want 404", response.StatusCode)
	}
}

type metricsHTTPTestServer struct {
	url          string
	client       *http.Client
	availability *Availability
	logs         *observer.ObservedLogs
}

func newMetricsHTTPTestServer(t *testing.T, gatherer prometheus.Gatherer, registerMetrics bool) *metricsHTTPTestServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	httpServer := server.New(server.WithListener(listener), server.WithExitWaitTime(time.Second))
	handler, err := NewHandler(&receiverStub{})
	if err != nil {
		t.Fatal(err)
	}
	core, logs := observer.New(zapcore.DebugLevel)
	accessLog, err := NewAccessLogMiddleware(zap.New(core))
	if err != nil {
		t.Fatal(err)
	}
	// A frozen clock makes the single business token deterministic: scrapes
	// cannot hide accidental quota use behind replenishment while tests run.
	frozenTime := time.Now()
	rateLimit, err := newRateLimitMiddleware(1, 1, func() time.Time { return frozenTime })
	if err != nil {
		t.Fatal(err)
	}
	availability := NewAvailability()
	if err := RegisterRoutes(httpServer.Engine, handler, availability, newTestSecurityMiddleware(t), accessLog, rateLimit); err != nil {
		t.Fatal(err)
	}
	if registerMetrics {
		security, err := NewSecurityMiddleware(testMetricsAuthToken)
		if err != nil {
			t.Fatal(err)
		}
		if err := RegisterMetricsRoute(httpServer.Engine, gatherer, security); err != nil {
			t.Fatal(err)
		}
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	serveDone := make(chan error, 1)
	go func() { serveDone <- httpServer.Run() }()
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		_ = httpServer.Close()
		_ = listener.Close()
		select {
		case <-serveDone:
		case <-time.After(3 * time.Second):
			t.Error("metrics HTTP test server did not stop")
		}
	})
	return &metricsHTTPTestServer{
		url:          "http://" + listener.Addr().String(),
		client:       &http.Client{Transport: transport, Timeout: 3 * time.Second},
		availability: availability,
		logs:         logs,
	}
}

func (s *metricsHTTPTestServer) request(t *testing.T, method, path, token string) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(validReceiveMessageJSON)
	}
	request, err := http.NewRequest(method, s.url+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set(RequestIDHeader, "metrics-request-123")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/plain; version=0.0.4")
	response, err := s.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, string(content)
}

func newMetricsTestRegistry(t *testing.T) *prometheus.Registry {
	t.Helper()
	registry := prometheus.NewRegistry()
	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "test_ai_replies_total",
		Help: "Test reply attempts.",
	})
	if err := registry.Register(counter); err != nil {
		t.Fatal(err)
	}
	counter.Add(3)
	return registry
}
