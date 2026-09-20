package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testOrigin = "http://127.0.0.1:8092"
const testHostSecret = "only-server-knows-this-demonstration-secret-12345"

type hostFixture struct {
	host     *exampleHost
	time     atomic.Int64
	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
	serial   int
}

func newHostFixture(t *testing.T, upstream http.Handler) *hostFixture {
	t.Helper()
	f := &hostFixture{}
	f.time.Store(time.Now().UTC().UnixNano())
	if upstream == nil {
		upstream = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			defer f.mu.Unlock()
			f.requests = append(f.requests, r.Clone(r.Context()))
			f.bodies = append(f.bodies, body)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Set-Cookie", "private=must-not-leak")
			w.Header().Set("X-Private-Diagnostic", testHostSecret)
			w.Header().Set("Access-Control-Allow-Origin", "*")
			if r.URL.Path == "/internal/chat/sessions" {
				var identity struct {
					CustomerID string `json:"customer_id"`
				}
				if err := json.Unmarshal(body, &identity); err != nil {
					t.Errorf("invalid exchange body: %v", err)
				}
				f.serial++
				w.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(w).Encode(chatSessionResponse{
					AccessToken: fmt.Sprintf("browser-token-for-%s-%032d", identity.CustomerID, f.serial),
					ExpiresAt:   f.now().Add(5 * time.Minute), ConversationID: "conversation-" + identity.CustomerID,
				})
				return
			}
			_, _ = io.WriteString(w, `{"messages":[{"content":"private customer history"}]}`)
		})
	}
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	host, err := newExampleHost(testOrigin, server.URL, testHostSecret, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "demo UI")
	}))
	if err != nil {
		t.Fatal(err)
	}
	host.now = f.now
	f.host = host
	t.Cleanup(host.client.CloseIdleConnections)
	return f
}

func (f *hostFixture) now() time.Time { return time.Unix(0, f.time.Load()).UTC() }

func hostRequest(method, path, body string, cookie *http.Cookie, token string) *http.Request {
	r := httptest.NewRequest(method, testOrigin+path, strings.NewReader(body))
	if method == http.MethodPost {
		r.Header.Set("Origin", testOrigin)
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func serveHost(h *exampleHost, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func (f *hostFixture) login(t *testing.T, account string, old *http.Cookie) *http.Cookie {
	t.Helper()
	w := serveHost(f.host, hostRequest(http.MethodPost, "/demo/login", `{"account":"`+account+`"}`, old, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("login status %d: %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login cookies: %v", cookies)
	}
	return cookies[0]
}

func (f *hostFixture) bootstrap(t *testing.T, cookie *http.Cookie) chatSessionResponse {
	t.Helper()
	w := serveHost(f.host, hostRequest(http.MethodPost, "/host/chat-session", `{}`, cookie, ""))
	if w.Code != http.StatusOK {
		t.Fatalf("bootstrap status %d: %s", w.Code, w.Body.String())
	}
	var session chatSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.AccessToken == "" {
		t.Fatal("missing browser token")
	}
	return session
}

func TestHostIdentityExchangeAndProxy(t *testing.T) {
	f := newHostFixture(t, nil)
	anonymous := serveHost(f.host, hostRequest(http.MethodGet, "/host/me", "", nil, ""))
	if anonymous.Code != http.StatusOK || !strings.Contains(anonymous.Body.String(), `"user":null`) {
		t.Fatalf("anonymous me: %d %s", anonymous.Code, anonymous.Body.String())
	}
	unauthenticated := serveHost(f.host, hostRequest(http.MethodPost, "/host/chat-session", `{}`, nil, ""))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous bootstrap: %d", unauthenticated.Code)
	}
	cookie := f.login(t, "lin", nil)
	if cookie.Name != hostCookieName || len(cookie.Value) != 43 || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Domain != "" || cookie.Path != "/" || cookie.MaxAge != int(hostSessionTTL.Seconds()) {
		t.Fatalf("unsafe session cookie attributes: %#v", cookie)
	}
	me := serveHost(f.host, hostRequest(http.MethodGet, "/host/me", "", cookie, ""))
	if !strings.Contains(me.Body.String(), "林澈") || !strings.Contains(me.Body.String(), `"demo":true`) {
		t.Fatalf("me: %s", me.Body.String())
	}
	session := f.bootstrap(t, cookie)
	if session.ConversationID != "conversation-lin" {
		t.Fatalf("wrong identity: %s", session.ConversationID)
	}
	f.mu.Lock()
	if f.requests[0].URL.Path != "/internal/chat/sessions" || f.requests[0].Header.Get("Authorization") != "Bearer "+testHostSecret || string(f.bodies[0]) != `{"customer_id":"lin"}` {
		t.Error("mint was not a server-to-server identity exchange")
	}
	f.mu.Unlock()
	r := hostRequest(http.MethodPost, "/chat/v1/messages", `{"content":"hello","event_id":"event-1"}`, cookie, session.AccessToken)
	r.Header.Set("X-Source-ID", "injected-source")
	r.Header.Set("X-Forwarded-Host", "evil.invalid")
	w := serveHost(f.host, r)
	if w.Code != http.StatusOK {
		t.Fatalf("message: %d %s", w.Code, w.Body.String())
	}
	f.mu.Lock()
	forwarded := f.requests[1]
	body := string(f.bodies[1])
	f.mu.Unlock()
	if forwarded.Header.Get("Authorization") != "Bearer "+session.AccessToken || forwarded.Header.Get("Cookie") != "" || forwarded.Header.Get("Origin") != "" || forwarded.Header.Get("X-Source-ID") != "" || forwarded.Header.Get("X-Forwarded-Host") != "" {
		t.Fatal("proxy forwarded headers outside the allowlist")
	}
	if !strings.Contains(body, `"event_id":"event-1"`) {
		t.Fatalf("wrong send contract: %s", body)
	}
	if w.Header().Get("Set-Cookie") != "" || w.Header().Get("X-Private-Diagnostic") != "" || w.Header().Get("Access-Control-Allow-Origin") != "" || strings.Contains(w.Body.String(), testHostSecret) {
		t.Fatal("upstream private state leaked")
	}
	for name, r := range map[string]*http.Request{
		"no cookie":       hostRequest(http.MethodGet, "/chat/v1/messages", "", nil, session.AccessToken),
		"no token":        hostRequest(http.MethodGet, "/chat/v1/messages", "", cookie, ""),
		"host credential": hostRequest(http.MethodGet, "/chat/v1/messages", "", cookie, testHostSecret),
		"unknown token":   hostRequest(http.MethodGet, "/chat/v1/messages", "", cookie, strings.Repeat("x", 43)),
	} {
		t.Run(name, func(t *testing.T) {
			if got := serveHost(f.host, r).Code; got != http.StatusUnauthorized {
				t.Fatalf("status %d", got)
			}
		})
	}
}

func TestHostRejectsImpersonationAndMalformedBodies(t *testing.T) {
	f := newHostFixture(t, nil)
	cookie := f.login(t, "lin", nil)
	for _, body := range []string{`{"customer_id":"zhou"}`, `{"source_id":"other"}`, `{"conversation_id":"other"}`, `null`, `[]`, `{} {}`, `{"account":"zhou"}`, `{"x":1,"x":2}`, strings.Repeat(" ", maxRequestBytes) + `{}`} {
		w := serveHost(f.host, hostRequest(http.MethodPost, "/host/chat-session", body, cookie, ""))
		if w.Code != http.StatusBadRequest {
			t.Errorf("bootstrap accepted malformed/impersonating body %q: %d", body[:min(len(body), 80)], w.Code)
		}
	}
	for _, body := range []string{`{"account":"lin","account":"zhou"}`, `{"Account":"lin"}`, `{"account":"lin","customer_id":"zhou"}`, `{"account":"unknown"}`, `null`, string([]byte{'{', '"', 'a', '"', ':', '"', 0xff, '"', '}'})} {
		if got := serveHost(f.host, hostRequest(http.MethodPost, "/demo/login", body, cookie, "")).Code; got != http.StatusBadRequest {
			t.Errorf("login body %q: %d", body, got)
		}
	}
	if len(f.requests) != 0 {
		t.Fatal("invalid bootstrap called the private gateway")
	}
	session := f.bootstrap(t, cookie)
	for _, body := range []string{`{"content":"hello","event_id":"1","customer_id":"zhou"}`, `{"content":"hello","client_message_id":"1"}`, `{"content":"a","content":"b","event_id":"1"}`} {
		if got := serveHost(f.host, hostRequest(http.MethodPost, "/chat/v1/messages", body, cookie, session.AccessToken)).Code; got != http.StatusBadRequest {
			t.Errorf("send body %q: %d", body, got)
		}
	}
}

func TestHostCSRFAndCORSFailClosed(t *testing.T) {
	f := newHostFixture(t, nil)
	for _, path := range []string{"/demo/login", "/demo/logout", "/host/chat-session", "/chat/v1/messages"} {
		for _, origin := range []string{"", "null", "https://evil.invalid", testOrigin + "/", "http://localhost:8092"} {
			r := hostRequest(http.MethodPost, path, `{}`, nil, "")
			if origin == "" {
				r.Header.Del("Origin")
			} else {
				r.Header.Set("Origin", origin)
			}
			w := serveHost(f.host, r)
			if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Errorf("%s origin %q: %d", path, origin, w.Code)
			}
		}
	}
	for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded"} {
		r := hostRequest(http.MethodPost, "/demo/login", `{"account":"lin"}`, nil, "")
		r.Header.Set("Content-Type", contentType)
		if got := serveHost(f.host, r).Code; got != http.StatusUnsupportedMediaType {
			t.Errorf("content type %q: %d", contentType, got)
		}
	}
	r := hostRequest(http.MethodPost, "/demo/login", `{"account":"lin"}`, nil, "")
	r.Header.Add("Origin", testOrigin)
	if got := serveHost(f.host, r).Code; got != http.StatusForbidden {
		t.Errorf("duplicate origin: %d", got)
	}
	r = hostRequest(http.MethodGet, "/host/me", "", nil, "")
	r.Header.Set("Origin", "https://evil.invalid")
	if got := serveHost(f.host, r).Code; got != http.StatusForbidden {
		t.Errorf("cross origin GET: %d", got)
	}
	r = hostRequest(http.MethodGet, "/host/me", "", nil, "")
	r.Host = "rebinding.attacker.invalid:8092"
	if got := serveHost(f.host, r).Code; got != http.StatusForbidden {
		t.Errorf("rebinding host: %d", got)
	}
	r = hostRequest(http.MethodGet, "/", "", nil, "")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if got := serveHost(f.host, r).Code; got != http.StatusForbidden {
		t.Errorf("cross-site: %d", got)
	}
	w := serveHost(f.host, hostRequest(http.MethodOptions, "/host/chat-session", "", nil, ""))
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("preflight allowed: %d", w.Code)
	}
}

func TestHostAccountSwitchLogoutAndTokenBinding(t *testing.T) {
	f := newHostFixture(t, nil)
	lin := f.login(t, "lin", nil)
	linSession := f.bootstrap(t, lin)
	otherLin := f.login(t, "lin", nil)
	zhou := f.login(t, "zhou", lin)
	zhouSession := f.bootstrap(t, zhou)
	for _, pair := range []struct {
		cookie *http.Cookie
		token  string
	}{{lin, linSession.AccessToken}, {zhou, linSession.AccessToken}, {otherLin, linSession.AccessToken}, {lin, zhouSession.AccessToken}} {
		if got := serveHost(f.host, hostRequest(http.MethodGet, "/chat/v1/messages", "", pair.cookie, pair.token)).Code; got != http.StatusUnauthorized {
			t.Errorf("old/foreign capability allowed: %d", got)
		}
	}
	if got := serveHost(f.host, hostRequest(http.MethodGet, "/chat/v1/messages", "", zhou, zhouSession.AccessToken)).Code; got != http.StatusOK {
		t.Fatalf("new account status %d", got)
	}
	logout := serveHost(f.host, hostRequest(http.MethodPost, "/demo/logout", `{}`, zhou, ""))
	if logout.Code != http.StatusNoContent || logout.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("logout failed: %d", logout.Code)
	}
	if got := serveHost(f.host, hostRequest(http.MethodGet, "/chat/v1/messages", "", zhou, zhouSession.AccessToken)).Code; got != http.StatusUnauthorized {
		t.Fatalf("logged out token still accepted: %d", got)
	}
	if got := serveHost(f.host, hostRequest(http.MethodPost, "/host/chat-session", `{}`, zhou, "")).Code; got != http.StatusUnauthorized {
		t.Fatalf("logged out cookie still accepted: %d", got)
	}
}

func TestHostExpiryAndBoundedIssuance(t *testing.T) {
	f := newHostFixture(t, nil)
	cookie := f.login(t, "lin", nil)
	session := f.bootstrap(t, cookie)
	f.time.Add(int64(5 * time.Minute))
	if got := serveHost(f.host, hostRequest(http.MethodGet, "/chat/v1/messages", "", cookie, session.AccessToken)).Code; got != http.StatusUnauthorized {
		t.Fatalf("expired token: %d", got)
	}
	_ = f.bootstrap(t, cookie)
	f.time.Add(int64(25 * time.Minute))
	if got := serveHost(f.host, hostRequest(http.MethodPost, "/host/chat-session", `{}`, cookie, "")).Code; got != http.StatusUnauthorized {
		t.Fatalf("expired cookie: %d", got)
	}
	if len(f.host.sessions) != 0 {
		t.Fatal("expired sessions not pruned")
	}
	cookie = f.login(t, "lin", nil)
	key := sha256.Sum256([]byte(cookie.Value))
	f.host.sessions[key].issues = maxSessionIssues
	if got := serveHost(f.host, hostRequest(http.MethodPost, "/host/chat-session", `{}`, cookie, "")).Code; got != http.StatusTooManyRequests {
		t.Fatalf("mint issue bound: %d", got)
	}
	f.host.loginIssues = maxLoginIssues
	if got := serveHost(f.host, hostRequest(http.MethodPost, "/demo/login", `{"account":"zhou"}`, nil, "")).Code; got != http.StatusTooManyRequests {
		t.Fatalf("login issue bound: %d", got)
	}
	f.time.Add(int64(time.Hour))
	_ = f.login(t, "zhou", nil)
	f.host.sessions = make(map[[32]byte]*hostSession)
	for i := 0; i < maxHostSessions; i++ {
		f.host.sessions[sha256.Sum256([]byte(fmt.Sprint(i)))] = &hostSession{expiresAt: f.now().Add(time.Hour)}
	}
	if got := serveHost(f.host, hostRequest(http.MethodPost, "/demo/login", `{"account":"lin"}`, nil, "")).Code; got != http.StatusTooManyRequests {
		t.Fatalf("session store bound: %d", got)
	}
}

func TestHostProxyRouteAndHeaderRestrictions(t *testing.T) {
	f := newHostFixture(t, nil)
	cookie := f.login(t, "lin", nil)
	session := f.bootstrap(t, cookie)
	for _, path := range []string{"/internal/chat/sessions", "/v1/conversations", "/chat/v1/messages/other", "/chat/v1/anything", "/chat/v1/messages?conversation_id=other"} {
		w := serveHost(f.host, hostRequest(http.MethodGet, path, "", cookie, session.AccessToken))
		if w.Code != http.StatusNotFound && w.Code != http.StatusBadRequest {
			t.Errorf("unexpected path %s accepted: %d", path, w.Code)
		}
	}
	r := hostRequest(http.MethodGet, "/chat/v1/messages", "", cookie, session.AccessToken)
	r.Header.Add("Authorization", "Bearer "+testHostSecret)
	if got := serveHost(f.host, r).Code; got != http.StatusUnauthorized {
		t.Fatalf("duplicate auth: %d", got)
	}
	r = hostRequest(http.MethodGet, "/chat/v1/messages", "", nil, session.AccessToken)
	r.Header.Set("Cookie", hostCookieName+"=; "+hostCookieName+"="+cookie.Value)
	if got := serveHost(f.host, r).Code; got != http.StatusUnauthorized {
		t.Fatalf("duplicate cookie: %d", got)
	}
	if got := serveHost(f.host, hostRequest(http.MethodDelete, "/chat/v1/messages", "", cookie, session.AccessToken)).Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("proxy delete: %d", got)
	}
	w := serveHost(f.host, hostRequest(http.MethodGet, "/", "", nil, ""))
	for key, want := range map[string]string{"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer"} {
		if got := w.Header().Get(key); got != want {
			t.Errorf("%s=%q want %q", key, got, want)
		}
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("unsafe CSP: %s", csp)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 1 {
		t.Fatalf("restricted routes reached upstream: %d requests", len(f.requests))
	}
}

func TestHostUpstreamFailuresDoNotLeakOrRedirect(t *testing.T) {
	var redirected atomic.Int64
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer trap.Close()
	for _, test := range []struct {
		name              string
		status            int
		contentType, body string
	}{
		{"redirect", 302, "application/json", `{}`},
		{"diagnostic", 500, "application/json", `{"secret":"` + testHostSecret + `"}`},
		{"not json", 200, "text/html", "<script>private()</script>"},
		{"invalid json", 200, "application/json", `oops`},
		{"oversized", 200, "application/json", `{"huge":"` + strings.Repeat("x", maxUpstreamBytes) + `"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newHostFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				w.Header().Set("Location", trap.URL)
				w.Header().Set("Set-Cookie", "leak="+testHostSecret)
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			cookie := f.login(t, "lin", nil)
			key := sha256.Sum256([]byte(cookie.Value))
			token := strings.Repeat("t", 43)
			f.host.sessions[key].tokens[sha256.Sum256([]byte(token))] = f.now().Add(time.Minute)
			for _, r := range []*http.Request{hostRequest(http.MethodPost, "/host/chat-session", `{}`, cookie, ""), hostRequest(http.MethodGet, "/chat/v1/messages", "", cookie, token)} {
				w := serveHost(f.host, r)
				if w.Code != http.StatusBadGateway || w.Header().Get("Location") != "" || w.Header().Get("Set-Cookie") != "" || strings.Contains(w.Body.String(), testHostSecret) {
					t.Fatalf("unsafe failure: %d %v %s", w.Code, w.Header(), w.Body.String())
				}
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("private client followed redirect")
	}
}

func TestHostRejectsLateResponsesAfterLogout(t *testing.T) {
	for _, path := range []string{"/host/chat-session", "/chat/v1/messages"} {
		t.Run(path, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			f := newHostFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/internal/chat/sessions" {
					_ = json.NewEncoder(w).Encode(chatSessionResponse{strings.Repeat("n", 43), time.Now().Add(time.Minute), "conversation-lin"})
				} else {
					_, _ = io.WriteString(w, `{"messages":["old private history"]}`)
				}
			}))
			cookie := f.login(t, "lin", nil)
			token := strings.Repeat("t", 43)
			key := sha256.Sum256([]byte(cookie.Value))
			f.host.sessions[key].tokens[sha256.Sum256([]byte(token))] = f.now().Add(time.Minute)
			method, body := http.MethodGet, ""
			if path == "/host/chat-session" {
				method, body = http.MethodPost, `{}`
			}
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- serveHost(f.host, hostRequest(method, path, body, cookie, token)) }()
			<-entered
			logout := serveHost(f.host, hostRequest(http.MethodPost, "/demo/logout", `{}`, cookie, ""))
			if logout.Code != http.StatusNoContent {
				t.Fatal("logout failed")
			}
			close(release)
			w := <-done
			if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), "old private history") || strings.Contains(w.Body.String(), strings.Repeat("n", 43)) {
				t.Fatalf("late private response leaked: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHostClientConfigurationAndInputValidation(t *testing.T) {
	f := newHostFixture(t, nil)
	transport, ok := f.host.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil || f.host.client.Timeout <= 0 || transport.MaxConnsPerHost <= 0 {
		t.Fatal("private client must be bounded and ignore environment proxies")
	}
	for _, origin := range []string{"http://0.0.0.0:8092", "http://localhost:8092", "https://127.0.0.1:8092", "http://127.0.0.1:8092/", "http://user:password@127.0.0.1:8092", "http://127.0.0.1:8092?x=1"} {
		if _, err := newExampleHost(origin, f.host.upstream.String(), testHostSecret, http.NotFoundHandler()); err == nil {
			t.Errorf("accepted unsafe origin %q", origin)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{}`, `{"account":"lin"}`, `{"account":"lin","account":"zhou"}`, `{"unknown":"x"}`, `{"account":"lin"} true`} {
		var target struct {
			Account string `json:"account"`
		}
		err := strictObject([]byte(raw), &target, "account")
		wantOK := raw == `{}` || raw == `{"account":"lin"}`
		if (err == nil) != wantOK {
			t.Errorf("strictObject(%q) err=%v", raw, err)
		}
	}
	if _, err := readBounded(bytes.NewReader([]byte("12345")), 4); err == nil {
		t.Fatal("oversize body accepted")
	}
}

func TestHostBootstrapReusesCapabilityAcrossReloads(t *testing.T) {
	f := newHostFixture(t, nil)
	cookie := f.login(t, "lin", nil)
	first := f.bootstrap(t, cookie)
	for i := 0; i < 12; i++ {
		if next := f.bootstrap(t, cookie); next != first {
			t.Fatal("page reload minted a different capability")
		}
	}
	f.mu.Lock()
	issues := f.serial
	f.mu.Unlock()
	if issues != 1 {
		t.Fatalf("reloads made %d private mint requests", issues)
	}
	f.time.Add(int64(4*time.Minute + 40*time.Second))
	next := f.bootstrap(t, cookie)
	if next.AccessToken == first.AccessToken {
		t.Fatal("near-expiry capability was not renewed")
	}
	f.mu.Lock()
	issues = f.serial
	f.mu.Unlock()
	if issues != 2 {
		t.Fatalf("renewal made %d total mint requests", issues)
	}
	key := sha256.Sum256([]byte(cookie.Value))
	if f.host.sessions[key].issues != 2 {
		t.Fatal("cached bootstrap consumed issuance budget")
	}
	logout := serveHost(f.host, hostRequest(http.MethodPost, "/demo/logout", `{}`, cookie, ""))
	if logout.Code != http.StatusNoContent {
		t.Fatal("logout failed")
	}
	if _, exists := f.host.sessions[key]; exists {
		t.Fatal("logout retained the cached capability")
	}
}

func TestHostConcurrentBootstrapDoesNotStampede(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	f := newHostFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(entered)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chatSessionResponse{strings.Repeat("n", 43), time.Now().Add(time.Minute), "conversation-lin"})
	}))
	cookie := f.login(t, "lin", nil)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- serveHost(f.host, hostRequest(http.MethodPost, "/host/chat-session", `{}`, cookie, ""))
	}()
	<-entered
	w := serveHost(f.host, hostRequest(http.MethodPost, "/host/chat-session", `{}`, cookie, ""))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" {
		t.Errorf("concurrent mint status/header: %d %v", w.Code, w.Header())
	}
	close(release)
	if completed := <-done; completed.Code != http.StatusOK {
		t.Fatalf("first mint failed: %d %s", completed.Code, completed.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatal("concurrent bootstrap reached the gateway more than once")
	}
	_ = f.bootstrap(t, cookie)
	if calls.Load() != 1 {
		t.Fatal("completed mint was not cached")
	}
}

func TestHostPreservesSafeUpstreamRateLimits(t *testing.T) {
	for _, raw := range []string{"2", "999999", "-3", "private-secret", ""} {
		f := newHostFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", raw)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, testHostSecret)
		}))
		cookie := f.login(t, "lin", nil)
		w := serveHost(f.host, hostRequest(http.MethodPost, "/host/chat-session", `{}`, cookie, ""))
		want := "1"
		if raw == "2" {
			want = "2"
		}
		if raw == "999999" {
			want = "300"
		}
		if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != want || strings.Contains(w.Body.String(), testHostSecret) {
			t.Errorf("rate limit %q: %d %v %s", raw, w.Code, w.Header(), w.Body.String())
		}
		key := sha256.Sum256([]byte(cookie.Value))
		if f.host.sessions[key].minting {
			t.Fatal("failed exchange retained in-flight flag")
		}
	}
}

func TestHostPreservesOnlyKnownGatewayErrors(t *testing.T) {
	for _, test := range []struct {
		status     int
		code       string
		wantStatus int
		wantCode   string
	}{
		{400, "invalid_request", 400, "invalid_request"},
		{401, "unauthorized", 401, "unauthorized"},
		{404, "not_found", 404, "not_found"},
		{409, "event_conflict", 409, "event_conflict"},
		{409, "conversation_closed", 409, "conversation_closed"},
		{429, "busy", 429, "busy"},
		{429, "rate_limited", 429, "rate_limited"},
		{503, "unavailable", 503, "unavailable"},
		{409, "private-stack-trace", 502, "unavailable"},
		{409, "invalid_request", 502, "unavailable"},
		{500, testHostSecret, 502, "unavailable"},
	} {
		t.Run(fmt.Sprintf("%d/%s", test.status, test.code), func(t *testing.T) {
			f := newHostFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(test.status)
				_ = json.NewEncoder(w).Encode(struct {
					Error string `json:"error"`
				}{test.code})
			}))
			cookie := f.login(t, "lin", nil)
			token := strings.Repeat("t", 43)
			key := sha256.Sum256([]byte(cookie.Value))
			f.host.sessions[key].tokens[sha256.Sum256([]byte(token))] = f.now().Add(time.Minute)
			w := serveHost(f.host, hostRequest(http.MethodPost, "/chat/v1/messages", `{"content":"hello","event_id":"1"}`, cookie, token))
			if w.Code != test.wantStatus || !strings.Contains(w.Body.String(), `"error":"`+test.wantCode+`"`) {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
			if test.status == 429 && w.Header().Get("Retry-After") != "2" {
				t.Fatal("POST rate limit lost Retry-After")
			}
		})
	}
}
