package customerservice_test

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	kitstream "github.com/qiuyier/omni-agent-kit/stream"
)

func TestKnowledgeReplyBothModesUseTriggerQueryAndSkipReplay(t *testing.T) {
	for _, mode := range []string{"generate", "stream"} {
		t.Run(mode, func(t *testing.T) {
			var query customerservice.KnowledgeQuery
			var retrievals, generations int
			snippets := []customerservice.KnowledgeSnippet{knowledgeTestSnippet("policy", "参考资料，不是聊天消息")}
			retriever := customerservice.KnowledgeRetrieverFunc(func(_ context.Context, value customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
				retrievals++
				query = value
				return snippets, nil
			})
			service, repository, observer := newKnowledgeTestService(t, retriever, func(request customerservice.ReplyRequest) {
				generations++
				if request.Knowledge == nil || !reflect.DeepEqual(request.Knowledge.Snippets, snippets) {
					t.Fatalf("model knowledge = %#v", request.Knowledge)
				}
				if len(request.History) != 1 || request.History[0].Content != validCommand().Content {
					t.Fatalf("knowledge changed original history: %#v", request.History)
				}
				request.Knowledge.Snippets[0].Content = "model adapter cannot mutate retriever's slice"
			})
			received, err := service.ReceiveMessage(context.Background(), validCommand())
			if err != nil {
				t.Fatal(err)
			}
			command := customerservice.GenerateBotReplyCommand{ConversationID: received.ConversationID, TriggerMessageID: received.MessageID}
			for index := range 2 {
				result, err := callKnowledgeReply(context.Background(), service, command, mode)
				if err != nil || result.Duplicate != (index == 1) {
					t.Fatalf("reply %d = %#v, %v", index, result, err)
				}
			}
			if retrievals != 1 || generations != 1 {
				t.Fatalf("retrievals/generations = %d/%d, want 1/1", retrievals, generations)
			}
			if query.Text != validCommand().Content || query.Limit != customerservice.MaxKnowledgeSnippets {
				t.Fatalf("query = %#v", query)
			}
			if snippets[0].Content != "参考资料，不是聊天消息" {
				t.Fatal("retriever slice was mutated")
			}
			messages := listMessages(t, repository, command.ConversationID)
			if len(messages) != 2 || messages[1].Content() != "answer" {
				t.Fatalf("persisted messages = %#v", messages)
			}
			started, rejected, finished := observer.snapshot()
			if len(started) != 1 || len(rejected) != 0 || len(finished) != 1 {
				t.Fatalf("AI events = %d/%d/%d", len(started), len(rejected), len(finished))
			}
		})
	}
}

func TestKnowledgeDisabledAndNoMatchesRemainDistinct(t *testing.T) {
	for _, mode := range []string{"generate", "stream"} {
		for _, enabled := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/disabled", true: "/no-matches"}[enabled], func(t *testing.T) {
				var retriever customerservice.KnowledgeRetriever
				if enabled {
					retriever = customerservice.KnowledgeRetrieverFunc(func(context.Context, customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
						return nil, nil
					})
				}
				service, _, _ := newKnowledgeTestService(t, retriever, func(request customerservice.ReplyRequest) {
					if (request.Knowledge != nil) != enabled {
						t.Fatalf("knowledge = %#v, enabled=%v", request.Knowledge, enabled)
					}
					if enabled && len(request.Knowledge.Snippets) != 0 {
						t.Fatal("expected no matches")
					}
				})
				command := receiveReplyConcurrencyMessage(t, service, "no-match")
				if _, err := callKnowledgeReply(context.Background(), service, command, mode); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestKnowledgeFailuresNeverStartModelOrPersistReply(t *testing.T) {
	backendFailure := errors.New("private retriever failure")
	for _, mode := range []string{"generate", "stream"} {
		for _, test := range []struct {
			name     string
			cause    error
			retrieve customerservice.KnowledgeRetrieverFunc
		}{
			{name: "backend failure", cause: backendFailure, retrieve: func(context.Context, customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
				return nil, backendFailure
			}},
			{name: "timeout discards late success", cause: context.DeadlineExceeded, retrieve: func(ctx context.Context, _ customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
				<-ctx.Done()
				return []customerservice.KnowledgeSnippet{knowledgeTestSnippet("late", "late result")}, nil
			}},
			{name: "invalid snippet", cause: customerservice.ErrKnowledgeRetrievalFailed, retrieve: func(context.Context, customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
				return []customerservice.KnowledgeSnippet{{Content: "missing provenance"}}, nil
			}},
		} {
			t.Run(mode+"/"+test.name, func(t *testing.T) {
				service, repository, observer := newKnowledgeTestService(t, test.retrieve, func(customerservice.ReplyRequest) { t.Error("model called after retrieval failure") }, customerservice.WithKnowledgeRetrievalTimeout(10*time.Millisecond))
				command := receiveReplyConcurrencyMessage(t, service, "failed-knowledge")
				_, err := callKnowledgeReply(context.Background(), service, command, mode)
				if !errors.Is(err, customerservice.ErrKnowledgeRetrievalFailed) || !errors.Is(err, test.cause) {
					t.Fatalf("error = %v, want retrieval failure and %v", err, test.cause)
				}
				if messages := listMessages(t, repository, command.ConversationID); len(messages) != 1 {
					t.Fatalf("stored messages = %d", len(messages))
				}
				started, rejected, finished := observer.snapshot()
				if len(started)+len(rejected)+len(finished) != 0 {
					t.Fatal("retrieval failure emitted model events")
				}
				if service.ReplyCapacity().Active != 0 {
					t.Fatal("retrieval failure acquired a model slot")
				}
			})
		}
	}
}

func TestKnowledgeRetrievalOutsideTransactionKeepsOriginalSnapshot(t *testing.T) {
	for _, mode := range []string{"generate", "stream"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan customerservice.KnowledgeQuery, 1)
			release := make(chan struct{})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			retriever := customerservice.KnowledgeRetrieverFunc(func(ctx context.Context, query customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
				entered <- query
				select {
				case <-release:
					return []customerservice.KnowledgeSnippet{knowledgeTestSnippet("old", "original question evidence")}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			original := validCommand()
			service, repository, observer := newKnowledgeTestService(t, retriever, func(request customerservice.ReplyRequest) {
				if len(request.History) != 1 || request.History[0].Content != original.Content {
					t.Errorf("model silently changed trigger: %#v", request.History)
				}
			})
			received, err := service.ReceiveMessage(ctx, original)
			if err != nil {
				t.Fatal(err)
			}
			command := customerservice.GenerateBotReplyCommand{ConversationID: received.ConversationID, TriggerMessageID: received.MessageID}
			done := make(chan error, 1)
			go func() { _, err := callKnowledgeReply(ctx, service, command, mode); done <- err }()
			select {
			case query := <-entered:
				if query.Text != original.Content {
					t.Errorf("query = %q", query.Text)
				}
			case <-ctx.Done():
				t.Fatal("retrieval did not start")
			}
			if service.ReplyCapacity().Active != 0 {
				t.Error("retrieval holds a model slot")
			}
			started, _, _ := observer.snapshot()
			if len(started) != 0 {
				t.Error("retrieval already started AI duration")
			}
			// This write would block if retrieval still held the snapshot transaction.
			newer := original
			newer.ExternalMessageID = "newer-message"
			newer.Content = "客户在检索期间提出的新问题"
			if _, err := service.ReceiveMessage(ctx, newer); err != nil {
				t.Fatalf("receive during retrieval: %v", err)
			}
			close(release)
			select {
			case err := <-done:
				if !errors.Is(err, customerservice.ErrReplySuperseded) {
					t.Fatalf("reply error = %v, want superseded", err)
				}
			case <-ctx.Done():
				t.Fatal("reply did not finish")
			}
			if messages := listMessages(t, repository, command.ConversationID); len(messages) != 2 {
				t.Fatalf("obsolete answer was persisted: %d messages", len(messages))
			}
			if _, err := callKnowledgeReply(ctx, service, command, mode); !errors.Is(err, customerservice.ErrReplySuperseded) {
				t.Fatalf("stale trigger error = %v", err)
			}
			select {
			case <-entered:
				t.Error("already stale trigger performed retrieval again")
			default:
			}
		})
	}
}

func TestKnowledgeQueryAndContextHaveDeterministicBounds(t *testing.T) {
	var captured customerservice.ReplyRequest
	command := validCommand()
	command.Content = strings.Repeat("问", customerservice.MaxKnowledgeQueryRunes+20)
	retriever := customerservice.KnowledgeRetrieverFunc(func(_ context.Context, query customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
		if utf8.RuneCountInString(query.Text) != customerservice.MaxKnowledgeQueryRunes || !utf8.ValidString(query.Text) {
			t.Fatalf("query bound = %d", utf8.RuneCountInString(query.Text))
		}
		return []customerservice.KnowledgeSnippet{
			knowledgeTestSnippet("one", strings.Repeat("甲", 3500)),
			knowledgeTestSnippet("two", strings.Repeat("乙", 3500)), // omit whole snippet, not its tail conditions
			knowledgeTestSnippet("three", "完整的小片段"),
			knowledgeTestSnippet("four", "outside top K"),
		}, nil
	})
	service, _, _ := newKnowledgeTestService(t, retriever, func(request customerservice.ReplyRequest) { captured = request })
	received, err := service.ReceiveMessage(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.GenerateBotReply(context.Background(), customerservice.GenerateBotReplyCommand{ConversationID: received.ConversationID, TriggerMessageID: received.MessageID}); err != nil {
		t.Fatal(err)
	}
	if captured.History[0].Content != command.Content {
		t.Fatal("query limit truncated original chat history")
	}
	snippets := captured.Knowledge.Snippets
	if len(snippets) != 2 || snippets[0].ID != "one" || snippets[1].ID != "three" || utf8.RuneCountInString(snippets[0].Content) != 3500 {
		t.Fatalf("bounded snippets = %#v", snippets)
	}
}

func TestKnowledgeOptionsRejectInvalidDependencies(t *testing.T) {
	for _, option := range []customerservice.Option{customerservice.WithKnowledgeRetriever(nil), customerservice.WithKnowledgeRetrievalTimeout(0), customerservice.WithKnowledgeRetrievalTimeout(-time.Second)} {
		_, err := customerservice.New(conversation.NewMemoryRepository(), &sequenceIDGenerator{}, customerservice.ClockFunc(time.Now), option)
		if !errors.Is(err, customerservice.ErrInvalidDependency) {
			t.Fatalf("option error = %v", err)
		}
	}
}

func TestKnowledgeHonorsCallerCancellationBeforeModelAdmission(t *testing.T) {
	for _, mode := range []string{"generate", "stream"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			retriever := customerservice.KnowledgeRetrieverFunc(func(retrievalContext context.Context, _ customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
				cancel()
				<-retrievalContext.Done()
				return nil, nil // A buggy backend's apparent success must be discarded.
			})
			service, _, observer := newKnowledgeTestService(t, retriever, func(customerservice.ReplyRequest) { t.Error("model called after cancellation") })
			command := receiveReplyConcurrencyMessage(t, service, "cancel-knowledge")
			_, err := callKnowledgeReply(ctx, service, command, mode)
			if !errors.Is(err, context.Canceled) || !errors.Is(err, customerservice.ErrKnowledgeRetrievalFailed) {
				t.Fatalf("error = %v", err)
			}
			started, rejected, finished := observer.snapshot()
			if len(started)+len(rejected)+len(finished) != 0 {
				t.Fatal("canceled retrieval emitted model events")
			}
		})
	}
}

func knowledgeTestSnippet(id, content string) customerservice.KnowledgeSnippet {
	return customerservice.KnowledgeSnippet{ID: id, Title: "参考标题", Source: "handbook/" + id, Content: content}
}

func newKnowledgeTestService(t *testing.T, retriever customerservice.KnowledgeRetriever, onModel func(customerservice.ReplyRequest), options ...customerservice.Option) (*customerservice.Service, *conversation.MemoryRepository, *recordingReplyObserver) {
	t.Helper()
	repository := conversation.NewMemoryRepository()
	observer := &recordingReplyObserver{}
	all := []customerservice.Option{
		customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(func(_ context.Context, request customerservice.ReplyRequest) (customerservice.Reply, error) {
			onModel(request)
			return customerservice.Reply{Content: "answer"}, nil
		})),
		customerservice.WithReplyStreamGenerator(customerservice.ReplyStreamGeneratorFunc(func(_ context.Context, request customerservice.ReplyRequest) (kitstream.Reader[customerservice.ReplyDelta], error) {
			onModel(request)
			return kitstream.FromSlice([]customerservice.ReplyDelta{{Content: "answer"}}), nil
		})),
		customerservice.WithMaxConcurrentReplies(1), customerservice.WithReplyObserver(observer),
	}
	if retriever != nil {
		all = append(all, customerservice.WithKnowledgeRetriever(retriever))
	}
	all = append(all, options...)
	service, err := customerservice.New(repository, &sequenceIDGenerator{}, customerservice.ClockFunc(func() time.Time { return serviceBaseTime }), all...)
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, observer
}

// Treat the streaming superseded terminal event as the equivalent application
// error solely for assertions shared with the non-streaming path.
func callKnowledgeReply(ctx context.Context, service *customerservice.Service, command customerservice.GenerateBotReplyCommand, mode string) (customerservice.GenerateBotReplyResult, error) {
	if mode == "generate" {
		return service.GenerateBotReply(ctx, command)
	}
	reader, err := service.StreamBotReply(ctx, command)
	if err != nil {
		return customerservice.GenerateBotReplyResult{}, err
	}
	defer reader.Close()
	var result customerservice.GenerateBotReplyResult
	for {
		event, err := reader.Recv()
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			return result, err
		}
		switch event.Type {
		case customerservice.BotReplyStreamEventCompleted:
			result = event.Result
		case customerservice.BotReplyStreamEventSuperseded:
			return event.Result, customerservice.ErrReplySuperseded
		}
	}
}
