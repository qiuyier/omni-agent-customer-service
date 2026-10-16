package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type childProcess struct {
	command *exec.Cmd
	done    chan struct{}
	waitErr error // Published by closing done, only read after done.
	origin  string
	result  processResult
}

func childEnvironment(opts options, source, secret string, demo bool) []string {
	// Do not inherit HOME, proxies, AI keys, PG*, service tokens or .env settings.
	values := []string{"PATH=/usr/bin:/bin", "LANG=C", "TZ=UTC"}
	if demo {
		return append(values, "WEBCHAT_DEMO_DATABASE_URL="+opts.databaseURL)
	}
	return append(values, "WEBCHAT_ADDRESS=127.0.0.1:0", "WEBCHAT_HOST_ID="+source,
		"WEBCHAT_HOST_SECRET="+secret, "DATABASE_URL="+opts.databaseURL,
		"STORAGE_DRIVER=postgres", "WEBCHAT_REPLY_MODE=fake")
}

func (h *harness) start(role, source, secret string, demo bool, multi ...string) (*childProcess, error) {
	if len(multi) != 0 && (len(multi) != 2 || demo) {
		return nil, errors.New("invalid multi-host child configuration")
	}
	binary, prefix := h.options.serverBinary, "Web chat server: "
	var args []string
	if demo {
		binary, prefix = h.options.demoBinary, "Web chat demo: "
		args = []string{"-address", "127.0.0.1:0", "-storage", "postgres"}
	}
	command := exec.Command(binary, args...)
	command.Dir = h.options.outputDir
	command.Env = childEnvironment(h.options, source, secret, demo)
	if len(multi) == 2 {
		command.Env = multiChildEnvironment(h.options, multi[0], multi[1])
	}
	command.Stderr = io.Discard // Never print or persist child diagnostics.
	command.WaitDelay = time.Second
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, errors.New("cannot initialize child output")
	}
	if err := command.Start(); err != nil {
		_ = stdout.Close()
		return nil, errors.New("cannot start acceptance child")
	}
	child := &childProcess{command: command, done: make(chan struct{}), result: processResult{Role: role, PID: command.Process.Pid}}
	h.children = append(h.children, child)
	go func() { child.waitErr = command.Wait(); close(child.done) }()
	firstLine := make(chan string, 1)
	go func() {
		defer stdout.Close()
		reader := bufio.NewReaderSize(io.LimitReader(stdout, 4097), 4096)
		line, err := reader.ReadString('\n')
		if err == nil && len(line) <= 4096 {
			firstLine <- line
		} else {
			firstLine <- ""
		}
		_, _ = io.Copy(io.Discard, stdout)
	}()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case line := <-firstLine:
		origin, err := startupOrigin(line, prefix)
		if err != nil {
			return nil, err
		}
		child.origin = origin
		select {
		case <-child.done:
			return nil, errors.New("child exited during startup")
		default:
		}
		return child, nil
	case <-child.done:
		return nil, errors.New("child exited before startup")
	case <-h.ctx.Done():
		return nil, errors.New("child startup was canceled")
	case <-timer.C:
		return nil, errors.New("child startup exceeded its deadline")
	}
}

func startupOrigin(line, prefix string) (string, error) {
	invalid := errors.New("child did not report a valid loopback startup URL")
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, "\n") {
		return "", invalid
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(line, prefix), "\n")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", invalid
	}
	port, err := strconv.Atoi(u.Port())
	if (u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") || err != nil || port < 1 || port > 65535 {
		return "", invalid
	}
	return u.String(), nil
}

func (child *childProcess) stop(crash bool) error {
	select {
	case <-child.done:
		child.result.Waited = true
		child.result.Stop = "unexpected_exit"
		return errors.New("child exited before the requested stop")
	default:
	}
	var err error
	if crash {
		child.result.Stop = "kill"
		err = child.command.Process.Kill()
	} else {
		child.result.Stop = "terminate"
		err = child.command.Process.Signal(syscall.SIGTERM)
	}
	if err != nil {
		return errors.New("cannot signal owned child process")
	}
	select {
	case <-child.done:
		child.result.Waited = true
		if !crash && child.waitErr == nil {
			return nil
		}
		if crash {
			var exit *exec.ExitError
			if errors.As(child.waitErr, &exit) {
				if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() && status.Signal() == syscall.SIGKILL {
					return nil
				}
			}
		}
		return errors.New("child exit did not match the requested stop")
	case <-time.After(8 * time.Second):
		return errors.New("child did not exit within its stop deadline")
	}
}

func (h *harness) cleanup() error {
	var failed bool
	for _, child := range h.children {
		select {
		case <-child.done:
			child.result.Waited = true
			if child.result.Stop == "" {
				child.result.Stop = "unexpected_exit"
				failed = true
			}
			continue
		default:
		}
		child.result.Stop = "cleanup_kill"
		if child.command.Process.Kill() != nil {
			failed = true
		}
		select {
		case <-child.done:
			child.result.Waited = true
		case <-time.After(3 * time.Second):
			failed = true
		}
	}
	if failed {
		return errors.New("not all owned children exited cleanly")
	}
	return nil
}

// Kept separate so lifecycle tests can provide an already canceled context.
func checkContext(ctx context.Context) error {
	if ctx == nil || ctx.Err() != nil {
		return errors.New("acceptance was canceled")
	}
	return nil
}
