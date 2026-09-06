package hertz

import (
	"github.com/cloudwego/hertz/pkg/common/adaptor"
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const metricsPath = "/metrics"

// RegisterMetricsRoute is called after RegisterRoutes so it inherits recovery,
// request IDs and access logging. Its dedicated credential and scrape concurrency
// guard do not share the business API's authentication or rate-limit quota.
// Prometheus exposition is an operational protocol, not a Thrift JSON DTO.
func RegisterMetricsRoute(engine *route.Engine, gatherer prometheus.Gatherer, security *SecurityMiddleware) error {
	if engine == nil {
		return invalidDependency("metrics route engine is nil")
	}
	if gatherer == nil {
		return invalidDependency("metrics gatherer is nil")
	}
	if security == nil {
		return invalidDependency("metrics security middleware is nil")
	}
	handler := promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{
		MaxRequestsInFlight: 2,
		// A gather failure happens before headers are sent: outer safe recovery
		// can return HTTP 500 without exposing raw registry errors. A later write
		// failure cannot change a status already sent to the scraping client.
		ErrorHandling: promhttp.PanicOnError,
	})
	engine.GET(metricsPath, security.RequireBearerToken, adaptor.HertzHandler(handler))
	return nil
}
