package reload

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

func fixed(text string) customerservice.KnowledgeRetriever {
	return customerservice.KnowledgeRetrieverFunc(func(context.Context, customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
		return []customerservice.KnowledgeSnippet{{Content: text}}, nil
	})
}
func TestRetrievalPinsVersionAndFailedReloadPreservesIt(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	old := customerservice.KnowledgeRetrieverFunc(func(context.Context, customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
		close(entered)
		<-release
		return []customerservice.KnowledgeSnippet{{Content: "old"}}, nil
	})
	var attempt atomic.Int32
	m, e := New(context.Background(), func(context.Context) (Candidate, error) {
		switch attempt.Add(1) {
		case 1:
			return Candidate{old, "v1", 1}, nil
		case 2:
			return Candidate{fixed("new"), "v2", 1}, nil
		default:
			return Candidate{}, errors.New("private provider data")
		}
	})
	if e != nil {
		t.Fatal(e)
	}
	result := make(chan string, 1)
	go func() {
		r, _ := m.Retrieve(context.Background(), customerservice.KnowledgeQuery{})
		result <- r[0].Content
	}()
	<-entered
	if e = m.Reload(context.Background()); e != nil {
		t.Fatal(e)
	}
	r, _ := m.Retrieve(context.Background(), customerservice.KnowledgeQuery{})
	if r[0].Content != "new" {
		t.Fatal("new request used old version")
	}
	close(release)
	if <-result != "old" {
		t.Fatal("in-flight request changed version")
	}
	if e = m.Reload(context.Background()); !errors.Is(e, ErrLoad) {
		t.Fatal(e)
	}
	s := m.Status()
	if s.Version != "v2" || s.Result != "failed" || s.Loading {
		t.Fatal(s)
	}
}
func TestBusyAndCanceledCandidateNeverReplacesCurrent(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	m, e := New(context.Background(), func(ctx context.Context) (Candidate, error) {
		if calls.Add(1) > 1 {
			close(started)
			<-ctx.Done()
		}
		return Candidate{fixed("x"), "version", 1}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	before := m.Status()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Reload(ctx) }()
	<-started
	if !m.Status().Loading {
		t.Fatal("status not available while loading")
	}
	if e = m.Reload(context.Background()); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	cancel()
	if !errors.Is(<-done, ErrLoad) {
		t.Fatal("accepted canceled load")
	}
	if m.Status().LoadedAt != before.LoadedAt {
		t.Fatal("canceled candidate published")
	}
}
