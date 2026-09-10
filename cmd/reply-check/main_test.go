package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/evaluation"
	kitstream "github.com/qiuyier/omni-agent-kit/stream"
)

func acceptanceExampleArgs() []string {
	return []string{"-faq", "../../examples/knowledge/faq.json", "-cases", "../../examples/knowledge/reply_cases.json", "-timeout", "15s"}
}

type fixtureGenerator struct {
	fail     bool
	closeErr bool
}

func (g fixtureGenerator) Generate(context.Context, customerservice.ReplyRequest) (customerservice.Reply, error) {
	if g.fail {
		return customerservice.Reply{}, errors.New("private-provider-payload")
	}
	return customerservice.Reply{Content: "fixture answer"}, nil
}

func (g fixtureGenerator) Stream(context.Context, customerservice.ReplyRequest) (kitstream.Reader[customerservice.ReplyDelta], error) {
	if g.fail {
		return nil, errors.New("private-provider-payload")
	}
	source := kitstream.FromSlice([]customerservice.ReplyDelta{{Content: "fixture "}, {Content: "answer"}})
	if g.closeErr {
		return kitstream.NewReader(source.Recv, func() error {
			_ = source.Close()
			return errors.New("private-cleanup-payload")
		}), nil
	}
	return source, nil
}

func fixtureDependencies(fail bool) dependencyFactory {
	return func(_ context.Context, opts checkOptions, snippets []customerservice.KnowledgeSnippet) (checkDependencies, error) {
		cases, err := evaluation.Load(opts.Cases)
		if err != nil {
			return checkDependencies{}, err
		}
		labels := make(map[string][]string)
		for _, c := range cases {
			labels[c.Query] = c.ExpectedIDs
		}
		retriever := customerservice.KnowledgeRetrieverFunc(func(_ context.Context, q customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
			var refs []customerservice.KnowledgeSnippet
			for _, id := range labels[q.Text] {
				for _, s := range snippets {
					if s.ID == id {
						refs = append(refs, s)
					}
				}
			}
			return refs, nil
		})
		return checkDependencies{Retriever: retriever, Generator: fixtureGenerator{fail: fail}, QueryTimeout: time.Second}, nil
	}
}

// This drives real Hertz sockets and business persistence with local stand-ins
// for the external ports. It asserts contracts, never a real model's prose.
func TestReplyCheckHTTPAndSSEWithSequentialCrossModeReplays(t *testing.T) {
	var out bytes.Buffer
	args := append(acceptanceExampleArgs(), "-allow-model-api", "-allow-embedding-api")
	if err := runWithFactory(context.Background(), args, &out, io.Discard, fixtureDependencies(false)); err != nil {
		t.Fatal(err)
	}
	var report checkReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.Complete || !report.MechanicalPassed || report.QualityReview != "manual_review_required" || len(report.Results) != 6 {
		t.Fatalf("incomplete acceptance report: %+v", report)
	}
	for _, result := range report.Results {
		if !result.MechanicalPassed || result.StoredMessages != 2 || result.RetrievalCalls != 1 || result.ModelCalls != 1 || len(result.Replays) != 2 || result.Reply.Content != "fixture answer" {
			t.Fatalf("invalid initial result: %+v", result)
		}
		if !sameIDs(result.ModelReferenceIDs, result.Case.ExpectedIDs) || !sameIDs(result.RetrievedIDs, result.Case.ExpectedIDs) {
			t.Fatal("references changed across service/model boundary")
		}
		if result.Mode == "stream" && (result.FirstDeltaMS == nil || result.DeltaCount != 2 || len(result.Events) != 3) {
			t.Fatal("initial SSE stream not recorded", result)
		}
		for _, replay := range result.Replays {
			if !replay.Duplicate || !replay.ReusedReply || !replay.NoNewCalls || replay.StoredMessages != 2 || replay.HTTPStatus != 200 || replay.DeltaCount != 0 {
				t.Fatal("invalid completed-reply replay", replay)
			}
			if replay.Mode == "stream" && (len(replay.Events) != 1 || replay.Events[0] != "reply.completed") {
				t.Fatal("SSE replay must be completed-only", replay)
			}
		}
	}
}

func TestReplyCheckFailureIsExplicitAndOmitsProviderPayload(t *testing.T) {
	var out bytes.Buffer
	args := append(acceptanceExampleArgs(), "-allow-model-api", "-allow-embedding-api")
	if err := runWithFactory(context.Background(), args, &out, io.Discard, fixtureDependencies(true)); err == nil {
		t.Fatal("provider failure passed acceptance")
	}
	var report checkReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Complete || report.MechanicalPassed || report.Failure == "" || len(report.Results) != 1 || report.Results[0].MechanicalPassed || strings.Contains(out.String(), "private-provider-payload") {
		t.Fatal("failure was hidden or leaked provider content", out.String())
	}
	failed := report.Results[0]
	if failed.RetrievalCalls != 1 || failed.ModelCalls != 1 || failed.HTTPStatus != 503 || failed.StoredMessages != 1 || failed.ModelError != "request_failed" || failed.ModelOutcome != "generation_failed" || failed.RetrievalMS <= 0 || failed.ModelMS <= 0 {
		t.Fatal("failed request lost its observed calls or timings", failed)
	}
}

func TestSafeModelErrorNeverCopiesProviderText(t *testing.T) {
	for _, item := range []struct {
		err    error
		class  string
		status int
	}{
		{&openai.Error{Code: "invalid_api_key", Message: "private-marker", StatusCode: 401}, "invalid_api_key", 401},
		{&openai.Error{Code: "private-marker", Message: "private-marker", StatusCode: 503}, "provider_http_error", 503},
		{context.DeadlineExceeded, "deadline_exceeded", 0},
		{errors.New("private-marker"), "request_failed", 0},
	} {
		class, status := safeModelError(item.err)
		if class != item.class || status != item.status || strings.Contains(class, "private-marker") {
			t.Fatal("unsafe or incorrect error classification", class, status)
		}
	}
}

func TestReplyCheckRejectsCompletedStreamWithCleanupFailure(t *testing.T) {
	factory := func(ctx context.Context, opts checkOptions, snippets []customerservice.KnowledgeSnippet) (checkDependencies, error) {
		deps, err := fixtureDependencies(false)(ctx, opts, snippets)
		deps.Generator = fixtureGenerator{closeErr: true}
		return deps, err
	}
	var out bytes.Buffer
	args := append(acceptanceExampleArgs(), "-allow-model-api", "-allow-embedding-api")
	if err := runWithFactory(context.Background(), args, &out, io.Discard, factory); err == nil {
		t.Fatal("completed stream with cleanup failure passed acceptance")
	}
	var report checkReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Complete || report.MechanicalPassed || len(report.Results) != 2 || !report.Results[1].CleanupFailed || report.Results[1].MechanicalPassed || strings.Contains(out.String(), "private-cleanup-payload") {
		t.Fatal("cleanup failure was hidden or leaked", out.String())
	}
}

func TestReplyCheckValidatesOptInAndLabelsBeforeBuildingDependencies(t *testing.T) {
	calls := 0
	factory := func(context.Context, checkOptions, []customerservice.KnowledgeSnippet) (checkDependencies, error) {
		calls++
		return checkDependencies{}, errors.New("factory must not run")
	}
	for _, flags := range [][]string{nil, {"-allow-model-api"}, {"-allow-embedding-api"}, {"-allow-model-api", "-allow-embedding-api", "-timeout", "0s"}} {
		var out bytes.Buffer
		if err := runWithFactory(context.Background(), append(acceptanceExampleArgs(), flags...), &out, io.Discard, factory); err == nil || out.Len() != 0 {
			t.Fatal("invalid opt-in or timeout accepted")
		}
	}
	path := filepath.Join(t.TempDir(), "cases.json")
	if err := os.WriteFile(path, []byte(`[{"id":"invalid","query":"demo","expected_ids":["unknown-id"]}]`), 0600); err != nil {
		t.Fatal(err)
	}
	args := append(acceptanceExampleArgs(), "-allow-model-api", "-allow-embedding-api", "-cases", path)
	if err := runWithFactory(context.Background(), args, io.Discard, io.Discard, factory); err == nil {
		t.Fatal("unknown reference label accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	args = append(acceptanceExampleArgs(), "-allow-model-api", "-allow-embedding-api")
	if err := runWithFactory(ctx, args, io.Discard, io.Discard, factory); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled run reached dependency construction", err)
	}
	if calls != 0 {
		t.Fatal("invalid or unauthorized run initialized external dependencies", calls)
	}
	if err := runWithFactory(context.Background(), []string{"-help"}, io.Discard, io.Discard, factory); err != nil {
		t.Fatal(err)
	}
}
