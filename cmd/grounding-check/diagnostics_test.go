package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSeparatedAdvice(t *testing.T) {
	input := judgeInput{Answer: "通过账号功能查询。请咨询官方资料。"}
	j := judgment{Claims: []claim{{Quote: "通过账号功能查询。", Kind: "unsupported", Reason: "无依据", Checks: []detailCheck{{Detail: "查询功能存在", Relation: "not_stated"}}}}, Advice: []generalAdvice{{Quote: "请咨询官方资料。", AdviceKind: "information_seeking", Reason: "求证"}}}
	raw, _ := json.Marshal(j)
	if _, v, e := parse(string(raw), input); e != nil || v != "unsupported" {
		t.Fatalf("%s %v", v, e)
	}
	j.Claims[0].Checks = append(j.Claims[0].Checks, detailCheck{Detail: "通用建议", Relation: "entailed"})
	raw, _ = json.Marshal(j)
	if _, _, e := parse(string(raw), input); e == nil || e.Error() != "invalid_detail_evidence" {
		t.Fatal("empty evidence accepted")
	}
	for _, tc := range []struct{ raw, want string }{
		{`{"claims":[],"advice":[{"quote":"请咨询官方资料。","advice_kind":"information_seeking","reason":"求证"}]}`, "clear"},
		{`{"advice":[{"quote":"请咨询官方资料。","advice_kind":"capability_use","reason":"操作"}]}`, "error"},
		{`{"advice":[{"quote":"请咨询官方资料。","advice_kind":"information_seeking","reason":"求证","evidence":""}]}`, "error"},
		{`{"advice":[{"quote":"请咨询","advice_kind":"information_seeking","reason":"求证"}]}`, "uncertain"},
	} {
		if _, v, _ := parse(tc.raw, judgeInput{Answer: "请咨询官方资料。"}); v != tc.want {
			t.Fatalf("%s: %s", tc.raw, v)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failedBody struct{}

func (failedBody) Read([]byte) (int, error) { return 0, context.DeadlineExceeded }
func (failedBody) Close() error             { return nil }

func TestTransportFailureStages(t *testing.T) {
	for _, tc := range []struct {
		name, stage, code, body string
		status                  int
		send, read              bool
	}{
		{name: "send", stage: "request_send", code: "request_send_failed", send: true},
		{name: "status", stage: "http_status", code: "provider_http_status", status: 429, body: "secret"},
		{name: "read", stage: "body_read", code: "model_timeout", status: 200, read: true},
		{name: "decode", stage: "response_decode", code: "invalid_provider_json", status: 200, body: "secret"},
		{name: "finish", stage: "response_finish", code: "incomplete_provider_response", status: 200, body: `{"choices":[{"finish_reason":"length"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				if tc.send {
					return nil, errors.New("secret URL key")
				}
				var body io.ReadCloser = io.NopCloser(strings.NewReader(tc.body))
				if tc.read {
					body = failedBody{}
				}
				return &http.Response{StatusCode: tc.status, Body: body, Header: make(http.Header)}, nil
			})}
			_, err := chatJudge(client, "https://example.invalid", "secret-key", "fixture")(context.Background(), judgeInput{})
			var f *judgeFailure
			if !errors.As(err, &f) || f.Stage != tc.stage || f.Code != tc.code || calls != 1 {
				t.Fatalf("failure: %+v calls=%d", f, calls)
			}
			if tc.read && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("lost timeout")
			}
			if tc.status == 429 && f.HTTPStatus != 429 {
				t.Fatal("lost status")
			}
			data, _ := json.Marshal(f)
			if strings.Contains(string(data)+err.Error(), "secret") {
				t.Fatal("leaked cause")
			}
		})
	}
}

func TestReviewDiagnosticsAndPrimaryPreservation(t *testing.T) {
	primaryRaw := `{"claims":[],"advice":[{"quote":"回答","advice_kind":"none","reason":"未知"}]}`
	for _, tc := range []struct {
		name, stage, raw string
		err              error
	}{
		{name: "status", stage: "http_status", err: &judgeFailure{Stage: "http_status", Code: "provider_http_status", HTTPStatus: 429}},
		{name: "parse", stage: "review_parse", raw: "secret malformed review"},
		{name: "validation", stage: "review_validation", raw: `{"findings":[],"uncertain":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			judge := withSpecificityReview(func(context.Context, judgeInput) (string, error) { calls++; return primaryRaw, nil }, func(context.Context, judgeInput) (string, error) { calls++; return tc.raw, tc.err })
			var out bytes.Buffer
			err := run(context.Background(), []string{"-cases", fixturePath(t), "-allow-model-api"}, &out, io.Discard, func() (judgeFunc, string, error) { return judge, "fixture", nil })
			var r report
			if err == nil || json.Unmarshal(out.Bytes(), &r) != nil || calls != 2 || r.Complete || r.Summary.Errors != 1 {
				t.Fatalf("bad report: %s", out.String())
			}
			got := r.Results[0]
			if got.Failure == nil || got.Failure.Phase != "review" || got.Failure.Stage != tc.stage || got.Raw != primaryRaw || len(got.Advice) != 1 {
				t.Fatalf("lost diagnostics: %+v", got)
			}
			if strings.Contains(out.String(), "secret malformed") {
				t.Fatal("review failure body leaked")
			}
		})
	}
}

func TestPrimaryDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, raw, stage string
		err              error
		stops            bool
	}{
		{name: "status", stage: "http_status", err: &judgeFailure{Stage: "http_status", Code: "provider_http_status", HTTPStatus: 429}, stops: true},
		{name: "parse", raw: "not JSON", stage: "judgment_parse"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			judge := withSpecificityReview(func(context.Context, judgeInput) (string, error) { calls++; return tc.raw, tc.err }, func(context.Context, judgeInput) (string, error) { t.Fatal("unexpected review"); return "", nil })
			var out bytes.Buffer
			err := run(context.Background(), []string{"-cases", fixturePath(t), "-allow-model-api"}, &out, io.Discard, func() (judgeFunc, string, error) { return judge, "fixture", nil })
			var r report
			if json.Unmarshal(out.Bytes(), &r) != nil || (err != nil) != tc.stops || calls != 1 || r.Summary.Errors != 1 {
				t.Fatalf("bad report: %s", out.String())
			}
			f := r.Results[0].Failure
			if f == nil || f.Phase != "primary" || f.Stage != tc.stage {
				t.Fatalf("bad failure: %+v", f)
			}
		})
	}
}
