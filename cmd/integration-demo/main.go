// integration-demo uses fictional data and local fakes; it never calls a model.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/integration"
)

type losingClient struct {
	*customerservice.Service
	receiveLost, replyLost bool
}

func (c *losingClient) ReceiveMessage(ctx context.Context, v customerservice.ReceiveMessageCommand) (customerservice.ReceiveMessageResult, error) {
	r, err := c.Service.ReceiveMessage(ctx, v)
	if err == nil && !c.receiveLost {
		c.receiveLost = true
		return customerservice.ReceiveMessageResult{}, context.DeadlineExceeded
	}
	return r, err
}
func (c *losingClient) GenerateBotReply(ctx context.Context, v customerservice.GenerateBotReplyCommand) (customerservice.GenerateBotReplyResult, error) {
	r, err := c.Service.GenerateBotReply(ctx, v)
	if err == nil && !c.replyLost {
		c.replyLost = true
		return customerservice.GenerateBotReplyResult{}, context.DeadlineExceeded
	}
	return r, err
}

type report struct {
	Directory            string                       `json:"private_state_directory"`
	States               map[string]integration.State `json:"states"`
	ModelCalls           int                          `json:"fake_model_calls"`
	Deliveries           int                          `json:"unique_deliveries"`
	LostAcknowledgements int                          `json:"lost_acknowledgements_recovered"`
}

func run() (report, error) {
	ctx := context.Background()
	directory, err := os.MkdirTemp("", "integration-demo-")
	if err != nil {
		return report{}, err
	}
	result := report{Directory: directory, States: map[string]integration.State{}}
	ids := 0
	service, err := customerservice.New(conversation.NewMemoryRepository(), customerservice.MessageIDGeneratorFunc(func() (string, error) { ids++; return fmt.Sprintf("message-%d", ids), nil }), customerservice.ClockFunc(time.Now), customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(func(context.Context, customerservice.ReplyRequest) (customerservice.Reply, error) {
		result.ModelCalls++
		return customerservice.Reply{Content: "请打开应用中的帮助中心。"}, nil
	})))
	if err != nil {
		return result, err
	}
	delivered := map[string]integration.Delivery{}
	deliveryLost := false
	sink := integration.DelivererFunc(func(_ context.Context, d integration.Delivery) error {
		if previous, ok := delivered[d.Key]; ok {
			if previous != d {
				return &integration.CallError{Code: integration.FailureConflict}
			}
			return nil
		}
		delivered[d.Key] = d
		if d.SourceID == "system-a" && !deliveryLost {
			deliveryLost = true
			return context.DeadlineExceeded
		}
		return nil
	})
	for _, source := range []string{"system-a", "system-b"} {
		var client integration.Client = service
		if source == "system-a" {
			client = &losingClient{Service: service}
		}
		cfg := integration.Config{Directory: filepath.Join(directory, source), SourceID: source, DestinationID: "demo-channel", RetryDelay: time.Millisecond}
		adapter, err := integration.Open(cfg, client, sink)
		if err != nil {
			return result, err
		}
		job, err := adapter.Accept(ctx, integration.Event{ConversationID: "42", CustomerID: "7", Channel: "web", ExternalMessageID: "event-1", Content: "帮助中心在哪里？"})
		closeErr := adapter.Close()
		if err != nil {
			return result, err
		}
		if closeErr != nil {
			return result, closeErr
		}
		for step := 0; step < 12 && job.State != integration.Completed; step++ {
			// Reopen only the adapter; backend and idempotent destination remain alive.
			if delay := time.Until(job.NextAttemptAt); delay > 0 {
				time.Sleep(delay)
			}
			adapter, err = integration.Open(cfg, client, sink)
			if err != nil {
				return result, err
			}
			job, err = adapter.Advance(ctx, job.ID)
			closeErr = adapter.Close()
			if errors.Is(err, integration.ErrAttemptFailed) {
				result.LostAcknowledgements++
			} else if err != nil {
				return result, err
			}
			if closeErr != nil {
				return result, closeErr
			}
		}
		if job.State != integration.Completed {
			return result, errors.New("demo did not complete")
		}
		result.States[source] = job.State
	}
	result.Deliveries = len(delivered)
	if result.ModelCalls != 2 || result.Deliveries != 2 || result.LostAcknowledgements != 3 {
		return result, errors.New("demo invariants failed")
	}
	return result, nil
}
func main() {
	result, err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration demo failed:", err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
		os.Exit(1)
	}
}
