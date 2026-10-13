// Test-only two-source assembly; not a production multi-tenant server.
package main

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/webchat"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "cross-system fixture failed")
		os.Exit(1)
	}
}
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	repo := conversation.NewMemoryRepository()
	service, err := customerservice.New(repo, customerservice.MessageIDGeneratorFunc(func() (string, error) { return uuid.NewString(), nil }), customerservice.ClockFunc(time.Now), customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(func(ctx context.Context, _ customerservice.ReplyRequest) (customerservice.Reply, error) {
		if err := ctx.Err(); err != nil {
			return customerservice.Reply{}, err
		}
		return customerservice.Reply{Content: "跨系统接入验收固定回复，不查询或修改业务数据。"}, nil
	})))
	if err != nil {
		return err
	}
	gateway, err := webchat.New(repo, service, webchat.Config{
		Hosts:  []webchat.Host{{SourceID: "commerce-fixture", Secret: os.Getenv("FIXTURE_HOST_A")}, {SourceID: "repair-portal", Secret: os.Getenv("FIXTURE_HOST_B")}},
		Agents: []webchat.Agent{{SourceID: "commerce-fixture", ID: "staff-1", Secret: os.Getenv("FIXTURE_AGENT_A")}, {SourceID: "repair-portal", ID: "staff-1", Secret: os.Getenv("FIXTURE_AGENT_B")}},
	})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{Handler: gateway, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	fmt.Printf("http://%s\n", listener.Addr())
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	case err := <-done:
		return err
	}
}
