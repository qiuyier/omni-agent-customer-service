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
	"github.com/google/uuid"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
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
	apiConfig, err := bootstrap.ResolveAPIConfig(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("resolve API configuration: %w", err)
	}
	securityMiddleware, err := hertztransport.NewSecurityMiddleware(apiConfig.BearerToken)
	if err != nil {
		return fmt.Errorf("create HTTP security middleware: %w", err)
	}
	aiConfig, err := bootstrap.ResolveAIConfig(os.LookupEnv, bootstrap.AIOverrides{})
	if err != nil {
		return fmt.Errorf("resolve AI configuration: %w", err)
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
	)
	if err != nil {
		return fmt.Errorf("create customer-service application: %w", err)
	}
	handler, err := hertztransport.NewHandler(service)
	if err != nil {
		return fmt.Errorf("create HTTP handler: %w", err)
	}

	httpServer := server.Default(
		server.WithHostPorts(strings.TrimSpace(*address)),
		server.WithSenseClientDisconnection(true),
	)
	if err := hertztransport.RegisterRoutes(
		httpServer.Engine,
		handler,
		securityMiddleware,
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
