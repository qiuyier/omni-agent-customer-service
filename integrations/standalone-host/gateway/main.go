// A private, loopback-only test gateway. Business login lives in Node, not here.
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/webchat"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "standalone test gateway failed")
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
		return customerservice.Reply{Content: "独立业务后端接入测试：消息已收到。这是固定模拟回复，不查询业务数据，也不执行外部操作。"}, nil
	})))
	if err != nil {
		return err
	}
	gateway, err := webchat.New(repo, service, webchat.Config{Hosts: []webchat.Host{{SourceID: "standalone-business", Secret: os.Getenv("STANDALONE_HOST_SECRET")}}})
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
