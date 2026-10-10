package postgres_test

import (
	"context"
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"testing"
	"time"
)

func TestRoundLookupAcrossPools(t *testing.T) {
	first := openTestRepository(t, true)
	second := openTestRepository(t, false)
	ctx := context.Background()
	for i, item := range []struct{ id, customer, channel string }{{"old", "customer", "channel"}, {"new", "customer", "channel"}, {"other-customer", "other", "channel"}, {"other-channel", "customer", "other"}} {
		c, err := conversation.New(conversation.CreateParams{ID: item.id, CustomerID: item.customer, Channel: item.channel, CreatedAt: integrationTime.Add(time.Duration(i) * time.Microsecond)})
		if err != nil {
			t.Fatal(err)
		}
		if err = first.WithinTx(ctx, func(tx conversation.Transaction) error { return tx.InsertConversation(c) }); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := second.FindLatestRound(ctx, "customer", "channel", time.Time{})
	if err != nil || latest.ID() != "new" {
		t.Fatalf("latest=%v err=%v", latest, err)
	}
	previous, err := second.FindLatestRound(ctx, "customer", "channel", latest.CreatedAt())
	if err != nil || previous.ID() != "old" {
		t.Fatalf("previous=%v err=%v", previous, err)
	}
	if _, err = second.FindLatestRound(ctx, "customer", "channel", previous.CreatedAt()); !errors.Is(err, conversation.ErrConversationNotFound) {
		t.Fatal(err)
	}
	if _, err = second.FindLatestRound(ctx, "missing", "channel", time.Time{}); !errors.Is(err, conversation.ErrConversationNotFound) {
		t.Fatal(err)
	}
}
