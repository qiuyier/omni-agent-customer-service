// Command webchat-demo runs an isolated, loopback-only example host. It never
// reads deployment credentials, connects to a database, or invokes a real model.
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

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/webchat"
	"github.com/qiuyier/omni-agent-customer-service/internal/webchat/ui"
)

const defaultDemoAddress = "127.0.0.1:8092"

func main() {
	address := flag.String("address", defaultDemoAddress, "literal loopback listen address (demo only)")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := runDemo(ctx, *address, os.Stdout); err != nil {
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
	// Validate before creating either listener; never resolve a hostname that
	// could unexpectedly expose this deliberately unauthenticated demo.
	if err := validateDemoAddress(address); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	secret, err := randomToken()
	if err != nil {
		return errors.New("cannot initialize demo credentials")
	}
	repository := conversation.NewMemoryRepository()
	service, err := customerservice.New(repository,
		customerservice.MessageIDGeneratorFunc(randomToken),
		customerservice.ClockFunc(time.Now),
		customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(demoReply)),
		customerservice.WithReplyGenerationTimeout(3*time.Second),
		customerservice.WithMaxConcurrentReplies(8),
	)
	if err != nil {
		return errors.New("cannot initialize demo application")
	}
	gateway, err := webchat.New(repository, service, webchat.Config{
		Hosts:      []webchat.Host{{SourceID: "example-host", Secret: secret}},
		SessionTTL: 5 * time.Minute,
	})
	if err != nil {
		return errors.New("cannot initialize demo gateway")
	}
	publicListener, err := net.Listen("tcp", address)
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
	privateServer := demoHTTPServer(gateway)
	publicServer := demoHTTPServer(host)
	defer privateServer.Close()
	defer publicServer.Close()
	serverErrors := make(chan error, 2)
	go func() { serverErrors <- privateServer.Serve(privateListener) }()
	go func() { serverErrors <- publicServer.Serve(publicListener) }()
	if _, err := fmt.Fprintf(output, "Web chat demo: %s\nSimulated login and reply only. No external actions, model, or database. In-memory history is lost on restart.\n", origin); err != nil {
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
	publicErr := publicServer.Shutdown(shutdownContext)
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
