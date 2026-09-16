package integration

import (
	"context"
	"time"
)

const MaxManualRetries = 3
const MaxResolutionTextBytes = 1024

type ResolutionAction string

const (
	ConfirmDelivered ResolutionAction = "confirm_delivered"
	StopProcessing   ResolutionAction = "stop"
	RetryOnce        ResolutionAction = "retry_once"
)

// ResolutionCommand is supplied by a trusted operator application, not by a
// customer or an LLM. ActorID is an assertion: callers must authenticate and
// authorize the operator. EvidenceRef should identify a reviewed receipt/check,
// never credentials or copied provider payloads.
type ResolutionCommand struct {
	OperationID      string           `json:"operation_id"`
	JobID            string           `json:"job_id"`
	ExpectedRevision uint64           `json:"expected_revision"`
	Action           ResolutionAction `json:"action"`
	ActorID          string           `json:"actor_id"`
	Reason           string           `json:"reason"`
	EvidenceRef      string           `json:"evidence_ref"`
}
type ResolutionAudit struct {
	Command             ResolutionCommand `json:"command"`
	At                  time.Time         `json:"at"`
	FromStage           State             `json:"from_stage"`
	Failure             FailureCode       `json:"failure"`
	Attempts            int               `json:"attempts"`
	TotalAttempts       int               `json:"total_attempts"`
	DeliveryAttempts    int               `json:"delivery_attempts"`
	MayHaveDelivered    bool              `json:"may_have_delivered"`
	ManualRetriesBefore int               `json:"manual_retries_before"`
	ManualRetriesAfter  int               `json:"manual_retries_after"`
	ToState             State             `json:"to_state"`
	Revision            uint64            `json:"revision"`
}
type Inspection struct {
	Job            Job
	Audit          []ResolutionAudit
	AllowedActions []ResolutionAction
}
type ResolutionResult struct {
	Job       Job
	Audit     ResolutionAudit
	Duplicate bool
}

func retryFailureAllowed(code FailureCode) bool {
	switch code {
	case FailureTransport, FailureTimeout, FailureCancelled, FailureBusy, FailureUnavailable, FailureRetryBudget:
		return true
	default:
		return false
	}
}
func resolutionAllowed(j Job, action ResolutionAction) bool {
	if j.State != NeedsAttention {
		return false
	}
	switch action {
	case StopProcessing:
		return true
	case ConfirmDelivered:
		return j.AttentionFrom == PendingDelivery && j.MayHaveDelivered
	case RetryOnce:
		return j.RecoveryAllowed && retryFailureAllowed(j.LastFailure) && j.ManualRetries < MaxManualRetries
	default:
		return false
	}
}
func validResolution(q ResolutionCommand) bool {
	return validID(q.OperationID) && validID(q.JobID) && q.ExpectedRevision > 0 && q.ExpectedRevision < ^uint64(0) && validID(q.ActorID) &&
		validText(q.Reason, MaxResolutionTextBytes) && validText(q.EvidenceRef, MaxResolutionTextBytes) &&
		(q.Action == StopProcessing || q.Action == ConfirmDelivered || q.Action == RetryOnce)
}

// Inspect returns owned snapshots; totals on migrated jobs are lower bounds.
func (a *Adapter) Inspect(ctx context.Context, id string) (Inspection, error) {
	if err := a.take(ctx); err != nil {
		return Inspection{}, err
	}
	defer a.release()
	index := a.index(id)
	if index < 0 {
		return Inspection{}, ErrNotFound
	}
	result := Inspection{Job: a.data.Jobs[index], Audit: []ResolutionAudit{}, AllowedActions: []ResolutionAction{}}
	for _, entry := range a.data.Audit {
		if entry.Command.JobID == id {
			result.Audit = append(result.Audit, entry)
		}
	}
	for _, action := range []ResolutionAction{ConfirmDelivered, StopProcessing, RetryOnce} {
		if resolutionAllowed(result.Job, action) {
			result.AllowedActions = append(result.AllowedActions, action)
		}
	}
	return result, nil
}

// Resolve records a decision without any external call. It does not verify an
// external receipt itself. A retry grants exactly one attempt of the same stage,
// never clears attempts or changes any upstream/downstream idempotency key.
func (a *Adapter) Resolve(ctx context.Context, q ResolutionCommand) (ResolutionResult, error) {
	if err := a.take(ctx); err != nil {
		return ResolutionResult{}, err
	}
	defer a.release()
	if !validResolution(q) {
		return ResolutionResult{}, ErrInvalidInput
	}
	for _, entry := range a.data.Audit {
		if entry.Command.OperationID == q.OperationID {
			if entry.Command != q {
				return ResolutionResult{}, ErrOperationConflict
			}
			return ResolutionResult{Job: a.data.Jobs[a.index(q.JobID)], Audit: entry, Duplicate: true}, nil
		}
	}
	index := a.index(q.JobID)
	if index < 0 {
		return ResolutionResult{}, ErrNotFound
	}
	j := a.data.Jobs[index]
	if j.Revision != q.ExpectedRevision {
		return ResolutionResult{}, ErrStaleRevision
	}
	if !resolutionAllowed(j, q.Action) {
		return ResolutionResult{}, ErrResolutionNotAllowed
	}
	if j.Revision >= ^uint64(0)-1 {
		return ResolutionResult{}, ErrStateInvalid
	}
	at := a.now().UTC()
	if at.IsZero() {
		return ResolutionResult{}, ErrStateInvalid
	}
	entry := ResolutionAudit{Command: q, At: at, FromStage: j.AttentionFrom, Failure: j.LastFailure, Attempts: j.Attempts, TotalAttempts: j.TotalAttempts,
		DeliveryAttempts: j.DeliveryAttempts, MayHaveDelivered: j.MayHaveDelivered, ManualRetriesBefore: j.ManualRetries}
	switch q.Action {
	case ConfirmDelivered:
		advanceTo(&j, Completed)
	case StopProcessing:
		j.State = Stopped
		j.RecoveryAllowed = false // preserve failure/origin; stop is not delivery confirmation
	case RetryOnce:
		j.State, j.AttentionFrom = j.AttentionFrom, ""
		j.ManualAttempt = true
		j.ManualRetries++
		j.RecoveryAllowed = false
		j.NextAttemptAt = at.Add(a.cfg.RetryDelay)
		if j.NextAttemptAt.Before(j.RetryNotBefore) {
			j.NextAttemptAt = j.RetryNotBefore
		}
	}
	j.Revision++
	entry.ManualRetriesAfter, entry.ToState, entry.Revision = j.ManualRetries, j.State, j.Revision
	next := a.data
	next.Jobs = append([]Job{}, a.data.Jobs...)
	next.Jobs[index] = j
	next.Audit = append(append([]ResolutionAudit{}, a.data.Audit...), entry)
	if err := a.commit(next); err != nil {
		return ResolutionResult{}, err
	}
	return ResolutionResult{Job: j, Audit: entry}, nil
}
