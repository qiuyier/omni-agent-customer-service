package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/webchat"
)

const testAccountReference = "Open Account settings, choose Change password, and follow the on-screen verification steps. Never share passwords or verification codes."

type recordedAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// The compatible adapter can encode text either as a string or as OpenAI
// content parts. Preserve the same text in both valid wire representations.
func (m *recordedAIMessage) UnmarshalJSON(data []byte) error {
	var wire struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	m.Role = wire.Role
	if err := json.Unmarshal(wire.Content, &m.Content); err == nil {
		return nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(wire.Content, &parts); err != nil {
		return err
	}
	m.Content = ""
	for _, part := range parts {
		if part.Type != "text" {
			return errors.New("test model received non-text content")
		}
		m.Content += part.Text
	}
	return nil
}

type recordedAIRequest struct {
	Model    string              `json:"model"`
	Messages []recordedAIMessage `json:"messages"`
}

type localChatModel struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []recordedAIRequest
	respond  func(http.ResponseWriter, *http.Request, int)
}

func newLocalChatModel(t *testing.T, respond func(http.ResponseWriter, *http.Request, int)) *localChatModel {
	t.Helper()
	model := &localChatModel{respond: respond}
	model.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-only" {
			t.Error("model request did not use the configured local endpoint and credential")
			http.Error(w, "invalid test request", http.StatusBadRequest)
			return
		}
		var request recordedAIRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid test payload", http.StatusBadRequest)
			return
		}
		if request.Model != "mock" {
			t.Error("model configuration was not server-owned")
		}
		model.mu.Lock()
		model.requests = append(model.requests, request)
		attempt := len(model.requests)
		model.mu.Unlock()
		if model.respond != nil {
			model.respond(w, r, attempt)
			return
		}
		writeLocalChatReply(w)
	}))
	t.Cleanup(model.server.Close)
	return model
}

func writeLocalChatReply(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"id":"chatcmpl-local","object":"chat.completion","created":1,"model":"mock","choices":[{"index":0,"message":{"role":"assistant","content":"This local test response explains the available reference only."},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
}

func (m *localChatModel) snapshot() []recordedAIRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]recordedAIRequest(nil), m.requests...)
}

func aiEnvironment(t *testing.T, model *localChatModel) map[string]string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "support-faq.json")
	fixture := `[{"id":"account-password","title":"Account password help","source":"help/account","content":"` + testAccountReference + `","keywords":["password"]},{"id":"service-window","title":"Support hours","source":"help/service","content":"The help desk is available Monday through Friday, 09:00–17:00 UTC.","keywords":["hours"]}]`
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	values := validEnvironment()
	values["WEBCHAT_HOST_ID"] = "generic-support"
	values["WEBCHAT_REPLY_MODE"] = "ai"
	values["AI_PROVIDER"] = "custom"
	values["AI_PROTOCOL"] = "chat_completions"
	values["AI_MODEL"] = "mock"
	values["AI_BASE_URL"] = model.server.URL + "/v1"
	values["AI_API_KEY"] = "test-only"
	values["KNOWLEDGE_FAQ_PATH"] = path
	return values
}

func aiGateway(t *testing.T, values map[string]string) (http.Handler, serverConfig) {
	t.Helper()
	config, err := resolveConfig(mapLookup(values), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := newGateway(conversation.NewMemoryRepository(), config)
	if err != nil {
		t.Fatal(err)
	}
	return gateway, config
}

func aiSession(t *testing.T, gateway http.Handler, config serverConfig, customer string) webchat.SessionResponse {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"customer_id": customer})
	response := requestHandler(gateway, "POST", webchat.SessionPath, config.HostSecret, string(body))
	if response.Code != http.StatusCreated {
		t.Fatalf("mint session: %d %s", response.Code, response.Body.String())
	}
	var session webchat.SessionResponse
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	return session
}

func aiHistory(t *testing.T, gateway http.Handler, token string) webchat.History {
	t.Helper()
	response := requestHandler(gateway, "GET", webchat.MessagesPath, token, "")
	if response.Code != http.StatusOK {
		t.Fatalf("read history: %d %s", response.Code, response.Body.String())
	}
	var history webchat.History
	if err := json.Unmarshal(response.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	return history
}

func aiSend(t *testing.T, gateway http.Handler, token, body string) webchat.SendResult {
	t.Helper()
	response := requestHandler(gateway, "POST", webchat.MessagesPath, token, body)
	if response.Code != http.StatusOK {
		t.Fatalf("send: %d %s", response.Code, response.Body.String())
	}
	var result webchat.SendResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "replied" || result.MessageID == "" || result.ReplyMessageID == "" {
		t.Fatalf("AI reply was not persisted: %#v", result)
	}
	return result
}

func assertAIReferences(t *testing.T, request recordedAIRequest, question string, expectedIDs ...string) {
	t.Helper()
	if len(request.Messages) < 4 || request.Messages[len(request.Messages)-1] != (recordedAIMessage{Role: "user", Content: question}) {
		t.Fatal("the original customer question did not remain a separate final user message")
	}
	var envelope struct {
		Kind     string `json:"kind"`
		Snippets []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"snippets"`
	}
	found, grounded := false, false
	for _, message := range request.Messages {
		if message.Role == "system" {
			if strings.Contains(message.Content, testAccountReference) {
				t.Fatal("reference text was promoted to trusted system instructions")
			}
			grounded = grounded || strings.Contains(message.Content, "If snippets is empty")
		}
		const prefix, suffix = "BEGIN_UNTRUSTED_KNOWLEDGE_JSON\n", "\nEND_UNTRUSTED_KNOWLEDGE_JSON"
		if strings.HasPrefix(message.Content, prefix) {
			if found || message.Role != "user" || !strings.HasSuffix(message.Content, suffix) {
				t.Fatal("reference isolation envelope was lost")
			}
			found = true
			encoded := strings.TrimSuffix(strings.TrimPrefix(message.Content, prefix), suffix)
			if err := json.Unmarshal([]byte(encoded), &envelope); err != nil {
				t.Fatal(err)
			}
			if len(expectedIDs) == 0 && !strings.Contains(encoded, `"snippets":[]`) {
				t.Fatal("retrieval miss did not produce a non-nil empty references array")
			}
		}
	}
	if !found || !grounded || envelope.Kind != "untrusted_knowledge_references" || len(envelope.Snippets) != len(expectedIDs) {
		t.Fatalf("wrong reference envelope: %#v", envelope)
	}
	for index, id := range expectedIDs {
		if envelope.Snippets[index].ID != id {
			t.Fatalf("wrong matched reference: %q", envelope.Snippets[index].ID)
		}
	}
	if len(expectedIDs) == 1 && expectedIDs[0] == "account-password" && envelope.Snippets[0].Content != testAccountReference {
		t.Fatal("matched reference content was altered or omitted")
	}
}

func TestAIGatewayKnowledgeReplayConflictAndCustomerIsolation(t *testing.T) {
	model := newLocalChatModel(t, nil)
	gateway, config := aiGateway(t, aiEnvironment(t, model))
	alice := aiSession(t, gateway, config, "alice")
	const body = `{"event_id":"first-event","content":"How do I change my password?"}`
	sent := aiSend(t, gateway, alice.AccessToken, body)
	history := aiHistory(t, gateway, alice.AccessToken)
	if len(history.Messages) != 2 {
		t.Fatal("expected one customer message and one AI reply")
	}
	requests := model.snapshot()
	if len(requests) != 1 {
		t.Fatal("expected one model invocation")
	}
	assertAIReferences(t, requests[0], "How do I change my password?", "account-password")
	if replay := aiSend(t, gateway, alice.AccessToken, body); replay != sent || !reflect.DeepEqual(aiHistory(t, gateway, alice.AccessToken), history) || len(model.snapshot()) != 1 {
		t.Fatal("persisted event replay regenerated a reply or changed history")
	}
	conflict := requestHandler(gateway, "POST", webchat.MessagesPath, alice.AccessToken, `{"event_id":"first-event","content":"Different content"}`)
	if conflict.Code != http.StatusConflict || len(model.snapshot()) != 1 || !reflect.DeepEqual(aiHistory(t, gateway, alice.AccessToken), history) {
		t.Fatal("changed content did not fail before model invocation")
	}
	bob := aiSession(t, gateway, config, "bob")
	if bob.ConversationID == alice.ConversationID || len(aiHistory(t, gateway, bob.AccessToken).Messages) != 0 {
		t.Fatal("customer histories were not isolated")
	}
	aiSend(t, gateway, bob.AccessToken, `{"event_id":"first-event","content":"Is lunar transport available?"}`)
	requests = model.snapshot()
	if len(requests) != 2 {
		t.Fatal("second customer event did not generate exactly once")
	}
	assertAIReferences(t, requests[1], "Is lunar transport available?")
	for _, message := range requests[1].Messages {
		if strings.Contains(message.Content, "How do I change my password?") {
			t.Fatal("another customer's history entered the AI prompt")
		}
	}
	if !reflect.DeepEqual(aiHistory(t, gateway, alice.AccessToken), history) || len(aiHistory(t, gateway, bob.AccessToken).Messages) != 2 {
		t.Fatal("second customer changed first customer's history")
	}
}

func TestAIGatewayProviderErrorIsSafeAndSameEventCanRetry(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	model := newLocalChatModel(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
		if fail.Load() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"provider-private-marker-and-key","type":"invalid_request_error","code":"bad_request"}}`)
			return
		}
		writeLocalChatReply(w)
	})
	gateway, config := aiGateway(t, aiEnvironment(t, model))
	session := aiSession(t, gateway, config, "retry-customer")
	const body = `{"event_id":"retry-event","content":"How do I change my password?"}`
	response := requestHandler(gateway, "POST", webchat.MessagesPath, session.AccessToken, body)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "provider-private-marker") || strings.Contains(response.Body.String(), "test-only") {
		t.Fatal("model failure was not safely mapped to 503")
	}
	before := aiHistory(t, gateway, session.AccessToken)
	if len(before.Messages) != 1 || before.Messages[0].Role != conversation.SenderCustomer {
		t.Fatal("failed generation should preserve only the accepted customer message")
	}
	fail.Store(false)
	sent := aiSend(t, gateway, session.AccessToken, body)
	after := aiHistory(t, gateway, session.AccessToken)
	if len(after.Messages) != 2 || after.Messages[0].ID != before.Messages[0].ID || sent.MessageID != before.Messages[0].ID || len(model.snapshot()) != 2 {
		t.Fatal("same-event retry duplicated the customer message or did not persist exactly one reply")
	}
	aiSend(t, gateway, session.AccessToken, body)
	if len(model.snapshot()) != 2 || !reflect.DeepEqual(aiHistory(t, gateway, session.AccessToken), after) {
		t.Fatal("successful retry was regenerated on replay")
	}
}

func TestAIGatewayCanceledGenerationCanRetryWithoutDuplicatingCustomer(t *testing.T) {
	entered, exited := make(chan struct{}), make(chan struct{})
	model := newLocalChatModel(t, func(w http.ResponseWriter, r *http.Request, attempt int) {
		if attempt == 1 {
			close(entered)
			defer close(exited)
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Second):
				t.Error("model request was not canceled")
			}
			return
		}
		writeLocalChatReply(w)
	})
	gateway, config := aiGateway(t, aiEnvironment(t, model))
	session := aiSession(t, gateway, config, "cancel-customer")
	const body = `{"event_id":"cancel-event","content":"How do I change my password?"}`
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest("POST", webchat.MessagesPath, strings.NewReader(body)).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer "+session.AccessToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); gateway.ServeHTTP(response, request) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("model request did not begin")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("gateway did not release the canceled generation")
	}
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("model connection did not observe cancellation")
	}
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("canceled generation returned %d", response.Code)
	}
	before := aiHistory(t, gateway, session.AccessToken)
	if len(before.Messages) != 1 {
		t.Fatal("canceled generation did not preserve exactly one customer message")
	}
	aiSend(t, gateway, session.AccessToken, body)
	after := aiHistory(t, gateway, session.AccessToken)
	if len(after.Messages) != 2 || before.Messages[0].ID != after.Messages[0].ID || len(model.snapshot()) != 2 {
		t.Fatal("retry after cancellation duplicated the customer message or model reply")
	}
}

func TestAIGatewayRequestDeadlinePreservesMessageForRetry(t *testing.T) {
	exited := make(chan struct{})
	model := newLocalChatModel(t, func(w http.ResponseWriter, r *http.Request, attempt int) {
		if attempt == 1 {
			defer close(exited)
			select {
			case <-r.Context().Done():
			case <-time.After(3 * time.Second):
				t.Error("request deadline did not cancel model HTTP")
			}
			return
		}
		writeLocalChatReply(w)
	})
	gateway, config := aiGateway(t, aiEnvironment(t, model))
	session := aiSession(t, gateway, config, "deadline-customer")
	const body = `{"event_id":"deadline-event","content":"How do I change my password?"}`
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("POST", webchat.MessagesPath, strings.NewReader(body)).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+session.AccessToken)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	gateway.ServeHTTP(w, r)
	if ctx.Err() != context.DeadlineExceeded || w.Code != http.StatusServiceUnavailable {
		t.Fatal("deadline was not mapped safely")
	}
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("model was not canceled")
	}
	before := aiHistory(t, gateway, session.AccessToken)
	if len(before.Messages) != 1 {
		t.Fatal("deadline lost or duplicated accepted message")
	}
	aiSend(t, gateway, session.AccessToken, body)
	after := aiHistory(t, gateway, session.AccessToken)
	if len(after.Messages) != 2 || after.Messages[0].ID != before.Messages[0].ID || len(model.snapshot()) != 2 {
		t.Fatal("deadline retry duplicated work")
	}
}

func TestAIGatewayDoesNotAcceptClientModelOrKnowledgeConfiguration(t *testing.T) {
	model := newLocalChatModel(t, nil)
	gateway, config := aiGateway(t, aiEnvironment(t, model))
	session := aiSession(t, gateway, config, "config-customer")
	for _, field := range []string{"model", "ai_base_url", "api_key", "system_prompt", "knowledge", "knowledge_faq_path", "reply_mode"} {
		body, _ := json.Marshal(map[string]any{"event_id": "injected-event", "content": "How do I change my password?", field: "client-config-marker"})
		response := requestHandler(gateway, "POST", webchat.MessagesPath, session.AccessToken, string(body))
		if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "client-config-marker") {
			t.Fatalf("client configuration field %s was accepted or echoed", field)
		}
	}
	if len(model.snapshot()) != 0 || len(aiHistory(t, gateway, session.AccessToken).Messages) != 0 {
		t.Fatal("invalid client configuration caused model calls or persisted messages")
	}
}

func TestAICheckConfigDoesNotOpenFilesCallModelsOrListen(t *testing.T) {
	model := newLocalChatModel(t, nil)
	values := aiEnvironment(t, model)
	values["KNOWLEDGE_FAQ_PATH"] = filepath.Join(t.TempDir(), "private-missing-faq.json")
	var output bytes.Buffer
	if err := runCommand(context.Background(), []string{"-check-config"}, mapLookup(values), &output, serverDependencies{}); err != nil {
		t.Fatal(err)
	}
	if len(model.snapshot()) != 0 || !strings.Contains(output.String(), "ai") {
		t.Fatal("AI check-config contacted the provider or reported the wrong mode")
	}
	for _, private := range []string{values["AI_API_KEY"], values["AI_BASE_URL"], values["KNOWLEDGE_FAQ_PATH"], values["DATABASE_URL"], values["WEBCHAT_HOST_SECRET"]} {
		if strings.Contains(output.String(), private) {
			t.Fatal("check-config output exposed private configuration")
		}
	}
	values["AI_BASE_URL"] = "http://remote.invalid/v1"
	if err := runCommand(context.Background(), []string{"-check-config"}, mapLookup(values), io.Discard, serverDependencies{}); err == nil || strings.Contains(err.Error(), "remote.invalid") {
		t.Fatal("offline validation accepted unsafe model endpoint or echoed its value")
	}
}

func TestAIInvalidKnowledgeFailsStartupWithoutFakeFallback(t *testing.T) {
	model := newLocalChatModel(t, nil)
	values := aiEnvironment(t, model)
	values["KNOWLEDGE_FAQ_PATH"] = filepath.Join(t.TempDir(), "private-missing-faq.json")
	config, err := resolveConfig(mapLookup(values), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var closed atomic.Bool
	deps := defaultDependencies()
	deps.open = func(context.Context, bootstrap.RepositoryConfig) (*repositoryHandle, error) {
		return &repositoryHandle{repository: conversation.NewMemoryRepository(), check: func(context.Context) error { return nil }, close: func() { closed.Store(true) }}, nil
	}
	deps.listen = func(string, string) (net.Listener, error) {
		t.Fatal("invalid knowledge started a listener")
		return nil, nil
	}
	var output bytes.Buffer
	err = runServer(context.Background(), config, &output, deps)
	if err == nil || strings.Contains(err.Error()+output.String(), "private-missing-faq") || strings.Contains(err.Error()+output.String(), "test-only") {
		t.Fatal("invalid knowledge silently fell back to fake or leaked private configuration")
	}
	if !closed.Load() || len(model.snapshot()) != 0 {
		t.Fatal("failed knowledge startup leaked storage or called the model")
	}
}

func TestAIGatewaySemanticRetrievalUsesLocalEmbeddingEndpoint(t *testing.T) {
	model := newLocalChatModel(t, nil)
	var embeddingCalls atomic.Int32
	embedding := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		embeddingCalls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/embeddings" || r.Header.Get("Authorization") != "Bearer embedding-test-only" {
			t.Error("incorrect embedding endpoint or independent credential")
		}
		var request struct {
			Input []string `json:"input"`
			Model string   `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Model != "mock-embedding" {
			t.Error("invalid embedding request")
			http.Error(w, "invalid", 400)
			return
		}
		rows := make([]map[string]any, len(request.Input))
		for i, input := range request.Input {
			vector := []float64{0, 1}
			if strings.Contains(strings.ToLower(input), "password") || strings.Contains(input, "credential") {
				vector = []float64{1, 0}
			}
			rows[i] = map[string]any{"index": i, "embedding": vector}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": rows})
	}))
	defer embedding.Close()
	values := aiEnvironment(t, model)
	values["KNOWLEDGE_RETRIEVER"] = "semantic"
	values["KNOWLEDGE_MIN_SCORE"] = "0.8"
	values["EMBEDDING_API_KEY"] = "embedding-test-only"
	values["EMBEDDING_MODEL"] = "mock-embedding"
	values["EMBEDDING_BASE_URL"] = embedding.URL + "/v1"
	gateway, config := aiGateway(t, values)
	if embeddingCalls.Load() != 1 || len(model.snapshot()) != 0 {
		t.Fatal("semantic startup must build only the embedding index")
	}
	session := aiSession(t, gateway, config, "semantic-customer")
	const body = `{"event_id":"semantic-event","content":"How can I replace my sign-in credential?"}`
	aiSend(t, gateway, session.AccessToken, body)
	requests := model.snapshot()
	if embeddingCalls.Load() != 2 || len(requests) != 1 {
		t.Fatal("semantic request did not use one query embedding and one model generation")
	}
	assertAIReferences(t, requests[0], "How can I replace my sign-in credential?", "account-password")
	aiSend(t, gateway, session.AccessToken, body)
	if embeddingCalls.Load() != 2 || len(model.snapshot()) != 1 {
		t.Fatal("event replay reran semantic retrieval or generation")
	}
}
