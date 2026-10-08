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
	err := run(context.Background(), []string{"-cases", "../../examples/regression/knowledge/webchat/grounding_judge_cases.json"}, &out, &progress, func() (judgeFunc, string, error) {
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
	input := judgeInput{Reference: "打开工作台。", Answer: "打开工作台。"}
	tests := []struct{ name, raw, want string }{
		{"supported", `{"claims":[{"quote":"打开工作台。","kind":"supported","evidence":"打开工作台。","reason":"直接支持","checks":[{"detail":"打开工作台","relation":"entailed","evidence":"打开工作台。"}]}]}`, "clear"},
		{"unsupported", `{"claims":[{"quote":"打开工作台。","kind":"unsupported","evidence":"","reason":"测试判定"}]}`, "unsupported"},
		{"uncertain", `{"claims":[{"quote":"打开工作台。","kind":"uncertain","evidence":"","reason":"含义不清"}]}`, "uncertain"},
		{"missing-evidence", `{"claims":[{"quote":"打开工作台。","kind":"supported","evidence":"","reason":"test"}]}`, "error"},
		{"invented-evidence", `{"claims":[{"quote":"打开工作台。","kind":"supported","evidence":"短信","reason":"test"}]}`, "error"},
		{"invented-quote", `{"claims":[{"quote":"邮件","kind":"general","advice_kind":"none","evidence":"","reason":"test"}]}`, "error"},
		{"bad-kind", `{"claims":[{"quote":"打开工作台。","kind":"pass","evidence":"","reason":"test"}]}`, "error"},
		{"empty", `{"claims":[]}`, "error"},
		{"null", `null`, "error"},
		{"extra-field", `{"claims":[],"verdict":"clear"}`, "error"},
		{"trailing", `{"claims":[]} {}`, "error"},
		{"fence", "```json\n{}\n```", "error"},
		{"empty-reason", `{"claims":[{"quote":"打开工作台。","kind":"general","advice_kind":"none","evidence":"","reason":""}]}`, "error"},
		{"unexpected-evidence", `{"claims":[{"quote":"打开工作台。","kind":"general","advice_kind":"none","evidence":"打开工作台。","reason":"test"}]}`, "error"},
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

func TestAdviceBoundaryIsSharedAndDoesNotRelaxCoverage(t *testing.T) {
	for _, prompt := range []string{judgePrompt, specificityPrompt} {
		if !strings.Contains(prompt, adviceBoundaryRules) {
			t.Fatal("stages disagree about advice policy")
		}
	}
	input := judgeInput{Answer: "建议您通过平台查询个人记录，具体入口未知。"}
	c := claim{Quote: input.Answer, Kind: "general", AdviceKind: "capability_use", Reason: "marked general incorrectly"}
	check := func(want string) {
		t.Helper()
		raw, _ := json.Marshal(judgment{Claims: []claim{c}})
		_, v, _ := parse(string(raw), input)
		if v != want {
			t.Fatalf("got %s want %s", v, want)
		}
	}
	check("error") // A capability cannot bypass detail checks through a general label.
	c.Checks = []detailCheck{{Detail: "平台能查询个人记录", Relation: "not_stated"}}
	check("unsupported")
	c.AdviceKind = "information_seeking"
	check("error") // Information-seeking cannot carry hidden capability checks.
	c.Checks = nil
	c.AdviceKind = ""
	check("error")
	c.AdviceKind = "anything"
	check("error")
	input = judgeInput{Answer: "请自行咨询人工，了解办理方法。随后可通过平台执行修改。"}
	c = claim{Quote: "请自行咨询人工，了解办理方法。", Kind: "general", AdviceKind: "information_seeking", Reason: "generic inquiry"}
	check("uncertain") // New advice types do not authorize skipping the remaining text.
	input.Answer = c.Quote
	check("clear")
}

func TestDetailChecksControlVerdict(t *testing.T) {
	input := judgeInput{Reference: "身份验证。", Answer: "请扫描二维码完成身份验证。"}
	base := claim{Quote: input.Answer, Kind: "supported", Evidence: input.Reference, Reason: "model says supported"}
	for _, relation := range []string{"not_stated", "contradicted"} {
		c := base
		c.Checks = []detailCheck{{Detail: "扫描二维码", Relation: relation}}
		if relation == "contradicted" {
			c.Checks[0].Evidence = input.Reference
		}
		raw, _ := json.Marshal(judgment{Claims: []claim{c}})
		_, verdict, err := parse(string(raw), input)
		if err != nil || verdict != "unsupported" {
			t.Fatalf("inconsistent model kind overrode missing evidence: %s %v", verdict, err)
		}
	}
	for _, checks := range [][]detailCheck{nil, {{Detail: "", Relation: "not_stated"}}, {{Detail: "扫描二维码", Relation: "entailed", Evidence: "扫描二维码"}}, {{Detail: "扫描二维码", Relation: "maybe"}}, {{Detail: "扫描二维码", Relation: "not_stated", Evidence: input.Reference}}} {
		c := base
		c.Checks = checks
		raw, _ := json.Marshal(judgment{Claims: []claim{c}})
		if _, v, err := parse(string(raw), input); v != "error" || err == nil {
			t.Fatalf("invalid detail checks accepted: %+v", checks)
		}
	}
}

func TestClearRequiresAnswerCoverage(t *testing.T) {
	input := judgeInput{Reference: "打开工作台。", Answer: "打开工作台。然后会收到短信。"}
	c := claim{Quote: "打开工作台。", Kind: "supported", Evidence: input.Reference, Reason: "supported", Checks: []detailCheck{{Detail: "打开工作台", Relation: "entailed", Evidence: input.Reference}}}
	raw, _ := json.Marshal(judgment{Claims: []claim{c}})
	if _, v, err := parse(string(raw), input); v != "uncertain" || err != nil {
		t.Fatalf("unchecked sentence passed: %s %v", v, err)
	}
	if !coversAnswer("**你好**，不要分享验证码。", []claim{{Quote: "你好"}, {Quote: "不要分享验证码"}}) {
		t.Fatal("formatting rejected")
	}
	if coversAnswer("最多2次", []claim{{Quote: "最多"}, {Quote: "次"}}) {
		t.Fatal("unchecked number passed")
	}
	safe := judgeInput{Answer: "不要分享验证码。"}
	safeRaw := `{"claims":[{"quote":"不要分享验证码。","kind":"general","advice_kind":"none","evidence":"","reason":"通用安全建议","checks":[]}]}`
	if _, v, err := parse(safeRaw, safe); err != nil || v != "clear" {
		t.Fatal("general safety advice rejected")
	}
}

func TestIndependentReviewCanVetoButNotEraseUncertainty(t *testing.T) {
	input := judgeInput{Reference: "完成验证", Answer: "在页面填入验证码"}
	primary := func(context.Context, judgeInput) (string, error) {
		return `{"claims":[{"quote":"在页面填入验证码","kind":"supported","evidence":"完成验证","reason":"bad model inference","checks":[{"detail":"页面输入代码","relation":"entailed","evidence":"完成验证"}]}]}`, nil
	}
	reviewCalls := 0
	reviewer := func(_ context.Context, got judgeInput) (string, error) {
		reviewCalls++
		if got.Reference != input.Reference || got.Answer != input.Answer {
			t.Fatal("review did not receive original data")
		}
		return `{"findings":[{"quote":"在页面填入验证码","missing_detail":"验证机制未说明"}],"uncertain":false}`, nil
	}
	raw, err := withSpecificityReview(primary, reviewer)(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	j, v, err := parse(raw, input)
	if err != nil || v != "unsupported" || j.Review == nil || reviewCalls != 1 {
		t.Fatalf("veto failed: %s %v", v, err)
	}
	for _, bad := range []string{`{"findings":[],"uncertain":null}`, `{"findings":[{"quote":"不存在的句子","missing_detail":"x"}],"uncertain":false}`} {
		var rev specificityReview
		json.Unmarshal([]byte(bad), &rev)
		j.Review = &rev
		data, _ := json.Marshal(j)
		if _, v, err := parse(string(data), input); err == nil || v != "error" {
			t.Fatal("invalid review accepted")
		}
	}
}

func TestReviewFailureAndPrimaryFailureCannotPass(t *testing.T) {
	input := judgeInput{Answer: "无法确认"}
	primary := func(context.Context, judgeInput) (string, error) {
		return `{"claims":[{"quote":"无法确认","kind":"general","advice_kind":"none","evidence":"","reason":"unknown","checks":[]}]}`, nil
	}
	if _, err := withSpecificityReview(primary, func(context.Context, judgeInput) (string, error) { return "", context.DeadlineExceeded })(context.Background(), input); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("review failure ignored")
	}
	called := false
	_, err := withSpecificityReview(func(context.Context, judgeInput) (string, error) { return "", context.Canceled }, func(context.Context, judgeInput) (string, error) { called = true; return "", nil })(context.Background(), input)
	if !errors.Is(err, context.Canceled) || called {
		t.Fatal("review ran after failed primary")
	}
}

func TestEffortIsExplicitAndOptional(t *testing.T) {
	for _, effort := range []string{"", "low", "high", "max"} {
		s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var data map[string]json.RawMessage
			json.NewDecoder(r.Body).Decode(&data)
			value, exists := data["reasoning_effort"]
			if effort == "" && exists {
				t.Error("default request acquired provider-specific setting")
			}
			if effort != "" && string(value) != `"`+effort+`"` {
				t.Error("effort not transmitted")
			}
			io.WriteString(w, `{"choices":[{"message":{"content":"{}"},"finish_reason":"stop"}]}`)
		}))
		_, err := chatJudge(s.Client(), s.URL, "fixture", "fixture", effort)(context.Background(), judgeInput{Answer: "test"})
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("GROUNDING_REASONING_EFFORT", "unsupported-value")
	if _, _, err := liveJudge(); err == nil {
		t.Fatal("invalid effort accepted")
	}
}
