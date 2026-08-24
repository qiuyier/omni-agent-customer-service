package customerservice_test

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	kitstream "github.com/qiuyier/omni-agent-kit/stream"
)

func TestStreamBotReplyEmitsDeltasThenPersistsCompletedReply(t *testing.T) {
	var sourceCloseCalls atomic.Int64
	streamer := &replyStreamerStub{reader: replyDeltaReader(
		[]customerservice.ReplyDelta{{Content: "Your order "}, {Content: "is on the way."}},
		&sourceCloseCalls,
	)}
	service, repository, ids := newStreamReplyTestService(t, streamer)
	received, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatal(err)
	}

	reader, err := service.StreamBotReply(context.Background(), customerservice.GenerateBotReplyCommand{
		ConversationID:   received.ConversationID,
		TriggerMessageID: received.MessageID,
	})
	if err != nil {
		t.Fatalf("StreamBotReply() error = %v", err)
	}
	first := receiveStreamEvent(t, reader)
	second := receiveStreamEvent(t, reader)
	completed := receiveStreamEvent(t, reader)
	if first.Type != customerservice.BotReplyStreamEventDelta || first.Delta != "Your order " {
		t.Fatalf("first event = %#v", first)
	}
	if second.Type != customerservice.BotReplyStreamEventDelta || second.Delta != "is on the way." {
		t.Fatalf("second event = %#v", second)
	}
	if completed.Type != customerservice.BotReplyStreamEventCompleted ||
		completed.Result.MessageID != "message-2" ||
		completed.Result.Content != "Your order is on the way." ||
		completed.Result.Duplicate {
		t.Fatalf("completed event = %#v", completed)
	}
	assertStreamEOF(t, reader)
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if sourceCloseCalls.Load() != 1 {
		t.Fatalf("source Close calls = %d, want 1", sourceCloseCalls.Load())
	}
	if streamer.calls != 1 || streamer.request.TriggerMessageID != received.MessageID {
		t.Fatalf("streamer calls = %d, request = %#v", streamer.calls, streamer.request)
	}
	if ids.calls.Load() != 2 {
		t.Fatalf("ID generator calls = %d, want 2", ids.calls.Load())
	}
	messages := listMessages(t, repository, received.ConversationID)
	if len(messages) != 2 || messages[1].Sender() != conversation.SenderBot ||
		messages[1].Content() != completed.Result.Content {
		t.Fatalf("stored messages = %#v", messages)
	}

	duplicateReader, err := service.StreamBotReply(context.Background(), customerservice.GenerateBotReplyCommand{
		ConversationID:   received.ConversationID,
		TriggerMessageID: received.MessageID,
	})
	if err != nil {
		t.Fatal(err)
	}
	duplicate := receiveStreamEvent(t, duplicateReader)
	if duplicate.Type != customerservice.BotReplyStreamEventCompleted ||
		!duplicate.Result.Duplicate ||
		duplicate.Result.MessageID != completed.Result.MessageID ||
		duplicate.Result.Content != completed.Result.Content {
		t.Fatalf("duplicate event = %#v", duplicate)
	}
	assertStreamEOF(t, duplicateReader)
	if err := duplicateReader.Close(); err != nil {
		t.Fatal(err)
	}
	if streamer.calls != 1 {
		t.Fatalf("streamer calls after duplicate = %d, want 1", streamer.calls)
	}
}

func TestStreamBotReplyEmitsSupersededAndDoesNotPersistTemporaryContent(t *testing.T) {
	streamer := &replyStreamerStub{reader: replyDeltaReader(
		[]customerservice.ReplyDelta{{Content: "temporary answer"}},
		nil,
	)}
	service, repository, _ := newStreamReplyTestService(t, streamer)
	firstMessage, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatal(err)
	}
	reader, err := service.StreamBotReply(context.Background(), customerservice.GenerateBotReplyCommand{
		ConversationID:   firstMessage.ConversationID,
		TriggerMessageID: firstMessage.MessageID,
	})
	if err != nil {
		t.Fatal(err)
	}
	delta := receiveStreamEvent(t, reader)
	if delta.Type != customerservice.BotReplyStreamEventDelta {
		t.Fatalf("first event = %#v, want delta", delta)
	}

	newer := validCommand()
	newer.ExternalMessageID = "external-2"
	newer.Content = "I also need to change the address"
	if _, err := service.ReceiveMessage(context.Background(), newer); err != nil {
		t.Fatal(err)
	}

	superseded := receiveStreamEvent(t, reader)
	if superseded.Type != customerservice.BotReplyStreamEventSuperseded ||
		superseded.Result.ConversationID != firstMessage.ConversationID ||
		superseded.Result.TriggerMessageID != firstMessage.MessageID ||
		superseded.Result.MessageID != "" {
		t.Fatalf("superseded event = %#v", superseded)
	}
	assertStreamEOF(t, reader)
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	messages := listMessages(t, repository, firstMessage.ConversationID)
	if len(messages) != 2 {
		t.Fatalf("stored messages = %d, want only two customer messages", len(messages))
	}
	for _, message := range messages {
		if message.Sender() == conversation.SenderBot {
			t.Fatalf("temporary reply was persisted: %#v", message)
		}
	}
}

func TestStreamBotReplyCloseCancelsGenerationWithoutPersisting(t *testing.T) {
	var sourceCloseCalls atomic.Int64
	var generationContext context.Context
	streamer := customerservice.ReplyStreamGeneratorFunc(func(
		ctx context.Context,
		_ customerservice.ReplyRequest,
	) (kitstream.Reader[customerservice.ReplyDelta], error) {
		generationContext = ctx
		return replyDeltaReader(
			[]customerservice.ReplyDelta{{Content: "temporary"}, {Content: "never read"}},
			&sourceCloseCalls,
		), nil
	})
	service, repository, _ := newStreamReplyTestService(t, streamer)
	received, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatal(err)
	}
	reader, err := service.StreamBotReply(context.Background(), customerservice.GenerateBotReplyCommand{
		ConversationID:   received.ConversationID,
		TriggerMessageID: received.MessageID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if event := receiveStreamEvent(t, reader); event.Type != customerservice.BotReplyStreamEventDelta {
		t.Fatalf("event = %#v, want delta", event)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if sourceCloseCalls.Load() != 1 {
		t.Fatalf("source Close calls = %d, want 1", sourceCloseCalls.Load())
	}
	select {
	case <-generationContext.Done():
	case <-time.After(time.Second):
		t.Fatal("generation context was not cancelled by Close")
	}
	if messages := listMessages(t, repository, received.ConversationID); len(messages) != 1 {
		t.Fatalf("stored messages = %d, want only customer trigger", len(messages))
	}
}

func TestStreamBotReplyPropagatesGenerationFailuresAndEmptyReplies(t *testing.T) {
	providerErr := errors.New("provider token=secret")
	tests := []struct {
		name    string
		reader  kitstream.Reader[customerservice.ReplyDelta]
		wantErr error
	}{
		{
			name: "provider read error",
			reader: kitstream.NewReader(func() (customerservice.ReplyDelta, error) {
				return customerservice.ReplyDelta{}, providerErr
			}, nil),
			wantErr: providerErr,
		},
		{
			name:    "empty stream",
			reader:  kitstream.FromSlice([]customerservice.ReplyDelta(nil)),
			wantErr: customerservice.ErrEmptyReply,
		},
		{
			name: "blank reply",
			reader: kitstream.FromSlice([]customerservice.ReplyDelta{
				{Content: "  "},
				{Content: "\n"},
			}),
			wantErr: customerservice.ErrEmptyReply,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, repository, _ := newStreamReplyTestService(t, &replyStreamerStub{reader: test.reader})
			received, err := service.ReceiveMessage(context.Background(), validCommand())
			if err != nil {
				t.Fatal(err)
			}
			reader, err := service.StreamBotReply(context.Background(), customerservice.GenerateBotReplyCommand{
				ConversationID:   received.ConversationID,
				TriggerMessageID: received.MessageID,
			})
			if err != nil {
				t.Fatal(err)
			}
			for {
				_, err = reader.Recv()
				if err != nil {
					break
				}
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Recv() error = %v, want %v", err, test.wantErr)
			}
			if errors.Is(test.wantErr, providerErr) &&
				!errors.Is(err, customerservice.ErrReplyGenerationFailed) {
				t.Fatalf("Recv() error = %v, want ErrReplyGenerationFailed", err)
			}
			if closeErr := reader.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if messages := listMessages(t, repository, received.ConversationID); len(messages) != 1 {
				t.Fatalf("stored messages = %d, want only customer trigger", len(messages))
			}
		})
	}
}

func TestStreamBotReplyAppliesOverallGenerationTimeout(t *testing.T) {
	const timeout = 50 * time.Millisecond
	streamer := customerservice.ReplyStreamGeneratorFunc(func(
		ctx context.Context,
		_ customerservice.ReplyRequest,
	) (kitstream.Reader[customerservice.ReplyDelta], error) {
		return kitstream.NewReader(func() (customerservice.ReplyDelta, error) {
			<-ctx.Done()
			return customerservice.ReplyDelta{}, ctx.Err()
		}, nil), nil
	})
	repository := conversation.NewMemoryRepository()
	service, err := customerservice.New(
		repository,
		&sequenceIDGenerator{},
		customerservice.ClockFunc(func() time.Time { return serviceBaseTime }),
		customerservice.WithReplyStreamGenerator(streamer),
		customerservice.WithReplyGenerationTimeout(timeout),
	)
	if err != nil {
		t.Fatal(err)
	}
	received, err := service.ReceiveMessage(context.Background(), validCommand())
	if err != nil {
		t.Fatal(err)
	}
	reader, err := service.StreamBotReply(context.Background(), customerservice.GenerateBotReplyCommand{
		ConversationID:   received.ConversationID,
		TriggerMessageID: received.MessageID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = reader.Recv()
	if !errors.Is(err, context.DeadlineExceeded) ||
		!errors.Is(err, customerservice.ErrReplyGenerationFailed) {
		t.Fatalf("Recv() error = %v, want deadline and generation failure", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if messages := listMessages(t, repository, received.ConversationID); len(messages) != 1 {
		t.Fatalf("stored messages = %d, want only customer trigger", len(messages))
	}
}

type replyStreamerStub struct {
	calls   int
	request customerservice.ReplyRequest
	reader  kitstream.Reader[customerservice.ReplyDelta]
	err     error
}

func (s *replyStreamerStub) Stream(
	_ context.Context,
	request customerservice.ReplyRequest,
) (kitstream.Reader[customerservice.ReplyDelta], error) {
	s.calls++
	s.request = request
	return s.reader, s.err
}

func newStreamReplyTestService(
	t *testing.T,
	streamer customerservice.ReplyStreamGenerator,
) (*customerservice.Service, *conversation.MemoryRepository, *sequenceIDGenerator) {
	t.Helper()
	repository := conversation.NewMemoryRepository()
	ids := &sequenceIDGenerator{}
	service, err := customerservice.New(
		repository,
		ids,
		customerservice.ClockFunc(func() time.Time { return serviceBaseTime }),
		customerservice.WithReplyStreamGenerator(streamer),
	)
	if err != nil {
		t.Fatalf("customerservice.New() error = %v", err)
	}
	return service, repository, ids
}

func replyDeltaReader(
	deltas []customerservice.ReplyDelta,
	closeCalls *atomic.Int64,
) kitstream.Reader[customerservice.ReplyDelta] {
	index := 0
	return kitstream.NewReader(func() (customerservice.ReplyDelta, error) {
		if index == len(deltas) {
			return customerservice.ReplyDelta{}, io.EOF
		}
		delta := deltas[index]
		index++
		return delta, nil
	}, func() error {
		if closeCalls != nil {
			closeCalls.Add(1)
		}
		return nil
	})
}

func receiveStreamEvent(
	t *testing.T,
	reader kitstream.Reader[customerservice.BotReplyStreamEvent],
) customerservice.BotReplyStreamEvent {
	t.Helper()
	event, err := reader.Recv()
	if err != nil {
		t.Fatalf("Recv() error = %v", err)
	}
	return event
}

func assertStreamEOF(
	t *testing.T,
	reader kitstream.Reader[customerservice.BotReplyStreamEvent],
) {
	t.Helper()
	_, err := reader.Recv()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("Recv() error = %v, want io.EOF", err)
	}
}
