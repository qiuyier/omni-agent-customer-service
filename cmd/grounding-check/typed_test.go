package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

func TestTypedContractDiscriminatesNonFacts(t *testing.T) {
	for _, scope := range []string{"assistant_limitation", "knowledge_limit", "inquiry", "social", "correction"} {
		s := typedSegment{Quote: "回答", Scope: scope, Reason: "非事实说明"}
		raw, _ := json.Marshal(typedOutput{[]typedSegment{s}})
		if _, v, e := parseTyped(string(raw), judgeInput{Answer: "回答"}); e != nil || v != "clear" {
			t.Fatalf("%s %s %v", scope, v, e)
		}
		for _, assessment := range []string{`null`, `{}`, `{"status":"supported","evidence":"","checks":[]}`} {
			s.Assessment = json.RawMessage(assessment)
			raw, _ = json.Marshal(typedOutput{[]typedSegment{s}})
			if _, _, e := parseTyped(string(raw), judgeInput{Answer: "回答"}); e == nil || e.Error() != "assessment_for_non_fact" {
				t.Fatalf("non-fact assessment accepted: %s %v", scope, e)
			}
		}
	}
}
func TestTypedFactsKeepEvidenceAndCoverageRules(t *testing.T) {
	input := judgeInput{Reference: "支持查询。", Answer: "支持查询。"}
	tests := []struct{ assessment, want string }{
		{`{"status":"supported","evidence":"支持查询。","checks":[{"detail":"支持查询","relation":"entailed","evidence":"支持查询。"}]}`, "clear"},
		{`{"status":"supported","evidence":"","checks":[{"detail":"支持查询","relation":"entailed","evidence":"支持查询。"}]}`, "error"},
		{`{"status":"supported","evidence":"支持查询。","checks":[{"detail":"支持查询","relation":"entailed","evidence":""}]}`, "error"},
		{`{"status":"unsupported","evidence":"","checks":[{"detail":"支持查询","relation":"not_stated","evidence":""}]}`, "unsupported"},
		{`{"status":"supported","evidence":"支持查询。","checks":[{"detail":"支持查询","relation":"not_stated","evidence":""}]}`, "unsupported"},
		{`{"status":"general","evidence":"","checks":[]}`, "error"},
		{`{"status":"supported","evidence":"支持查询。","checks":[]}`, "error"},
		{`null`, "error"},
	}
	for _, tc := range tests {
		raw, _ := json.Marshal(typedOutput{[]typedSegment{{Quote: input.Answer, Scope: "system_fact", Reason: "核验", Assessment: json.RawMessage(tc.assessment)}}})
		if _, v, e := parseTyped(string(raw), input); v != tc.want || (e != nil) != (tc.want == "error") {
			t.Fatalf("%s: %s %v", tc.assessment, v, e)
		}
	}
	raw := `{"segments":[{"quote":"支持","scope":"knowledge_limit","reason":"测试覆盖"}]}`
	if _, v, e := parseTyped(raw, input); e != nil || v != "uncertain" {
		t.Fatal("coverage loosened")
	}
	for _, raw := range []string{`{"segments":[]}`, `{"segments":[{"quote":"支持查询。","scope":"social","reason":"说明","evidence":""}]}`, `{"segments":[{"quote":"支持查询。","scope":"other","reason":"说明"}]}`} {
		if _, _, e := parseTyped(raw, input); e == nil {
			t.Fatal("invalid contract accepted")
		}
	}
}
func TestTypedIndependentReviewAndRawTrace(t *testing.T) {
	raw := `{"segments":[{"quote":"可通过官方渠道提交申请。","scope":"inquiry","reason":"故意模拟错误分类"}]}`
	calls := 0
	primary := func(context.Context, judgeInput) (string, error) { calls++; return raw, nil }
	reviewer := func(_ context.Context, got judgeInput) (string, error) {
		calls++
		if got.Answer != "可通过官方渠道提交申请。" || len(got.Classification) != 0 {
			t.Fatal("review lost original")
		}
		return `{"findings":[{"quote":"可通过官方渠道提交申请。","missing_detail":"资料未提供提交渠道"}],"uncertain":false}`, nil
	}
	trace := &typedPrimaryTrace{}
	ctx := context.WithValue(context.Background(), typedPrimaryTraceKey{}, trace)
	input := judgeInput{Answer: "可通过官方渠道提交申请。"}
	combined, e := withTypedPrimary(primary, reviewer)(ctx, input)
	if _, v, err := parse(combined, input); e != nil || err != nil || v != "unsupported" || calls != 2 || trace.Raw != raw {
		t.Fatalf("%s %v %v", v, e, err)
	}
}
func TestTypedFailureDoesNotRetryOrReview(t *testing.T) {
	primary := func(context.Context, judgeInput) (string, error) {
		return `{"segments":[{"quote":"回答","scope":"assistant_limitation","reason":"限制","assessment":null}]}`, nil
	}
	reviewer := func(context.Context, judgeInput) (string, error) {
		t.Fatal("review ran after invalid primary")
		return "", nil
	}
	_, err := withTypedPrimary(primary, reviewer)(context.Background(), judgeInput{Answer: "回答"})
	var f *judgeFailure
	if !errors.As(err, &f) || f.Phase != "primary" || f.Stage != "typed_primary_parse" {
		t.Fatalf("bad diagnostics: %v", err)
	}
}
func TestTypedOfflineAndModes(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-typed-primary", "-cases", fixturePath(t)}, &out, io.Discard, func() (judgeFunc, string, error) { t.Fatal("network in offline"); return nil, "", nil }); err != nil {
		t.Fatal(err)
	}
	var r report
	if json.Unmarshal(out.Bytes(), &r) != nil || r.SchemaVersion != 8 || r.Prompt != typedPrimaryPrompt || r.Calls != 0 {
		t.Fatal("incorrect typed plan")
	}
	for _, flag := range []string{"-classify-only", "-with-classification", "-omit-replayed-hints"} {
		if err := run(context.Background(), []string{"-typed-primary", flag}, io.Discard, io.Discard, nil); err == nil {
			t.Fatal("conflicting modes accepted")
		}
	}
}
