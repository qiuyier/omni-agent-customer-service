package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	hostCookieName   = "webchat_demo_session"
	hostSessionTTL   = 30 * time.Minute
	maxHostSessions  = 128
	maxLoginIssues   = 1024
	maxSessionIssues = 32
	maxRequestBytes  = 32 << 10
	maxUpstreamBytes = 1 << 20
)

type demoUser struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type hostSession struct {
	user      demoUser
	expiresAt time.Time
	issues    int
	tokens    map[[32]byte]time.Time
	// Only the current capability is cached in this host's ephemeral memory so
	// page reloads do not mint new credentials. It is never persisted or logged.
	cached  *chatSessionResponse
	minting bool
}

type chatSessionResponse struct {
	AccessToken    string    `json:"access_token"`
	ExpiresAt      time.Time `json:"expires_at"`
	ConversationID string    `json:"conversation_id"`
}

// exampleHost is a deliberately simulated host application. Its cookie is not
// production authentication: the two known demonstration users need no password.
// Only this server can exchange the selected identity for a browser capability.
type exampleHost struct {
	storage     string
	origin      *url.URL
	upstream    *url.URL
	secret      string
	assets      http.Handler
	client      *http.Client
	now         func() time.Time
	mu          sync.Mutex
	sessions    map[[32]byte]*hostSession
	loginWindow time.Time
	loginIssues int
}

func newExampleHost(origin, upstream, secret string, assets http.Handler) (*exampleHost, error) {
	public, err := localHTTPURL(origin)
	if err != nil {
		return nil, errors.New("invalid public demo origin")
	}
	private, err := localHTTPURL(upstream)
	if err != nil {
		return nil, errors.New("invalid private gateway origin")
	}
	if len(secret) < 32 || !validBearer(secret) || assets == nil {
		return nil, errors.New("invalid demo host configuration")
	}
	return &exampleHost{
		origin: public, upstream: private, secret: secret, assets: assets,
		client: &http.Client{
			Transport: &http.Transport{
				Proxy:        nil,
				DialContext:  (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
				MaxIdleConns: 8, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 16,
				IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 8 * time.Second,
				DisableCompression: true,
			},
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now: time.Now, sessions: make(map[[32]byte]*hostSession), storage: "memory",
	}, nil
}

func localHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "http" || ip == nil || !ip.IsLoopback() || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return nil, errors.New("expected a literal loopback HTTP origin with port")
	}
	return u, nil
}

func (h *exampleHost) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	// Exact Host also prevents DNS-rebinding access to this loopback demo.
	if r.Host != h.origin.Host || r.URL.RawPath != "" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(w, http.StatusForbidden, "request_not_allowed")
		return
	}
	origins := r.Header.Values("Origin")
	if len(origins) > 1 || (len(origins) == 1 && origins[0] != h.origin.String()) {
		writeError(w, http.StatusForbidden, "request_not_allowed")
		return
	}
	if r.Method == http.MethodPost {
		if len(origins) != 1 {
			writeError(w, http.StatusForbidden, "origin_required")
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if len(r.Header.Values("Content-Type")) != 1 || err != nil || mediaType != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "json_required")
			return
		}
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, http.StatusBadRequest, "query_not_allowed")
		return
	}
	switch r.URL.Path {
	case "/host/me":
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		h.mu.Lock()
		_, session := h.sessionLocked(r)
		var user *demoUser
		if session != nil {
			copy := session.user
			user = &copy
		}
		h.mu.Unlock()
		writeJSON(w, http.StatusOK, struct {
			User    *demoUser `json:"user"`
			Demo    bool      `json:"demo"`
			Storage string    `json:"storage"`
		}{user, true, h.storage})
	case "/demo/login":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		h.login(w, r)
	case "/demo/logout":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		h.logout(w, r)
	case "/host/chat-session":
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		h.bootstrap(w, r)
	case "/chat/v1/messages":
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		h.proxyMessages(w, r)
	default:
		if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/chat/" || strings.HasPrefix(r.URL.Path, "/assets/")) {
			h.assets.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (h *exampleHost) login(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Account string `json:"account"`
	}
	if !readJSON(w, r, &request, "account") {
		return
	}
	var user demoUser
	switch request.Account {
	case "lin":
		user = demoUser{"lin", "林澈"}
	case "zhou":
		user = demoUser{"zhou", "周宁"}
	default:
		writeError(w, http.StatusBadRequest, "unknown_demo_account")
		return
	}
	raw, err := randomToken()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "demo_unavailable")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	h.pruneLocked(now)
	if h.loginWindow.IsZero() || !now.Before(h.loginWindow.Add(time.Hour)) {
		h.loginWindow = now
		h.loginIssues = 0
	}
	old, oldSession := h.sessionLocked(r)
	if h.loginIssues >= maxLoginIssues || (len(h.sessions) >= maxHostSessions && oldSession == nil) {
		writeError(w, http.StatusTooManyRequests, "demo_capacity_reached")
		return
	}
	delete(h.sessions, old)
	expires := now.Add(hostSessionTTL)
	h.sessions[sha256.Sum256([]byte(raw))] = &hostSession{user: user, expiresAt: expires, tokens: make(map[[32]byte]time.Time)}
	h.loginIssues++
	h.setCookie(w, raw, expires, int(hostSessionTTL.Seconds()))
	writeJSON(w, http.StatusOK, struct {
		User    demoUser `json:"user"`
		Demo    bool     `json:"demo"`
		Storage string   `json:"storage"`
	}{user, true, h.storage})
}

func (h *exampleHost) logout(w http.ResponseWriter, r *http.Request) {
	if !readJSON(w, r, &struct{}{}) {
		return
	}
	h.mu.Lock()
	key, _ := h.sessionLocked(r)
	delete(h.sessions, key)
	h.mu.Unlock()
	h.setCookie(w, "", time.Unix(1, 0), -1)
	w.WriteHeader(http.StatusNoContent)
}

func (h *exampleHost) bootstrap(w http.ResponseWriter, r *http.Request) {
	// The browser cannot select a customer, source, or conversation here.
	if !readJSON(w, r, &struct{}{}) {
		return
	}
	h.mu.Lock()
	key, session := h.sessionLocked(r)
	if session == nil {
		h.mu.Unlock()
		writeError(w, http.StatusUnauthorized, "host_login_required")
		return
	}
	if session.cached != nil && session.cached.ExpiresAt.After(h.now().Add(30*time.Second)) {
		writeHostSession(w, *session.cached, session.user, h.storage)
		h.mu.Unlock()
		return
	}
	if session.minting {
		h.mu.Unlock()
		writeRateLimit(w, "busy", "1")
		return
	}
	if session.issues >= maxSessionIssues {
		h.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, "session_limit_reached")
		return
	}
	session.issues++ // Also counts failed/in-flight exchanges, bounding private calls.
	session.minting = true
	user := session.user
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		session.minting = false
		h.mu.Unlock()
	}()
	body, _ := json.Marshal(struct {
		CustomerID string `json:"customer_id"`
	}{user.ID})
	response, err := h.upstreamRequest(r, http.MethodPost, "/internal/chat/sessions", h.secret, body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "unavailable")
		return
	}
	defer response.Body.Close()
	data, err := readBounded(response.Body, maxRequestBytes)
	if response.StatusCode == http.StatusTooManyRequests {
		writeUpstreamError(w, response, data)
		return
	}
	var minted chatSessionResponse
	if err != nil || (response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated) || !jsonContentType(response.Header) || strictObject(data, &minted, "access_token", "expires_at", "conversation_id") != nil || !validBearer(minted.AccessToken) || minted.AccessToken == h.secret || minted.ConversationID == "" || len(minted.ConversationID) > 256 {
		writeError(w, http.StatusBadGateway, "unavailable")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	current := h.sessions[key]
	if current != session || !now.Before(session.expiresAt) {
		writeError(w, http.StatusUnauthorized, "host_login_required")
		return
	}
	if !now.Before(minted.ExpiresAt) || minted.ExpiresAt.After(now.Add(10*time.Minute)) {
		writeError(w, http.StatusBadGateway, "unavailable")
		return
	}
	if minted.ExpiresAt.After(session.expiresAt) {
		minted.ExpiresAt = session.expiresAt
	}
	session.tokens[sha256.Sum256([]byte(minted.AccessToken))] = minted.ExpiresAt
	session.cached = &minted
	writeHostSession(w, minted, user, h.storage)
}

func writeHostSession(w http.ResponseWriter, minted chatSessionResponse, user demoUser, storage string) {
	writeJSON(w, http.StatusOK, struct {
		chatSessionResponse
		User    demoUser `json:"user"`
		Demo    bool     `json:"demo"`
		Storage string   `json:"storage"`
	}{minted, user, true, storage})
}

func (h *exampleHost) proxyMessages(w http.ResponseWriter, r *http.Request) {
	token := requestBearer(r)
	h.mu.Lock()
	key, session := h.sessionLocked(r)
	allowed := false
	if session != nil && token != "" {
		expires, ok := session.tokens[sha256.Sum256([]byte(token))]
		allowed = ok && h.now().Before(expires)
	}
	h.mu.Unlock()
	if !allowed {
		writeError(w, http.StatusUnauthorized, "chat_session_required")
		return
	}
	var body []byte
	if r.Method == http.MethodPost {
		var request struct {
			Content string `json:"content"`
			EventID string `json:"event_id"`
		}
		if !readJSON(w, r, &request, "content", "event_id") {
			return
		}
		body, _ = json.Marshal(request)
	}
	response, err := h.upstreamRequest(r, r.Method, "/chat/v1/messages", token, body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "unavailable")
		return
	}
	defer response.Body.Close()
	data, err := readBounded(response.Body, maxUpstreamBytes)
	// A request admitted before logout may have reached the gateway, but never
	// return its history or reply to an account-switched/revoked host session.
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now()
	if session != h.sessions[key] || !now.Before(session.expiresAt) || !now.Before(session.tokens[sha256.Sum256([]byte(token))]) {
		writeError(w, http.StatusUnauthorized, "chat_session_required")
		return
	}
	// Never forward redirects, cookies, CORS, diagnostic headers, or non-JSON
	// errors from the private service to the browser.
	if err != nil || !jsonContentType(response.Header) || !json.Valid(data) || (response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated) {
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			delete(session.tokens, sha256.Sum256([]byte(token)))
			if session.cached != nil && session.cached.AccessToken == token {
				session.cached = nil
			}
		}
		writeUpstreamError(w, response, data)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(data)
}

func (h *exampleHost) upstreamRequest(r *http.Request, method, path, token string, body []byte) (*http.Response, error) {
	target := *h.upstream
	target.Path = path
	request, err := http.NewRequestWithContext(r.Context(), method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	// Construct a new request instead of copying any browser or proxy headers.
	return h.client.Do(request)
}

func (h *exampleHost) sessionLocked(r *http.Request) ([32]byte, *hostSession) {
	now := h.now()
	h.pruneLocked(now)
	var raw string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name != hostCookieName {
			continue
		}
		count++
		if count > 1 {
			return [32]byte{}, nil
		}
		raw = cookie.Value
	}
	if len(raw) != 43 {
		return [32]byte{}, nil
	}
	key := sha256.Sum256([]byte(raw))
	return key, h.sessions[key]
}

func (h *exampleHost) pruneLocked(now time.Time) {
	for key, session := range h.sessions {
		if !now.Before(session.expiresAt) {
			delete(h.sessions, key)
			continue
		}
		if session.cached != nil && !now.Before(session.cached.ExpiresAt) {
			session.cached = nil
		}
		for token, expiry := range session.tokens {
			if !now.Before(expiry) {
				delete(session.tokens, token)
			}
		}
	}
}

func (h *exampleHost) setCookie(w http.ResponseWriter, value string, expires time.Time, age int) {
	http.SetCookie(w, &http.Cookie{Name: hostCookieName, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: age, Expires: expires})
}

func randomToken() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data[:]), nil
}

func requestBearer(r *http.Request) string {
	if len(r.Header.Values("Authorization")) != 1 {
		return ""
	}
	raw := r.Header.Get("Authorization")
	if !strings.HasPrefix(raw, "Bearer ") {
		return ""
	}
	token := strings.TrimPrefix(raw, "Bearer ")
	if !validBearer(token) {
		return ""
	}
	return token
}

func validBearer(token string) bool {
	if len(token) < 32 || len(token) > 4096 {
		return false
	}
	for _, char := range token {
		if char <= ' ' || char >= 127 {
			return false
		}
	}
	return true
}

func readJSON(w http.ResponseWriter, r *http.Request, target any, fields ...string) bool {
	body, err := readBounded(r.Body, maxRequestBytes)
	if err != nil || strictObject(body, target, fields...) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func readBounded(reader io.Reader, max int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, errors.New("body too large")
	}
	return body, nil
}

// strictObject requires exactly one JSON object with unique, known fields.
func strictObject(body []byte, target any, fields ...string) error {
	if !utf8.Valid(body) {
		return errors.New("invalid UTF-8")
	}
	allowed := make(map[string]bool, len(fields))
	for _, field := range fields {
		allowed[field] = true
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return errors.New("object required")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok || seen[name] || !allowed[name] {
			return errors.New("duplicate or unknown field")
		}
		seen[name] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	decoder = json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func jsonContentType(header http.Header) bool {
	mediaType, _, err := mime.ParseMediaType(header.Get("Content-Type"))
	return err == nil && mediaType == "application/json"
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	return false
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{code})
}

func writeRateLimit(w http.ResponseWriter, code, rawRetryAfter string) {
	seconds, err := strconv.Atoi(rawRetryAfter)
	if err != nil || seconds < 1 {
		seconds = 1
	}
	if seconds > 300 {
		seconds = 300
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeError(w, http.StatusTooManyRequests, code)
}

// Keep only the gateway's documented, status-matched codes. Unknown details,
// arbitrary diagnostic text and headers are never returned to the browser.
func writeUpstreamError(w http.ResponseWriter, response *http.Response, body []byte) {
	var problem struct {
		Error string `json:"error"`
	}
	if len(body) <= 1024 && jsonContentType(response.Header) {
		if strictObject(body, &problem, "error") != nil {
			problem.Error = ""
		}
	}
	allowed := false
	switch response.StatusCode {
	case http.StatusBadRequest:
		allowed = problem.Error == "invalid_request"
	case http.StatusNotFound:
		allowed = problem.Error == "not_found"
	case http.StatusConflict:
		allowed = problem.Error == "event_conflict" || problem.Error == "conversation_closed"
	case http.StatusTooManyRequests:
		allowed = problem.Error == "busy" || problem.Error == "rate_limited"
	case http.StatusServiceUnavailable:
		allowed = problem.Error == "unavailable"
	case http.StatusUnauthorized, http.StatusForbidden:
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if response.StatusCode == http.StatusTooManyRequests {
		code := "unavailable"
		if allowed {
			code = problem.Error
		}
		writeRateLimit(w, code, response.Header.Get("Retry-After"))
		return
	}
	if allowed {
		writeError(w, response.StatusCode, problem.Error)
		return
	}
	writeError(w, http.StatusBadGateway, "unavailable")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
