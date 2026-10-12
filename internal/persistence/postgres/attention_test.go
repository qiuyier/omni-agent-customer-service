package postgres_test

import (
	"context"
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"testing"
)

func TestDeskAttentionDurableAndScoped(t *testing.T) {
	r := openTestRepository(t, true)
	ctx := context.Background()
	c := newConversation(t, "attention", "customer", "web")
	if err := c.RequestHandoff(integrationTime); err != nil {
		t.Fatal(err)
	}
	m := newCustomerMessage(t, "seen-message", c.ID(), "one", "first")
	if err := r.WithinTx(ctx, func(tx conversation.Transaction) error {
		if err := tx.InsertConversation(c); err != nil {
			return err
		}
		return tx.InsertMessage(c.Channel(), m)
	}); err != nil {
		t.Fatal(err)
	}
	a, err := r.DeskAttention(ctx, c.ID(), "web", "agent-a", nil)
	if err != nil || a.Unread != 1 {
		t.Fatal(a, err)
	}
	if _, err = r.DeskAttention(ctx, c.ID(), "web", "agent-a", []string{m.ID()}); err != nil {
		t.Fatal(err)
	}
	reload := openTestRepository(t, false)
	a, err = reload.DeskAttention(ctx, c.ID(), "web", "agent-a", nil)
	if err != nil || a.Unread != 0 || !a.Seen[m.ID()] {
		t.Fatal(a, err)
	}
	a, err = reload.DeskAttention(ctx, c.ID(), "web", "agent-b", nil)
	if err != nil || a.Unread != 1 {
		t.Fatal(a, err)
	}
	later := newCustomerMessage(t, "later-message", c.ID(), "two", "later")
	if err = r.WithinTx(ctx, func(tx conversation.Transaction) error { return tx.InsertMessage(c.Channel(), later) }); err != nil {
		t.Fatal(err)
	}
	a, err = r.DeskAttention(ctx, c.ID(), "web", "agent-a", []string{m.ID()})
	if err != nil || a.Unread != 1 {
		t.Fatal(a, err)
	}
	if _, err = r.DeskAttention(ctx, c.ID(), "web", "agent-a", []string{later.ID(), "foreign"}); !errors.Is(err, conversation.ErrInvalidArgument) {
		t.Fatal(err)
	}
	a, err = reload.DeskAttention(ctx, c.ID(), "web", "agent-a", nil)
	if err != nil || a.Unread != 1 {
		t.Fatal(a, err)
	}
	for _, args := range [][2]string{{"other", "agent-a"}, {"web", ""}} {
		if _, err = r.DeskAttention(ctx, c.ID(), args[0], args[1], []string{later.ID()}); !errors.Is(err, conversation.ErrAttentionDenied) {
			t.Fatal(err)
		}
	}
}
