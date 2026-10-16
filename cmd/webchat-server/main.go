// Command webchat-server is the loopback-only PostgreSQL gateway for a trusted
// host backend. It has no login simulator, public proxy, UI or service-wide API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/webchat"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx = bootstrap.WithKnowledgeSignals(ctx, os.Stderr)
	// A second signal retains its normal immediate-exit behavior during drain.
	go func() { <-ctx.Done(); stop() }()
	if err := runCommand(ctx, os.Args[1:], os.LookupEnv, os.Stdout, defaultDependencies()); err != nil {
		fmt.Fprintln(os.Stderr, "webchat-server:", err)
		os.Exit(1)
	}
}

func runCommand(ctx context.Context, args []string, lookup func(string) (string, bool), output io.Writer, deps serverDependencies) error {
	flags := flag.NewFlagSet("webchat-server", flag.ContinueOnError)
	// Flag errors can echo arbitrary user-supplied arguments. Emit only fixed
	// diagnostics, including for misspelled flags that accidentally contain keys.
	flags.SetOutput(io.Discard)
	address := flags.String("address", "", "literal loopback listen address (overrides WEBCHAT_ADDRESS)")
	check := flags.Bool("check-config", false, "validate settings only; do not connect or listen")
	resources := flags.Bool("check-resources", false, "validate settings, local FAQ and PostgreSQL; never call models or listen")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err = fmt.Fprintln(output, "Usage: webchat-server [-address IP:PORT] [-check-config | -check-resources]\nRequires DATABASE_URL and either WEBCHAT_HOST_ID/WEBCHAT_HOST_SECRET or WEBCHAT_HOSTS_JSON. PostgreSQL required; replies default to fake. Multiple hosts require fake mode; single-host WEBCHAT_REPLY_MODE=ai enables configured AI and optional knowledge.\n-check-config is offline format validation. -check-resources also reads the configured FAQ and checks PostgreSQL connectivity/schema; neither calls models or embeddings, migrates the database or starts a listener.")
			if err == nil {
				return nil
			}
			return errors.New("cannot display command usage")
		}
		return errors.New("invalid command line; use -help for supported flags")
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected command-line arguments")
	}
	if *check && *resources {
		return errors.New("choose only one of -check-config and -check-resources")
	}
	config, err := resolveConfig(lookup, *address)
	if err != nil {
		return err
	}
	if *check {
		if _, err := fmt.Fprintf(output, "Web chat server configuration is valid. PostgreSQL connectivity, schema, knowledge files and model availability were not checked. No model or embedding requests were made. Reply mode: %s.\n", config.ReplyMode); err != nil {
			return errors.New("cannot display configuration status")
		}
		return nil
	}
	if *resources {
		return checkResources(ctx, config, output, deps)
	}
	return runServer(ctx, config, output, deps)
}

type repositoryHandle struct {
	repository conversation.Repository
	check      func(context.Context) error
	close      func()
}

func openRepository(ctx context.Context, config bootstrap.RepositoryConfig) (*repositoryHandle, error) {
	runtime, err := bootstrap.NewConversationRepository(ctx, config)
	if err != nil {
		return nil, err
	}
	// PostgreSQL owns these checks. Do not replace a missing readiness contract
	// with a successful static probe or an in-memory repository.
	probe, ok := runtime.Repository.(interface {
		Ping(context.Context) error
		CheckSchema(context.Context) error
	})
	if !ok {
		runtime.Close()
		return nil, errors.New("PostgreSQL readiness checks are unavailable")
	}
	return &repositoryHandle{repository: runtime.Repository, close: runtime.Close,
		check: func(ctx context.Context) error {
			if err := probe.Ping(ctx); err != nil {
				return err
			}
			return probe.CheckSchema(ctx)
		}}, nil
}

func newGateway(repository conversation.Repository, config serverConfig) (http.Handler, error) {
	return newGatewayContext(context.Background(), repository, config)
}

func newGatewayContext(ctx context.Context, repository conversation.Repository, config serverConfig) (http.Handler, error) {
	options, err := bootstrap.NewWebchatReplyOptions(ctx, config.Reply, customerservice.ReplyGeneratorFunc(fakeReply))
	if err != nil {
		return nil, err
	}
	service, err := customerservice.New(repository,
		customerservice.MessageIDGeneratorFunc(func() (string, error) {
			id, err := uuid.NewRandom()
			return id.String(), err
		}),
		customerservice.ClockFunc(time.Now),
		options...,
	)
	if err != nil {
		return nil, errors.New("cannot initialize the web chat application")
	}
	gateway, err := webchat.New(repository, service, webchat.Config{
		Agents:         config.Agents,
		Hosts:          config.trustedHosts(),
		RequestTimeout: config.Reply.RequestTimeout(),
	})
	if err != nil {
		return nil, errors.New("cannot initialize the trusted-host gateway")
	}
	return gateway, nil
}

func fakeReply(ctx context.Context, _ customerservice.ReplyRequest) (customerservice.Reply, error) {
	if err := ctx.Err(); err != nil {
		return customerservice.Reply{}, err
	}
	return customerservice.Reply{Content: "已收到你的问题。这是一条用于验证网页客服持久化接入流程的演示回复，不会查询或修改业务数据，也不会执行任何外部操作。"}, nil
}
