package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOfflineDoesNotConstructJudge(t *testing.T) {
	var out, progress bytes.Buffer
	err := run(context.Background(), []string{"-cases", "../../examples/knowledge/webchat/grounding_judge_cases.json"}, &out, &progress, func() (judgeFunc, string, error) {
		t.Fatal("offline mode constructed network judge")
		return nil, "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var r report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Mode != "offline_validation_only" || r.Calls != 0 || len(r.Samples) != 12 || len(r.Results) != 0 || !r.Complete {
		t.Fatalf("unexpected dry-run: %+v", r)
	}
}

func TestParseJudgment(t *testing.T) {
	input := judgeInput{Reference: "打开工作台。", Answer: "打开工作台。不要分享验证码。"}
	tests := []struct{ name, raw, want string }{
		{"supported", `{"claims":[{"quote":"打开工作台。","kind":"supported","evidence":"打开工作台。","reason":"直接支持"}]}`, "clear"},
		{"general", `{"claims":[{"quote":"不要分享验证码。","kind":"general","evidence":"","reason":"安全建议"}]}`, "clear"},
		{"unsupported", `{"claims":[{"quote":"打开工作台。","kind":"unsupported","evidence":"","reason":"测试判定"}]}`, "unsupported"},
		{"uncertain", `{"claims":[{"quote":"打开工作台。","kind":"uncertain","evidence":"","reason":"含义不清"}]}`, "uncertain"},
		{"missing-evidence", `{"claims":[{"quote":"打开工作台。","kind":"supported","evidence":"","reason":"test"}]}`, "error"},
		{"invented-evidence", `{"claims":[{"quote":"打开工作台。","kind":"supported","evidence":"短信","reason":"test"}]}`, "error"},
		{"invented-quote", `{"claims":[{"quote":"邮件","kind":"general","evidence":"","reason":"test"}]}`, "error"},
		{"bad-kind", `{"claims":[{"quote":"打开工作台。","kind":"pass","evidence":"","reason":"test"}]}`, "error"},
		{"empty", `{"claims":[]}`, "error"},
		{"null", `null`, "error"},
		{"extra-field", `{"claims":[],"verdict":"clear"}`, "error"},
		{"trailing", `{"claims":[]} {}`, "error"},
		{"fence", "```json\n{}\n```", "error"},
		{"empty-reason", `{"claims":[{"quote":"打开工作台。","kind":"general","evidence":"","reason":""}]}`, "error"},
		{"unexpected-evidence", `{"claims":[{"quote":"打开工作台。","kind":"general","evidence":"打开工作台。","reason":"test"}]}`, "error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, v, err := parse(tc.raw, input)
			if v != tc.want || (err != nil) != (tc.want == "error") {
				t.Fatalf("got %s %v", v, err)
			}
		})
	}
}

func fixturePath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cases.json")
	data, _ := json.Marshal([]sample{{ID: "label-secret", Reference: "资料", History: []turn{{"user", "问题"}}, Answer: "回答", Expected: "unsupported", Rationale: "rationale-secret", Origin: "origin-secret"}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestJudgeCannotSeeLabelsAndReportDoesNotRepair(t *testing.T) {
	path := fixturePath(t)
	var out, progress bytes.Buffer
	calls := 0
	err := run(context.Background(), []string{"-cases", path, "-allow-model-api"}, &out, &progress, func() (judgeFunc, string, error) {
		return func(_ context.Context, input judgeInput) (string, error) {
			calls++
			data, _ := json.Marshal(input)
			for _, secret := range []string{"label-secret", "unsupported", "rationale-secret", "origin-secret"} {
				if strings.Contains(string(data), secret) {
					t.Fatal("labels leaked")
				}
			}
			return `{"claims":[{"quote":"回答","kind":"unsupported","evidence":"","reason":"资料未说明"}]}`, nil
		}, "fixture", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var r report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !r.Complete || r.Summary.UnsupportedDetected != 1 || r.Samples[0].Answer != "回答" {
		t.Fatal("incorrect report")
	}
}
func TestCallFailureIsNotPassAndNotRetried(t *testing.T) {
	path := fixturePath(t)
	var out, progress bytes.Buffer
	calls := 0
	err := run(context.Background(), []string{"-cases", path, "-allow-model-api"}, &out, &progress, func() (judgeFunc, string, error) {
		return func(context.Context, judgeInput) (string, error) {
			calls++
			return "secret raw", errors.New("secret provider")
		}, "fixture", nil
	})
	if err == nil || calls != 1 {
		t.Fatal("expected one failed call")
	}
	if strings.Contains(out.String()+err.Error(), "secret provider") || strings.Contains(out.String(), "secret raw") {
		t.Fatal("provider failure leaked")
	}
	var r report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Complete || r.Summary.Errors != 1 || r.Results[0].Verdict != "error" {
		t.Fatal("failure counted as success")
	}
}
func TestSummarySeparatesMissesFalseAlarmsAndAbstentions(t *testing.T) {
	s := aggregate([]result{{Expected: "unsupported", Verdict: "unsupported"}, {Expected: "unsupported", Verdict: "clear"}, {Expected: "clear", Verdict: "unsupported"}, {Expected: "clear", Verdict: "clear"}, {Expected: "unsupported", Verdict: "uncertain"}, {Expected: "clear", Verdict: "error"}})
	if s != (summary{1, 1, 1, 1, 1, 1}) {
		t.Fatalf("summary = %+v", s)
	}
}
func TestLiveJudgeRequiresExplicitHTTPSAndProtocol(t *testing.T) {
	t.Setenv("AI_PROVIDER", "glm")
	t.Setenv("AI_MODEL", "fixture")
	t.Setenv("AI_API_KEY", "fixture")
	t.Setenv("AI_PROTOCOL", "chat_completions")
	for _, endpoint := range []string{"", "http://127.0.0.1:8092", "https://user:pass@example.com", "https://example.com?q=secret", "https://example.com#fragment"} {
		t.Setenv("AI_BASE_URL", endpoint)
		if _, _, err := liveJudge(); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
}

func TestInvalidCorpusNeverConstructsJudge(t *testing.T) {
	for _, data := range []string{`[]`, `null`, `[{"id":"x","extra":1}]`, `[] {}`, `[{"id":"x","answer":"a","expected":"pass"}]`} {
		path := filepath.Join(t.TempDir(), "cases.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		err := run(context.Background(), []string{"-cases", path, "-allow-model-api"}, &bytes.Buffer{}, &bytes.Buffer{}, func() (judgeFunc, string, error) {
			t.Fatal("invalid corpus constructed judge")
			return nil, "", nil
		})
		if err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}

func TestMalformedJudgmentRecordedAsErrorWithoutRetry(t *testing.T) {
	var out bytes.Buffer
	calls := 0
	err := run(context.Background(), []string{"-cases", fixturePath(t), "-allow-model-api"}, &out, &bytes.Buffer{}, func() (judgeFunc, string, error) {
		return func(context.Context, judgeInput) (string, error) { calls++; return "not JSON", nil }, "fixture", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var r report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !r.Complete || r.Summary.Errors != 1 || r.Results[0].Raw != "not JSON" {
		t.Fatal("malformed answer not preserved as error")
	}
}

func TestUnsupportedTakesPrecedenceOverUncertain(t *testing.T) {
	for _, kinds := range [][]string{{"unsupported", "uncertain"}, {"uncertain", "unsupported"}} {
		j := judgment{Claims: []claim{{Quote: "a", Kind: kinds[0], Reason: "test"}, {Quote: "b", Kind: kinds[1], Reason: "test"}}}
		data, _ := json.Marshal(j)
		_, verdict, err := parse(string(data), judgeInput{Answer: "ab"})
		if err != nil || verdict != "unsupported" {
			t.Fatalf("%s %v", verdict, err)
		}
	}
}

func TestHTTPJudgeTransport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		good   bool
	}{
		{"success", 200, `{"choices":[{"message":{"content":"raw judgment"},"finish_reason":"stop"}]}`, true},
		{"rate-limit", 429, `secret provider body`, false},
		{"truncated", 200, `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`, false},
		{"empty", 200, `{"choices":[]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer fixture-key" {
					t.Error("invalid request")
				}
				data, _ := io.ReadAll(r.Body)
				var request struct {
					Model    string `json:"model"`
					Messages []turn `json:"messages"`
					Stream   bool   `json:"stream"`
				}
				if json.Unmarshal(data, &request) != nil || request.Model != "fixture-model" || len(request.Messages) != 2 || request.Messages[0].Content != judgePrompt || request.Messages[1].Role != "user" || request.Stream {
					t.Error("wrong prompt transport")
				}
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer s.Close()
			got, err := chatJudge(s.Client(), s.URL+"/chat/completions", "fixture-key", "fixture-model")(context.Background(), judgeInput{Answer: "test"})
			if calls != 1 || (err == nil) != tc.good {
				t.Fatalf("calls=%d error=%v", calls, err)
			}
			if tc.good && got != "raw judgment" {
				t.Fatal("lost content")
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("provider body leaked")
			}
		})
	}
}

func TestHTTPTimeoutIsCategorized(t *testing.T) {
	release := make(chan struct{})
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer s.Close()
	defer close(release)
	client := s.Client()
	client.Timeout = 50 * time.Millisecond
	_, err := chatJudge(client, s.URL, "fixture", "fixture")(context.Background(), judgeInput{Answer: "test"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected sanitized timeout, got %v", err)
	}
}
