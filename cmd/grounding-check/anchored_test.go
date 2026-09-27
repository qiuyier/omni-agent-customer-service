package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnswerAnchorsLossless(t *testing.T) {
	for _, answer := range []string{"", "\n\n ", "\n\n您好！\n\n很抱歉。\n", "您好！很抱歉。", "支持查询，入口未知。", "😀设备\r\n1. 设置密码；\n", "同句。同句。", "我不能代办但用户可以下载记录"} {
		var restored strings.Builder
		for i, a := range answerAnchors(answer) {
			if strings.TrimSpace(answer) != "" && strings.TrimSpace(a.Text) == "" {
				t.Fatal("standalone blank anchor")
			}
			if a.ID != i+1 {
				t.Fatal("unstable id")
			}
			restored.WriteString(a.Text)
		}
		if restored.String() != answer {
			t.Fatal("changed bytes")
		}
	}
}

func TestAnchoredWhitespaceNoLongerRequiresBlankClaim(t *testing.T) {
	in := judgeInput{Answer: "\n您好！\n\n请勿泄露密码。\n"}
	a := answerAnchors(in.Answer)
	if len(a) != 2 {
		t.Fatal(a)
	}
	raw := `{"segments":[{"start_id":1,"end_id":1,"scope":"social","reason":"问候"},{"start_id":2,"end_id":2,"scope":"social","reason":"安全提醒"}]}`
	j, v, e := parseAnchored(raw, in)
	if e != nil || v != "clear" || j.Advice[0].Quote != "\n您好！\n\n" || j.Advice[1].Quote != "请勿泄露密码。\n" {
		t.Fatal(j, v, e)
	}
}
func TestAnchoredValidation(t *testing.T) {
	in := judgeInput{Answer: "可查询，入口未知。", Reference: "支持查询。"}
	for _, tc := range []struct{ raw, want string }{
		{`{"segments":[{"start_id":1,"end_id":1,"scope":"system_fact","reason":"业务","assessment":{"status":"supported","evidence":"支持查询。","checks":[{"detail":"查询","relation":"entailed","evidence":"支持查询。"}]}},{"start_id":2,"end_id":2,"scope":"knowledge_limit","reason":"入口未知"}]}`, "clear"},
		{`{"segments":[{"start_id":1,"end_id":2,"scope":"system_fact","reason":"业务","assessment":{"status":"unsupported","evidence":"","checks":[{"detail":"查询","relation":"not_stated","evidence":""}]}}]}`, "unsupported"},
		{`{"segments":[{"start_id":1,"end_id":2,"scope":"system_fact","reason":"业务","assessment":{"status":"supported","evidence":"","checks":[{"detail":"查询","relation":"entailed","evidence":"支持查询。"}]}}]}`, "error"},
		{`{"segments":[{"start_id":1,"end_id":1,"scope":"social","reason":"故意漏掉片段"}]}`, "error"},
		{`{"segments":[{"start_id":0,"end_id":2,"scope":"social","reason":"非法编号"}]}`, "error"},
		{`{"segments":[{"start_id":2,"end_id":1,"scope":"social","reason":"逆序"}]}`, "error"},
		{`{"segments":[{"start_id":1,"end_id":3,"scope":"social","reason":"越界"}]}`, "error"},
		{`{"segments":[{"start_id":1,"end_id":2,"scope":"social","reason":"非事实","assessment":null}]}`, "error"},
		{`{"segments":[{"start_id":1,"end_id":2,"scope":"social","reason":"非事实","quote":"改写"}]}`, "error"},
	} {
		_, v, e := parseAnchored(tc.raw, in)
		if v != tc.want || (e != nil) != (tc.want == "error") {
			t.Fatal(v, e, tc.raw)
		}
	}
}
func TestAnchoredMixedSpanStillNeedsIndependentReview(t *testing.T) {
	in := judgeInput{Answer: "我不能代办但您可以下载记录"}
	if len(answerAnchors(in.Answer)) != 1 {
		t.Fatal("unexpected split")
	}
	primary := func(context.Context, judgeInput) (string, error) {
		return `{"segments":[{"start_id":1,"end_id":1,"scope":"assistant_limitation","reason":"模拟语义漏判"}]}`, nil
	}
	reviewer := func(_ context.Context, got judgeInput) (string, error) {
		if got.Answer != in.Answer {
			t.Fatal("context lost")
		}
		return `{"findings":[{"quote":"您可以下载记录","missing_detail":"未提供下载能力"}],"uncertain":false}`, nil
	}
	raw, e := withPrimaryParser(primary, reviewer, parseAnchored, "anchored_primary_parse")(context.Background(), in)
	_, v, err := parse(raw, in)
	if e != nil || err != nil || v != "unsupported" {
		t.Fatal(v, e, err)
	}
	// A missing ID is an error, even when the omitted text appears elsewhere.
	_, _, err = parseAnchored(`{"segments":[{"start_id":1,"end_id":1,"scope":"social","reason":"重复文字"}]}`, judgeInput{Answer: "您好。您好。"})
	if err == nil || err.Error() != "missing_answer_anchor" {
		t.Fatal("missing occurrence accepted")
	}
}
func TestAnchoredStrictSchemaBranches(t *testing.T) {
	s := anchoredSchema(4)
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if x["type"] == "object" {
				props := x["properties"].(map[string]any)
				required := x["required"].([]string)
				if x["additionalProperties"] != false || len(props) != len(required) {
					t.Fatal("not strict")
				}
				for _, k := range required {
					if _, ok := props[k]; !ok {
						t.Fatal("missing required property")
					}
				}
			}
			for _, child := range x {
				visit(child)
			}
		case []any:
			for _, child := range x {
				visit(child)
			}
		}
	}
	visit(s)
}
func TestAnchoredHTTPContract(t *testing.T) {
	for _, tc := range []struct {
		finish, name string
		status       int
		good         bool
	}{{"tool_calls", "submit_review", 200, true}, {"stop", "submit_review", 200, false}, {"tool_calls", "other", 200, false}, {"length", "submit_review", 200, false}, {"tool_calls", "submit_review", 400, false}} {
		calls := 0
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Fatal("bad request")
			}
			if r.Header.Get("Authorization") != "Bearer fixture" || body["thinking"].(map[string]any)["type"] != "disabled" {
				t.Error("bad transport")
			}
			fn := body["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)
			if fn["strict"] != true || fn["name"] != "submit_review" {
				t.Error("strict missing")
			}
			var input struct {
				Answer  string         `json:"answer"`
				Anchors []answerAnchor `json:"answer_anchors"`
			}
			msg := body["messages"].([]any)[1].(map[string]any)["content"].(string)
			if json.Unmarshal([]byte(msg), &input) != nil || input.Answer != "您好！" || len(input.Anchors) != 1 {
				t.Error("lost full context")
			}
			w.WriteHeader(tc.status)
			if tc.status != 200 {
				io.WriteString(w, "secret provider body")
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": tc.finish, "message": map[string]any{"tool_calls": []any{map[string]any{"type": "function", "function": map[string]any{"name": tc.name, "arguments": "{}"}}}}}}})
		}))
		count := 0
		ctx := context.WithValue(context.Background(), requestCounterKey{}, &count)
		raw, e := anchoredHTTP(s.Client(), s.URL, "fixture", "deepseek-flash")(ctx, judgeInput{Answer: "您好！"})
		s.Close()
		if (e == nil) != tc.good || calls != 1 || count != 1 {
			t.Fatal("unexpected transport", e, calls, count)
		}
		if tc.good && raw != "{}" {
			t.Fatal("lost raw")
		}
		if e != nil && strings.Contains(e.Error(), "secret") {
			t.Fatal("leaked error")
		}
	}
}
func TestAnchoredOfflineAndInvalidPrimary(t *testing.T) {
	var out bytes.Buffer
	if e := run(context.Background(), []string{"-anchored-primary", "-cases", fixturePath(t)}, &out, io.Discard, nil); e != nil {
		t.Fatal(e)
	}
	var r report
	if json.Unmarshal(out.Bytes(), &r) != nil || r.SchemaVersion != 11 || r.ProviderRequests != 0 || r.Prompt != anchoredPrompt {
		t.Fatal("bad offline plan")
	}
	for _, flag := range []string{"-typed-primary", "-split-primary", "-inventory-review", "-classify-only", "-with-classification"} {
		if e := run(context.Background(), []string{"-anchored-primary", flag}, io.Discard, io.Discard, nil); e == nil {
			t.Fatal("conflict accepted")
		}
	}
	p := func(context.Context, judgeInput) (string, error) { return `{"segments":[]}`, nil }
	rv := func(context.Context, judgeInput) (string, error) {
		t.Fatal("review after invalid primary")
		return "", nil
	}
	_, err := withPrimaryParser(p, rv, parseAnchored, "anchored_primary_parse")(context.Background(), judgeInput{Answer: "您好"})
	var f *judgeFailure
	if !errors.As(err, &f) || f.Phase != "primary" || f.Stage != "anchored_primary_parse" {
		t.Fatal(err)
	}
}
