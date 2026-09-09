// Command server runs the customer-service HTTP API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/observability"
	hertztransport "github.com/qiuyier/omni-agent-customer-service/internal/transport/hertz"
)

const defaultHTTPAddress = "127.0.0.1:8080"

const startupTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	address := flag.String(
		"address",
		environmentOrDefault("HTTP_ADDRESS", defaultHTTPAddress),
		"HTTP listen address",
	)
	flag.Parse()
	if strings.TrimSpace(*address) == "" {
		return errors.New("HTTP address must not be blank")
	}
	loggingConfig, err := bootstrap.ResolveLoggingConfig(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("resolve logging configuration: %w", err)
	}
	hertzLogger, err := bootstrap.NewZapLogger(loggingConfig)
	if err != nil {
		return fmt.Errorf("create Zap logger: %w", err)
	}
	logger := hertzLogger.Logger()
	defer func() { _ = logger.Sync() }()
	hlog.SetLogger(hertzLogger)

	accessLogMiddleware, err := hertztransport.NewAccessLogMiddleware(logger)
	if err != nil {
		return fmt.Errorf("create HTTP access log middleware: %w", err)
	}
	zapReplyObserver, err := observability.NewZapReplyObserver(
		logger,
		hertztransport.RequestIDFromContext,
	)
	if err != nil {
		return fmt.Errorf("create AI reply observer: %w", err)
	}
	apiConfig, err := bootstrap.ResolveAPIConfig(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("resolve API configuration: %w", err)
	}
	metricsConfig, err := bootstrap.ResolveMetricsConfig(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("resolve metrics configuration: %w", err)
	}
	metricsObserver := observability.NewPrometheusReplyObserver()
	replyObserver, err := observability.NewReplyObserverGroup(zapReplyObserver, metricsObserver)
	if err != nil {
		return fmt.Errorf("create combined AI reply observer: %w", err)
	}
	securityMiddleware, err := hertztransport.NewSecurityMiddleware(apiConfig.BearerToken)
	if err != nil {
		return fmt.Errorf("create HTTP security middleware: %w", err)
	}
	rateLimitMiddleware, err := hertztransport.NewRateLimitMiddleware(
		apiConfig.RateLimitRPS,
		apiConfig.RateLimitBurst,
	)
	if err != nil {
		return fmt.Errorf("create HTTP rate limit middleware: %w", err)
	}
	aiConfig, err := bootstrap.ResolveAIConfig(os.LookupEnv, bootstrap.AIOverrides{})
	if err != nil {
		return fmt.Errorf("resolve AI configuration: %w", err)
	}
	aiRuntimeConfig, err := bootstrap.ResolveAIReplyRuntimeConfig(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("resolve AI runtime configuration: %w", err)
	}
	repositoryConfig, err := bootstrap.ResolveRepositoryConfig(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("resolve storage configuration: %w", err)
	}
	knowledgeConfig, err := bootstrap.ResolveKnowledgeConfig(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("resolve knowledge configuration: %w", err)
	}
	knowledgeRetriever, err := bootstrap.NewKnowledgeRetriever(knowledgeConfig)
	if err != nil {
		return fmt.Errorf("create knowledge retriever: %w", err)
	}
	startupContext, cancelStartup := context.WithTimeout(context.Background(), startupTimeout)
	defer cancelStartup()
	repositoryRuntime, err := bootstrap.NewConversationRepository(startupContext, repositoryConfig)
	if err != nil {
		return fmt.Errorf("create conversation repository: %w", err)
	}
	closeRepository := true
	defer func() {
		if closeRepository {
			repositoryRuntime.Close()
		}
	}()
	replyGenerator, err := bootstrap.NewAIReplyGenerator(startupContext, aiConfig)
	if err != nil {
		return err
	}
	cancelStartup()

	serviceOptions := []customerservice.Option{
		customerservice.WithReplyGenerator(replyGenerator),
		customerservice.WithReplyStreamGenerator(replyGenerator),
		customerservice.WithReplyGenerationTimeout(bootstrap.DefaultAIReplyTimeout),
		customerservice.WithMaxConcurrentReplies(aiRuntimeConfig.MaxConcurrentReplies),
		customerservice.WithReplyObserver(replyObserver),
	}
	if knowledgeRetriever != nil {
		serviceOptions = append(serviceOptions, customerservice.WithKnowledgeRetriever(knowledgeRetriever))
		serviceOptions = append(serviceOptions, customerservice.WithKnowledgeRetrievalTimeout(knowledgeConfig.RetrievalTimeout))
	}
	service, err := customerservice.New(
		repositoryRuntime.Repository,
		customerservice.MessageIDGeneratorFunc(func() (string, error) {
			return uuid.NewString(), nil
		}),
		customerservice.ClockFunc(time.Now),
		serviceOptions...,
	)
	if err != nil {
		return fmt.Errorf("create customer-service application: %w", err)
	}
	handler, err := hertztransport.NewHandler(service)
	if err != nil {
		return fmt.Errorf("create HTTP handler: %w", err)
	}

	serverConfig, err := bootstrap.ResolveServerConfig(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("resolve server configuration: %w", err)
	}
	listener, err := net.Listen("tcp", strings.TrimSpace(*address))
	if err != nil {
		return fmt.Errorf("listen for HTTP requests: %w", err)
	}
	// Closing an already-closed net.Listener is harmless. Keeping this fallback
	// unconditional also covers a failure before Hertz starts owning it.
	defer func() { _ = listener.Close() }()

	availability := hertztransport.NewAvailability()
	requestRootContext, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()

	// RegisterRoutes installs a safe Recovery before the Zap access logger.
	// server.Default is intentionally avoided because its Recovery logs raw
	// panic values and stack source through hlog.
	httpServer := newHertzServer(
		listener,
		requestRootContext,
		serverConfig.ShutdownTimeout,
	)
	if err := hertztransport.RegisterRoutes(
		httpServer.Engine,
		handler,
		availability,
		securityMiddleware,
		accessLogMiddleware,
		rateLimitMiddleware,
	); err != nil {
		return fmt.Errorf("register HTTP routes: %w", err)
	}
	if metricsConfig.BearerToken != "" {
		// Build one registry per server, after Service exists. The collector
		// reads live capacity; no mutable callback or global registry is needed.
		capacityCollector, err := observability.NewReplyCapacityCollector(service.ReplyCapacity)
		if err != nil {
			return fmt.Errorf("create AI reply capacity collector: %w", err)
		}
		registry := prometheus.NewRegistry()
		for _, collector := range []prometheus.Collector{metricsObserver, capacityCollector} {
			if err := registry.Register(collector); err != nil {
				return fmt.Errorf("register AI reply metrics: %w", err)
			}
		}
		metricsSecurity, err := hertztransport.NewSecurityMiddleware(metricsConfig.BearerToken)
		if err != nil {
			return fmt.Errorf("create metrics security middleware: %w", err)
		}
		if err := hertztransport.RegisterMetricsRoute(httpServer.Engine, registry, metricsSecurity); err != nil {
			return fmt.Errorf("register metrics route: %w", err)
		}
	}

	stopContext, stopSignals := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	restoreDefaultSignals := sync.OnceFunc(stopSignals)
	defer restoreDefaultSignals()
	serveErr := serveHTTP(
		stopContext,
		httpServer,
		availability,
		serverConfig.ShutdownTimeout,
		restoreDefaultSignals,
		cancelRequests,
		logger,
	)
	if errors.Is(serveErr, errForcedShutdown) {
		// PostgreSQL pool Close waits without a context for checked-out
		// connections. After a forced stop, process exit is safer than starting
		// another unbounded wait on handlers that exceeded the grace period.
		closeRepository = false
	}
	return serveErr
}

func newHertzServer(
	listener net.Listener,
	requestRootContext context.Context,
	shutdownTimeout time.Duration,
) *server.Hertz {
	return server.New(
		server.WithListener(listener),
		server.WithExitWaitTime(shutdownTimeout),
		server.WithOnAccept(func(net.Conn) context.Context {
			return requestRootContext
		}),
		server.WithSenseClientDisconnection(true),
	)
}

func environmentOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
