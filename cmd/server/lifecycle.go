package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
)

var (
	errInvalidLifecycleDependency = errors.New("server: invalid lifecycle dependency")
	errForcedShutdown             = errors.New("server: forced shutdown")
	errServerRunPanic             = errors.New("server: HTTP run panicked")
	errServerClosePanic           = errors.New("server: HTTP close panicked")
)

const forcedRunJoinTimeout = time.Second

type lifecycleServer interface {
	Run() error
	Shutdown(context.Context) error
	Close() error
}

type lifecycleAvailability interface {
	BeginDraining() bool
	MarkStopped()
}

// serveHTTP coordinates Hertz with the process signal lifecycle. The stop
// context only triggers draining; it is deliberately not used as the request
// root or Shutdown context, because it is already cancelled when grace begins.
func serveHTTP(
	stopContext context.Context,
	httpServer lifecycleServer,
	availability lifecycleAvailability,
	shutdownTimeout time.Duration,
	restoreDefaultSignals func(),
	forceCancelRequests context.CancelFunc,
	logger *zap.Logger,
) error {
	if stopContext == nil {
		return fmt.Errorf("%w: stop context is nil", errInvalidLifecycleDependency)
	}
	if httpServer == nil {
		return fmt.Errorf("%w: HTTP server is nil", errInvalidLifecycleDependency)
	}
	if availability == nil {
		return fmt.Errorf("%w: availability is nil", errInvalidLifecycleDependency)
	}
	if shutdownTimeout <= 0 {
		return fmt.Errorf("%w: shutdown timeout must be positive", errInvalidLifecycleDependency)
	}
	if restoreDefaultSignals == nil {
		return fmt.Errorf("%w: signal restore is nil", errInvalidLifecycleDependency)
	}
	if forceCancelRequests == nil {
		return fmt.Errorf("%w: request cancellation is nil", errInvalidLifecycleDependency)
	}
	if logger == nil {
		return fmt.Errorf("%w: logger is nil", errInvalidLifecycleDependency)
	}

	runErrors := make(chan error, 1)
	go runHTTPServer(httpServer, runErrors)

	select {
	case runErr := <-runErrors:
		availability.MarkStopped()
		forceCancelRequests()
		closeContext, cancelClose := context.WithTimeout(context.Background(), forcedRunJoinTimeout)
		defer cancelClose()
		closeAttemptFailed, closeAttemptTimedOut := closeHTTPServerWithin(closeContext, httpServer)
		logger.Error(
			"HTTP server stopped before a shutdown signal",
			zap.String("event", "server.stopped.unexpectedly"),
			zap.Bool("run_failed", runErr != nil),
			zap.Bool("close_attempt_failed", closeAttemptFailed),
			zap.Bool("close_attempt_timed_out", closeAttemptTimedOut),
		)
		if runErr != nil {
			return fmt.Errorf("%w: run HTTP server: %w", errForcedShutdown, runErr)
		}
		return fmt.Errorf("%w: HTTP server stopped unexpectedly", errForcedShutdown)

	case <-stopContext.Done():
		// signal.NotifyContext otherwise keeps intercepting later signals. Stop
		// it now so a second SIGINT/SIGTERM regains the operating system's
		// default immediate-termination behavior.
		restoreDefaultSignals()
	}

	availability.BeginDraining()
	defer availability.MarkStopped()
	startedAt := time.Now()
	// Start the budget before logging so time spent writing the started event
	// is deducted from the grace period once Shutdown begins.
	shutdownContext, cancelShutdown := context.WithTimeout(
		context.Background(),
		shutdownTimeout,
	)
	defer cancelShutdown()
	logger.Info(
		"HTTP server graceful shutdown started",
		zap.String("event", "server.shutdown.started"),
		zap.Duration("shutdown_timeout", shutdownTimeout),
	)

	shutdownErr := httpServer.Shutdown(shutdownContext)
	// Hertz v0.10.5 can return nil when its transport returned ctx.Err(). The
	// context therefore remains the authoritative timeout signal.
	if contextErr := shutdownContext.Err(); contextErr != nil {
		return forceHTTPShutdown(
			httpServer,
			forceCancelRequests,
			logger,
			startedAt,
			runErrors,
			false,
			"timeout",
			contextErr,
		)
	}
	if shutdownErr != nil {
		return forceHTTPShutdown(
			httpServer,
			forceCancelRequests,
			logger,
			startedAt,
			runErrors,
			false,
			"failed",
			shutdownErr,
		)
	}

	// Shutdown waits for active Hertz connections; joining Run also guarantees
	// the listener goroutine has returned before callers close shared resources.
	select {
	case runErr := <-runErrors:
		if contextErr := shutdownContext.Err(); contextErr != nil {
			return forceHTTPShutdown(
				httpServer,
				forceCancelRequests,
				logger,
				startedAt,
				runErrors,
				true,
				"timeout",
				contextErr,
			)
		}
		if runErr != nil {
			logger.Error(
				"HTTP server run failed during graceful shutdown",
				zap.String("event", "server.shutdown.failed"),
				zap.Float64("duration_ms", durationMillisecondsSince(startedAt)),
			)
			return fmt.Errorf("run HTTP server during graceful shutdown: %w", runErr)
		}

		logger.Info(
			"HTTP server graceful shutdown completed",
			zap.String("event", "server.shutdown.completed"),
			zap.Float64("duration_ms", durationMillisecondsSince(startedAt)),
		)
		return nil

	case <-shutdownContext.Done():
		return forceHTTPShutdown(
			httpServer,
			forceCancelRequests,
			logger,
			startedAt,
			runErrors,
			false,
			"timeout",
			shutdownContext.Err(),
		)
	}
}

func runHTTPServer(httpServer lifecycleServer, runErrors chan<- error) {
	defer func() {
		if recover() != nil {
			runErrors <- errServerRunPanic
		}
	}()
	runErrors <- httpServer.Run()
}

func forceHTTPShutdown(
	httpServer lifecycleServer,
	forceCancelRequests context.CancelFunc,
	logger *zap.Logger,
	startedAt time.Time,
	runErrors <-chan error,
	runAlreadyStopped bool,
	outcome string,
	cause error,
) error {
	return forceHTTPShutdownWithin(
		httpServer,
		forceCancelRequests,
		logger,
		startedAt,
		runErrors,
		runAlreadyStopped,
		forcedRunJoinTimeout,
		outcome,
		cause,
	)
}

func forceHTTPShutdownWithin(
	httpServer lifecycleServer,
	forceCancelRequests context.CancelFunc,
	logger *zap.Logger,
	startedAt time.Time,
	runErrors <-chan error,
	runAlreadyStopped bool,
	hardStopTimeout time.Duration,
	outcome string,
	cause error,
) error {
	forceCancelRequests()
	hardStopContext, cancelHardStop := context.WithTimeout(
		context.Background(),
		hardStopTimeout,
	)
	defer cancelHardStop()

	closeAttemptFailed, closeAttemptTimedOut := closeHTTPServerWithin(hardStopContext, httpServer)

	runStopped := runAlreadyStopped
	if !runStopped {
		select {
		case <-runErrors:
			runStopped = true
		case <-hardStopContext.Done():
		}
	}
	event := "server.shutdown.failed"
	if outcome == "timeout" {
		event = "server.shutdown.timeout"
	}
	logger.Error(
		"HTTP server graceful shutdown did not complete",
		zap.String("event", event),
		zap.String("outcome", outcome),
		zap.Float64("duration_ms", durationMillisecondsSince(startedAt)),
		zap.Bool("close_attempt_failed", closeAttemptFailed),
		zap.Bool("close_attempt_timed_out", closeAttemptTimedOut),
		zap.Bool("run_stopped", runStopped),
	)
	return fmt.Errorf("%w: %s: %w", errForcedShutdown, outcome, cause)
}

// Close has no context argument. A buffered result lets process cleanup stop
// waiting at its deadline even if the transport does not finish closing.
func closeHTTPServerWithin(ctx context.Context, httpServer lifecycleServer) (failed, timedOut bool) {
	closeResults := make(chan error, 1)
	go closeHTTPServer(httpServer, closeResults)
	select {
	case closeErr := <-closeResults:
		return closeErr != nil, false
	case <-ctx.Done():
		return false, true
	}
}

func closeHTTPServer(httpServer lifecycleServer, closeResults chan<- error) {
	defer func() {
		if recover() != nil {
			closeResults <- errServerClosePanic
		}
	}()
	closeResults <- httpServer.Close()
}

func durationMillisecondsSince(startedAt time.Time) float64 {
	duration := time.Since(startedAt)
	if duration < 0 {
		duration = 0
	}
	return float64(duration.Nanoseconds()) / 1e6
}
