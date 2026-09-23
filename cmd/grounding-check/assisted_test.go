package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

func TestAssistedHintsCannotSkipOriginalOrIndependentReview(t *testing.T) {
	input := judgeInput{Reference: "提供服务。", Answer: "请用门户查询记录。", History: []turn{{Role: "user", Content: "怎么查？"}}}
	trace := &classificationTrace{}
	ctx := context.WithValue(context.Background(), classificationTraceKey{}, trace)
	calls := 0
	classifier := func(_ context.Context, got judgeInput) (string, error) {
		calls++
		if got.Answer != input.Answer || len(got.Classification) != 0 {
			t.Fatal("classifier input changed")
		}
		return `{"segments":[{"quote":"请用门户查询记录。","act":"information_seeking","reason":"deliberately wrong"}]}`, nil
	}
	primary := func(_ context.Context, got judgeInput) (string, error) {
		calls++
		if got.Answer != input.Answer || got.Reference != input.Reference || len(got.History) != 1 || len(got.Classification) != 1 {
			t.Fatal("primary lost full input or hint")
		}
		return `{"claims":[],"advice":[{"quote":"请用门户查询记录。","advice_kind":"information_seeking","reason":"deliberately wrong"}]}`, nil
	}
	review := func(_ context.Context, got judgeInput) (string, error) {
		calls++
		if got.Answer != input.Answer || len(got.Classification) != 0 {
			t.Fatal("review contaminated")
		}
		return `{"findings":[{"quote":"请用门户查询记录。","missing_detail":"未提供门户查询能力"}],"uncertain":false}`, nil
	}
	raw, err := withClassification(classifier, primary, review)(ctx, input)
	_, v, e := parse(raw, input)
	if err != nil || e != nil || v != "unsupported" || calls != 3 || trace.Output == nil {
		t.Fatalf("lost veto: %s %v %v", v, err, e)
	}
}
func TestAssistedClassificationFailureStops(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		err  error
		code string
	}{
		{raw: `{"segments":[]}`, code: "invalid_segments"},
		{raw: "not JSON", code: "invalid_speech_json"},
		{err: context.DeadlineExceeded, code: "model_timeout"},
	} {
		calls := 0
		classifier := func(context.Context, judgeInput) (string, error) { calls++; return tc.raw, tc.err }
		forbidden := func(context.Context, judgeInput) (string, error) {
			t.Fatal("later stage ran after failure")
			return "", nil
		}
		_, err := withClassification(classifier, forbidden, forbidden)(context.Background(), judgeInput{Answer: "回答"})
		var f *judgeFailure
		if !errors.As(err, &f) || f.Phase != "classification" || f.Code != tc.code || calls != 1 {
			t.Fatalf("bad diagnostic: %v", err)
		}
	}
}
func TestAssistedPrimaryUnsupportedSkipsReview(t *testing.T) {
	classifier := func(context.Context, judgeInput) (string, error) {
		return `{"segments":[{"quote":"回答","act":"assertion","reason":"事实"}]}`, nil
	}
	primary := func(context.Context, judgeInput) (string, error) {
		return `{"claims":[{"quote":"回答","kind":"unsupported","reason":"无依据","evidence":""}]}`, nil
	}
	review := func(context.Context, judgeInput) (string, error) { t.Fatal("unneeded review"); return "", nil }
	raw, err := withClassification(classifier, primary, review)(context.Background(), judgeInput{Answer: "回答"})
	if _, v, e := parse(raw, judgeInput{Answer: "回答"}); err != nil || e != nil || v != "unsupported" {
		t.Fatalf("%s %v %v", v, err, e)
	}
}
func TestAssistedOfflineAndConflictingModes(t *testing.T) {
	var out bytes.Buffer
	err := run(context.Background(), []string{"-with-classification", "-cases", fixturePath(t)}, &out, io.Discard, func() (judgeFunc, string, error) { t.Fatal("offline constructed judge"); return nil, "", nil })
	var r report
	if err != nil || json.Unmarshal(out.Bytes(), &r) != nil || r.SchemaVersion != 6 || r.Prompt != assistedPrompt || r.ClassificationPrompt != speechPrompt || r.Calls != 0 {
		t.Fatalf("offline: %v", err)
	}
	if err := run(context.Background(), []string{"-with-classification", "-classify-only"}, io.Discard, io.Discard, nil); err == nil {
		t.Fatal("conflicting modes accepted")
	}
}
func TestAssistedReportRetainsTraceButNotSuccessOnReviewFailure(t *testing.T) {
	classifier := func(context.Context, judgeInput) (string, error) {
		return `{"segments":[{"quote":"回答","act":"uncertainty","reason":"未知"}]}`, nil
	}
	primary := func(context.Context, judgeInput) (string, error) {
		return `{"claims":[],"advice":[{"quote":"回答","advice_kind":"none","reason":"未知"}]}`, nil
	}
	reviewer := func(context.Context, judgeInput) (string, error) { return "", context.Canceled }
	var out bytes.Buffer
	err := run(context.Background(), []string{"-cases", fixturePath(t), "-allow-model-api"}, &out, io.Discard, func() (judgeFunc, string, error) {
		return withClassification(classifier, primary, reviewer), "fixture", nil
	})
	var r report
	if err == nil || json.Unmarshal(out.Bytes(), &r) != nil || r.Complete || r.Results[0].Classification == nil || r.Results[0].Verdict != "error" || r.Results[0].Failure.Phase != "review" {
		t.Fatalf("report: %s", out.String())
	}
}
