package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/qiuyier/omni-agent-customer-service/internal/integration"
)

type caseReport struct {
	Name       string            `json:"name"`
	Passed     bool              `json:"passed"`
	State      integration.State `json:"state"`
	Counts     callCounts        `json:"counts"`
	WorkerPIDs []int             `json:"worker_pids"`
	KilledPID  int               `json:"killed_pid,omitempty"`
	Audits     int               `json:"operator_audits"`
	Verified   []string          `json:"verified"`
}
type acceptanceReport struct {
	Passed     bool         `json:"passed"`
	Directory  string       `json:"directory"`
	ReportPath string       `json:"report_path,omitempty"`
	Cases      []caseReport `json:"cases"`
	Failure    string       `json:"failure,omitempty"`
	Boundaries []string     `json:"boundaries"`
}

func runAcceptance(ctx context.Context, root string, program workerProgram) (acceptanceReport, error) {
	result := acceptanceReport{Directory: root, Cases: []caseReport{}, Boundaries: []string{
		"Only adapter child processes restart; service and simulated destination stay alive.",
		"Loopback HTTP and fake replies only; no live model, real channel, PostgreSQL restart, power-loss or multi-tenant authorization claim.",
		"Operator actions are trusted local application calls, not a public management API.",
		"Eligibility is a snapshot, not atomic conditional delivery or a send lease.",
	}}
	for _, phase := range []string{"receive", "reply", "delivery"} {
		r, err := runCrashCase(ctx, root, program, phase)
		result.Cases = append(result.Cases, r)
		if err != nil {
			return result, fmt.Errorf("%s: %w", r.Name, err)
		}
	}
	for _, action := range []string{"confirm", "retry", "stop", "retry_after_close"} {
		r, err := runOperatorCase(ctx, root, program, action)
		result.Cases = append(result.Cases, r)
		if err != nil {
			return result, fmt.Errorf("%s: %w", r.Name, err)
		}
	}
	r, err := runNamespaceCase(ctx, root, program)
	result.Cases = append(result.Cases, r)
	if err != nil {
		return result, fmt.Errorf("%s: %w", r.Name, err)
	}
	result.Passed = true
	return result, nil
}
func runChild(ctx context.Context, program workerProgram, cfg workerConfig, r *caseReport) (workerResult, error) {
	c, err := startChild(ctx, program, cfg)
	if err != nil {
		return workerResult{}, err
	}
	defer c.stop()
	r.WorkerPIDs = append(r.WorkerPIDs, c.pid)
	return c.result(ctx)
}
func inspectJob(ctx context.Context, cfg workerConfig) (integration.Inspection, error) {
	a, closeClients, err := openWorker(cfg)
	if err != nil {
		return integration.Inspection{}, err
	}
	defer closeClients()
	defer a.Close()
	jobs, err := a.List(ctx)
	if err != nil {
		return integration.Inspection{}, err
	}
	if len(jobs) != 1 {
		return integration.Inspection{}, errors.New("expected one persisted adapter job")
	}
	return a.Inspect(ctx, jobs[0].ID)
}
func resolveJob(ctx context.Context, cfg workerConfig, q integration.ResolutionCommand) (integration.ResolutionResult, error) {
	a, closeClients, err := openWorker(cfg)
	if err != nil {
		return integration.ResolutionResult{}, err
	}
	defer closeClients()
	defer a.Close()
	return a.Resolve(ctx, q)
}
func checkedCounts(got, want callCounts) error {
	if got != want {
		return fmt.Errorf("HTTP/effect counters differ: got %+v want %+v", got, want)
	}
	return nil
}
func sameJob(before, after integration.Job) bool {
	return before.ID == after.ID && before.Event == after.Event && (before.TriggerMessageID == "" || before.TriggerMessageID == after.TriggerMessageID) &&
		(before.ReplyMessageID == "" || before.ReplyMessageID == after.ReplyMessageID) && (before.DeliveryKey == "" || before.DeliveryKey == after.DeliveryKey)
}

func runCrashCase(ctx context.Context, root string, program workerProgram, phase string) (caseReport, error) {
	r := caseReport{Name: "crash_after_" + phase + "_commit"}
	f, err := newFixture(ctx, phase, false, false)
	if err != nil {
		return r, err
	}
	defer f.close()
	cfg := f.config(filepath.Join(root, r.Name, "state"), "system-a", 3)
	c, err := startChild(ctx, program, cfg)
	if err != nil {
		return r, err
	}
	defer c.stop()
	r.WorkerPIDs = append(r.WorkerPIDs, c.pid)
	select {
	case <-ctx.Done():
		return r, ctx.Err()
	case err := <-c.done:
		c.finished = true
		return r, fmt.Errorf("child exited before commit barrier: %v", err)
	case <-f.gate.reached:
	}
	// This PID belongs to the child just started here. Do not kill a shell or go-run wrapper.
	if err := c.cmd.Process.Kill(); err != nil {
		return r, err
	}
	if err := <-c.done; err == nil {
		c.finished = true
		return r, errors.New("child exited normally instead of being interrupted")
	}
	c.finished = true
	r.KilledPID = c.pid
	f.gate.unblock()
	before, err := inspectJob(ctx, cfg)
	if err != nil {
		return r, err
	}
	expected := map[string]integration.State{"receive": integration.PendingReceive, "reply": integration.PendingReply, "delivery": integration.PendingDelivery}[phase]
	if before.Job.State != expected || before.Job.Attempts != 1 {
		return r, errors.New("crash checkpoint was not retained")
	}
	if phase == "delivery" && (!before.Job.MayHaveDelivered || before.Job.DeliveryAttempts != 1) {
		return r, errors.New("delivery intent missing after crash")
	}
	resumed, err := runChild(ctx, program, cfg, &r)
	if err != nil {
		return r, err
	}
	j := resumed.Inspection.Job
	if resumed.PID == r.KilledPID || j.State != integration.Completed || !sameJob(before.Job, j) || j.TotalAttempts != 4 {
		return r, errors.New("new process did not recover the original job")
	}
	if err := f.assertSaved(j, true); err != nil {
		return r, err
	}
	want := callCounts{Receive: 1, Reply: 1, Eligibility: 1, Delivery: 1, Models: 1, Unique: 1}
	switch phase {
	case "receive":
		want.Receive++
	case "reply":
		want.Reply++
	case "delivery":
		want.Eligibility++
		want.Delivery++
	}
	r.Counts = f.counts()
	if err := checkedCounts(r.Counts, want); err != nil {
		return r, err
	}
	r.State = j.State
	if err := f.close(); err != nil {
		return r, err
	}
	r.Passed = true
	r.Verified = []string{"real bearer-protected HTTP", "commit-before-kill barrier", "different adapter PID after restart", "stable persisted IDs", "one saved reply and one sink effect"}
	return r, nil
}

func operatorCommand(j integration.Job, action integration.ResolutionAction) integration.ResolutionCommand {
	return integration.ResolutionCommand{OperationID: fmt.Sprintf("review-%d-%s", j.Revision, action), JobID: j.ID, ExpectedRevision: j.Revision, Action: action,
		ActorID: "local-acceptance-operator", Reason: "reviewed fictional channel state", EvidenceRef: "local-receipt-check/" + j.DeliveryKey}
}
func runOperatorCase(ctx context.Context, root string, program workerProgram, action string) (caseReport, error) {
	r := caseReport{Name: "operator_" + action}
	beforeCommit := action == "retry" || action == "retry_after_close"
	f, err := newFixture(ctx, "", beforeCommit, !beforeCommit)
	if err != nil {
		return r, err
	}
	defer f.close()
	cfg := f.config(filepath.Join(root, r.Name, "state"), "system-a", 1)
	first, err := runChild(ctx, program, cfg, &r)
	if err != nil {
		return r, err
	}
	view, err := inspectJob(ctx, cfg)
	if err != nil {
		return r, err
	}
	j := view.Job
	if j.State != integration.NeedsAttention || !j.MayHaveDelivered || j.AttentionFrom != integration.PendingDelivery || j.ID != first.Inspection.Job.ID {
		return r, errors.New("ambiguous delivery was not retained for review")
	}
	receipt, exists := f.sink.receipt(j.DeliveryKey)
	if exists == beforeCommit {
		return r, errors.New("receipt investigation disagrees with injected failure")
	}
	if exists && (receipt.ReplyMessageID != j.ReplyMessageID || receipt.Content != j.ReplyContent) {
		return r, errors.New("receipt does not match saved reply")
	}
	selected := integration.StopProcessing
	if action == "confirm" {
		selected = integration.ConfirmDelivered
	} else if beforeCommit {
		selected = integration.RetryOnce
	}
	q := operatorCommand(j, selected)
	resolved, err := resolveJob(ctx, cfg, q)
	if err != nil {
		return r, err
	}
	if resolved.Duplicate || !sameJob(j, resolved.Job) {
		return r, errors.New("resolution changed job identity")
	}
	stale := operatorCommand(j, integration.StopProcessing)
	stale.OperationID = "stale-review"
	if _, err := resolveJob(ctx, cfg, stale); !errors.Is(err, integration.ErrStaleRevision) {
		return r, errors.New("stale operator command was not rejected")
	}
	if action == "retry_after_close" {
		if err := f.closeConversation(ctx); err != nil {
			return r, err
		}
	}
	next, err := runChild(ctx, program, cfg, &r)
	if err != nil {
		return r, err
	}
	expectedState := integration.Completed
	if action == "stop" {
		expectedState = integration.Stopped
	}
	if action == "retry_after_close" {
		stoppedAt := next.Inspection.Job
		if stoppedAt.State != integration.NeedsAttention || stoppedAt.LastFailure != integration.FailureDeliveryClosed || f.counts().Delivery != 1 {
			return r, errors.New("retry bypassed current delivery eligibility")
		}
		if _, err := resolveJob(ctx, cfg, operatorCommand(stoppedAt, integration.StopProcessing)); err != nil {
			return r, err
		}
		next, err = runChild(ctx, program, cfg, &r)
		if err != nil {
			return r, err
		}
		expectedState = integration.Stopped
	}
	if next.Inspection.Job.State != expectedState || !sameJob(j, next.Inspection.Job) {
		return r, errors.New("operator decision did not survive process restart")
	}
	replay, err := resolveJob(ctx, cfg, q)
	if err != nil || !replay.Duplicate || replay.Job.State != expectedState {
		return r, errors.New("operator command replay failed")
	}
	final, err := inspectJob(ctx, cfg)
	if err != nil {
		return r, err
	}
	r.Audits = len(final.Audit)
	expectedAudits := 1
	if action == "retry_after_close" {
		expectedAudits = 2
	}
	if r.Audits != expectedAudits {
		return r, errors.New("audit history missing or duplicated")
	}
	if err := f.assertSaved(final.Job, action != "retry_after_close"); err != nil {
		return r, err
	}
	want := callCounts{Receive: 1, Reply: 1, Eligibility: 1, Delivery: 1, Models: 1, Unique: 1}
	if action == "retry" {
		want.Eligibility = 2
		want.Delivery = 2
	}
	if action == "retry_after_close" {
		want.Eligibility = 2
		want.Unique = 0
	}
	r.Counts = f.counts()
	if err := checkedCounts(r.Counts, want); err != nil {
		return r, err
	}
	r.State = expectedState
	if err := f.close(); err != nil {
		return r, err
	}
	r.Passed = true
	r.Verified = []string{"real HTTP delivery ambiguity", "Inspect and receipt reconciliation", "persisted operator decision and audit", "stale command rejected", "duplicate command replay without new audit"}
	if beforeCommit {
		r.Verified = append(r.Verified, "resumed worker rechecked current eligibility")
	} else {
		r.Verified = append(r.Verified, "terminal re-entry made no further delivery call")
	}
	return r, nil
}

func runNamespaceCase(ctx context.Context, root string, program workerProgram) (caseReport, error) {
	r := caseReport{Name: "two_sources_same_external_ids"}
	f, err := newFixture(ctx, "", false, false)
	if err != nil {
		return r, err
	}
	defer f.close()
	results := []integration.Job{}
	for _, source := range []string{"system-a", "system-b"} {
		cfg := f.config(filepath.Join(root, r.Name, source), source, 3)
		result, err := runChild(ctx, program, cfg, &r)
		if err != nil {
			return r, err
		}
		if result.Inspection.Job.State != integration.Completed {
			return r, errors.New("source did not complete")
		}
		results = append(results, result.Inspection.Job)
	}
	if results[0].Event != results[1].Event || results[0].ID == results[1].ID || results[0].DeliveryKey == results[1].DeliveryKey {
		return r, errors.New("source namespace collision")
	}
	if err := f.assertNamespaces(results); err != nil {
		return r, err
	}
	r.Counts = f.counts()
	if err := checkedCounts(r.Counts, callCounts{Receive: 2, Reply: 2, Eligibility: 2, Delivery: 2, Models: 2, Unique: 2}); err != nil {
		return r, err
	}
	r.State = integration.Completed
	if err := f.close(); err != nil {
		return r, err
	}
	r.Passed = true
	r.Verified = []string{"identical external event fields", "different source conversation/customer/event namespaces", "shared HTTP backend without core changes", "two unique sink effects, not tenant authorization"}
	return r, nil
}
