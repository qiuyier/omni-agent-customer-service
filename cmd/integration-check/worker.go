package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/integration"
)

type workerConfig struct {
	Directory   string
	Source      string
	APIURL      string
	SinkURL     string
	Token       string
	MaxAttempts int
}
type workerResult struct {
	PID        int                    `json:"pid"`
	Inspection integration.Inspection `json:"inspection"`
}
type workerProgram struct {
	Path string
	Args []string
}

func event() integration.Event {
	return integration.Event{ConversationID: "42", CustomerID: "7", Channel: "web", ExternalMessageID: "event-1", Content: "帮助中心在哪里？"}
}
func (cfg workerConfig) adapterConfig() integration.Config {
	return integration.Config{Directory: cfg.Directory, SourceID: cfg.Source, DestinationID: "local-fake-channel", MaxAttempts: cfg.MaxAttempts, AttemptTimeout: 10 * time.Second, RetryDelay: 10 * time.Millisecond}
}
func localURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" || u.Port() == "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}
func openWorker(cfg workerConfig) (*integration.Adapter, func(), error) {
	if !localURL(cfg.APIURL) || !localURL(cfg.SinkURL) || cfg.Token == "" {
		return nil, nil, errors.New("worker requires explicit local HTTP endpoints")
	}
	client, err := integration.NewHTTPClient(integration.HTTPConfig{BaseURL: cfg.APIURL, BearerToken: cfg.Token, AllowInsecureHTTP: true})
	if err != nil {
		return nil, nil, err
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	httpClient := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	sink := integration.DelivererFunc(func(ctx context.Context, d integration.Delivery) error {
		body, err := json.Marshal(d)
		if err != nil {
			return &integration.CallError{Code: integration.FailureInvalidRequest}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.SinkURL+"/deliver", bytes.NewReader(body))
		if err != nil {
			return &integration.CallError{Code: integration.FailureInvalidRequest}
		}
		req.Header.Set("Authorization", "Bearer "+cfg.Token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		if err != nil {
			return &integration.CallError{Code: integration.FailureTransport, Retryable: true}
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNoContent {
			return nil
		}
		if resp.StatusCode == http.StatusServiceUnavailable {
			return &integration.CallError{Code: integration.FailureUnavailable, Retryable: true}
		}
		return &integration.CallError{Code: integration.FailureInvalidResponse}
	})
	a, err := integration.Open(cfg.adapterConfig(), client, sink)
	closeClients := func() { client.Close(); transport.CloseIdleConnections() }
	if err != nil {
		closeClients()
		return nil, nil, err
	}
	return a, closeClients, nil
}
func processWorker(ctx context.Context, cfg workerConfig) (workerResult, error) {
	a, closeClients, err := openWorker(cfg)
	if err != nil {
		return workerResult{}, err
	}
	defer closeClients()
	defer a.Close()
	j, err := a.Accept(ctx, event())
	if err != nil {
		return workerResult{}, err
	}
	for step := 0; step < 20; step++ {
		if j.State == integration.Completed || j.State == integration.Stopped || j.State == integration.NeedsAttention {
			view, err := a.Inspect(ctx, j.ID)
			return workerResult{PID: os.Getpid(), Inspection: view}, err
		}
		if delay := time.Until(j.NextAttemptAt); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return workerResult{}, ctx.Err()
			case <-timer.C:
			}
		}
		j, err = a.Advance(ctx, j.ID)
		if err != nil && !errors.Is(err, integration.ErrAttemptFailed) && !errors.Is(err, integration.ErrNeedsAttention) && !errors.Is(err, integration.ErrNotReady) {
			return workerResult{}, err
		}
	}
	return workerResult{}, errors.New("worker stage budget exceeded")
}

type child struct {
	cmd            *exec.Cmd
	stdout, stderr bytes.Buffer
	done           chan error
	pid            int
	finished       bool
}

func startChild(ctx context.Context, program workerProgram, cfg workerConfig) (*child, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	c := &child{cmd: exec.CommandContext(ctx, program.Path, program.Args...), done: make(chan error, 1)}
	c.cmd.Env = []string{"INTEGRATION_ACCEPTANCE_WORKER=1"} // no inherited provider keys/proxies
	c.cmd.Stdin = bytes.NewReader(data)
	c.cmd.Stdout = &c.stdout
	c.cmd.Stderr = &c.stderr
	c.cmd.WaitDelay = time.Second
	if err := c.cmd.Start(); err != nil {
		return nil, err
	}
	c.pid = c.cmd.Process.Pid
	go func() { c.done <- c.cmd.Wait() }()
	return c, nil
}
func (c *child) stop() {
	if !c.finished {
		_ = c.cmd.Process.Kill()
		<-c.done
		c.finished = true
	}
}
func (c *child) result(ctx context.Context) (workerResult, error) {
	select {
	case <-ctx.Done():
		c.stop()
		return workerResult{}, ctx.Err()
	case err := <-c.done:
		c.finished = true
		if err != nil {
			return workerResult{}, fmt.Errorf("worker exited unsuccessfully: %s", c.stderr.String())
		}
		var result workerResult
		if err := json.Unmarshal(c.stdout.Bytes(), &result); err != nil {
			return workerResult{}, errors.New("invalid worker result")
		}
		if result.PID != c.pid {
			return workerResult{}, errors.New("worker PID mismatch")
		}
		return result, nil
	}
}
