package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/webchat"
)

func TestDemoAIUsesActualHostProxyKnowledgeAndModel(t *testing.T) {
	var calls atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer private-model-marker" || !bytes.Contains(body, []byte("demo-account-guide")) || !bytes.Contains(body, []byte("How can I reset my password?")) {
			t.Error("host proxy did not reach the configured AI/knowledge chain")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"local-demo","object":"chat.completion","created":1,"model":"mock","choices":[{"index":0,"message":{"role":"assistant","content":"Use the account settings instructions."},"finish_reason":"stop"}]}`)
	}))
	defer model.Close()
	faq := filepath.Join(t.TempDir(), "faq.json")
	if err := os.WriteFile(faq, []byte(`[{"id":"demo-account-guide","title":"Account help","source":"help/account","content":"Open Account settings to change your password. Never share verification codes.","keywords":["password"]}]`), 0600); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{"AI_PROVIDER": "custom", "AI_PROTOCOL": "chat_completions", "AI_MODEL": "mock", "AI_BASE_URL": model.URL + "/v1", "AI_API_KEY": "private-model-marker", "KNOWLEDGE_FAQ_PATH": faq}
	config, err := resolveDemoConfigWithReply("127.0.0.1:0", "memory", "ai", func(k string) (string, bool) { v, ok := values[k]; return v, ok })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	out := make(startupWriter, 1)
	done := make(chan error, 1)
	go func() { done <- runDemoWithConfig(ctx, config, out) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(7 * time.Second):
			t.Error("AI demo did not shut down")
		}
	})
	var startup string
	select {
	case startup = <-out:
	case err := <-done:
		done <- err
		t.Fatalf("startup: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("startup timeout")
	}
	if !strings.Contains(startup, "AI replies enabled") || strings.Contains(startup, "private-model-marker") || strings.Contains(startup, faq) {
		t.Fatal("incorrect or unsafe mode notice")
	}
	origin := strings.TrimPrefix(strings.Split(startup, "\n")[0], "Web chat demo: ")
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	call := func(method, path, body, token string) []byte {
		t.Helper()
		r, _ := http.NewRequest(method, origin+path, strings.NewReader(body))
		if method == "POST" {
			r.Header.Set("Origin", origin)
			r.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 {
			t.Fatalf("%s status=%d", path, response.StatusCode)
		}
		if bytes.Contains(data, []byte("private-model-marker")) || bytes.Contains(data, []byte(faq)) || bytes.Contains(data, []byte(model.URL)) {
			t.Fatal("public response leaked AI configuration")
		}
		return data
	}
	for _, route := range []struct{ method, path, body string }{{"GET", "/host/me", ""}, {"POST", "/demo/login", `{"account":"lin"}`}} {
		if !bytes.Contains(call(route.method, route.path, route.body, ""), []byte(`"reply_mode":"ai"`)) {
			t.Fatal("missing mode metadata")
		}
	}
	var session struct {
		AccessToken string `json:"access_token"`
		ReplyMode   string `json:"reply_mode"`
	}
	if err := json.Unmarshal(call("POST", "/host/chat-session", `{}`, ""), &session); err != nil || session.ReplyMode != "ai" || session.AccessToken == "" {
		t.Fatal("bad session")
	}
	body := `{"event_id":"demo-ai-event","content":"How can I reset my password?"}`
	first := call("POST", "/chat/v1/messages", body, session.AccessToken)
	second := call("POST", "/chat/v1/messages", body, session.AccessToken)
	if !bytes.Equal(first, second) || calls.Load() != 1 {
		t.Fatal("replay called model or changed saved IDs")
	}
	var history webchat.History
	if err := json.Unmarshal(call("GET", "/chat/v1/messages", "", session.AccessToken), &history); err != nil || len(history.Messages) != 2 || history.Messages[1].Content != "Use the account settings instructions." {
		t.Fatal("AI result not persisted through actual demo proxy")
	}
}

func TestDemoModeIsExplicitAndMetadataCannotBeChosenByBrowser(t *testing.T) {
	config, err := resolveDemoConfig("127.0.0.1:0", "memory", func(string) (string, bool) { t.Fatal("default demo read environment"); return "", false })
	if err != nil || config.reply.Mode != "fake" {
		t.Fatal("default demo must remain fake")
	}
	if _, err := resolveDemoConfigWithReply("127.0.0.1:0", "memory", "ai", nil); err == nil {
		t.Fatal("unconfigured AI accepted")
	}
	f := newHostFixture(t, nil)
	f.host.replyMode = "ai"
	cookie := f.login(t, "lin", nil)
	for _, path := range []string{"/host/chat-session", "/demo/login"} {
		response := serveHost(f.host, hostRequest("POST", path, `{"reply_mode":"ai"}`, cookie, ""))
		if response.Code != 400 {
			t.Fatal("browser supplied model configuration")
		}
	}
	for _, mode := range []string{"fake", "ai"} {
		c, _ := bootstrap.ResolveWebchatReplyConfig("fake", nil)
		c.Mode = mode
		gatewayBudget := c.RequestTimeout()
		if mode == "ai" && (gatewayBudget <= bootstrap.DefaultAIReplyTimeout+20*time.Second || gatewayBudget+15*time.Second >= 150*time.Second) {
			t.Fatal("AI request budget does not fit browser and proxy")
		}
	}
}

func TestDemoDrainRejectsNewWorkAndJoinsExistingWork(t *testing.T) {
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	h := &demoRequestTracker{next: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(entered); <-release })}
	go func() { h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)); close(finished) }()
	<-entered
	h.beginDrain()
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/", nil))
	if r.Code != 503 {
		t.Fatal("draining handler admitted work")
	}
	close(release)
	h.active.Wait()
	<-finished
}
