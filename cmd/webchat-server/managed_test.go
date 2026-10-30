package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagedResourceCheckIsReadOnly(t *testing.T) {
	model := newLocalChatModel(t, nil)
	values := aiEnvironment(t, model)
	delete(values, "KNOWLEDGE_FAQ_PATH")
	values["WEBCHAT_KNOWLEDGE_SOURCE"] = "managed"
	store := &knowledgeMemory{}
	admin, _ := manage.New(store, func(context.Context, manage.Permission) (string, error) { return "admin", nil })
	closed := 0
	deps := serverDependencies{startupTimeout: time.Second, open: func(context.Context, bootstrap.RepositoryConfig) (*repositoryHandle, error) {
		return &repositoryHandle{repository: withKnowledge{conversation.NewMemoryRepository(), store}, check: func(context.Context) error { return nil }, close: func() { closed++ }}, nil
	}}
	var out bytes.Buffer
	if e := runCommand(context.Background(), []string{"-check-resources"}, mapLookup(values), &out, deps); e == nil {
		t.Fatal("unpublished readiness accepted")
	}
	admin.SaveDraft(context.Background(), 0, []byte("[]"))
	admin.PublishDraft(context.Background(), 1, true)
	if e := runCommand(context.Background(), []string{"-check-resources"}, mapLookup(values), &out, deps); e != nil {
		t.Fatal(e)
	}
	if len(model.snapshot()) != 0 || closed != 2 || !strings.Contains(out.String(), "valid published managed FAQ") {
		t.Fatal("preflight side effect or incorrect output")
	}
	s, _ := store.Load(context.Background())
	if s.Revision != 2 {
		t.Fatal("preflight wrote state")
	}
}

type knowledgeMemory struct {
	mu    sync.Mutex
	state manage.State
}

func (m *knowledgeMemory) Load(context.Context) (manage.State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, _ := json.Marshal(m.state)
	var s manage.State
	json.Unmarshal(b, &s)
	return s, nil
}
func (m *knowledgeMemory) CompareAndSwap(_ context.Context, n uint64, s manage.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n != m.state.Revision {
		return manage.ErrConflict
	}
	m.state = s
	return nil
}

type withKnowledge struct {
	conversation.Repository
	store manage.Repository
}

func (r withKnowledge) KnowledgeStore() (manage.Repository, error) { return r.store, nil }
func TestManagedGatewayUsesOnlyPublishedKnowledge(t *testing.T) {
	model := newLocalChatModel(t, nil)
	values := aiEnvironment(t, model)
	delete(values, "KNOWLEDGE_FAQ_PATH")
	values["WEBCHAT_KNOWLEDGE_SOURCE"] = "managed"
	config, e := resolveConfig(mapLookup(values), "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	store := &knowledgeMemory{}
	repo := withKnowledge{conversation.NewMemoryRepository(), store}
	if _, e := newGateway(repo, config); e == nil {
		t.Fatal("unpublished store accepted")
	}
	if len(model.snapshot()) != 0 {
		t.Fatal("startup called model")
	}
	admin, _ := manage.New(store, func(context.Context, manage.Permission) (string, error) { return "admin", nil })
	ctx := context.Background()
	faq := func(text string) []byte {
		return []byte(`[{"id":"hours","title":"Hours","source":"fictional","content":"` + text + `","keywords":["hours"]}]`)
	}
	admin.SaveDraft(ctx, 0, faq("old-managed-reference"))
	state, _ := admin.View(ctx)
	old := state.Draft.ID
	admin.PublishDraft(ctx, 1, false)
	gateway, e := newGateway(repo, config)
	if e != nil {
		t.Fatal(e)
	}
	session := aiSession(t, gateway, config, "customer-a")
	send := func(id, want, absent string) {
		t.Helper()
		aiSend(t, gateway, session.AccessToken, `{"event_id":"`+id+`","content":"hours"}`)
		requests := model.snapshot()
		if len(requests) == 0 {
			t.Fatal("no mock request")
		}
		b, _ := json.Marshal(requests[len(requests)-1])
		if !strings.Contains(string(b), want) || strings.Contains(string(b), absent) {
			t.Fatal("incorrect published reference sent to local mock")
		}
	}
	send("first", "old-managed-reference", "new-managed-reference")
	admin.SaveDraft(ctx, 2, faq("new-managed-reference"))
	send("draft", "old-managed-reference", "new-managed-reference")
	admin.PublishDraft(ctx, 3, false)
	send("published", "new-managed-reference", "old-managed-reference")
	admin.Rollback(ctx, 4, old, false)
	send("rollback", "old-managed-reference", "new-managed-reference")
	// No customer/agent/host token automatically opens the management boundary.
	if w := requestHandler(gateway, "GET", "/internal/knowledge/state", config.HostSecret, ""); w.Code != http.StatusNotFound {
		t.Fatal(w.Code)
	}
}
func TestManagedConfigurationRejectsMixedSources(t *testing.T) {
	model := newLocalChatModel(t, nil)
	base := aiEnvironment(t, model)
	base["WEBCHAT_KNOWLEDGE_SOURCE"] = "managed"
	if _, e := resolveConfig(mapLookup(base), ""); e == nil {
		t.Fatal("managed and FAQ file accepted")
	}
	delete(base, "KNOWLEDGE_FAQ_PATH")
	base["KNOWLEDGE_RETRIEVER"] = "semantic"
	if _, e := resolveConfig(mapLookup(base), ""); e == nil {
		t.Fatal("managed semantic accepted")
	}
	delete(base, "KNOWLEDGE_RETRIEVER")
	base["WEBCHAT_REPLY_MODE"] = "fake"
	if _, e := resolveConfig(mapLookup(base), ""); e == nil {
		t.Fatal("managed fake accepted")
	}
	base["WEBCHAT_KNOWLEDGE_SOURCE"] = "unexpected"
	if _, e := resolveConfig(mapLookup(base), ""); e == nil {
		t.Fatal("unknown source accepted")
	}
	if len(model.snapshot()) != 0 {
		t.Fatal("validation called model")
	}
}
