package customerservice_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	kitstream "github.com/qiuyier/omni-agent-kit/stream"
)

func TestWithMaxConcurrentRepliesRejectsNonPositiveValues(t *testing.T) {
	for _, value := range []int{0, -1} {
		t.Run(fmt.Sprintf("max_%d", value), func(t *testing.T) {
			_, err := customerservice.New(
				conversation.NewMemoryRepository(),
				&sequenceIDGenerator{},
				customerservice.ClockFunc(func() time.Time { return serviceBaseTime }),
				customerservice.WithMaxConcurrentReplies(value),
			)
			if !errors.Is(err, customerservice.ErrInvalidDependency) {
				t.Fatalf("customerservice.New() error = %v, want ErrInvalidDependency", err)
			}
		})
	}
}

func TestReplyConcurrencyLimitRejectsImmediatelyAndReleasesAfterGenerate(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int64
	generator := customerservice.ReplyGeneratorFunc(func(
		ctx context.Context,
		_ customerservice.ReplyRequest,
	) (customerservice.Reply, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			return customerservice.Reply{Content: "answer"}, nil
		case <-ctx.Done():
			return customerservice.Reply{}, ctx.Err()
		}
	})
	service := newReplyConcurrencyTestService(t, generator, nil, 1)
	firstCommand := receiveReplyConcurrencyMessage(t, service, "first")
	secondCommand := receiveReplyConcurrencyMessage(t, service, "second")

	firstOutcome := make(chan replyOutcome, 1)
	go func() {
		result, err := service.GenerateBotReply(context.Background(), firstCommand)
		firstOutcome <- replyOutcome{result: result, err: err}
	}()
	waitForGeneratorStart(t, started)

	rejected := make(chan error, 1)
	go func() {
		_, err := service.GenerateBotReply(context.Background(), secondCommand)
		rejected <- err
	}()
	select {
	case err := <-rejected:
		if !errors.Is(err, customerservice.ErrReplyCapacityExceeded) {
			t.Fatalf("second GenerateBotReply() error = %v, want ErrReplyCapacityExceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("capacity-exhausted reply waited instead of failing immediately")
	}
	if calls.Load() != 1 {
		t.Fatalf("generator calls = %d, want 1", calls.Load())
	}

	close(release)
	if outcome := waitForReplyOutcome(t, firstOutcome); outcome.err != nil {
		t.Fatalf("first GenerateBotReply() error = %v", outcome.err)
	}
	if _, err := service.GenerateBotReply(context.Background(), secondCommand); err != nil {
		t.Fatalf("GenerateBotReply() after release error = %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("generator calls after release = %d, want 2", calls.Load())
	}
}

func TestReplyConcurrencyLimitReleasesAfterGenerateFailureAndCancellation(t *testing.T) {
	t.Run("provider failure", func(t *testing.T) {
		providerErr := errors.New("provider failed")
		var calls atomic.Int64
		generator := customerservice.ReplyGeneratorFunc(func(
			context.Context,
			customerservice.ReplyRequest,
		) (customerservice.Reply, error) {
			if calls.Add(1) == 1 {
				return customerservice.Reply{}, providerErr
			}
			return customerservice.Reply{Content: "recovered"}, nil
		})
		service := newReplyConcurrencyTestService(t, generator, nil, 1)
		first := receiveReplyConcurrencyMessage(t, service, "failure-first")
		second := receiveReplyConcurrencyMessage(t, service, "failure-second")

		_, err := service.GenerateBotReply(context.Background(), first)
		if !errors.Is(err, providerErr) || !errors.Is(err, customerservice.ErrReplyGenerationFailed) {
			t.Fatalf("first GenerateBotReply() error = %v", err)
		}
		if _, err := service.GenerateBotReply(context.Background(), second); err != nil {
			t.Fatalf("GenerateBotReply() after provider failure error = %v", err)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		started := make(chan struct{}, 1)
		var calls atomic.Int64
		generator := customerservice.ReplyGeneratorFunc(func(
			ctx context.Context,
			_ customerservice.ReplyRequest,
		) (customerservice.Reply, error) {
			if calls.Add(1) == 1 {
				started <- struct{}{}
				<-ctx.Done()
				return customerservice.Reply{}, ctx.Err()
			}
			return customerservice.Reply{Content: "after cancellation"}, nil
		})
		service := newReplyConcurrencyTestService(t, generator, nil, 1)
		first := receiveReplyConcurrencyMessage(t, service, "cancel-first")
		second := receiveReplyConcurrencyMessage(t, service, "cancel-second")
		ctx, cancel := context.WithCancel(context.Background())
		outcome := make(chan replyOutcome, 1)
		go func() {
			result, err := service.GenerateBotReply(ctx, first)
			outcome <- replyOutcome{result: result, err: err}
		}()
		waitForGeneratorStart(t, started)
		cancel()
		if result := waitForReplyOutcome(t, outcome); !errors.Is(result.err, context.Canceled) {
			t.Fatalf("cancelled GenerateBotReply() error = %v, want context.Canceled", result.err)
		}
		if _, err := service.GenerateBotReply(context.Background(), second); err != nil {
			t.Fatalf("GenerateBotReply() after cancellation error = %v", err)
		}
	})
}

func TestReplyConcurrencyLimitIsSharedByStreamingAndNonStreaming(t *testing.T) {
	generateStarted := make(chan struct{}, 1)
	releaseGenerate := make(chan struct{})
	generator := customerservice.ReplyGeneratorFunc(func(
		ctx context.Context,
		_ customerservice.ReplyRequest,
	) (customerservice.Reply, error) {
		select {
		case generateStarted <- struct{}{}:
		default:
		}
		select {
		case <-releaseGenerate:
			return customerservice.Reply{Content: "answer"}, nil
		case <-ctx.Done():
			return customerservice.Reply{}, ctx.Err()
		}
	})
	streamer := customerservice.ReplyStreamGeneratorFunc(func(
		ctx context.Context,
		_ customerservice.ReplyRequest,
	) (kitstream.Reader[customerservice.ReplyDelta], error) {
		return kitstream.NewReader(func() (customerservice.ReplyDelta, error) {
			<-ctx.Done()
			return customerservice.ReplyDelta{}, ctx.Err()
		}, nil), nil
	})
	service := newReplyConcurrencyTestService(t, generator, streamer, 1)
	generateCommand := receiveReplyConcurrencyMessage(t, service, "shared-generate")
	streamCommand := receiveReplyConcurrencyMessage(t, service, "shared-stream")
	afterStreamCommand := receiveReplyConcurrencyMessage(t, service, "shared-after-stream")

	generateOutcome := make(chan replyOutcome, 1)
	go func() {
		result, err := service.GenerateBotReply(context.Background(), generateCommand)
		generateOutcome <- replyOutcome{result: result, err: err}
	}()
	waitForGeneratorStart(t, generateStarted)
	if _, err := service.StreamBotReply(context.Background(), streamCommand); !errors.Is(
		err,
		customerservice.ErrReplyCapacityExceeded,
	) {
		t.Fatalf("StreamBotReply() while Generate active error = %v", err)
	}

	close(releaseGenerate)
	if outcome := waitForReplyOutcome(t, generateOutcome); outcome.err != nil {
		t.Fatalf("GenerateBotReply() error = %v", outcome.err)
	}
	stream, err := service.StreamBotReply(context.Background(), streamCommand)
	if err != nil {
		t.Fatalf("StreamBotReply() after Generate release error = %v", err)
	}
	if _, err := service.GenerateBotReply(context.Background(), afterStreamCommand); !errors.Is(
		err,
		customerservice.ErrReplyCapacityExceeded,
	) {
		t.Fatalf("GenerateBotReply() while stream active error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("stream Close() error = %v", err)
	}
	if _, err := service.GenerateBotReply(context.Background(), afterStreamCommand); err != nil {
		t.Fatalf("GenerateBotReply() after stream Close error = %v", err)
	}
}

func TestReplyConcurrencyLimitDoesNotChargePreGenerationOutcomes(t *testing.T) {
	holderStarted := make(chan struct{}, 1)
	releaseHolder := make(chan struct{})
	var calls atomic.Int64
	generator := customerservice.ReplyGeneratorFunc(func(
		ctx context.Context,
		request customerservice.ReplyRequest,
	) (customerservice.Reply, error) {
		calls.Add(1)
		if request.ConversationID != "conversation-capacity-holder" {
			return customerservice.Reply{Content: "persisted answer"}, nil
		}
		holderStarted <- struct{}{}
		select {
		case <-releaseHolder:
			return customerservice.Reply{Content: "holder answer"}, nil
		case <-ctx.Done():
			return customerservice.Reply{}, ctx.Err()
		}
	})
	service := newReplyConcurrencyTestService(t, generator, nil, 1)
	persistedCommand := receiveReplyConcurrencyMessage(t, service, "persisted")
	holderCommand := receiveReplyConcurrencyMessage(t, service, "capacity-holder")
	if _, err := service.GenerateBotReply(context.Background(), persistedCommand); err != nil {
		t.Fatalf("initial GenerateBotReply() error = %v", err)
	}

	holderOutcome := make(chan replyOutcome, 1)
	go func() {
		result, err := service.GenerateBotReply(context.Background(), holderCommand)
		holderOutcome <- replyOutcome{result: result, err: err}
	}()
	waitForGeneratorStart(t, holderStarted)

	duplicate, err := service.GenerateBotReply(context.Background(), persistedCommand)
	if err != nil || !duplicate.Duplicate {
		t.Fatalf("existing GenerateBotReply() = %#v, error %v", duplicate, err)
	}
	invalid := holderCommand
	invalid.TriggerMessageID = "missing-trigger"
	if _, err := service.GenerateBotReply(context.Background(), invalid); !errors.Is(
		err,
		customerservice.ErrInvalidReplyTrigger,
	) {
		t.Fatalf("invalid GenerateBotReply() error = %v, want ErrInvalidReplyTrigger", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("generator calls = %d, want persisted plus capacity holder", calls.Load())
	}

	close(releaseHolder)
	if outcome := waitForReplyOutcome(t, holderOutcome); outcome.err != nil {
		t.Fatalf("capacity holder GenerateBotReply() error = %v", outcome.err)
	}
}

func TestReplyConcurrencyLimitReleasesAtStreamTerminalBeforeClose(t *testing.T) {
	providerErr := errors.New("stream failed")
	tests := []struct {
		name    string
		suffix  string
		reader  kitstream.Reader[customerservice.ReplyDelta]
		wantErr error
	}{
		{
			name:   "EOF",
			suffix: "eof",
			reader: kitstream.FromSlice([]customerservice.ReplyDelta{
				{Content: "complete answer"},
			}),
		},
		{
			name:   "provider error",
			suffix: "provider-error",
			reader: kitstream.NewReader(func() (customerservice.ReplyDelta, error) {
				return customerservice.ReplyDelta{}, providerErr
			}, nil),
			wantErr: providerErr,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			streamer := customerservice.ReplyStreamGeneratorFunc(func(
				context.Context,
				customerservice.ReplyRequest,
			) (kitstream.Reader[customerservice.ReplyDelta], error) {
				return test.reader, nil
			})
			generator := customerservice.ReplyGeneratorFunc(func(
				context.Context,
				customerservice.ReplyRequest,
			) (customerservice.Reply, error) {
				return customerservice.Reply{Content: "next answer"}, nil
			})
			service := newReplyConcurrencyTestService(t, generator, streamer, 1)
			streamCommand := receiveReplyConcurrencyMessage(t, service, "terminal-stream-"+test.suffix)
			nextCommand := receiveReplyConcurrencyMessage(t, service, "terminal-next-"+test.suffix)
			stream, err := service.StreamBotReply(context.Background(), streamCommand)
			if err != nil {
				t.Fatal(err)
			}
			for {
				_, err = stream.Recv()
				if err != nil {
					break
				}
			}
			if test.wantErr == nil {
				if !errors.Is(err, io.EOF) {
					t.Fatalf("stream Recv() error = %v, want io.EOF", err)
				}
			} else if !errors.Is(err, test.wantErr) {
				t.Fatalf("stream Recv() error = %v, want %v", err, test.wantErr)
			}

			if _, err := service.GenerateBotReply(context.Background(), nextCommand); err != nil {
				t.Fatalf("GenerateBotReply() before stream Close error = %v", err)
			}
			closeDone := make(chan error, 1)
			go func() {
				closeDone <- stream.Close()
			}()
			select {
			case err := <-closeDone:
				if err != nil {
					t.Fatalf("stream Close() error = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("stream Close() blocked after terminal capacity release")
			}
			if err := stream.Close(); err != nil {
				t.Fatalf("second stream Close() error = %v", err)
			}
		})
	}
}

func TestReplyConcurrencyLimitReleasesAfterStreamInitializationFailure(t *testing.T) {
	providerErr := errors.New("stream initialization failed")
	var calls atomic.Int64
	streamer := customerservice.ReplyStreamGeneratorFunc(func(
		context.Context,
		customerservice.ReplyRequest,
	) (kitstream.Reader[customerservice.ReplyDelta], error) {
		if calls.Add(1) == 1 {
			return nil, providerErr
		}
		return kitstream.FromSlice([]customerservice.ReplyDelta{{Content: "recovered"}}), nil
	})
	service := newReplyConcurrencyTestService(t, nil, streamer, 1)
	first := receiveReplyConcurrencyMessage(t, service, "stream-init-first")
	second := receiveReplyConcurrencyMessage(t, service, "stream-init-second")

	_, err := service.StreamBotReply(context.Background(), first)
	if !errors.Is(err, providerErr) || !errors.Is(err, customerservice.ErrReplyGenerationFailed) {
		t.Fatalf("first StreamBotReply() error = %v", err)
	}
	reader, err := service.StreamBotReply(context.Background(), second)
	if err != nil {
		t.Fatalf("StreamBotReply() after initialization failure error = %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReplyConcurrencyLimitReleasesAfterNilStreamReader(t *testing.T) {
	var calls atomic.Int64
	streamer := customerservice.ReplyStreamGeneratorFunc(func(
		context.Context,
		customerservice.ReplyRequest,
	) (kitstream.Reader[customerservice.ReplyDelta], error) {
		if calls.Add(1) == 1 {
			return nil, nil
		}
		return kitstream.FromSlice([]customerservice.ReplyDelta{{Content: "recovered"}}), nil
	})
	service := newReplyConcurrencyTestService(t, nil, streamer, 1)
	first := receiveReplyConcurrencyMessage(t, service, "nil-stream-first")
	second := receiveReplyConcurrencyMessage(t, service, "nil-stream-second")

	_, err := service.StreamBotReply(context.Background(), first)
	if !errors.Is(err, customerservice.ErrReplyGenerationFailed) {
		t.Fatalf("first StreamBotReply() error = %v, want ErrReplyGenerationFailed", err)
	}
	reader, err := service.StreamBotReply(context.Background(), second)
	if err != nil {
		t.Fatalf("StreamBotReply() after nil reader error = %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReplyConcurrencyLimitDoesNotOversubscribe(t *testing.T) {
	const (
		maxConcurrent = 4
		totalRequests = 64
	)
	release := make(chan struct{})
	started := make(chan struct{}, maxConcurrent)
	var active atomic.Int64
	var maxActive atomic.Int64
	var calls atomic.Int64
	generator := customerservice.ReplyGeneratorFunc(func(
		ctx context.Context,
		_ customerservice.ReplyRequest,
	) (customerservice.Reply, error) {
		calls.Add(1)
		started <- struct{}{}
		current := active.Add(1)
		for {
			maximum := maxActive.Load()
			if current <= maximum || maxActive.CompareAndSwap(maximum, current) {
				break
			}
		}
		defer active.Add(-1)
		select {
		case <-release:
			return customerservice.Reply{Content: "answer"}, nil
		case <-ctx.Done():
			return customerservice.Reply{}, ctx.Err()
		}
	})
	service := newReplyConcurrencyTestService(t, generator, nil, maxConcurrent)
	commands := make([]customerservice.GenerateBotReplyCommand, totalRequests)
	for index := range totalRequests {
		commands[index] = receiveReplyConcurrencyMessage(t, service, fmt.Sprintf("load-%d", index))
	}

	start := make(chan struct{})
	outcomes := make(chan replyOutcome, totalRequests)
	var group sync.WaitGroup
	group.Add(totalRequests)
	for _, command := range commands {
		go func(command customerservice.GenerateBotReplyCommand) {
			defer group.Done()
			<-start
			result, err := service.GenerateBotReply(context.Background(), command)
			outcomes <- replyOutcome{result: result, err: err}
		}(command)
	}
	close(start)

	rejected := 0
	deadline := time.After(3 * time.Second)
	for rejected < totalRequests-maxConcurrent {
		select {
		case outcome := <-outcomes:
			if !errors.Is(outcome.err, customerservice.ErrReplyCapacityExceeded) {
				t.Fatalf("early outcome error = %v, want ErrReplyCapacityExceeded", outcome.err)
			}
			rejected++
		case <-deadline:
			t.Fatalf("received %d rejected outcomes, want %d", rejected, totalRequests-maxConcurrent)
		}
	}
	for range maxConcurrent {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for admitted model generation")
		}
	}
	if calls.Load() != maxConcurrent || maxActive.Load() > maxConcurrent {
		t.Fatalf(
			"generator calls = %d, max active = %d, limit = %d",
			calls.Load(),
			maxActive.Load(),
			maxConcurrent,
		)
	}

	close(release)
	group.Wait()
	for completed := 0; completed < maxConcurrent; completed++ {
		outcome := <-outcomes
		if outcome.err != nil {
			t.Fatalf("admitted GenerateBotReply() error = %v", outcome.err)
		}
	}
	if active.Load() != 0 {
		t.Fatalf("active generators after completion = %d, want 0", active.Load())
	}
}

func newReplyConcurrencyTestService(
	t *testing.T,
	generator customerservice.ReplyGenerator,
	streamer customerservice.ReplyStreamGenerator,
	maxConcurrent int,
) *customerservice.Service {
	t.Helper()
	options := []customerservice.Option{
		customerservice.WithMaxConcurrentReplies(maxConcurrent),
	}
	if generator != nil {
		options = append(options, customerservice.WithReplyGenerator(generator))
	}
	if streamer != nil {
		options = append(options, customerservice.WithReplyStreamGenerator(streamer))
	}
	service, err := customerservice.New(
		conversation.NewMemoryRepository(),
		&sequenceIDGenerator{},
		customerservice.ClockFunc(func() time.Time { return serviceBaseTime }),
		options...,
	)
	if err != nil {
		t.Fatalf("customerservice.New() error = %v", err)
	}
	return service
}

func receiveReplyConcurrencyMessage(
	t *testing.T,
	service *customerservice.Service,
	suffix string,
) customerservice.GenerateBotReplyCommand {
	t.Helper()
	command := validCommand()
	command.ConversationID = "conversation-" + suffix
	command.CustomerID = "customer-" + suffix
	command.ExternalMessageID = "external-" + suffix
	received, err := service.ReceiveMessage(context.Background(), command)
	if err != nil {
		t.Fatalf("ReceiveMessage() error = %v", err)
	}
	return customerservice.GenerateBotReplyCommand{
		ConversationID:   received.ConversationID,
		TriggerMessageID: received.MessageID,
	}
}
