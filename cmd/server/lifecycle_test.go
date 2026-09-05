package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	zapobserver "go.uber.org/zap/zaptest/observer"
)

func TestServeHTTPReturnsUnexpectedRunFailureAndForcesCleanup(t *testing.T) {
	runErr := errors.New("run failed")
	server := newFakeLifecycleServer(t)
	server.releaseRun(runErr)
	availability := &fakeLifecycleAvailability{}
	var forceCalls atomic.Int64

	err := serveHTTP(
		context.Background(),
		server,
		availability,
		time.Second,
		func() {},
		func() { forceCalls.Add(1) },
		zap.NewNop(),
	)

	if !errors.Is(err, errForcedShutdown) || !errors.Is(err, runErr) {
		t.Fatalf("serveHTTP() error = %v, want forced shutdown wrapping run error", err)
	}
	if availability.beginCalls.Load() != 0 {
		t.Fatalf("BeginDraining calls = %d, want 0", availability.beginCalls.Load())
	}
	if availability.stopCalls.Load() != 1 {
		t.Fatalf("MarkStopped calls = %d, want 1", availability.stopCalls.Load())
	}
	if server.shutdownCalls.Load() != 0 {
		t.Fatalf("Shutdown calls = %d, want 0", server.shutdownCalls.Load())
	}
	if server.closeCalls.Load() != 1 || forceCalls.Load() != 1 {
		t.Fatalf(
			"cleanup calls = close:%d force:%d, want 1,1",
			server.closeCalls.Load(),
			forceCalls.Load(),
		)
	}
}

func TestServeHTTPUnexpectedExitHandlesRunAndClosePanicsSafely(t *testing.T) {
	server := newFakeLifecycleServer(t)
	server.runPanic = true
	server.close = func() error { panic("private transport details") }
	availability := &fakeLifecycleAvailability{}
	logger, logs := newLifecycleTestLogger()
	var forceCalls atomic.Int64
	result := make(chan error, 1)
	go func() {
		result <- serveHTTP(
			context.Background(), server, availability, time.Second,
			func() {}, func() { forceCalls.Add(1) }, logger,
		)
	}()
	err := awaitValue(t, result, "unexpected Run and Close panic cleanup")
	if !errors.Is(err, errForcedShutdown) || !errors.Is(err, errServerRunPanic) {
		t.Fatalf("serveHTTP() error = %v, want forced Run panic error", err)
	}
	if forceCalls.Load() != 1 || availability.stopCalls.Load() != 1 {
		t.Fatal("unexpected exit did not cancel requests and mark stopped exactly once")
	}
	assertLifecycleEvents(t, logs, "server.stopped.unexpectedly")
	fields := logs.All()[0].ContextMap()
	if fields["close_attempt_failed"] != true || fields["close_attempt_timed_out"] != false {
		t.Fatalf("Close panic cleanup fields = %#v", fields)
	}
	// The lifecycle emits bounded classifications rather than panic contents.
	if fields["run_failed"] != true || len(fields) != 4 {
		t.Fatalf("unexpected lifecycle log fields = %#v", fields)
	}
}

func TestServeHTTPGracefullyDrainsAndWaitsForRun(t *testing.T) {
	server := newFakeLifecycleServer(t)
	availability := &fakeLifecycleAvailability{}
	shutdownCalled := make(chan struct{})
	server.shutdown = func(ctx context.Context) error {
		if !availability.draining.Load() {
			t.Error("Shutdown called before BeginDraining")
		}
		if ctx.Err() != nil {
			t.Errorf("Shutdown context is already cancelled: %v", ctx.Err())
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("Shutdown context has no deadline")
		}
		close(shutdownCalled)
		return nil
	}
	stopContext, stop := context.WithCancel(context.Background())
	var forceCalls atomic.Int64
	var restoreSignalCalls atomic.Int64
	logger, logs := newLifecycleTestLogger()
	result := make(chan error, 1)
	go func() {
		result <- serveHTTP(
			stopContext,
			server,
			availability,
			time.Second,
			func() { restoreSignalCalls.Add(1) },
			func() { forceCalls.Add(1) },
			logger,
		)
	}()

	awaitSignal(t, server.runStarted, "HTTP server Run start")
	stop()
	awaitSignal(t, shutdownCalled, "Shutdown call")
	select {
	case err := <-result:
		t.Fatalf("serveHTTP returned before Run ended: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	server.releaseRun(nil)
	if err := awaitValue(t, result, "serveHTTP result"); err != nil {
		t.Fatalf("serveHTTP() error = %v", err)
	}
	if availability.beginCalls.Load() != 1 || availability.stopCalls.Load() != 1 {
		t.Fatalf(
			"availability calls = begin:%d stop:%d, want 1,1",
			availability.beginCalls.Load(),
			availability.stopCalls.Load(),
		)
	}
	if server.shutdownCalls.Load() != 1 || server.closeCalls.Load() != 0 {
		t.Fatalf(
			"server calls = shutdown:%d close:%d, want 1,0",
			server.shutdownCalls.Load(),
			server.closeCalls.Load(),
		)
	}
	if forceCalls.Load() != 0 {
		t.Fatalf("force-cancel calls = %d, want 0", forceCalls.Load())
	}
	if restoreSignalCalls.Load() != 1 {
		t.Fatalf("restore-signal calls = %d, want 1", restoreSignalCalls.Load())
	}
	assertLifecycleEvents(t, logs, "server.shutdown.started", "server.shutdown.completed")
}

func TestServeHTTPDetectsHertzStyleSilentShutdownTimeout(t *testing.T) {
	server := newFakeLifecycleServer(t)
	server.shutdown = func(ctx context.Context) error {
		<-ctx.Done()
		// Hertz v0.10.5 converts a transport ctx.Err() into nil.
		return nil
	}
	server.close = func() error {
		server.releaseRun(nil)
		return nil
	}
	availability := &fakeLifecycleAvailability{}
	stopContext, stop := context.WithCancel(context.Background())
	var forceCalls atomic.Int64
	logger, logs := newLifecycleTestLogger()
	result := make(chan error, 1)
	go func() {
		result <- serveHTTP(
			stopContext,
			server,
			availability,
			10*time.Millisecond,
			func() {},
			func() { forceCalls.Add(1) },
			logger,
		)
	}()

	awaitSignal(t, server.runStarted, "HTTP server Run start")
	stop()
	err := awaitValue(t, result, "serveHTTP timeout result")
	if !errors.Is(err, errForcedShutdown) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("serveHTTP() error = %v, want forced deadline error", err)
	}
	if forceCalls.Load() != 1 || server.closeCalls.Load() != 1 {
		t.Fatalf(
			"forced cleanup calls = cancel:%d close:%d, want 1,1",
			forceCalls.Load(),
			server.closeCalls.Load(),
		)
	}
	if availability.stopCalls.Load() != 1 {
		t.Fatalf("MarkStopped calls = %d, want 1", availability.stopCalls.Load())
	}
	assertLifecycleEvents(t, logs, "server.shutdown.started", "server.shutdown.timeout")
}

func TestServeHTTPForcesWhenRunOutlivesGracePeriod(t *testing.T) {
	server := newFakeLifecycleServer(t)
	server.shutdown = func(context.Context) error { return nil }
	server.close = func() error {
		server.releaseRun(nil)
		return nil
	}
	availability := &fakeLifecycleAvailability{}
	stopContext, stop := context.WithCancel(context.Background())
	var forceCalls atomic.Int64
	logger, logs := newLifecycleTestLogger()
	result := make(chan error, 1)
	go func() {
		result <- serveHTTP(
			stopContext,
			server,
			availability,
			10*time.Millisecond,
			func() {},
			func() { forceCalls.Add(1) },
			logger,
		)
	}()

	awaitSignal(t, server.runStarted, "HTTP server Run start")
	stop()
	err := awaitValue(t, result, "serveHTTP timeout result")
	if !errors.Is(err, errForcedShutdown) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("serveHTTP() error = %v, want forced deadline error", err)
	}
	if server.shutdownCalls.Load() != 1 || server.closeCalls.Load() != 1 {
		t.Fatalf(
			"server calls = shutdown:%d close:%d, want 1,1",
			server.shutdownCalls.Load(),
			server.closeCalls.Load(),
		)
	}
	if forceCalls.Load() != 1 {
		t.Fatalf("force-cancel calls = %d, want 1", forceCalls.Load())
	}
	assertLifecycleEvents(t, logs, "server.shutdown.started", "server.shutdown.timeout")
}

func TestServeHTTPForcesCloseAfterShutdownFailure(t *testing.T) {
	shutdownErr := errors.New("deregister failed")
	closeErr := errors.New("close failed")
	server := newFakeLifecycleServer(t)
	var forceCalls atomic.Int64
	server.shutdown = func(context.Context) error { return shutdownErr }
	server.close = func() error {
		if forceCalls.Load() != 1 {
			t.Error("Close called before request force-cancel")
		}
		server.releaseRun(nil)
		return closeErr
	}
	availability := &fakeLifecycleAvailability{}
	stopContext, stop := context.WithCancel(context.Background())
	logger, logs := newLifecycleTestLogger()
	result := make(chan error, 1)
	go func() {
		result <- serveHTTP(
			stopContext,
			server,
			availability,
			time.Second,
			func() {},
			func() { forceCalls.Add(1) },
			logger,
		)
	}()

	awaitSignal(t, server.runStarted, "HTTP server Run start")
	stop()
	err := awaitValue(t, result, "serveHTTP failure result")
	if !errors.Is(err, errForcedShutdown) || !errors.Is(err, shutdownErr) {
		t.Fatalf("serveHTTP() error = %v, want forced shutdown wrapping failure", err)
	}
	if forceCalls.Load() != 1 || server.closeCalls.Load() != 1 {
		t.Fatalf(
			"forced cleanup calls = cancel:%d close:%d, want 1,1",
			forceCalls.Load(),
			server.closeCalls.Load(),
		)
	}
	if availability.stopCalls.Load() != 1 {
		t.Fatalf("MarkStopped calls = %d, want 1", availability.stopCalls.Load())
	}
	assertLifecycleEvents(t, logs, "server.shutdown.started", "server.shutdown.failed")
	fields := logs.All()[1].ContextMap()
	if closeFailed, _ := fields["close_attempt_failed"].(bool); !closeFailed {
		t.Fatalf("close_attempt_failed = %#v, want true", fields["close_attempt_failed"])
	}
	if runStopped, _ := fields["run_stopped"].(bool); !runStopped {
		t.Fatalf("run_stopped = %#v, want true", fields["run_stopped"])
	}
}

func TestForceHTTPShutdownBoundsCloseAndRunJoin(t *testing.T) {
	server := newFakeLifecycleServer(t)
	closeBlocked := make(chan struct{})
	closeExited := make(chan struct{})
	var releaseClose sync.Once
	t.Cleanup(func() {
		releaseClose.Do(func() { close(closeBlocked) })
		select {
		case <-closeExited:
		case <-time.After(time.Second):
			t.Error("bounded Close goroutine did not exit during cleanup")
		}
	})
	var forceCalled atomic.Bool
	server.close = func() error {
		defer close(closeExited)
		if !forceCalled.Load() {
			t.Error("Close called before request force-cancel")
		}
		<-closeBlocked
		return nil
	}
	logger, logs := newLifecycleTestLogger()
	startedAt := time.Now()

	err := forceHTTPShutdownWithin(
		server,
		func() { forceCalled.Store(true) },
		logger,
		startedAt,
		make(chan error),
		false,
		10*time.Millisecond,
		"timeout",
		context.DeadlineExceeded,
	)

	if !errors.Is(err, errForcedShutdown) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("forceHTTPShutdownWithin() error = %v, want forced deadline error", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("forced shutdown elapsed = %v, want bounded completion", elapsed)
	}
	assertLifecycleEvents(t, logs, "server.shutdown.timeout")
	fields := logs.All()[0].ContextMap()
	if timedOut, _ := fields["close_attempt_timed_out"].(bool); !timedOut {
		t.Fatalf("close_attempt_timed_out = %#v, want true", fields["close_attempt_timed_out"])
	}
	if runStopped, _ := fields["run_stopped"].(bool); runStopped {
		t.Fatalf("run_stopped = %#v, want false", fields["run_stopped"])
	}
}

func TestRunHTTPServerConvertsPanicToBoundedError(t *testing.T) {
	server := newFakeLifecycleServer(t)
	server.runPanic = true
	runErrors := make(chan error, 1)

	go runHTTPServer(server, runErrors)

	if err := awaitValue(t, runErrors, "Run panic result"); !errors.Is(err, errServerRunPanic) {
		t.Fatalf("runHTTPServer() error = %v, want %v", err, errServerRunPanic)
	}
}

func TestHertzServerForcedShutdownCancelsActiveRequestContext(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	requestRootContext, cancelRequests := context.WithCancel(context.Background())
	stopContext, stop := context.WithCancel(context.Background())
	httpServer := newHertzServer(listener, requestRootContext, 30*time.Millisecond)
	t.Cleanup(func() {
		stop()
		cancelRequests()
		_ = httpServer.Close()
		_ = listener.Close()
	})

	requestEntered := make(chan struct{})
	requestContextErrors := make(chan error, 1)
	httpServer.GET("/block", func(ctx context.Context, requestContext *app.RequestContext) {
		close(requestEntered)
		<-ctx.Done()
		requestContextErrors <- ctx.Err()
		requestContext.AbortWithStatus(http.StatusServiceUnavailable)
	})

	availability := &fakeLifecycleAvailability{}
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- serveHTTP(
			stopContext,
			httpServer,
			availability,
			30*time.Millisecond,
			func() {},
			cancelRequests,
			zap.NewNop(),
		)
	}()

	transport := &http.Transport{Proxy: nil}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{
		Timeout:   time.Second,
		Transport: transport,
	}
	requestResult := make(chan error, 1)
	go func() {
		response, requestErr := client.Get("http://" + listener.Addr().String() + "/block")
		if response != nil {
			_ = response.Body.Close()
		}
		requestResult <- requestErr
	}()

	select {
	case <-requestEntered:
	case <-time.After(time.Second):
		t.Fatal("request did not reach Hertz handler")
	}
	stop()

	select {
	case contextErr := <-requestContextErrors:
		if !errors.Is(contextErr, context.Canceled) {
			t.Fatalf("request context error = %v, want context.Canceled", contextErr)
		}
	case <-time.After(time.Second):
		t.Fatal("forced shutdown did not cancel active request context")
	}
	select {
	case serveErr := <-serveResult:
		if !errors.Is(serveErr, errForcedShutdown) ||
			!errors.Is(serveErr, context.DeadlineExceeded) {
			t.Fatalf("serveHTTP() error = %v, want forced deadline error", serveErr)
		}
	case <-time.After(time.Second):
		t.Fatal("serveHTTP did not finish after forced request cancellation")
	}
	select {
	case <-requestResult:
	case <-time.After(time.Second):
		t.Fatal("HTTP client did not finish after forced shutdown")
	}
}

type fakeLifecycleServer struct {
	runStarted    chan struct{}
	runResults    chan error
	runPanic      bool
	shutdown      func(context.Context) error
	close         func() error
	shutdownCalls atomic.Int64
	closeCalls    atomic.Int64
	runWasStarted atomic.Bool
	startOnce     sync.Once
	releaseOnce   sync.Once
	exitOnce      sync.Once
	runExited     chan struct{}
}

func newFakeLifecycleServer(t *testing.T) *fakeLifecycleServer {
	t.Helper()
	server := &fakeLifecycleServer{
		runStarted: make(chan struct{}),
		runResults: make(chan error, 1),
		runExited:  make(chan struct{}),
	}
	t.Cleanup(func() {
		server.releaseRun(nil)
		if server.runWasStarted.Load() {
			select {
			case <-server.runExited:
			case <-time.After(time.Second):
				t.Errorf("fake HTTP server Run did not exit during cleanup")
			}
		}
	})
	return server
}

func (server *fakeLifecycleServer) Run() error {
	server.runWasStarted.Store(true)
	server.startOnce.Do(func() { close(server.runStarted) })
	defer server.exitOnce.Do(func() { close(server.runExited) })
	if server.runPanic {
		panic("sensitive panic detail")
	}
	return <-server.runResults
}

func (server *fakeLifecycleServer) releaseRun(err error) {
	server.releaseOnce.Do(func() { server.runResults <- err })
}

func (server *fakeLifecycleServer) Shutdown(ctx context.Context) error {
	server.shutdownCalls.Add(1)
	if server.shutdown != nil {
		return server.shutdown(ctx)
	}
	return nil
}

func (server *fakeLifecycleServer) Close() error {
	server.closeCalls.Add(1)
	if server.close != nil {
		return server.close()
	}
	return nil
}

type fakeLifecycleAvailability struct {
	draining   atomic.Bool
	beginCalls atomic.Int64
	stopCalls  atomic.Int64
}

func (availability *fakeLifecycleAvailability) BeginDraining() bool {
	availability.beginCalls.Add(1)
	return availability.draining.CompareAndSwap(false, true)
}

func (availability *fakeLifecycleAvailability) MarkStopped() {
	availability.stopCalls.Add(1)
}

func newLifecycleTestLogger() (*zap.Logger, *zapobserver.ObservedLogs) {
	core, logs := zapobserver.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

func assertLifecycleEvents(
	t *testing.T,
	logs *zapobserver.ObservedLogs,
	want ...string,
) {
	t.Helper()
	entries := logs.All()
	if len(entries) != len(want) {
		t.Fatalf("log entries = %d, want %d: %#v", len(entries), len(want), entries)
	}
	for index, entry := range entries {
		if event, ok := entry.ContextMap()["event"].(string); !ok || event != want[index] {
			t.Fatalf("log %d event = %#v, want %q", index, event, want[index])
		}
	}
}

func awaitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func awaitValue[T any](t *testing.T, values <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}
