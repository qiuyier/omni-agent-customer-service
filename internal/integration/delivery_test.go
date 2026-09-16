package integration

import (
	"context"
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"testing"
	"time"
)

func pendingDelivery(t *testing.T, c *testClient, sink Deliverer) (Config, *Adapter, Job) {
	t.Helper()
	cfg := testConfig(t)
	a := openTestAdapter(t, cfg, c, sink, nil)
	j, err := a.Accept(context.Background(), sampleEvent())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		j, err = a.Advance(context.Background(), j.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if j.State != PendingDelivery {
		t.Fatal(j)
	}
	return cfg, a, j
}
func changeOwnership(t *testing.T, c *testClient, cid, action string) {
	t.Helper()
	err := c.repository.WithinTx(context.Background(), func(tx conversation.Transaction) error {
		current, err := tx.FindConversation(cid)
		if err != nil {
			return err
		}
		if action == "closed" {
			err = current.Close(time.Now())
		} else {
			err = current.RequestHandoff(time.Now())
			if err == nil && action == "human" {
				err = current.AssignAgent("agent-1", time.Now())
			}
		}
		if err != nil {
			return err
		}
		return tx.UpdateConversation(current)
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestDeliveryEligibilityAfterReopen(t *testing.T) {
	for _, tc := range []struct {
		name string
		want FailureCode
	}{{"waiting", FailureDeliveryOwnership}, {"human", FailureDeliveryOwnership}, {"closed", FailureDeliveryClosed}, {"new_message", FailureDeliverySuperseded}} {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t)
			sink := &testSink{}
			cfg, a, j := pendingDelivery(t, c, sink)
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			cmd := mapEvent(cfg.SourceID, j.Event)
			if tc.name == "new_message" {
				cmd.ExternalMessageID = "new-event"
				cmd.Content = "another question"
				if _, err := c.service.ReceiveMessage(context.Background(), cmd); err != nil {
					t.Fatal(err)
				}
			} else {
				changeOwnership(t, c, cmd.ConversationID, tc.name)
			}
			a = openTestAdapter(t, cfg, c, sink, nil)
			j, err := a.Advance(context.Background(), j.ID)
			if !errors.Is(err, ErrNeedsAttention) || j.LastFailure != tc.want || sink.calls != 0 {
				t.Fatalf("%+v %v deliveries=%d", j, err, sink.calls)
			}
			if c.models.Load() != 1 {
				t.Fatal("check generated a reply")
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			a = openTestAdapter(t, cfg, c, sink, nil)
			if _, err := a.Advance(context.Background(), j.ID); !errors.Is(err, ErrNeedsAttention) {
				t.Fatal(err)
			}
		})
	}
}

type checkedClient struct {
	Client
	check func(context.Context, customerservice.DeliveryCheck) (customerservice.DeliveryEligibility, error)
}

func (c checkedClient) CheckDeliveryEligibility(ctx context.Context, q customerservice.DeliveryCheck) (customerservice.DeliveryEligibility, error) {
	return c.check(ctx, q)
}

func TestDeliveryCheckFailsClosed(t *testing.T) {
	for _, mode := range []string{"unsupported", "timeout", "invalid", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			c := newTestClient(t)
			sink := &testSink{}
			_, a, j := pendingDelivery(t, c, sink)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "unsupported" {
				a.client = struct{ Client }{c}
			} else {
				a.client = checkedClient{Client: c, check: func(context.Context, customerservice.DeliveryCheck) (customerservice.DeliveryEligibility, error) {
					if mode == "timeout" {
						return customerservice.DeliveryEligibility{}, context.DeadlineExceeded
					}
					if mode == "cancelled" {
						cancel()
						return customerservice.DeliveryEligibility{Eligible: true, Reason: "eligible"}, nil
					}
					return customerservice.DeliveryEligibility{Eligible: true, Reason: "unexpected"}, nil
				}}
			}
			j, err := a.Advance(ctx, j.ID)
			if err == nil || sink.calls != 0 || j.State == Completed {
				t.Fatalf("%+v %v", j, err)
			}
		})
	}
}

func TestLostDeliveryAckRechecksCurrentOwnership(t *testing.T) {
	c := newTestClient(t)
	sink := &testSink{loseAck: true}
	cfg, a, j := pendingDelivery(t, c, sink)
	j, err := a.Advance(context.Background(), j.ID)
	if !errors.Is(err, ErrAttemptFailed) {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	changeOwnership(t, c, mapEvent(cfg.SourceID, j.Event).ConversationID, "human")
	now := j.NextAttemptAt.Add(time.Second)
	a = openTestAdapter(t, cfg, c, sink, &now)
	j, err = a.Advance(context.Background(), j.ID)
	if !errors.Is(err, ErrNeedsAttention) || j.LastFailure != FailureDeliveryOwnership || sink.calls != 1 || len(sink.delivered) != 1 {
		t.Fatalf("%+v %v calls=%d", j, err, sink.calls)
	}
	// The previous delivery might have succeeded. Attention is not a claim that nothing was sent.
}

func TestEligibilitySnapshotIsNotDeliveryLease(t *testing.T) {
	c := newTestClient(t)
	sink := &testSink{}
	_, a, j := pendingDelivery(t, c, sink)
	a.client = checkedClient{Client: c, check: func(ctx context.Context, q customerservice.DeliveryCheck) (customerservice.DeliveryEligibility, error) {
		result, err := c.service.CheckDeliveryEligibility(ctx, q)
		// Deterministically place a transition after the read transaction, before delivery.
		changeOwnership(t, c, q.ConversationID, "closed")
		return result, err
	}}
	j, err := a.Advance(context.Background(), j.ID)
	if err != nil || j.State != Completed || sink.calls != 1 {
		t.Fatalf("%+v %v", j, err)
	}
	// Strict prevention requires destination-side conditional delivery/fencing.
}

func TestServiceDeliveryCheckValidatesSavedReplyAndIdentity(t *testing.T) {
	c := newTestClient(t)
	cfg, _, j := pendingDelivery(t, c, &testSink{})
	cmd := mapEvent(cfg.SourceID, j.Event)
	base := customerservice.DeliveryCheck{ConversationID: cmd.ConversationID, CustomerID: cmd.CustomerID, Channel: cmd.Channel, TriggerMessageID: j.TriggerMessageID, ReplyMessageID: j.ReplyMessageID, Content: j.ReplyContent}
	for _, field := range []string{"customer", "channel", "trigger", "reply", "content"} {
		q := base
		switch field {
		case "customer":
			q.CustomerID = "other"
		case "channel":
			q.Channel = "other"
		case "trigger":
			q.TriggerMessageID = "other"
		case "reply":
			q.ReplyMessageID = "other"
		case "content":
			q.Content = "altered"
		}
		result, err := c.service.CheckDeliveryEligibility(context.Background(), q)
		if err != nil || result.Eligible {
			t.Fatalf("%s %+v %v", field, result, err)
		}
	}
	result, err := c.service.CheckDeliveryEligibility(context.Background(), base)
	if err != nil || !result.Eligible {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err := c.service.CheckDeliveryEligibility(nil, base); !errors.Is(err, customerservice.ErrInvalidInput) {
		t.Fatal(err)
	}
}
