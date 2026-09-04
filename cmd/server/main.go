// Command server runs the customer-service HTTP API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/google/uuid"

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
	replyObserver, err := observability.NewZapReplyObserver(
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
	startupContext, cancelStartup := context.WithTimeout(context.Background(), startupTimeout)
	defer cancelStartup()
	repositoryRuntime, err := bootstrap.NewConversationRepository(startupContext, repositoryConfig)
	if err != nil {
		return fmt.Errorf("create conversation repository: %w", err)
	}
	defer repositoryRuntime.Close()
	replyGenerator, err := bootstrap.NewAIReplyGenerator(startupContext, aiConfig)
	if err != nil {
		return err
	}
	cancelStartup()

	service, err := customerservice.New(
		repositoryRuntime.Repository,
		customerservice.MessageIDGeneratorFunc(func() (string, error) {
			return uuid.NewString(), nil
		}),
		customerservice.ClockFunc(time.Now),
		customerservice.WithReplyGenerator(replyGenerator),
		customerservice.WithReplyStreamGenerator(replyGenerator),
		customerservice.WithReplyGenerationTimeout(bootstrap.DefaultAIReplyTimeout),
		customerservice.WithMaxConcurrentReplies(aiRuntimeConfig.MaxConcurrentReplies),
		customerservice.WithReplyObserver(replyObserver),
	)
	if err != nil {
		return fmt.Errorf("create customer-service application: %w", err)
	}
	handler, err := hertztransport.NewHandler(service)
	if err != nil {
		return fmt.Errorf("create HTTP handler: %w", err)
	}

	// RegisterRoutes installs a safe Recovery before the Zap access logger.
	// server.Default is intentionally avoided because its Recovery logs raw
	// panic values and stack source through hlog.
	httpServer := server.New(
		server.WithHostPorts(strings.TrimSpace(*address)),
		server.WithSenseClientDisconnection(true),
	)
	if err := hertztransport.RegisterRoutes(
		httpServer.Engine,
		handler,
		securityMiddleware,
		accessLogMiddleware,
		rateLimitMiddleware,
	); err != nil {
		return fmt.Errorf("register HTTP routes: %w", err)
	}

	httpServer.Spin()
	return nil
}

func environmentOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
