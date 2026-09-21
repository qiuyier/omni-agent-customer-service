package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
)

const (
	startupTimeout   = 10 * time.Second
	drainTimeout     = 5 * time.Second
	readinessTimeout = 2 * time.Second
)

type serverDependencies struct {
	open           func(context.Context, bootstrap.RepositoryConfig) (*repositoryHandle, error)
	listen         func(string, string) (net.Listener, error)
	startupTimeout time.Duration
	drainTimeout   time.Duration
}

func defaultDependencies() serverDependencies {
	return serverDependencies{openRepository, net.Listen, startupTimeout, drainTimeout}
}

func runServer(stopContext context.Context, config serverConfig, output io.Writer, deps serverDependencies) error {
	if err := config.validate(); err != nil {
		return err
	}
	if stopContext == nil || output == nil || deps.open == nil || deps.listen == nil || deps.startupTimeout <= 0 || deps.drainTimeout <= 0 {
		return errors.New("invalid server lifecycle dependency")
	}
	if err := stopContext.Err(); err != nil {
		return errors.New("startup was canceled")
	}
	startupContext, cancelStartup := context.WithTimeout(stopContext, deps.startupTimeout)
	defer cancelStartup()
	repository, err := deps.open(startupContext, config.repositoryConfig())
	if err != nil {
		// Never expose the underlying driver error: it can contain connection
		// details or values sent by an untrusted PostgreSQL endpoint.
		return errors.New("PostgreSQL startup failed; verify DATABASE_URL, connectivity and schema migrations (cmd/migrate); no memory fallback")
	}
	if repository == nil || repository.repository == nil || repository.check == nil || repository.close == nil {
		if repository != nil && repository.close != nil {
			repository.close()
		}
		return errors.New("PostgreSQL repository initialization is incomplete")
	}
	defer repository.close()
	if err := repository.check(startupContext); err != nil || startupContext.Err() != nil {
		return errors.New("PostgreSQL readiness failed during startup; verify connectivity and schema migrations (cmd/migrate)")
	}
	cancelStartup()
	// Knowledge indexing owns a separate startup budget and remains cancelable
	// by the signal context; the PostgreSQL startup context is already canceled.
	gateway, err := newGatewayContext(stopContext, repository.repository, config)
	if err != nil {
		return err
	}
	if stopContext.Err() != nil {
		return errors.New("startup was canceled")
	}
	listener, err := deps.listen("tcp", config.Address)
	if err != nil {
		return errors.New("cannot listen on the loopback web chat address (it may already be in use)")
	}
	defer listener.Close()
	// The signal context initiates draining, not immediate request cancellation.
	// Give accepted requests their grace period before canceling this root.
	requestContext, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	availability := &serverHandler{gateway: gateway, check: repository.check}
	server := &http.Server{
		Handler: availability, BaseContext: func(net.Listener) context.Context { return requestContext },
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: config.Reply.RequestTimeout() + 5*time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10,
		ErrorLog: log.New(io.Discard, "", 0),
	}
	defer server.Close()
	if _, err := fmt.Fprintf(output, "Web chat server: http://%s\nStorage: PostgreSQL. Reply mode: %s; no external business tools or actions.\nTrusted host gateway only: no login, authentication UI, public proxy or /v1 API.\n", listener.Addr().String(), config.ReplyMode); err != nil {
		return errors.New("cannot display the web chat server URL")
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	var serveErr error
	serveStopped := false
	select {
	case <-stopContext.Done():
	case serveErr = <-serveErrors:
		serveStopped = true
	}
	availability.beginDrain()
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), deps.drainTimeout)
	defer cancelShutdown()
	shutdownErr := server.Shutdown(shutdownContext)
	// Close HTTP and cancel all repository work before releasing the pool.
	// Close alone does not wait for handlers, hence the explicit request join.
	cancelRequests()
	_ = server.Close()
	availability.active.Wait()
	if !serveStopped {
		serveErr = <-serveErrors
	}
	if shutdownErr != nil {
		return errors.New("web chat shutdown exceeded its drain deadline; remaining requests were canceled")
	}
	if serveStopped || (serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed)) {
		return errors.New("web chat HTTP server stopped unexpectedly")
	}
	return nil
}

type serverHandler struct {
	gateway  http.Handler
	check    func(context.Context) error
	mu       sync.Mutex
	draining bool
	active   sync.WaitGroup
}

func (h *serverHandler) beginDrain() {
	h.mu.Lock()
	h.draining = true
	h.mu.Unlock()
}

func (h *serverHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	h.mu.Lock()
	if h.draining {
		h.mu.Unlock()
		writeProbe(w, http.StatusServiceUnavailable, "not_ready")
		return
	}
	// Add and the draining transition share a lock. No new repository work can
	// join after shutdown starts waiting for accepted handlers to finish.
	h.active.Add(1)
	h.mu.Unlock()
	defer h.active.Done()
	if r.URL.Path != "/healthz" && r.URL.Path != "/readyz" {
		h.gateway.ServeHTTP(w, r)
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		writeProbe(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeProbe(w, http.StatusMethodNotAllowed, "invalid_request")
		return
	}
	if r.URL.Path == "/readyz" {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()
		if err := h.check(ctx); err != nil || ctx.Err() != nil {
			writeProbe(w, http.StatusServiceUnavailable, "not_ready")
			return
		}
	}
	writeProbe(w, http.StatusOK, "ok")
}

func writeProbe(w http.ResponseWriter, status int, state string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "{\"status\":%q}\n", state)
}
