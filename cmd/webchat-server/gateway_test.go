package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/webchat"
)

func testServerHandler(t *testing.T, check func(context.Context) error) (*serverHandler, serverConfig) {
	t.Helper()
	config := validConfig(t)
	gateway, err := newGateway(conversation.NewMemoryRepository(), config)
	if err != nil {
		t.Fatal(err)
	}
	return &serverHandler{gateway: gateway, check: check}, config
}

func requestHandler(handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	result := httptest.NewRecorder()
	handler.ServeHTTP(result, request)
	return result
}

func TestGatewayHasNoDemoUIHostOrServiceRoutes(t *testing.T) {
	handler, _ := testServerHandler(t, func(context.Context) error { return nil })
	for _, path := range []string{"/", "/chat/", "/index.html", "/assets/app.js", "/host/me", "/host/chat-session", "/demo/login", "/demo/logout", "/host/bootstrap", "/host/login", "/host/logout", "/bootstrap", "/v1", "/v1/conversations", "/v1/messages", "/metrics", "/ready"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			response := requestHandler(handler, method, path, "", "")
			if response.Code != http.StatusNotFound {
				t.Fatalf("%s %s = %d", method, path, response.Code)
			}
		}
	}
	for _, route := range []struct{ method, path string }{{"POST", webchat.SessionPath}, {"GET", webchat.MessagesPath}, {"POST", webchat.MessagesPath}} {
		if got := requestHandler(handler, route.method, route.path, "", "").Code; got != http.StatusUnauthorized {
			t.Fatalf("unprotected route %s %s: %d", route.method, route.path, got)
		}
	}
}

func TestTrustedGatewayUsesStaticFakeReplyAndUUIDs(t *testing.T) {
	handler, config := testServerHandler(t, func(context.Context) error { return nil })
	sessionResponse := requestHandler(handler, "POST", webchat.SessionPath, config.HostSecret, `{"customer_id":"customer-one"}`)
	if sessionResponse.Code != http.StatusCreated {
		t.Fatalf("mint status %d", sessionResponse.Code)
	}
	var session webchat.SessionResponse
	if err := json.Unmarshal(sessionResponse.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	response := requestHandler(handler, "POST", webchat.MessagesPath, session.AccessToken, `{"event_id":"event-one","content":"hello"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("send status %d: %s", response.Code, response.Body.String())
	}
	var sent webchat.SendResult
	if err := json.Unmarshal(response.Body.Bytes(), &sent); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{sent.MessageID, sent.ReplyMessageID} {
		if _, err := uuid.Parse(id); err != nil {
			t.Fatalf("message ID is not a UUID")
		}
	}
	historyResponse := requestHandler(handler, "GET", webchat.MessagesPath, session.AccessToken, "")
	var history webchat.History
	if err := json.Unmarshal(historyResponse.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	want, err := fakeReply(context.Background(), customerservice.ReplyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Messages) != 2 || history.Messages[1].Content != want.Content {
		t.Fatal("static fake reply was not persisted")
	}
	if bytes.Contains(historyResponse.Body.Bytes(), []byte(config.HostSecret)) {
		t.Fatal("history exposed host credential")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fakeReply(ctx, customerservice.ReplyRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatal("fake generator ignored cancellation")
	}
}

func TestHealthReadinessAndDrain(t *testing.T) {
	checks := 0
	checkErr := error(nil)
	handler, _ := testServerHandler(t, func(ctx context.Context) error {
		checks++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > readinessTimeout {
			t.Error("unbounded readiness")
		}
		return checkErr
	})
	if response := requestHandler(handler, "GET", "/healthz", "", ""); response.Code != 200 || checks != 0 {
		t.Fatal("liveness should not access storage")
	}
	if response := requestHandler(handler, "GET", "/readyz", "", ""); response.Code != 200 || checks != 1 {
		t.Fatal("readiness did not check storage")
	}
	checkErr = errors.New("postgres://private-user:private-password@private-host/private-db")
	response := requestHandler(handler, "GET", "/readyz", "", "")
	if response.Code != 503 || strings.Contains(response.Body.String(), "private") {
		t.Fatal("unsafe readiness failure")
	}
	for _, path := range []string{"/healthz", "/readyz"} {
		if requestHandler(handler, "POST", path, "", "").Code != 405 {
			t.Fatal("probe accepted mutation method")
		}
		if requestHandler(handler, "GET", path+"?credential=private", "", "").Code != 400 {
			t.Fatal("probe accepted query parameters")
		}
	}
	previous := checks
	handler.beginDrain()
	if requestHandler(handler, "GET", "/readyz", "", "").Code != 503 || checks != previous {
		t.Fatal("draining server admitted storage work")
	}
	if requestHandler(handler, "POST", webchat.SessionPath, "", "").Code != 503 {
		t.Fatal("draining server admitted sessions")
	}
	handler.active.Wait()
}
