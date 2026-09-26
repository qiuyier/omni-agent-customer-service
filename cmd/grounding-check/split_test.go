package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

func TestSplitEquivalentEvidenceAndCoverage(t *testing.T) {
	input := judgeInput{Reference: "支持查询。", Answer: "支持查询。入口未知。"}
	for _, tc := range []struct{ status, evidence, relation, detailEvidence, want string }{
		{"supported", "支持查询。", "entailed", "支持查询。", "clear"},
		{"supported", "", "entailed", "支持查询。", "error"},
		{"supported", "支持查询。", "entailed", "", "error"},
		{"unsupported", "", "not_stated", "", "unsupported"},
		{"supported", "支持查询。", "not_stated", "", "unsupported"},
		{"unsupported", "", "contradicted", "编造证据", "error"},
		{"unsupported", "", "not_stated", "支持查询。", "error"},
		{"uncertain", "", "not_stated", "", "unsupported"},
	} {
		f := splitFact{Quote: "支持查询。", Reason: "业务事实", Status: tc.status, Evidence: tc.evidence, Checks: []detailCheck{{Detail: "支持查询", Relation: tc.relation, Evidence: tc.detailEvidence}}}
		s := splitOutput{Facts: []splitFact{f}, NonFacts: []splitNonFact{{Quote: "入口未知。", Scope: "knowledge_limit", Reason: "未知入口"}}}
		a, _ := json.Marshal(factAssessment{Status: f.Status, Evidence: f.Evidence, Checks: f.Checks})
		old, _ := json.Marshal(typedOutput{Segments: []typedSegment{{Quote: f.Quote, Scope: "system_fact", Reason: f.Reason, Assessment: a}, {Quote: "入口未知。", Scope: "knowledge_limit", Reason: "未知入口"}}})
		raw, _ := json.Marshal(s)
		_, v, err := parseSplit(string(raw), input)
		_, oldV, oldErr := parseTyped(string(old), input)
		if v != tc.want || v != oldV || (err != nil) != (oldErr != nil) {
			t.Fatalf("changed semantics: %s %v vs %s %v", v, err, oldV, oldErr)
		}
	}
	raw := `{"facts":[],"non_facts":[{"quote":"入口未知。","scope":"knowledge_limit","reason":"未知"}]}`
	if _, v, e := parseSplit(raw, input); e != nil || v != "uncertain" {
		t.Fatal("coverage loosened", v, e)
	}
}

func TestSplitRejectsIllegalFields(t *testing.T) {
	for _, raw := range []string{
		`{"facts":[],"non_facts":[{"quote":"未知","scope":"knowledge_limit","reason":"未知","assessment":null}]}`,
		`{"facts":[],"non_facts":[{"quote":"未知","scope":"knowledge_limit","reason":"未知","claim":""}]}`,
		`{"facts":[],"non_facts":[{"quote":"未知","scope":"system_fact","reason":"事实"}]}`,
		`{"facts":[],"non_facts":[{"quote":"未知","scope":"general","reason":"未知"}]}`,
		`{"facts":null,"non_facts":[]}`,
		`{"facts":[]}`,
		`{"facts":[],"non_facts":[]}`,
		`{"facts":[{"quote":"未知","scope":"system_fact","reason":"事实","status":"uncertain","evidence":"","checks":[]}],"non_facts":[]}`,
	} {
		if _, _, err := parseSplit(raw, judgeInput{Answer: "未知"}); err == nil {
			t.Fatal("invalid accepted", raw)
		}
	}
}

func TestSplitFailureNoRetryAndIndependentReview(t *testing.T) {
	for _, valid := range []bool{false, true} {
		calls := 0
		primary := func(context.Context, judgeInput) (string, error) {
			calls++
			if !valid {
				return `{"facts":[],"non_facts":null}`, nil
			}
			return `{"facts":[],"non_facts":[{"quote":"可提交","scope":"inquiry","reason":"模拟错误分类"}]}`, nil
		}
		reviewer := func(_ context.Context, in judgeInput) (string, error) {
			calls++
			if in.Answer != "可提交" || len(in.Classification) != 0 {
				t.Fatal("review not independent")
			}
			return `{"findings":[{"quote":"可提交","missing_detail":"无提交依据"}],"uncertain":false}`, nil
		}
		trace := &typedPrimaryTrace{}
		ctx := context.WithValue(context.Background(), typedPrimaryTraceKey{}, trace)
		raw, err := withPrimaryParser(primary, reviewer, parseSplit, "split_primary_parse")(ctx, judgeInput{Answer: "可提交"})
		if valid {
			_, v, e := parse(raw, judgeInput{Answer: "可提交"})
			if err != nil || e != nil || v != "unsupported" || calls != 2 {
				t.Fatal(v, err, e, calls)
			}
		} else {
			var f *judgeFailure
			if !errors.As(err, &f) || f.Stage != "split_primary_parse" || f.Phase != "primary" || calls != 1 {
				t.Fatal(err, calls)
			}
		}
		if trace.Raw == "" {
			t.Fatal("lost raw")
		}
	}
}

func TestSplitOfflineModes(t *testing.T) {
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-split-primary", "-cases", fixturePath(t)}, &out, io.Discard, nil); err != nil {
		t.Fatal(err)
	}
	var r report
	if json.Unmarshal(out.Bytes(), &r) != nil || r.SchemaVersion != 10 || r.Prompt != splitPrimaryPrompt || r.ReviewPrompt != specificityPrompt || r.ProviderRequests != 0 {
		t.Fatal("wrong offline plan")
	}
	for _, flag := range []string{"-typed-primary", "-inventory-review", "-classify-only", "-with-classification", "-omit-replayed-hints"} {
		if err := run(context.Background(), []string{"-split-primary", flag}, io.Discard, io.Discard, nil); err == nil {
			t.Fatal("conflict accepted", flag)
		}
	}
}
