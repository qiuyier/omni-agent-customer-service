package customerservice_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	kitstream "github.com/qiuyier/omni-agent-kit/stream"
)

func TestReplyCapacityWithoutLimiterIsUntracked(t *testing.T) {
	var service *customerservice.Service
	service, _, _ = newReplyTestService(t, customerservice.ReplyGeneratorFunc(func(
		context.Context,
		customerservice.ReplyRequest,
	) (customerservice.Reply, error) {
		if got := service.ReplyCapacity(); got != (customerservice.ReplyCapacitySnapshot{}) {
			t.Errorf("unconfigured ReplyCapacity() during generation = %#v, want zero snapshot", got)
		}
		return customerservice.Reply{Content: "answer"}, nil
	}))
	command := receiveReplyConcurrencyMessage(t, service, "untracked-capacity")
	if _, err := service.GenerateBotReply(context.Background(), command); err != nil {
		t.Fatal(err)
	}
}

func TestReplyCapacityReleasesBeforePersistenceCompletes(t *testing.T) {
	repository := &capacityBlockingRepository{
		Repository: conversation.NewMemoryRepository(),
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
	defer close(repository.release)
	observer := &recordingReplyObserver{}
	var service *customerservice.Service
	var err error
	service, err = customerservice.New(
		repository,
		&sequenceIDGenerator{},
		customerservice.ClockFunc(func() time.Time { return serviceBaseTime }),
		customerservice.WithMaxConcurrentReplies(1),
		customerservice.WithReplyObserver(observer),
		customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(func(
			context.Context,
			customerservice.ReplyRequest,
		) (customerservice.Reply, error) {
			if got := service.ReplyCapacity(); got != (customerservice.ReplyCapacitySnapshot{Active: 1, Limit: 1}) {
				t.Errorf("ReplyCapacity() during generation = %#v, want active 1 limit 1", got)
			}
			return customerservice.Reply{Content: "answer"}, nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	command := receiveReplyConcurrencyMessage(t, service, "capacity-before-persist")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	outcomes := make(chan replyOutcome, 1)
	go func() {
		result, generateErr := service.GenerateBotReply(ctx, command)
		outcomes <- replyOutcome{result: result, err: generateErr}
	}()
	select {
	case <-repository.entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reply persistence")
	}
	if got := service.ReplyCapacity(); got != (customerservice.ReplyCapacitySnapshot{Limit: 1}) {
		t.Errorf("ReplyCapacity() during persistence = %#v, want active 0 limit 1", got)
	}
	_, _, finished := observer.snapshot()
	if len(finished) != 0 {
		t.Errorf("finished events before persistence returns = %d, want 0", len(finished))
	}
	cancel()
	waitForReplyOutcome(t, outcomes)
}

func TestReplyCapacityStreamingSnapshotsAreSafeDuringConcurrentUse(t *testing.T) {
	const limit = 4
	service := newReplyConcurrencyTestService(t, nil, customerservice.ReplyStreamGeneratorFunc(func(
		context.Context,
		customerservice.ReplyRequest,
	) (kitstream.Reader[customerservice.ReplyDelta], error) {
		return kitstream.FromSlice([]customerservice.ReplyDelta{{Content: "answer"}}), nil
	}), limit)
	commands := make([]customerservice.GenerateBotReplyCommand, limit)
	for index := range limit {
		commands[index] = receiveReplyConcurrencyMessage(t, service, fmt.Sprintf("snapshot-%d", index))
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					got := service.ReplyCapacity()
					if got.Limit != limit || got.Active < 0 || got.Active > limit {
						t.Errorf("concurrent ReplyCapacity() = %#v, want active in [0,%d]", got, limit)
						return
					}
				}
			}
		}()
	}
	defer func() {
		close(stop)
		readers.Wait()
	}()
	for range 20 {
		streams := make([]kitstream.Reader[customerservice.BotReplyStreamEvent], 0, limit)
		for index, command := range commands {
			stream, err := service.StreamBotReply(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			streams = append(streams, stream)
			if got := service.ReplyCapacity(); got != (customerservice.ReplyCapacitySnapshot{Active: index + 1, Limit: limit}) {
				t.Errorf("ReplyCapacity() after stream initialization = %#v", got)
			}
		}
		var closers sync.WaitGroup
		for _, stream := range streams {
			closers.Add(1)
			go func() {
				defer closers.Done()
				for range 2 {
					if err := stream.Close(); err != nil {
						t.Errorf("stream.Close() error = %v", err)
					}
				}
			}()
		}
		closers.Wait()
		if got := service.ReplyCapacity(); got != (customerservice.ReplyCapacitySnapshot{Limit: limit}) {
			t.Errorf("ReplyCapacity() after stream cleanup = %#v, want active 0 limit %d", got, limit)
		}
	}
}

// The first transaction receives the customer message, the second prepares
// generation, and the third persists its reply.
type capacityBlockingRepository struct {
	conversation.Repository
	calls   atomic.Int64
	entered chan struct{}
	release chan struct{}
}

func (r *capacityBlockingRepository) WithinTx(ctx context.Context, fn func(conversation.Transaction) error) error {
	if r.calls.Add(1) == 3 {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.Repository.WithinTx(ctx, fn)
}
