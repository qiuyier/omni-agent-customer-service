package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

type testClient struct {
	service                   *customerservice.Service
	repository                *conversation.MemoryRepository
	receives, replies, models atomic.Int32
	loseReceive, loseReply    atomic.Bool
}

func newTestClient(t *testing.T) *testClient {
	t.Helper()
	c := &testClient{repository: conversation.NewMemoryRepository()}
	var ids atomic.Int32
	service, err := customerservice.New(c.repository,
		customerservice.MessageIDGeneratorFunc(func() (string, error) { return fmt.Sprintf("message-%d", ids.Add(1)), nil }),
		customerservice.ClockFunc(time.Now),
		customerservice.WithReplyGenerator(customerservice.ReplyGeneratorFunc(func(context.Context, customerservice.ReplyRequest) (customerservice.Reply, error) {
			c.models.Add(1)
			return customerservice.Reply{Content: "请从应用首页进入帮助中心。"}, nil
		})))
	if err != nil {
		t.Fatal(err)
	}
	c.service = service
	return c
}
func (c *testClient) ReceiveMessage(ctx context.Context, cmd customerservice.ReceiveMessageCommand) (customerservice.ReceiveMessageResult, error) {
	c.receives.Add(1)
	r, err := c.service.ReceiveMessage(ctx, cmd)
	if err == nil && c.loseReceive.Swap(false) {
		return customerservice.ReceiveMessageResult{}, fmt.Errorf("secret-provider-detail: %w", context.DeadlineExceeded)
	}
	return r, err
}
func (c *testClient) GenerateBotReply(ctx context.Context, cmd customerservice.GenerateBotReplyCommand) (customerservice.GenerateBotReplyResult, error) {
	c.replies.Add(1)
	r, err := c.service.GenerateBotReply(ctx, cmd)
	if err == nil && c.loseReply.Swap(false) {
		return customerservice.GenerateBotReplyResult{}, fmt.Errorf("secret-provider-detail: %w", context.DeadlineExceeded)
	}
	return r, err
}

type clientFuncs struct {
	receive func(context.Context, customerservice.ReceiveMessageCommand) (customerservice.ReceiveMessageResult, error)
	reply   func(context.Context, customerservice.GenerateBotReplyCommand) (customerservice.GenerateBotReplyResult, error)
}

func (c clientFuncs) ReceiveMessage(ctx context.Context, v customerservice.ReceiveMessageCommand) (customerservice.ReceiveMessageResult, error) {
	return c.receive(ctx, v)
}
func (c clientFuncs) GenerateBotReply(ctx context.Context, v customerservice.GenerateBotReplyCommand) (customerservice.GenerateBotReplyResult, error) {
	return c.reply(ctx, v)
}

type testSink struct {
	mu        sync.Mutex
	calls     int
	delivered map[string]Delivery
	loseAck   bool
}

func (s *testSink) Deliver(_ context.Context, d Delivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.delivered == nil {
		s.delivered = map[string]Delivery{}
	}
	if existing, ok := s.delivered[d.Key]; ok {
		if existing != d {
			return &CallError{Code: FailureConflict}
		}
		return nil
	}
	s.delivered[d.Key] = d
	if s.loseAck {
		s.loseAck = false
		return errors.New("secret-provider-detail")
	}
	return nil
}
func sampleEvent() Event {
	return Event{ConversationID: "42", CustomerID: "7", Channel: "web", ExternalMessageID: "event-1", Content: "帮助中心在哪里？"}
}

func TestAdmissionValidationAndCapacity(t *testing.T) {
	c := newTestClient(t)
	a := openTestAdapter(t, testConfig(t), c, &testSink{}, nil)
	for _, value := range []string{"", " leading", "line\nbreak", strings.Repeat("x", 257), string([]byte{0xff})} {
		e := sampleEvent()
		e.ExternalMessageID = value
		if _, err := a.Accept(context.Background(), e); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid ID accepted: %v", err)
		}
	}
	e := sampleEvent()
	e.Content = strings.Repeat("x", MaxContentBytes+1)
	if _, err := a.Accept(context.Background(), e); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
	for i := 0; i < MaxJobs; i++ {
		e = sampleEvent()
		e.ExternalMessageID = fmt.Sprintf("event-%d", i)
		if _, err := a.Accept(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	e.ExternalMessageID = "overflow"
	if _, err := a.Accept(context.Background(), e); !errors.Is(err, ErrFull) {
		t.Fatal(err)
	}
	e.ExternalMessageID = "event-0"
	if _, err := a.Accept(context.Background(), e); err != nil {
		t.Fatal("duplicate rejected at capacity", err)
	}
	if c.receives.Load() != 0 {
		t.Fatal("admission called service")
	}
}
func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{Directory: filepath.Join(t.TempDir(), "private"), SourceID: "source-a", DestinationID: "target-a", RetryDelay: time.Millisecond}
}
func openTestAdapter(t *testing.T, cfg Config, c Client, d Deliverer, now *time.Time) *Adapter {
	t.Helper()
	a, err := Open(cfg, c, d)
	if err != nil {
		t.Fatal(err)
	}
	if now != nil {
		a.now = func() time.Time { return *now }
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Error(err)
		}
	})
	return a
}

// Each reopen recreates the adapter and reloads its actual private JSON journal.
// The service and destination survive in the test process, as separate systems
// would. This is not a power-loss/fsync or full distributed-restart experiment.
func TestLostAcknowledgementsRecoverAfterReopen(t *testing.T) {
	for _, phase := range []string{"receive", "reply", "delivery"} {
		t.Run(phase, func(t *testing.T) {
			cfg := testConfig(t)
			c := newTestClient(t)
			sink := &testSink{}
			c.loseReceive.Store(phase == "receive")
			c.loseReply.Store(phase == "reply")
			sink.loseAck = phase == "delivery"
			now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
			a := openTestAdapter(t, cfg, c, sink, &now)
			job, err := a.Accept(context.Background(), sampleEvent())
			if err != nil {
				t.Fatal(err)
			}
			originalID := job.ID
			if c.receives.Load() != 0 {
				t.Fatal("admission called the service")
			}
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			a = openTestAdapter(t, cfg, c, sink, &now)
			failures := 0
			for step := 0; step < 8; step++ {
				now = now.Add(time.Second)
				job, err = a.Advance(context.Background(), originalID)
				if errors.Is(err, ErrAttemptFailed) {
					failures++
				} else if err != nil {
					t.Fatal(err)
				}
				if err := a.Close(); err != nil {
					t.Fatal(err)
				}
				a = openTestAdapter(t, cfg, c, sink, &now)
				if job.State == Completed {
					break
				}
			}
			if job.State != Completed || failures != 1 || job.ID != originalID {
				t.Fatalf("job=%+v failures=%d", job, failures)
			}
			duplicate, err := a.Accept(context.Background(), sampleEvent())
			if err != nil || duplicate != job {
				t.Fatal("duplicate reset progress", duplicate, err)
			}
			if _, err := a.Advance(context.Background(), job.ID); err != nil {
				t.Fatal(err)
			}
			wantReceives, wantReplies, wantDeliveries := int32(1), int32(1), 1
			if phase == "receive" {
				wantReceives = 2
			}
			if phase == "reply" {
				wantReplies = 2
			}
			if phase == "delivery" {
				wantDeliveries = 2
			}
			if c.receives.Load() != wantReceives || c.replies.Load() != wantReplies || c.models.Load() != 1 || sink.calls != wantDeliveries || len(sink.delivered) != 1 {
				t.Fatalf("receives=%d replies=%d models=%d delivery attempts=%d unique=%d", c.receives.Load(), c.replies.Load(), c.models.Load(), sink.calls, len(sink.delivered))
			}
			stored, err := c.repository.ListMessages(context.Background(), mapEvent(cfg.SourceID, sampleEvent()).ConversationID)
			if err != nil || len(stored) != 2 || stored[1].ID() != job.ReplyMessageID || stored[0].ID() != job.TriggerMessageID {
				t.Fatal(stored, err)
			}
			raw, err := os.ReadFile(filepath.Join(cfg.Directory, "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "secret-provider-detail") {
				t.Fatal("raw call error persisted")
			}
			info, err := os.Stat(filepath.Join(cfg.Directory, "state.json"))
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("journal permissions", info, err)
			}
		})
	}
}

func TestTwoSourcesWithIdenticalExternalIDsDoNotCollide(t *testing.T) {
	c := newTestClient(t)
	sink := &testSink{}
	var jobs []Job
	for _, source := range []string{"source-a", "source-b"} {
		cfg := testConfig(t)
		cfg.SourceID = source
		a := openTestAdapter(t, cfg, c, sink, nil)
		j, err := a.Accept(context.Background(), sampleEvent())
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			j, err = a.Advance(context.Background(), j.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		if j.State != Completed {
			t.Fatal(j)
		}
		jobs = append(jobs, j)
		messages, err := c.repository.ListMessages(context.Background(), mapEvent(source, sampleEvent()).ConversationID)
		if err != nil || len(messages) != 2 {
			t.Fatal(messages, err)
		}
	}
	if jobs[0].ID == jobs[1].ID || jobs[0].DeliveryKey == jobs[1].DeliveryKey || len(sink.delivered) != 2 || c.models.Load() != 2 {
		t.Fatal("sources collided")
	}
	a, b := mapEvent("source-a", sampleEvent()), mapEvent("source-b", sampleEvent())
	if a.ConversationID == b.ConversationID || a.CustomerID == b.CustomerID || a.Channel == b.Channel || a.ExternalMessageID == b.ExternalMessageID {
		t.Fatal("mapping not source-qualified")
	}
	if mappedID("tuple", "a:b", "c") == mappedID("tuple", "a", "b:c") {
		t.Fatal("ambiguous tuple")
	}
}

func TestConcurrentAdmissionAndProcessingAreSerialized(t *testing.T) {
	c := newTestClient(t)
	sink := &testSink{}
	a := openTestAdapter(t, testConfig(t), c, sink, nil)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.Accept(context.Background(), sampleEvent()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	jobs, err := a.List(context.Background())
	if err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.Advance(context.Background(), jobs[0].ID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if c.receives.Load() != 1 || c.models.Load() != 1 || sink.calls != 1 {
		t.Fatal("parallel calls bypassed serialization")
	}
	original := jobs[0].Event
	jobs[0].Event.Content = "mutated"
	actual, _ := a.List(context.Background())
	if actual[0].Event != original {
		t.Fatal("returned mutable state")
	}
	conflicting := sampleEvent()
	conflicting.ConversationID = "different"
	if _, err := a.Accept(context.Background(), conflicting); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func TestRetryBudgetBackoffAndSafeErrors(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxAttempts = 2
	calls := 0
	client := clientFuncs{receive: func(context.Context, customerservice.ReceiveMessageCommand) (customerservice.ReceiveMessageResult, error) {
		calls++
		return customerservice.ReceiveMessageResult{}, &CallError{Code: "secret-provider-detail", Retryable: true, RetryAfter: time.Minute}
	}}
	now := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)
	a := openTestAdapter(t, cfg, client, &testSink{}, &now)
	j, _ := a.Accept(context.Background(), sampleEvent())
	j, err := a.Advance(context.Background(), j.ID)
	if !errors.Is(err, ErrAttemptFailed) || j.LastFailure != FailureUnknown || j.Attempts != 1 || !j.NextAttemptAt.Equal(now.Add(time.Minute)) {
		t.Fatal(j, err)
	}
	if _, err := a.Advance(context.Background(), j.ID); !errors.Is(err, ErrNotReady) || calls != 1 {
		t.Fatal("ignored retry delay", err)
	}
	now = now.Add(time.Minute)
	j, err = a.Advance(context.Background(), j.ID)
	if !errors.Is(err, ErrNeedsAttention) || j.State != NeedsAttention || j.AttentionFrom != PendingReceive || j.Attempts != 2 {
		t.Fatal(j, err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openTestAdapter(t, cfg, client, &testSink{}, &now)
	if _, err := a.Advance(context.Background(), j.ID); !errors.Is(err, ErrNeedsAttention) || calls != 2 {
		t.Fatal("automatic retry after exhaustion")
	}
}

func TestInvalidResponsesAndManualStateStopBeforeDelivery(t *testing.T) {
	for _, name := range []string{"identity", "manual", "reply"} {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(t)
			sink := &testSink{}
			client := clientFuncs{receive: func(ctx context.Context, cmd customerservice.ReceiveMessageCommand) (customerservice.ReceiveMessageResult, error) {
				r, err := c.ReceiveMessage(ctx, cmd)
				if name == "identity" {
					r.ConversationID = "wrong"
				}
				if name == "manual" {
					r.Status = conversation.StatusWaitingHuman
					r.NextAction = customerservice.NextActionWaitForHuman
				}
				return r, err
			}, reply: func(context.Context, customerservice.GenerateBotReplyCommand) (customerservice.GenerateBotReplyResult, error) {
				return customerservice.GenerateBotReplyResult{MessageID: "wrong"}, nil
			}}
			a := openTestAdapter(t, testConfig(t), client, sink, nil)
			j, _ := a.Accept(context.Background(), sampleEvent())
			j, err := a.Advance(context.Background(), j.ID)
			if name == "reply" && err == nil {
				j, err = a.Advance(context.Background(), j.ID)
			}
			if !errors.Is(err, ErrNeedsAttention) || j.State != NeedsAttention || sink.calls != 0 {
				t.Fatal(j, err)
			}
			if name == "manual" && j.LastFailure != FailureManual {
				t.Fatal(j)
			}
		})
	}
}

func TestCloseCancelsActiveCallAndPreservesPendingState(t *testing.T) {
	cfg := testConfig(t)
	started := make(chan struct{})
	client := clientFuncs{receive: func(ctx context.Context, _ customerservice.ReceiveMessageCommand) (customerservice.ReceiveMessageResult, error) {
		close(started)
		<-ctx.Done()
		return customerservice.ReceiveMessageResult{}, ctx.Err()
	}}
	a := openTestAdapter(t, cfg, client, &testSink{}, nil)
	j, _ := a.Accept(context.Background(), sampleEvent())
	result := make(chan error, 1)
	go func() { _, err := a.Advance(context.Background(), j.ID); result <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("call not started")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrAttemptFailed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("call not canceled")
	}
	if _, err := a.List(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	reopened := openTestAdapter(t, cfg, newTestClient(t), &testSink{}, nil)
	jobs, _ := reopened.List(context.Background())
	if jobs[0].State != PendingReceive || jobs[0].LastFailure != FailureCancelled {
		t.Fatal(jobs)
	}
}

func TestJournalLockBindingAndCorruptData(t *testing.T) {
	cfg := testConfig(t)
	c := newTestClient(t)
	sink := &testSink{}
	a := openTestAdapter(t, cfg, c, sink, nil)
	if other, err := Open(cfg, c, sink); !errors.Is(err, ErrStateInUse) {
		if other != nil {
			other.Close()
		}
		t.Fatal(err)
	}
	a.Accept(context.Background(), sampleEvent())
	a.Close()
	otherCfg := cfg
	otherCfg.SourceID = "other-source"
	if other, err := Open(otherCfg, c, sink); !errors.Is(err, ErrStateInvalid) {
		if other != nil {
			other.Close()
		}
		t.Fatal(err)
	}
	path := filepath.Join(cfg.Directory, "state.json")
	raw, _ := os.ReadFile(path)
	var data snapshot
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	data.Jobs[0].ID = "corrupt"
	raw, _ = json.Marshal(data)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if other, err := Open(cfg, c, sink); !errors.Is(err, ErrStateInvalid) {
		if other != nil {
			other.Close()
		}
		t.Fatal(err)
	}
	if c.receives.Load() != 0 {
		t.Fatal("opening invalid state called external service")
	}
}

func TestFailedProgressSaveStopsCallsUntilReopen(t *testing.T) {
	cfg := testConfig(t)
	c := newTestClient(t)
	sink := &testSink{}
	path := filepath.Join(cfg.Directory, "state.json")
	backup := filepath.Join(cfg.Directory, "test-backup.json")
	inject := true
	client := clientFuncs{receive: c.ReceiveMessage, reply: func(ctx context.Context, cmd customerservice.GenerateBotReplyCommand) (customerservice.GenerateBotReplyResult, error) {
		r, err := c.GenerateBotReply(ctx, cmd)
		if err == nil && inject {
			inject = false
			if err := os.Rename(path, backup); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		}
		return r, err
	}}
	now := time.Now()
	a := openTestAdapter(t, cfg, client, sink, &now)
	j, _ := a.Accept(context.Background(), sampleEvent())
	if _, err := a.Advance(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Advance(context.Background(), j.ID); !errors.Is(err, ErrStore) {
		t.Fatal("save should fail", err)
	}
	if _, err := a.Advance(context.Background(), j.ID); !errors.Is(err, ErrStore) || c.replies.Load() != 1 || sink.calls != 0 {
		t.Fatal("poisoned adapter continued", err)
	}
	a.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, path); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	a = openTestAdapter(t, cfg, client, sink, &now)
	for i := 0; i < 2; i++ {
		var err error
		j, err = a.Advance(context.Background(), j.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if j.State != Completed || c.models.Load() != 1 || c.replies.Load() != 2 || sink.calls != 1 {
		t.Fatal("did not recover saved reply", j)
	}
}
