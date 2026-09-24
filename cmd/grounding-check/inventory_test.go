package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

func TestInventoryMustBeExplicit(t *testing.T) {
	if err := run(context.Background(), []string{"-inventory-review"}, io.Discard, io.Discard, nil); err == nil {
		t.Fatal("inventory enabled without typed mode")
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-typed-primary", "-inventory-review", "-cases", fixturePath(t)}, &out, io.Discard, nil); err != nil {
		t.Fatal(err)
	}
	var r report
	if json.Unmarshal(out.Bytes(), &r) != nil || r.SchemaVersion != 9 || r.ReviewPrompt != inventoryReviewPrompt || r.Calls != 0 {
		t.Fatal("incorrect opt-in plan")
	}
}

func TestInventoryEvidenceAndCoverage(t *testing.T) {
	input := judgeInput{Answer: "可查询。入口未知。", Reference: "支持查询。"}
	for _, tc := range []struct {
		raw                string
		findings           int
		uncertain, invalid bool
	}{
		{raw: `{"checks":[{"quote":"可查询。","claim":"支持查询","relation":"entailed","evidence":"支持查询。"}],"non_facts":[{"quote":"入口未知。","reason":"未知位置"}],"uncertain":false}`},
		{raw: `{"checks":[{"quote":"可查询。","claim":"支持查询","relation":"not_stated","evidence":""}],"non_facts":[{"quote":"入口未知。","reason":"未知位置"}],"uncertain":false}`, findings: 1},
		{raw: `{"checks":[{"quote":"可查询。","claim":"支持查询","relation":"entailed","evidence":"支持查询。"}],"non_facts":[],"uncertain":false}`, uncertain: true},
		{raw: `{"checks":[{"quote":"可查询。","claim":"支持查询","relation":"entailed","evidence":""}],"non_facts":[],"uncertain":false}`, invalid: true},
		{raw: `{"checks":[],"non_facts":[{"quote":"不存在","reason":"未知"}],"uncertain":false}`, invalid: true},
		{raw: `{"checks":[],"non_facts":[],"uncertain":false}`, invalid: true},
		{raw: `{"checks":[],"non_facts":[{"quote":"可查询。入口未知。","reason":"故意错误分类","note":"不接受额外字段"}],"uncertain":false}`, invalid: true},
		{raw: `{"checks":[],"non_facts":null,"uncertain":false}`, invalid: true},
	} {
		r, err := parseInventory(tc.raw, input)
		if (err != nil) != tc.invalid {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		if err == nil && (len(r.Findings) != tc.findings || *r.Uncertain != tc.uncertain) {
			t.Fatalf("review: %+v", r)
		}
	}
}
func TestInventoryVetoesHiddenCapabilityWithoutThirdCall(t *testing.T) {
	input := judgeInput{Answer: "可查询。入口未知。", Reference: "提供服务。"}
	calls := 0
	primary := func(context.Context, judgeInput) (string, error) {
		calls++
		return `{"segments":[{"quote":"可查询。入口未知。","scope":"knowledge_limit","reason":"故意模拟合并漏判"}]}`, nil
	}
	reviewer := func(_ context.Context, got judgeInput) (string, error) {
		calls++
		if got.Answer != input.Answer || len(got.Classification) != 0 {
			t.Fatal("review not independent")
		}
		return `{"checks":[{"quote":"可查询。","claim":"系统支持查询","relation":"not_stated","evidence":""}],"non_facts":[{"quote":"入口未知。","reason":"位置未知"}],"uncertain":false}`, nil
	}
	trace := &inventoryTrace{}
	ctx := context.WithValue(context.Background(), inventoryTraceKey{}, trace)
	raw, err := withTypedPrimary(primary, withInventoryReview(reviewer))(ctx, input)
	j, v, e := parse(raw, input)
	if err != nil || e != nil || v != "unsupported" || j.Review == nil || calls != 2 || trace.Raw == "" {
		t.Fatalf("veto: %s %v %v", v, err, e)
	}
}
func TestInventoryFailureIsReviewFailure(t *testing.T) {
	primary := func(context.Context, judgeInput) (string, error) {
		return `{"segments":[{"quote":"未知","scope":"knowledge_limit","reason":"未知"}]}`, nil
	}
	review := func(context.Context, judgeInput) (string, error) {
		return `{"checks":[],"non_facts":[],"uncertain":false}`, nil
	}
	raw, err := withTypedPrimary(primary, withInventoryReview(review))(context.Background(), judgeInput{Answer: "未知"})
	var f *judgeFailure
	if !errors.As(err, &f) || f.Phase != "review" || f.Stage != "inventory_review_parse" || raw == "" {
		t.Fatalf("failure: %v", err)
	}
}
