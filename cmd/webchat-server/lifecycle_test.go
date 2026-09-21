package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
)

func TestStartupFailureNeverListensOrLeaksDriverErrors(t *testing.T) {
	for _, stage := range []string{"open", "readiness", "timeout"} {
		t.Run(stage, func(t *testing.T) {
			closed := false
			deps := defaultDependencies()
			deps.startupTimeout = 10 * time.Millisecond
			deps.listen = func(string, string) (net.Listener, error) { t.Fatal("listened before readiness"); return nil, nil }
			deps.open = func(ctx context.Context, config bootstrap.RepositoryConfig) (*repositoryHandle, error) {
				if config.Driver != bootstrap.StorageDriverPostgres {
					t.Fatal("storage fallback")
				}
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > deps.startupTimeout {
					t.Fatal("startup is unbounded")
				}
				if stage == "open" {
					return nil, errors.New("private-driver-dsn-and-password")
				}
				return &repositoryHandle{repository: conversation.NewMemoryRepository(), close: func() { closed = true }, check: func(ctx context.Context) error {
					if stage == "timeout" {
						<-ctx.Done()
					}
					return errors.New("private-schema-error-with-password")
				}}, nil
			}
			var output bytes.Buffer
			err := runServer(context.Background(), validConfig(t), &output, deps)
			if err == nil || strings.Contains(err.Error()+output.String(), "private") {
				t.Fatal("startup failure was not safe")
			}
			if stage != "open" && !closed {
				t.Fatal("startup failed without closing repository")
			}
		})
	}
}

func TestCanceledStartupDoesNotOpenRepository(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	deps := defaultDependencies()
	deps.open = func(context.Context, bootstrap.RepositoryConfig) (*repositoryHandle, error) {
		t.Fatal("opened repository after cancellation")
		return nil, nil
	}
	if err := runServer(ctx, validConfig(t), io.Discard, deps); err == nil {
		t.Fatal("expected canceled startup")
	}
}

func TestServerShutdownDrainsBeforeClosingRepository(t *testing.T) {
	for _, forced := range []bool{false, true} {
		name := "graceful"
		if forced {
			name = "forced"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan context.Context, 1)
			release := make(chan struct{})
			finished := make(chan struct{})
			address := make(chan string, 1)
			var calls atomic.Int32
			var closed atomic.Bool
			var prematureClose atomic.Bool
			deps := defaultDependencies()
			deps.drainTimeout = 100 * time.Millisecond
			deps.listen = func(network, listenAddress string) (net.Listener, error) {
				listener, err := net.Listen(network, listenAddress)
				if err == nil {
					address <- listener.Addr().String()
				}
				return listener, err
			}
			deps.open = func(context.Context, bootstrap.RepositoryConfig) (*repositoryHandle, error) {
				return &repositoryHandle{repository: conversation.NewMemoryRepository(), check: func(requestContext context.Context) error {
					if calls.Add(1) == 1 {
						return nil
					}
					entered <- requestContext
					defer close(finished)
					select {
					case <-release:
						return nil
					case <-requestContext.Done():
						return requestContext.Err()
					}
				}, close: func() {
					select {
					case <-finished:
					default:
						prematureClose.Store(true)
					}
					closed.Store(true)
				}}, nil
			}
			var output bytes.Buffer
			result := make(chan error, 1)
			go func() { result <- runServer(ctx, validConfig(t), &output, deps) }()
			var endpoint string
			select {
			case endpoint = <-address:
			case <-time.After(2 * time.Second):
				t.Fatal("server never listened")
			}
			client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
			defer client.CloseIdleConnections()
			requestDone := make(chan struct{})
			go func() {
				defer close(requestDone)
				response, err := client.Get("http://" + endpoint + "/readyz")
				if err == nil {
					_, _ = io.Copy(io.Discard, response.Body)
					_ = response.Body.Close()
				}
			}()
			var requestContext context.Context
			select {
			case requestContext = <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("readiness request did not start")
			}
			cancel()
			if !forced {
				if requestContext.Err() != nil {
					t.Fatal("signal canceled accepted request before drain")
				}
				close(release)
			}
			select {
			case err := <-result:
				if forced && (err == nil || !strings.Contains(err.Error(), "drain deadline")) {
					t.Fatalf("expected forced shutdown, got %v", err)
				}
				if !forced && err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("shutdown did not finish")
			}
			<-requestDone
			if !closed.Load() || prematureClose.Load() {
				t.Fatal("repository closed before handlers exited, or not closed")
			}
			if requestContext.Err() == nil {
				t.Fatal("request root was not canceled")
			}
			if !strings.HasPrefix(output.String(), "Web chat server: http://"+endpoint+"\n") {
				t.Fatal("unexpected first startup line")
			}
			for _, private := range validEnvironment() {
				if strings.Contains(output.String(), private) {
					t.Fatal("startup output leaked configuration")
				}
			}
			conn, err := net.DialTimeout("tcp", endpoint, 100*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				t.Fatal("listener survived shutdown")
			}
		})
	}
}

type brokenListener struct{}

func (brokenListener) Accept() (net.Conn, error) { return nil, errors.New("private-listener-error") }
func (brokenListener) Close() error              { return nil }
func (brokenListener) Addr() net.Addr            { return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8093} }

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("private-output-error") }

func TestStartupAndServeFailuresCloseRepository(t *testing.T) {
	for _, stage := range []string{"listen", "output", "serve"} {
		t.Run(stage, func(t *testing.T) {
			closed := 0
			deps := defaultDependencies()
			deps.open = func(context.Context, bootstrap.RepositoryConfig) (*repositoryHandle, error) {
				return &repositoryHandle{repository: conversation.NewMemoryRepository(), check: func(context.Context) error { return nil }, close: func() { closed++ }}, nil
			}
			deps.listen = func(string, string) (net.Listener, error) {
				if stage == "listen" {
					return nil, errors.New("private-listener-address")
				}
				return brokenListener{}, nil
			}
			var output io.Writer = io.Discard
			if stage == "output" {
				output = brokenWriter{}
			}
			err := runServer(context.Background(), validConfig(t), output, deps)
			if err == nil || strings.Contains(err.Error(), "private") || closed != 1 {
				t.Fatalf("unsafe lifecycle failure or unclosed repository: %v", err)
			}
		})
	}
}
