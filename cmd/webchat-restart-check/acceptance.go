package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"time"
)

const (
	sessionPath  = "/internal/chat/sessions"
	messagesPath = "/chat/v1/messages"
)

type harness struct {
	ctx      context.Context
	options  options
	client   *http.Client
	children []*childProcess
	report   report
}

type session struct {
	AccessToken    string    `json:"access_token"`
	ExpiresAt      time.Time `json:"expires_at"`
	ConversationID string    `json:"conversation_id"`
}

type message struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
	ReplyTo   string `json:"reply_to_message_id"`
}

type history struct {
	ConversationID string    `json:"conversation_id"`
	Status         string    `json:"status"`
	Messages       []message `json:"messages"`
	Truncated      bool      `json:"truncated"`
}

type sendResult struct {
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	ReplyMessageID string `json:"reply_message_id"`
	Outcome        string `json:"outcome"`
}

func newHarness(ctx context.Context, opts options) *harness {
	return &harness{ctx: ctx, options: opts, report: report{Version: 1, Demo: opts.demoBinary != "", Checks: []checkResult{}, Processes: []processResult{}},
		client: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
				MaxIdleConns: 8, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 16, IdleConnTimeout: 15 * time.Second,
				ResponseHeaderTimeout: 10 * time.Second, DisableCompression: true}}}
}

func (h *harness) check(name string, f func() error) error {
	err := checkContext(h.ctx)
	if err == nil {
		err = f()
	}
	result := checkResult{Name: name, Passed: err == nil}
	if err != nil {
		// Every acceptance helper returns fixed diagnostics or numeric HTTP
		// status only; raw child/HTTP bodies and network errors never reach here.
		result.Detail = err.Error()
	}
	h.report.Checks = append(h.report.Checks, result)
	return err
}

func (h *harness) request(ctx context.Context, origin, method, path, token string, body any, cookie *http.Cookie) (int, []byte, http.Header, error) {
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return 0, nil, nil, errors.New("cannot encode acceptance request")
		}
	}
	r, err := http.NewRequestWithContext(ctx, method, origin+path, bytes.NewReader(encoded))
	if err != nil {
		return 0, nil, nil, errors.New("cannot initialize acceptance request")
	}
	r.Header.Set("Accept", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if method == http.MethodPost {
		r.Header.Set("Origin", origin)
	}
	response, err := h.client.Do(r)
	if err != nil {
		return 0, nil, nil, errors.New("acceptance HTTP request failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return 0, nil, nil, errors.New("acceptance response exceeded its limit or was incomplete")
	}
	return response.StatusCode, data, response.Header, nil
}

func (h *harness) expect(origin, method, path, token string, body any, cookie *http.Cookie, status int, result any) error {
	actual, data, header, err := h.request(h.ctx, origin, method, path, token, body, cookie)
	if err != nil {
		return err
	}
	if actual != status {
		return fmt.Errorf("unexpected HTTP status: got %d, expected %d", actual, status)
	}
	if result != nil {
		mediaType, _, err := mime.ParseMediaType(header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" || json.Unmarshal(data, result) != nil {
			return errors.New("response was not a valid JSON acceptance result")
		}
	}
	return nil
}

func (h *harness) mint(child *childProcess, secret, customer string) (session, error) {
	var result session
	err := h.expect(child.origin, "POST", sessionPath, secret, map[string]string{"customer_id": customer}, nil, 201, &result)
	if err != nil {
		return session{}, err
	}
	if result.AccessToken == "" || result.AccessToken == secret || result.ConversationID == "" || !result.ExpiresAt.After(time.Now()) {
		return session{}, errors.New("gateway returned an invalid restricted credential")
	}
	return result, nil
}

func (h *harness) history(child *childProcess, session session, cookie *http.Cookie) (history, error) {
	var result history
	err := h.expect(child.origin, "GET", messagesPath, session.AccessToken, nil, cookie, 200, &result)
	if err == nil && result.ConversationID != session.ConversationID {
		err = errors.New("history conversation did not match the credential")
	}
	return result, err
}

func (h *harness) send(child *childProcess, session session, cookie *http.Cookie, event, content string) (sendResult, error) {
	var result sendResult
	err := h.expect(child.origin, "POST", messagesPath, session.AccessToken, map[string]string{"event_id": event, "content": content}, cookie, 200, &result)
	if err == nil && (result.ConversationID != session.ConversationID || result.MessageID == "" || result.ReplyMessageID == "" || result.Outcome != "replied") {
		err = errors.New("message did not produce a persisted reply acknowledgement")
	}
	return result, err
}

func savedPair(value history, sent sendResult) bool {
	var customer, bot bool
	for _, current := range value.Messages {
		if current.ID == sent.MessageID && current.Role == "customer" && current.Content != "" {
			customer = true
		}
		if current.ID == sent.ReplyMessageID && current.Role == "bot" && current.ReplyTo == sent.MessageID && current.Content != "" {
			bot = true
		}
	}
	return customer && bot
}

// holdAcknowledgement models a response lost after the real gateway commits.
// The proxy observes the upstream acknowledgement but never forwards it to its
// caller. After the child has been killed and reaped it aborts the connection.
// Only this local fixture has a gate; production code has no crash/test hooks.
func (h *harness) holdAcknowledgement(child *childProcess, current session, event, content string) (sendResult, func() error, error) {
	committed := make(chan sendResult, 1)
	failed := make(chan error, 1)
	release := make(chan struct{})
	var once sync.Once
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/" || r.Header.Get("Authorization") != "Bearer "+current.AccessToken {
			w.WriteHeader(404)
			return
		}
		result, err := h.send(child, current, nil, event, content)
		if err != nil {
			failed <- err
			return
		}
		committed <- result
		select {
		case <-release:
		case <-r.Context().Done():
		}
		panic(http.ErrAbortHandler)
	}))
	callerDone := make(chan error, 1)
	go func() {
		_, _, _, err := h.request(h.ctx, proxy.URL, "POST", "/", current.AccessToken, map[string]string{"event_id": event, "content": content}, nil)
		callerDone <- err
	}()
	finish := func() error {
		once.Do(func() { close(release) })
		defer proxy.Close()
		select {
		case err := <-callerDone:
			if err == nil {
				return errors.New("the held acknowledgement unexpectedly reached its caller")
			}
			return nil
		case <-time.After(3 * time.Second):
			proxy.CloseClientConnections()
			return errors.New("lost-acknowledgement request did not finish")
		}
	}
	select {
	case result := <-committed:
		return result, finish, nil
	case err := <-failed:
		_ = finish()
		return sendResult{}, nil, err
	case <-h.ctx.Done():
		_ = finish()
		return sendResult{}, nil, errors.New("lost-acknowledgement check was canceled")
	case <-time.After(12 * time.Second):
		_ = finish()
		return sendResult{}, nil, errors.New("gateway did not commit the gated response")
	}
}

func (h *harness) accept() error {
	runID, err := randomID()
	if err != nil {
		return err
	}
	firstSecret, err := randomID()
	if err != nil {
		return err
	}
	rotatedSecret, err := randomID()
	if err != nil {
		return err
	}
	source, event, content := "restart-check-"+runID, "event-"+runID, "Local restart acceptance message."
	var first, second, third *childProcess
	var oldSession, renewed session
	var acknowledged sendResult
	var original history
	var finishHeld func() error
	defer func() {
		if finishHeld != nil {
			_ = finishHeld()
		}
	}()
	if err := h.check("persistent_gateway_start_and_private_route_boundary", func() error {
		var err error
		first, err = h.start("gateway_before_crash", source, firstSecret, false)
		if err != nil {
			return err
		}
		if err := h.expect(first.origin, "GET", "/readyz", "", nil, nil, 200, nil); err != nil {
			return err
		}
		for _, path := range []string{"/", "/chat/", "/demo/login", "/host/me", "/host/chat-session", "/v1/conversations"} {
			if err := h.expect(first.origin, "GET", path, "", nil, nil, 404, nil); err != nil {
				return err
			}
		}
		oldSession, err = h.mint(first, firstSecret, "customer-a")
		if err != nil {
			return err
		}
		value, err := h.history(first, oldSession, nil)
		if err == nil && len(value.Messages) != 0 {
			return errors.New("run-specific conversation was not empty")
		}
		return err
	}); err != nil {
		return err
	}
	if err := h.check("reply_committed_while_acknowledgement_withheld", func() error {
		var err error
		acknowledged, finishHeld, err = h.holdAcknowledgement(first, oldSession, event, content)
		if err != nil {
			return err
		}
		original, err = h.history(first, oldSession, nil)
		if err == nil && (len(original.Messages) != 2 || !savedPair(original, acknowledged)) {
			return errors.New("committed history did not contain exactly one message and its reply")
		}
		return err
	}); err != nil {
		return err
	}
	if err := h.check("abrupt_process_exit_waited_and_acknowledgement_lost", func() error {
		if err := first.stop(true); err != nil {
			return err
		}
		err := finishHeld()
		finishHeld = nil
		return err
	}); err != nil {
		return err
	}
	if err := h.check("new_process_rotated_host_key_and_reissued_browser_credential", func() error {
		var err error
		second, err = h.start("gateway_after_crash", source, rotatedSecret, false)
		if err != nil {
			return err
		}
		if first.result.PID == second.result.PID {
			return errors.New("restart did not use a distinct process")
		}
		if err := h.expect(second.origin, "GET", messagesPath, oldSession.AccessToken, nil, nil, 401, nil); err != nil {
			return err
		}
		if err := h.expect(second.origin, "POST", sessionPath, firstSecret, map[string]string{"customer_id": "customer-a"}, nil, 401, nil); err != nil {
			return err
		}
		renewed, err = h.mint(second, rotatedSecret, "customer-a")
		if err == nil && (renewed.ConversationID != oldSession.ConversationID || renewed.AccessToken == oldSession.AccessToken) {
			return errors.New("credential reissue changed identity or reused the old token")
		}
		return err
	}); err != nil {
		return err
	}
	if err := h.check("history_ids_content_and_order_survive_process_restart", func() error {
		value, err := h.history(second, renewed, nil)
		if err == nil && !reflect.DeepEqual(original, value) {
			return errors.New("persisted history changed across process restart")
		}
		return err
	}); err != nil {
		return err
	}
	if err := h.check("lost_acknowledgement_replay_is_idempotent", func() error {
		value, err := h.send(second, renewed, nil, event, content)
		if err != nil {
			return err
		}
		if value != acknowledged {
			return errors.New("retry changed message or reply identifiers")
		}
		current, err := h.history(second, renewed, nil)
		if err == nil && !reflect.DeepEqual(original, current) {
			return errors.New("retry duplicated or changed persisted history")
		}
		return err
	}); err != nil {
		return err
	}
	if err := h.check("reused_event_with_changed_content_is_rejected", func() error {
		return h.expect(second.origin, "POST", messagesPath, renewed.AccessToken, map[string]string{"event_id": event, "content": "Changed acceptance message."}, nil, 409, nil)
	}); err != nil {
		return err
	}
	if err := h.check("same_host_different_customer_isolation", func() error {
		other, err := h.mint(second, rotatedSecret, "customer-b")
		if err != nil {
			return err
		}
		if other.ConversationID == renewed.ConversationID {
			return errors.New("different customers shared a conversation")
		}
		before, err := h.history(second, other, nil)
		if err != nil {
			return err
		}
		if len(before.Messages) != 0 {
			return errors.New("different customer saw existing history")
		}
		sent, err := h.send(second, other, nil, event, content)
		if err != nil {
			return err
		}
		if sent.MessageID == acknowledged.MessageID || sent.ReplyMessageID == acknowledged.ReplyMessageID {
			return errors.New("event identifiers collided across customers")
		}
		after, err := h.history(second, renewed, nil)
		if err == nil && !reflect.DeepEqual(original, after) {
			return errors.New("other customer's send modified the first customer's history")
		}
		return err
	}); err != nil {
		return err
	}
	if err := h.check("graceful_gateway_shutdown_waited", func() error { return second.stop(false) }); err != nil {
		return err
	}
	if err := h.check("different_host_same_customer_and_event_isolation", func() error {
		var err error
		third, err = h.start("gateway_other_host", source+"-other", rotatedSecret, false)
		if err != nil {
			return err
		}
		if third.result.PID == second.result.PID || third.result.PID == first.result.PID {
			return errors.New("host-isolation restart reused a process")
		}
		other, err := h.mint(third, rotatedSecret, "customer-a")
		if err != nil {
			return err
		}
		if other.ConversationID == renewed.ConversationID {
			return errors.New("different hosts shared a conversation")
		}
		before, err := h.history(third, other, nil)
		if err != nil {
			return err
		}
		if len(before.Messages) != 0 {
			return errors.New("different host saw existing history")
		}
		sent, err := h.send(third, other, nil, event, content)
		if err != nil {
			return err
		}
		if sent.MessageID == acknowledged.MessageID || sent.ReplyMessageID == acknowledged.ReplyMessageID {
			return errors.New("event identifiers collided across hosts")
		}
		return third.stop(false)
	}); err != nil {
		return err
	}
	if h.options.demoBinary != "" {
		return h.acceptDemo(runID)
	}
	return nil
}

func (h *harness) demoLogin(child *childProcess) (*http.Cookie, error) {
	status, _, header, err := h.request(h.ctx, child.origin, "POST", "/demo/login", "", map[string]string{"account": "lin"}, nil)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, errors.New("demo login was not accepted")
	}
	response := http.Response{Header: header}
	for _, cookie := range response.Cookies() {
		if cookie.Name == "webchat_demo_session" && cookie.Value != "" && cookie.HttpOnly {
			return cookie, nil
		}
	}
	return nil, errors.New("demo login did not return the expected HttpOnly cookie")
}

func (h *harness) demoMint(child *childProcess, cookie *http.Cookie) (session, error) {
	var result session
	err := h.expect(child.origin, "POST", "/host/chat-session", "", struct{}{}, cookie, 200, &result)
	if err == nil && (result.AccessToken == "" || result.ConversationID == "" || !result.ExpiresAt.After(time.Now())) {
		return session{}, errors.New("demo host returned an invalid restricted credential")
	}
	return result, err
}

func (h *harness) acceptDemo(runID string) error {
	var first, second *childProcess
	var cookie *http.Cookie
	var previous, renewed session
	var sent sendResult
	var original history
	event, content := "demo-event-"+runID, "Local persistent demo restart acceptance."
	if err := h.check("demo_host_login_send_and_saved_history", func() error {
		var err error
		first, err = h.start("demo_before_crash", "", "", true)
		if err != nil {
			return err
		}
		cookie, err = h.demoLogin(first)
		if err != nil {
			return err
		}
		previous, err = h.demoMint(first, cookie)
		if err != nil {
			return err
		}
		sent, err = h.send(first, previous, cookie, event, content)
		if err != nil {
			return err
		}
		original, err = h.history(first, previous, cookie)
		if err == nil && !savedPair(original, sent) {
			return errors.New("demo history did not contain the saved exchange")
		}
		return err
	}); err != nil {
		return err
	}
	if err := h.check("demo_restart_rejects_old_login_and_reauthenticates_same_identity", func() error {
		if err := first.stop(true); err != nil {
			return err
		}
		var err error
		second, err = h.start("demo_after_crash", "", "", true)
		if err != nil {
			return err
		}
		if first.result.PID == second.result.PID {
			return errors.New("demo restart did not use a distinct process")
		}
		if err := h.expect(second.origin, "POST", "/host/chat-session", "", struct{}{}, cookie, 401, nil); err != nil {
			return err
		}
		cookie, err = h.demoLogin(second)
		if err != nil {
			return err
		}
		renewed, err = h.demoMint(second, cookie)
		if err != nil {
			return err
		}
		if renewed.ConversationID != previous.ConversationID || renewed.AccessToken == previous.AccessToken {
			return errors.New("demo restart did not preserve identity and rotate credential")
		}
		return h.expect(second.origin, "GET", messagesPath, previous.AccessToken, nil, cookie, 401, nil)
	}); err != nil {
		return err
	}
	if err := h.check("demo_history_and_idempotency_survive_process_restart", func() error {
		value, err := h.history(second, renewed, cookie)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(original, value) {
			return errors.New("demo history changed across process restart")
		}
		replayed, err := h.send(second, renewed, cookie, event, content)
		if err != nil {
			return err
		}
		if sent != replayed {
			return errors.New("demo retry changed persisted identifiers")
		}
		after, err := h.history(second, renewed, cookie)
		if err == nil && !reflect.DeepEqual(original, after) {
			return errors.New("demo replay duplicated or changed history")
		}
		return err
	}); err != nil {
		return err
	}
	return h.check("demo_graceful_shutdown_waited", func() error { return second.stop(false) })
}
