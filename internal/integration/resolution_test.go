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
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

func attentionFixture(t *testing.T, phase string) (Config, *Adapter, *testClient, *testSink, Job) {
	t.Helper()
	cfg := testConfig(t)
	cfg.MaxAttempts = 1
	c := newTestClient(t)
	sink := &testSink{loseAck: phase == "delivery"}
	c.loseReceive.Store(phase == "receive")
	c.loseReply.Store(phase == "reply")
	a := openTestAdapter(t, cfg, c, DelivererFunc(func(ctx context.Context, d Delivery) error {
		if err := sink.Deliver(ctx, d); err != nil {
			return context.DeadlineExceeded
		}
		return nil
	}), nil)
	j, err := a.Accept(context.Background(), sampleEvent())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		j, err = a.Advance(context.Background(), j.ID)
		if errors.Is(err, ErrNeedsAttention) {
			return cfg, a, c, sink, j
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("did not stop")
	return cfg, a, c, sink, j
}
func commandFor(j Job, action ResolutionAction) ResolutionCommand {
	return ResolutionCommand{JobID: j.ID, OperationID: fmt.Sprintf("op-%d-%s", j.Revision, action), ExpectedRevision: j.Revision, Action: action, ActorID: "operator-1", Reason: "reviewed the stopped job", EvidenceRef: "receipt-review-42"}
}
func advanceWhenDue(t *testing.T, a *Adapter, j Job) (Job, error) {
	t.Helper()
	now := j.NextAttemptAt.Add(time.Second)
	if now.Before(time.Now()) {
		now = time.Now().Add(time.Second)
	}
	a.now = func() time.Time { return now }
	return a.Advance(context.Background(), j.ID)
}

func TestConfirmDeliveredIsAuditedIdempotentAndDoesNotSend(t *testing.T) {
	cfg, a, c, sink, j := attentionFixture(t, "delivery")
	changeOwnership(t, c, mapEvent(cfg.SourceID, j.Event).ConversationID, "human")
	view, err := a.Inspect(context.Background(), j.ID)
	if err != nil || !view.Job.MayHaveDelivered || view.Job.DeliveryAttempts != 1 {
		t.Fatalf("%+v %v", view, err)
	}
	q := commandFor(j, ConfirmDelivered)
	result, err := a.Resolve(context.Background(), q)
	if err != nil || result.Job.State != Completed || result.Duplicate {
		t.Fatalf("%+v %v", result, err)
	}
	if result.Audit.Failure != FailureTimeout || result.Audit.FromStage != PendingDelivery || result.Audit.Revision != j.Revision+1 || sink.calls != 1 || c.models.Load() != 1 {
		t.Fatal(result)
	}
	if result.Job.DeliveryKey != j.DeliveryKey || result.Job.TriggerMessageID != j.TriggerMessageID {
		t.Fatal("IDs changed")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openTestAdapter(t, cfg, c, sink, nil)
	replay, err := a.Resolve(context.Background(), q)
	if err != nil || !replay.Duplicate || replay.Audit != result.Audit {
		t.Fatalf("%+v %v", replay, err)
	}
	altered := q
	altered.Reason = "changed"
	if _, err := a.Resolve(context.Background(), altered); !errors.Is(err, ErrOperationConflict) {
		t.Fatal(err)
	}
	view, _ = a.Inspect(context.Background(), j.ID)
	if len(view.Audit) != 1 {
		t.Fatal(view)
	}
	view.Audit[0].Command.Reason = "mutated copy"
	view.Job.Event.Content = "mutated copy"
	again, _ := a.Inspect(context.Background(), j.ID)
	if again.Audit[0].Command.Reason != q.Reason || again.Job.Event != j.Event {
		t.Fatal("snapshot alias")
	}
	duplicate, err := a.Accept(context.Background(), j.Event)
	if err != nil || duplicate.State != Completed {
		t.Fatal(duplicate, err)
	}
	if _, err := a.Advance(context.Background(), j.ID); err != nil || sink.calls != 1 {
		t.Fatal("terminal job called sink", err)
	}
}

func TestStopPreservesDedupAndFailure(t *testing.T) {
	cfg, a, c, sink, j := attentionFixture(t, "receive")
	q := commandFor(j, StopProcessing)
	result, err := a.Resolve(context.Background(), q)
	if err != nil || result.Job.State != Stopped || result.Job.LastFailure != j.LastFailure || result.Job.AttentionFrom != PendingReceive {
		t.Fatal(result, err)
	}
	a.Close()
	a = openTestAdapter(t, cfg, c, sink, nil)
	duplicate, err := a.Accept(context.Background(), j.Event)
	if err != nil || duplicate.State != Stopped {
		t.Fatal(duplicate, err)
	}
	if _, err := a.Advance(context.Background(), j.ID); err != nil || c.receives.Load() != 1 || sink.calls != 0 {
		t.Fatal(err)
	}
	if _, err := a.Resolve(context.Background(), commandFor(duplicate, RetryOnce)); !errors.Is(err, ErrResolutionNotAllowed) {
		t.Fatal(err)
	}
}

func TestManualRetryPreservesStageIDsAndCounts(t *testing.T) {
	for _, phase := range []string{"receive", "reply", "delivery"} {
		t.Run(phase, func(t *testing.T) {
			cfg, a, c, sink, j := attentionFixture(t, phase)
			before := j
			q := commandFor(j, RetryOnce)
			result, err := a.Resolve(context.Background(), q)
			if err != nil || result.Job.State != before.AttentionFrom || result.Job.Attempts != before.Attempts || result.Job.TotalAttempts != before.TotalAttempts || !result.Job.ManualAttempt {
				t.Fatal(result, err)
			}
			a.Close()
			a = openTestAdapter(t, cfg, c, sink, nil)
			j = result.Job
			for i := 0; i < 3 && j.State != Completed; i++ {
				j, err = advanceWhenDue(t, a, j)
				if err != nil {
					t.Fatal(j, err)
				}
			}
			if j.State != Completed || j.ManualRetries != 1 || j.TotalAttempts != 4 || len(sink.delivered) != 1 || c.models.Load() != 1 {
				t.Fatalf("%+v", j)
			}
			if j.ID != before.ID || (before.DeliveryKey != "" && j.DeliveryKey != before.DeliveryKey) {
				t.Fatal("changed idempotency key")
			}
			replay, err := a.Resolve(context.Background(), q)
			if err != nil || !replay.Duplicate || replay.Job.State != Completed {
				t.Fatal(replay, err)
			}
		})
	}
}

func TestManualRetryRechecksEligibility(t *testing.T) {
	cfg, a, c, sink, j := attentionFixture(t, "delivery")
	result, err := a.Resolve(context.Background(), commandFor(j, RetryOnce))
	if err != nil {
		t.Fatal(err)
	}
	changeOwnership(t, c, mapEvent(cfg.SourceID, j.Event).ConversationID, "closed")
	j, err = advanceWhenDue(t, a, result.Job)
	if !errors.Is(err, ErrNeedsAttention) || j.LastFailure != FailureDeliveryClosed || sink.calls != 1 || !j.MayHaveDelivered {
		t.Fatal(j, err)
	}
	if _, err := a.Resolve(context.Background(), commandFor(j, RetryOnce)); !errors.Is(err, ErrResolutionNotAllowed) {
		t.Fatal(err)
	}
}

func TestManualRetryBudgetAndRetryAfter(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxAttempts = 1
	calls := 0
	client := clientFuncs{receive: func(context.Context, customerservice.ReceiveMessageCommand) (customerservice.ReceiveMessageResult, error) {
		calls++
		return customerservice.ReceiveMessageResult{}, &CallError{Code: FailureBusy, Retryable: true, RetryAfter: time.Hour}
	}}
	now := time.Now()
	a := openTestAdapter(t, cfg, client, &testSink{}, &now)
	j, _ := a.Accept(context.Background(), sampleEvent())
	j, err := a.Advance(context.Background(), j.ID)
	if !errors.Is(err, ErrNeedsAttention) {
		t.Fatal(err)
	}
	for i := 0; i < MaxManualRetries; i++ {
		result, err := a.Resolve(context.Background(), commandFor(j, RetryOnce))
		if err != nil {
			t.Fatal(err)
		}
		if result.Job.NextAttemptAt.Before(j.RetryNotBefore) || result.Job.Attempts != i+1 {
			t.Fatal(result)
		}
		if _, err := a.Advance(context.Background(), j.ID); !errors.Is(err, ErrNotReady) {
			t.Fatal("ignored cooldown", err)
		}
		a.Close()
		a = openTestAdapter(t, cfg, client, &testSink{}, &now)
		now = result.Job.NextAttemptAt.Add(time.Second)
		j, err = a.Advance(context.Background(), j.ID)
		if !errors.Is(err, ErrNeedsAttention) || j.Attempts != i+2 {
			t.Fatal(j, err)
		}
	}
	if _, err := a.Resolve(context.Background(), commandFor(j, RetryOnce)); !errors.Is(err, ErrResolutionNotAllowed) || calls != 4 {
		t.Fatal(err, calls)
	}
	view, _ := a.Inspect(context.Background(), j.ID)
	if len(view.Audit) != MaxManualRetries || j.TotalAttempts != 4 {
		t.Fatal(view)
	}
}

func TestOperatorValidationAndConcurrentStaleDecisions(t *testing.T) {
	_, a, _, _, j := attentionFixture(t, "receive")
	for _, mutate := range []func(*ResolutionCommand){func(q *ResolutionCommand) { q.ActorID = "" }, func(q *ResolutionCommand) { q.EvidenceRef = "" }, func(q *ResolutionCommand) { q.Reason = strings.Repeat("x", MaxResolutionTextBytes+1) }, func(q *ResolutionCommand) { q.ExpectedRevision = 0 }} {
		q := commandFor(j, StopProcessing)
		mutate(&q)
		if _, err := a.Resolve(context.Background(), q); !errors.Is(err, ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	if _, err := a.Resolve(context.Background(), commandFor(j, ConfirmDelivered)); !errors.Is(err, ErrResolutionNotAllowed) {
		t.Fatal("confirmed before delivery", err)
	}
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			q := commandFor(j, StopProcessing)
			q.OperationID = fmt.Sprintf("concurrent-%d", n)
			_, err := a.Resolve(context.Background(), q)
			out <- err
		}(i)
	}
	wg.Wait()
	close(out)
	success, stale := 0, 0
	for err := range out {
		if err == nil {
			success++
		} else if errors.Is(err, ErrStaleRevision) {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || stale != 1 {
		t.Fatal(success, stale)
	}
	view, _ := a.Inspect(context.Background(), j.ID)
	if len(view.Audit) != 1 {
		t.Fatal(view)
	}
}

func TestCheckOnlyFailureCannotBeConfirmedDelivered(t *testing.T) {
	c := newTestClient(t)
	sink := &testSink{}
	_, a, j := pendingDelivery(t, c, sink)
	a.cfg.MaxAttempts = 1
	a.client = checkedClient{Client: c, check: func(context.Context, customerservice.DeliveryCheck) (customerservice.DeliveryEligibility, error) {
		return customerservice.DeliveryEligibility{}, context.DeadlineExceeded
	}}
	j, err := a.Advance(context.Background(), j.ID)
	if !errors.Is(err, ErrNeedsAttention) || j.MayHaveDelivered || j.DeliveryAttempts != 0 {
		t.Fatal(j, err)
	}
	if _, err := a.Resolve(context.Background(), commandFor(j, ConfirmDelivered)); !errors.Is(err, ErrResolutionNotAllowed) || sink.calls != 0 {
		t.Fatal(err)
	}
}

func TestResolutionSaveFailureIsAtomicAndPoisonsAdapter(t *testing.T) {
	cfg, a, c, sink, j := attentionFixture(t, "delivery")
	q := commandFor(j, ConfirmDelivered)
	path := filepath.Join(cfg.Directory, "state.json")
	backup := filepath.Join(cfg.Directory, "test-backup.json")
	if err := os.Rename(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Resolve(context.Background(), q); !errors.Is(err, ErrStore) {
		t.Fatal(err)
	}
	if _, err := a.Inspect(context.Background(), j.ID); !errors.Is(err, ErrStore) {
		t.Fatal(err)
	}
	a.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, path); err != nil {
		t.Fatal(err)
	}
	a = openTestAdapter(t, cfg, c, sink, nil)
	view, err := a.Inspect(context.Background(), j.ID)
	beforeJSON, _ := json.Marshal(j)
	afterJSON, _ := json.Marshal(view.Job)
	if err != nil || len(view.Audit) != 0 || string(beforeJSON) != string(afterJSON) {
		t.Fatal(view, err)
	}
	if _, err := a.Resolve(context.Background(), q); err != nil || sink.calls != 1 {
		t.Fatal(err)
	}
}

func TestLegacyMigrationConservativelyPreservesDeliveryUncertainty(t *testing.T) {
	cfg, a, c, sink, j := attentionFixture(t, "delivery")
	a.Close()
	path := filepath.Join(cfg.Directory, "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["version"] = float64(1)
	delete(document, "audit")
	legacy := document["jobs"].([]any)[0].(map[string]any)
	for _, key := range []string{"revision", "total_attempts", "delivery_attempts", "may_have_delivered", "legacy_history", "recovery_allowed", "manual_attempt", "manual_in_flight", "manual_retries", "retry_not_before"} {
		delete(legacy, key)
	}
	raw, _ = json.Marshal(document)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	a = openTestAdapter(t, cfg, c, sink, nil)
	view, err := a.Inspect(context.Background(), j.ID)
	if err != nil || !view.Job.LegacyHistory || !view.Job.MayHaveDelivered || view.Job.RecoveryAllowed || view.Job.DeliveryKey != j.DeliveryKey {
		t.Fatal(view, err)
	}
	if _, err := a.Resolve(context.Background(), commandFor(view.Job, RetryOnce)); !errors.Is(err, ErrResolutionNotAllowed) {
		t.Fatal(err)
	}
	if _, err := a.Resolve(context.Background(), commandFor(view.Job, ConfirmDelivered)); err != nil {
		t.Fatal(err)
	}
	a.Close()
	raw, _ = os.ReadFile(path)
	var state snapshot
	if err := json.Unmarshal(raw, &state); err != nil || state.Version != 2 || len(state.Audit) != 1 {
		t.Fatal(state, err)
	}
	// Corrupt audit data must fail closed on reopen.
	state.Audit[0].Revision++
	raw, _ = json.Marshal(state)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if other, err := Open(cfg, c, sink); !errors.Is(err, ErrStateInvalid) {
		if other != nil {
			other.Close()
		}
		t.Fatal(err)
	}
}

func TestManualAttemptCrashCannotStartAutomaticBatch(t *testing.T) {
	cfg, a, c, sink, j := attentionFixture(t, "receive")
	result, err := a.Resolve(context.Background(), commandFor(j, RetryOnce))
	if err != nil {
		t.Fatal(err)
	}
	// Persist exactly the intent that would precede the single manual call,
	// then simulate loss of its result. No unsafe process termination is needed.
	j = result.Job
	j.ManualAttempt = false
	j.ManualInFlight = true
	j.Attempts++
	j.TotalAttempts++
	if _, err := a.saveJob(a.index(j.ID), j, nil); err != nil {
		t.Fatal(err)
	}
	a.Close()
	cfg.MaxAttempts = 10
	now := j.NextAttemptAt.Add(time.Second)
	a = openTestAdapter(t, cfg, c, sink, &now)
	j, err = a.Advance(context.Background(), j.ID)
	if !errors.Is(err, ErrNeedsAttention) || j.ManualInFlight || c.receives.Load() != 1 {
		t.Fatal(j, err)
	}
}

func TestLegacyPendingExhaustedDoesNotInferRecoveryPermission(t *testing.T) {
	cfg, a, c, sink, j := attentionFixture(t, "delivery")
	a.Close()
	legacyJob := Job{ID: j.ID, Event: j.Event, State: PendingDelivery, TriggerMessageID: j.TriggerMessageID, ReplyMessageID: j.ReplyMessageID,
		ReplyContent: j.ReplyContent, DeliveryKey: j.DeliveryKey, Attempts: j.Attempts, LastFailure: j.LastFailure}
	legacy := snapshot{Version: 1, SourceID: cfg.SourceID, DestinationID: cfg.DestinationID, Jobs: []Job{legacyJob}}
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(cfg.Directory, "state.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	a = openTestAdapter(t, cfg, c, sink, nil)
	j, err := a.Advance(context.Background(), j.ID)
	if !errors.Is(err, ErrNeedsAttention) || j.RecoveryAllowed || !j.LegacyHistory || !j.MayHaveDelivered || sink.calls != 1 {
		t.Fatal(j, err)
	}
	if _, err := a.Resolve(context.Background(), commandFor(j, RetryOnce)); !errors.Is(err, ErrResolutionNotAllowed) {
		t.Fatal(err)
	}
}

func TestDeliveryIntentMustBeDurableBeforeSink(t *testing.T) {
	c := newTestClient(t)
	sink := &testSink{}
	cfg, a, j := pendingDelivery(t, c, sink)
	path := filepath.Join(cfg.Directory, "state.json")
	backup := filepath.Join(cfg.Directory, "test-backup.json")
	a.client = checkedClient{Client: c, check: func(ctx context.Context, q customerservice.DeliveryCheck) (customerservice.DeliveryEligibility, error) {
		result, err := c.service.CheckDeliveryEligibility(ctx, q)
		if err := os.Rename(path, backup); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		return result, err
	}}
	if _, err := a.Advance(context.Background(), j.ID); !errors.Is(err, ErrStore) || sink.calls != 0 {
		t.Fatal(err)
	}
	a.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backup, path); err != nil {
		t.Fatal(err)
	}
	a = openTestAdapter(t, cfg, c, sink, nil)
	view, err := a.Inspect(context.Background(), j.ID)
	if err != nil || view.Job.MayHaveDelivered || view.Job.DeliveryAttempts != 0 {
		t.Fatal(view, err)
	}
}
