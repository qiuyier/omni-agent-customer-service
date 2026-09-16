package integration

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

// Adapter serializes admission and one-step processing. Open holds an OS lock
// for the entire lifetime; do not share this local journal over a network FS.
type Adapter struct {
	cfg       Config
	client    Client
	deliverer Deliverer
	journal   *journal
	data      snapshot
	gate      chan struct{}
	lifetime  context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
	closeErr  error
	poisoned  bool
	now       func() time.Time
}

func Open(cfg Config, client Client, deliverer Deliverer) (*Adapter, error) {
	if client == nil || deliverer == nil || cfg.Directory == "" || !validID(cfg.SourceID) || !validID(cfg.DestinationID) {
		return nil, ErrInvalidInput
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.AttemptTimeout == 0 {
		cfg.AttemptTimeout = 90 * time.Second
	}
	if cfg.RetryDelay == 0 {
		cfg.RetryDelay = time.Second
	}
	if cfg.MaxAttempts < 1 || cfg.MaxAttempts > 10 || cfg.AttemptTimeout <= 0 ||
		cfg.AttemptTimeout > 5*time.Minute || cfg.RetryDelay <= 0 || cfg.RetryDelay > maxRetryDelay {
		return nil, ErrInvalidInput
	}
	journal, data, err := openJournal(cfg)
	if err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(context.Background())
	a := &Adapter{cfg: cfg, client: client, deliverer: deliverer, journal: journal,
		data: data, gate: make(chan struct{}, 1), lifetime: lifetime, cancel: cancel, now: time.Now}
	a.gate <- struct{}{}
	return a, nil
}

func (a *Adapter) take(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidInput
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.lifetime.Done():
		return ErrClosed
	case <-a.gate:
	}
	if a.lifetime.Err() != nil {
		a.release()
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		a.release()
		return err
	}
	if a.poisoned {
		a.release()
		return ErrStore
	}
	return nil
}
func (a *Adapter) release() { a.gate <- struct{}{} }

func (a *Adapter) Close() error {
	a.closeOnce.Do(func() {
		a.cancel()
		<-a.gate
		a.closeErr = a.journal.close()
		a.release()
	})
	return a.closeErr
}

// Accept durably records an automatic-reply intent before any external call.
func (a *Adapter) Accept(ctx context.Context, event Event) (Job, error) {
	if err := a.take(ctx); err != nil {
		return Job{}, err
	}
	defer a.release()
	if !validEvent(event) {
		return Job{}, ErrInvalidInput
	}
	id := jobID(a.cfg.SourceID, event)
	if index := a.index(id); index >= 0 {
		existing := a.data.Jobs[index]
		if existing.Event != event {
			return Job{}, ErrConflict
		}
		return existing, nil
	}
	if len(a.data.Jobs) >= MaxJobs {
		return Job{}, ErrFull
	}
	j := Job{ID: id, Event: event, State: PendingReceive, Revision: 1}
	next := a.data
	next.Jobs = append(append([]Job{}, a.data.Jobs...), j)
	if err := a.commit(next); err != nil {
		return Job{}, err
	}
	return j, nil
}

// List returns owned value snapshots, including attention/completed records.
// Completed records are retained to preserve inbound deduplication.
func (a *Adapter) List(ctx context.Context) ([]Job, error) {
	if err := a.take(ctx); err != nil {
		return nil, err
	}
	defer a.release()
	return append([]Job{}, a.data.Jobs...), nil
}

// Advance performs one stage; delivery checks eligibility then calls the sink.
// No eligibility is cached between attempts. No background goroutine,
// implicit retry loop, or public inbound endpoint is installed.
func (a *Adapter) Advance(ctx context.Context, id string) (Job, error) {
	if err := a.take(ctx); err != nil {
		return Job{}, err
	}
	defer a.release()
	index := a.index(id)
	if index < 0 {
		return Job{}, ErrNotFound
	}
	j := a.data.Jobs[index]
	if j.State == Completed || j.State == Stopped {
		return j, nil
	}
	if j.State == NeedsAttention {
		return j, ErrNeedsAttention
	}
	if a.now().Before(j.NextAttemptAt) {
		return j, ErrNotReady
	}
	if j.ManualInFlight {
		// A crash consumed the single granted call. Never restart an automatic
		// retry batch, even if startup configuration changed in the meantime.
		j.ManualInFlight = false
		j.AttentionFrom, j.State, j.LastFailure = j.State, NeedsAttention, FailureRetryBudget
		j.RecoveryAllowed = !j.LegacyHistory
		j.NextAttemptAt = time.Time{}
		return a.saveJob(index, j, ErrNeedsAttention)
	}
	if j.Attempts >= a.cfg.MaxAttempts && !j.ManualAttempt {
		j.RecoveryAllowed = !j.LegacyHistory && (retryFailureAllowed(j.LastFailure) || j.LastFailure == "")
		j.AttentionFrom, j.State, j.LastFailure = j.State, NeedsAttention, FailureRetryBudget
		j.NextAttemptAt = time.Time{}
		return a.saveJob(index, j, ErrNeedsAttention)
	}
	manualAttempt := j.ManualAttempt
	j.ManualAttempt = false
	j.ManualInFlight = manualAttempt
	j.Attempts++
	j.TotalAttempts++
	j.NextAttemptAt = a.now().Add(a.cfg.RetryDelay)
	// Persist attempt intent before calling out. A crash consumes a bounded
	// attempt, but never manufactures a new upstream/downstream idempotency key.
	if _, err := a.saveJob(index, j, nil); err != nil {
		return Job{}, err
	}
	callCtx, cancel := context.WithTimeout(ctx, a.cfg.AttemptTimeout)
	stop := context.AfterFunc(a.lifetime, cancel)
	defer func() { stop(); cancel() }()
	command := mapEvent(a.cfg.SourceID, j.Event)
	var err error
	switch j.State {
	case PendingReceive:
		var result customerservice.ReceiveMessageResult
		result, err = a.client.ReceiveMessage(callCtx, command)
		if err == nil {
			expectedAction := customerservice.NextActionNone
			if !result.Duplicate {
				switch result.Status {
				case conversation.StatusBotActive:
					expectedAction = customerservice.NextActionGenerateBotReply
				case conversation.StatusWaitingHuman:
					expectedAction = customerservice.NextActionWaitForHuman
				case conversation.StatusHumanActive:
					expectedAction = customerservice.NextActionDeliverToHuman
				}
			}
			if result.ConversationID != command.ConversationID || !validID(result.MessageID) ||
				!result.Status.Valid() || result.NextAction != expectedAction {
				err = &CallError{Code: FailureInvalidResponse}
			} else {
				j.TriggerMessageID = result.MessageID
				if result.Status != conversation.StatusBotActive {
					j.ManualInFlight = false
					j.State, j.AttentionFrom, j.Attempts, j.LastFailure = NeedsAttention, PendingReply, 0, FailureManual
					j.NextAttemptAt = time.Time{}
					return a.saveJob(index, j, ErrNeedsAttention)
				}
				// duplicate+none can still represent an unfulfilled automatic-reply
				// intent owned by this journal.
				advanceTo(&j, PendingReply)
			}
		}
	case PendingReply:
		var result customerservice.GenerateBotReplyResult
		result, err = a.client.GenerateBotReply(callCtx, customerservice.GenerateBotReplyCommand{
			ConversationID: command.ConversationID, TriggerMessageID: j.TriggerMessageID,
		})
		if err == nil {
			if result.ConversationID != command.ConversationID || result.TriggerMessageID != j.TriggerMessageID ||
				!validID(result.MessageID) || result.MessageID == j.TriggerMessageID || !validText(result.Content, MaxReplyBytes) {
				err = &CallError{Code: FailureInvalidResponse}
			} else {
				j.ReplyMessageID, j.ReplyContent = result.MessageID, result.Content
				j.DeliveryKey = deliveryKey(a.cfg, j)
				advanceTo(&j, PendingDelivery)
			}
		}
	case PendingDelivery:
		checker, ok := a.client.(DeliveryChecker)
		if !ok {
			err = &CallError{Code: FailureDeliveryUnavailable}
			break
		}
		var eligibility customerservice.DeliveryEligibility
		eligibility, err = checker.CheckDeliveryEligibility(callCtx, customerservice.DeliveryCheck{
			ConversationID: command.ConversationID, CustomerID: command.CustomerID, Channel: command.Channel,
			TriggerMessageID: j.TriggerMessageID, ReplyMessageID: j.ReplyMessageID, Content: j.ReplyContent,
		})
		if err != nil {
			break
		}
		if !eligibility.Eligible || eligibility.Reason != "eligible" {
			code := FailureDeliveryDenied
			switch eligibility.Reason {
			case "superseded":
				code = FailureDeliverySuperseded
			case "conversation_closed":
				code = FailureDeliveryClosed
			case "not_bot_owned", "ownership_mismatch":
				code = FailureDeliveryOwnership
			}
			err = &CallError{Code: code}
			break
		}
		if err = callCtx.Err(); err != nil {
			break
		}
		// Persist possible delivery before the sink call, even when its result or
		// the following save is lost. A later eligibility failure cannot clear it.
		j.DeliveryAttempts++
		j.MayHaveDelivered = true
		if _, err := a.saveJob(index, j, nil); err != nil {
			return Job{}, err
		}
		if err = callCtx.Err(); err != nil {
			break
		}
		err = a.deliverer.Deliver(callCtx, Delivery{
			Key: j.DeliveryKey, SourceID: a.cfg.SourceID, DestinationID: a.cfg.DestinationID,
			Channel: j.Event.Channel, ConversationID: j.Event.ConversationID, CustomerID: j.Event.CustomerID,
			ExternalMessageID: j.Event.ExternalMessageID, TriggerMessageID: j.TriggerMessageID,
			ReplyMessageID: j.ReplyMessageID, Content: j.ReplyContent,
		})
		if err == nil {
			advanceTo(&j, Completed)
		}
	default:
		return Job{}, ErrStateInvalid
	}
	j.ManualInFlight = false
	if err != nil {
		code, retryable, retryAfter := classifyCall(err)
		// Negative or excessively long retry instructions require review, not an
		// earlier retry that violates an upstream Retry-After.
		if retryAfter < 0 || retryAfter > maxRetryDelay {
			retryable = false
		}
		j.LastFailure = code
		if retryable {
			j.RetryNotBefore = a.now().Add(max(a.cfg.RetryDelay, retryAfter))
		}
		if !retryable || manualAttempt || j.Attempts >= a.cfg.MaxAttempts {
			j.RecoveryAllowed = retryable && retryFailureAllowed(code)
			j.AttentionFrom, j.State = j.State, NeedsAttention
			j.NextAttemptAt = time.Time{}
			return a.saveJob(index, j, ErrNeedsAttention)
		}
		j.NextAttemptAt = a.now().Add(max(a.cfg.RetryDelay, retryAfter))
		return a.saveJob(index, j, ErrAttemptFailed)
	}
	return a.saveJob(index, j, nil)
}

func advanceTo(j *Job, state State) {
	j.RecoveryAllowed, j.ManualAttempt = false, false
	j.ManualInFlight = false
	j.RetryNotBefore = time.Time{}
	j.State, j.Attempts, j.LastFailure, j.AttentionFrom = state, 0, "", ""
	j.NextAttemptAt = time.Time{}
}
func (a *Adapter) index(id string) int {
	for i := range a.data.Jobs {
		if a.data.Jobs[i].ID == id {
			return i
		}
	}
	return -1
}
func (a *Adapter) saveJob(index int, j Job, outcome error) (Job, error) {
	if a.data.Jobs[index].Revision >= ^uint64(0)-1 {
		return Job{}, ErrStateInvalid
	}
	j.Revision = a.data.Jobs[index].Revision + 1
	next := a.data
	next.Jobs = append([]Job{}, a.data.Jobs...)
	next.Jobs[index] = j
	if err := a.commit(next); err != nil {
		return Job{}, err
	}
	return j, outcome
}
func (a *Adapter) commit(next snapshot) error {
	if err := a.journal.save(next); err != nil {
		a.poisoned = true
		return ErrStore
	}
	a.data = next
	return nil
}

func classifyCall(err error) (FailureCode, bool, time.Duration) {
	var call *CallError
	if errors.As(err, &call) && call != nil {
		code := call.Code
		if !validFailure(code) || code == "" {
			code = FailureUnknown
		}
		return code, call.Retryable, call.RetryAfter
	}
	switch {
	case errors.Is(err, context.Canceled):
		return FailureCancelled, true, 0
	case errors.Is(err, context.DeadlineExceeded):
		return FailureTimeout, true, 0
	case errors.Is(err, customerservice.ErrIdempotencyConflict), errors.Is(err, customerservice.ErrConversationMismatch), errors.Is(err, customerservice.ErrReplySuperseded):
		return FailureConflict, false, 0
	case errors.Is(err, customerservice.ErrInvalidInput), errors.Is(err, customerservice.ErrInvalidReplyTrigger):
		return FailureInvalidRequest, false, 0
	case errors.Is(err, conversation.ErrConversationClosed), errors.Is(err, conversation.ErrSenderNotAllowed):
		return FailurePrecondition, false, 0
	case errors.Is(err, conversation.ErrConversationNotFound), errors.Is(err, conversation.ErrMessageNotFound):
		return FailureNotFound, false, 0
	case errors.Is(err, customerservice.ErrReplyCapacityExceeded):
		return FailureBusy, true, 0
	default:
		return FailureUnknown, true, 0
	}
}
