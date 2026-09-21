// Command webchat-demo runs an isolated, loopback-only example host. It does not
// use DATABASE_URL or host deployment secrets. AI and persistent demo storage
// require explicit flags; PostgreSQL is restricted to its own loopback database.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/webchat"
	"github.com/qiuyier/omni-agent-customer-service/internal/webchat/ui"
)

const defaultDemoAddress = "127.0.0.1:8092"

func main() {
	address := flag.String("address", defaultDemoAddress, "literal loopback listen address (demo only)")
	storage := flag.String("storage", "memory", "demo storage: memory or explicitly configured local postgres")
	reply := flag.String("reply", "fake", "fake (offline default) or ai (configured model; simulated login only)")
	check := flag.Bool("check-config", false, "validate demo configuration without connecting or listening")
	flag.Parse()
	config, err := resolveDemoConfigWithReply(*address, *storage, *reply, os.LookupEnv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "webchat-demo:", err)
		os.Exit(1)
	}
	if *check {
		fmt.Printf("webchat-demo: configuration valid (simulated login, reply mode: %s); no database, knowledge file, model or embedding connectivity checked\n", config.reply.Mode)
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := runDemoWithConfig(ctx, config, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "webchat-demo:", err)
		os.Exit(1)
	}
}

func validateDemoAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("address must be a literal loopback IP and port")
	}
	ip := net.ParseIP(host)
	number, err := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || err != nil || number < 0 || number > 65535 {
		return errors.New("demo can listen only on a literal loopback IP and valid port")
	}
	return nil
}

func runDemo(ctx context.Context, address string, output io.Writer) error {
	config, err := resolveDemoConfig(address, "memory", nil)
	if err != nil {
		return err
	}
	return runDemoWithConfig(ctx, config, output)
}

func runDemoWithConfig(ctx context.Context, config demoConfig, output io.Writer) error {
	// Validate before creating either listener; never resolve a hostname that
	// could unexpectedly expose this deliberately unauthenticated demo.
	if err := validateDemoAddress(config.address); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := config.reply.Validate(); err != nil {
		return err
	}
	secret, err := randomToken()
	if err != nil {
		return errors.New("cannot initialize demo credentials")
	}
	if config.repository.Driver == bootstrap.StorageDriverPostgres {
		if err := validateDemoDatabaseURL(config.repository.DatabaseURL); err != nil {
			return err
		}
	}
	startup, stopStartup := context.WithTimeout(ctx, 10*time.Second)
	runtime, err := bootstrap.NewConversationRepository(startup, config.repository)
	stopStartup()
	if err != nil {
		return errors.New("cannot open demo storage; verify local PostgreSQL and run migrations; no memory fallback")
	}
	defer runtime.Close()
	repository := runtime.Repository
	options, err := bootstrap.NewWebchatReplyOptions(ctx, config.reply, customerservice.ReplyGeneratorFunc(demoReply))
	if err != nil {
		return err
	}
	service, err := customerservice.New(repository,
		customerservice.MessageIDGeneratorFunc(randomToken),
		customerservice.ClockFunc(time.Now),
		options...,
	)
	if err != nil {
		return errors.New("cannot initialize demo application")
	}
	sourceID := "example-host"
	if config.repository.Driver == bootstrap.StorageDriverPostgres {
		sourceID = "webchat-demo"
	}
	gateway, err := webchat.New(repository, service, webchat.Config{
		Hosts:          []webchat.Host{{SourceID: sourceID, Secret: secret}},
		SessionTTL:     5 * time.Minute,
		RequestTimeout: config.reply.RequestTimeout(),
	})
	if err != nil {
		return errors.New("cannot initialize demo gateway")
	}
	publicListener, err := net.Listen("tcp", config.address)
	if err != nil {
		return errors.New("cannot listen on the public demo address (it may already be in use)")
	}
	defer publicListener.Close()
	privateListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return errors.New("cannot initialize private demo listener")
	}
	defer privateListener.Close()
	origin := "http://" + publicListener.Addr().String()
	host, err := newExampleHost(origin, "http://"+privateListener.Addr().String(), secret, ui.Handler())
	if err != nil {
		return err
	}
	defer host.client.CloseIdleConnections()
	host.storage = string(config.repository.Driver)
	host.replyMode = config.reply.Mode
	requestTimeout := config.reply.RequestTimeout()
	host.client.Timeout = requestTimeout + 10*time.Second
	host.client.Transport.(*http.Transport).ResponseHeaderTimeout = requestTimeout + 5*time.Second
	requestRoot, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests() // Runs before closing the PostgreSQL pool.
	privateRequests := &demoRequestTracker{next: gateway}
	publicRequests := &demoRequestTracker{next: host}
	privateServer := demoHTTPServer(privateRequests)
	publicServer := demoHTTPServer(publicRequests)
	privateServer.WriteTimeout = requestTimeout + 5*time.Second
	publicServer.WriteTimeout = requestTimeout + 15*time.Second
	privateServer.BaseContext = func(net.Listener) context.Context { return requestRoot }
	publicServer.BaseContext = func(net.Listener) context.Context { return requestRoot }
	defer privateServer.Close()
	defer publicServer.Close()
	defer func() {
		publicRequests.beginDrain()
		privateRequests.beginDrain()
		cancelRequests()
		_ = publicServer.Close()
		_ = privateServer.Close()
		publicRequests.active.Wait()
		privateRequests.active.Wait()
	}() // Join canceled model/database work before runtime.Close releases storage.
	serverErrors := make(chan error, 2)
	go func() { serverErrors <- privateServer.Serve(privateListener) }()
	go func() { serverErrors <- publicServer.Serve(publicListener) }()
	storageNotice := "No database. In-memory history is lost on restart."
	if config.repository.Driver == bootstrap.StorageDriverPostgres {
		storageNotice = "PostgreSQL demo history survives restart; simulated login must be repeated. Not production authentication."
	}
	replyNotice := "Fake replies only; no external model calls."
	if config.reply.Mode == "ai" {
		replyNotice = "AI replies enabled: prompts and matched knowledge are sent to the configured provider and may incur costs. Simulated accounts are not production authentication."
	}
	if _, err := fmt.Fprintf(output, "Web chat demo: %s\nSimulated login only. No external business actions. %s %s\n", origin, replyNotice, storageNotice); err != nil {
		return errors.New("cannot display the public demo URL")
	}
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-serverErrors:
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Stop admitting public requests first, then stop the private gateway.
	publicRequests.beginDrain()
	publicErr := publicServer.Shutdown(shutdownContext)
	privateRequests.beginDrain()
	privateErr := privateServer.Shutdown(shutdownContext)
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return errors.New("demo HTTP server stopped unexpectedly")
	}
	if publicErr != nil || privateErr != nil {
		return errors.New("demo shutdown exceeded its deadline")
	}
	return nil
}

func demoHTTPServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler: handler, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10,
		// net/http diagnostics can contain request-derived values. The demo
		// intentionally emits no request, credential, body, or private URL logs.
		ErrorLog: log.New(io.Discard, "", 0),
	}
}

func demoReply(ctx context.Context, _ customerservice.ReplyRequest) (customerservice.Reply, error) {
	if err := ctx.Err(); err != nil {
		return customerservice.Reply{}, err
	}
	return customerservice.Reply{Content: "已收到你的问题。这是一条用于验证网页客服接入流程的演示回复，不会查询或修改业务数据，也不会执行任何外部操作。"}, nil
}
