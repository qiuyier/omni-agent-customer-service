package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

func TestValidateDemoAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8092", "127.0.0.2:0", "[::1]:8092"} {
		if err := validateDemoAddress(address); err != nil {
			t.Errorf("%s: %v", address, err)
		}
	}
	for _, address := range []string{"", ":8092", "0.0.0.0:8092", "[::]:8092", "192.168.1.2:8092", "example.invalid:8092", "localhost:8092", "127.0.0.1:invalid", "127.0.0.1:-1", "127.0.0.1:65536"} {
		if err := validateDemoAddress(address); err == nil {
			t.Errorf("accepted unsafe address %q", address)
		}
		if err := runDemo(context.Background(), address, io.Discard); err == nil {
			t.Errorf("run accepted unsafe address %q", address)
		}
	}
}

type startupWriter chan string

func (w startupWriter) Write(data []byte) (int, error) { w <- string(data); return len(data), nil }

func TestDemoLifecycleAndPublicBoundary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := make(startupWriter, 1)
	done := make(chan error, 1)
	go func() { done <- runDemo(ctx, "127.0.0.1:0", output) }()
	var startup string
	select {
	case startup = <-output:
	case err := <-done:
		t.Fatalf("demo exited before startup: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("demo did not start")
	}
	lines := strings.Split(startup, "\n")
	if !strings.HasPrefix(lines[0], "Web chat demo: http://127.0.0.1:") || strings.Count(startup, "http://") != 1 || !strings.Contains(startup, "lost on restart") {
		t.Fatalf("unsafe/incomplete startup output: %q", startup)
	}
	origin := strings.TrimPrefix(lines[0], "Web chat demo: ")
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	for path, want := range map[string]int{"/": http.StatusOK, "/host/me": http.StatusOK, "/internal/chat/sessions": http.StatusNotFound, "/v1/conversations": http.StatusNotFound} {
		response, err := client.Get(origin + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != want {
			t.Errorf("GET %s = %d want %d", path, response.StatusCode, want)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("demo did not stop")
	}
	if response, err := client.Get(origin + "/host/me"); err == nil {
		response.Body.Close()
		t.Fatal("public listener still open after shutdown")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("output unavailable") }

func TestDemoStartupFailureClosesListeners(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	if err := runDemo(context.Background(), address, failingWriter{}); err == nil {
		t.Fatal("expected output failure")
	}
	reopened, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("public listener leaked on startup error: %v", err)
	}
	reopened.Close()
}

func TestDemoReplyIsHonestAndOffline(t *testing.T) {
	reply, err := demoReply(context.Background(), customerservice.ReplyRequest{})
	if err != nil || !strings.Contains(reply.Content, "演示回复") || !strings.Contains(reply.Content, "不会执行任何外部操作") {
		t.Fatalf("misleading reply: %+v %v", reply, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := demoReply(ctx, customerservice.ReplyRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ignored cancellation: %v", err)
	}
}
