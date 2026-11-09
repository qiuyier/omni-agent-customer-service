package feedback

import (
	"context"
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"strings"
	"testing"
	"time"
)

func TestSanitize(t *testing.T) {
	for _, secret := range []string{"alice@example.org", "https://example.org/private?token=x", "13800138000", "password=fictional-secret", "密码：虚构密码", "sk-fictional-secret", "token=fictional-secret"} {
		got := Sanitize("帮助 " + secret + " 谢谢")
		if strings.Contains(got, secret) || !strings.Contains(got, "[已脱敏]") {
			t.Fatalf("rule failed: %q", got)
		}
		if Sanitize(got) != got {
			t.Fatal("not idempotent")
		}
	}
	if Sanitize("hello\x00world") != "" || Sanitize(string([]byte{255})) != "" || Sanitize(strings.Repeat("a", 8193)) != "" {
		t.Fatal("invalid input accepted")
	}
	if len([]rune(Sanitize(strings.Repeat("问", 600)))) != 512 {
		t.Fatal("limit")
	}
	if Sanitize(" \n 帮助\t问题 ") != "帮助 问题" {
		t.Fatal("normalization")
	}
}

type sinkFunc func(context.Context, string) error

func TestNearDeadlineSkipsCapture(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	wrapped := Wrap(customerservice.KnowledgeRetrieverFunc(func(context.Context, customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
		return nil, nil
	}), sinkFunc(func(context.Context, string) error { t.Fatal("near-deadline capture"); return nil }))
	if _, err := wrapped.Retrieve(ctx, customerservice.KnowledgeQuery{Text: "help"}); err != nil {
		t.Fatal(err)
	}
}

func (f sinkFunc) RecordFeedback(c context.Context, s string) error { return f(c, s) }
func TestCaptureOnlySuccessfulMiss(t *testing.T) {
	for _, kind := range []string{"miss", "hit", "error", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "canceled" {
				cancel()
			}
			original := errors.New("failure")
			next := customerservice.KnowledgeRetrieverFunc(func(context.Context, customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
				if kind == "error" {
					return nil, original
				}
				if kind == "hit" {
					return []customerservice.KnowledgeSnippet{{ID: "x"}}, nil
				}
				return nil, nil
			})
			wrapped := Wrap(next, sinkFunc(func(c context.Context, s string) error {
				calls++
				deadline, ok := c.Deadline()
				if !ok || time.Until(deadline) > 100*time.Millisecond || strings.Contains(s, "13800138000") {
					t.Fatal("unsafe record")
				}
				return errors.New("storage failed")
			}))
			hits, err := wrapped.Retrieve(ctx, customerservice.KnowledgeQuery{Text: "帮助 13800138000"})
			if (kind == "miss") != (calls == 1) || (kind == "error") != (err == original) || (kind == "hit") != (len(hits) == 1) {
				t.Fatal("retrieval semantics changed")
			}
		})
	}
}
