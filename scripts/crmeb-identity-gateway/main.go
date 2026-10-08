// Isolated acceptance fixture; not a deployment server. No model or database.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/webchat"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		panic("requires private secret file and ready file")
	}
	secret, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic("secret unavailable")
	}
	repo := conversation.NewMemoryRepository()
	var ids atomic.Int64
	app, err := customerservice.New(repo, customerservice.MessageIDGeneratorFunc(func() (string, error) { return fmt.Sprintf("check-%d", ids.Add(1)), nil }), customerservice.ClockFunc(time.Now), customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(func(context.Context, customerservice.ReplyRequest) (customerservice.Reply, error) {
		return customerservice.Reply{Content: "identity check only"}, nil
	})))
	if err != nil {
		panic("application initialization failed")
	}
	gateway, err := webchat.New(repo, app, webchat.Config{Hosts: []webchat.Host{{SourceID: "crmeb-identity-check", Secret: string(secret)}}})
	if err != nil {
		panic("gateway initialization failed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic("listen failed")
	}
	data, _ := json.Marshal(map[string]interface{}{"pid": os.Getpid(), "origin": "http://" + listener.Addr().String()})
	if os.WriteFile(os.Args[2], data, 0600) != nil {
		panic("ready file failed")
	}
	server := &http.Server{Handler: gateway, ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() {
		select {
		case <-ctx.Done():
		case <-time.After(15 * time.Minute):
		}
		server.Close()
	}()
	if err := server.Serve(listener); err != http.ErrServerClosed {
		panic("serve failed")
	}
}
