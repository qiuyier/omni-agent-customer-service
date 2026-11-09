package main

import (
	"context"
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"sync"
	"testing"
)

type captureMemory struct {
	knowledgeMemory
	captureMu sync.Mutex
	questions []string
}

func (m *captureMemory) RecordFeedback(_ context.Context, s string) error {
	m.captureMu.Lock()
	defer m.captureMu.Unlock()
	m.questions = append(m.questions, s)
	return errors.New("fictional storage failure")
}
func TestManagedFeedbackGatewayOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "default_off", true: "enabled"}[enabled], func(t *testing.T) {
			model := newLocalChatModel(t, nil)
			env := aiEnvironment(t, model)
			delete(env, "KNOWLEDGE_FAQ_PATH")
			env["WEBCHAT_KNOWLEDGE_SOURCE"] = "managed"
			if enabled {
				env["WEBCHAT_KNOWLEDGE_FEEDBACK"] = "true"
			}
			config, err := resolveConfig(mapLookup(env), "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			store := &captureMemory{}
			admin, _ := manage.New(store, func(context.Context, manage.Permission) (string, error) { return "fixture", nil })
			if _, err = admin.SaveDraft(context.Background(), 0, []byte(`[{"id":"hours","title":"Hours","source":"fictional","content":"test answer","keywords":["hours"]}]`)); err != nil {
				t.Fatal(err)
			}
			if _, err = admin.PublishDraft(context.Background(), 1, false); err != nil {
				t.Fatal(err)
			}
			gateway, err := newGateway(withKnowledge{conversation.NewMemoryRepository(), store}, config)
			if err != nil {
				t.Fatal(err)
			}
			if len(store.questions) != 0 {
				t.Fatal("readiness collected")
			}
			session := aiSession(t, gateway, config, "fictional-customer")
			aiSend(t, gateway, session.AccessToken, `{"event_id":"miss","content":"help alice@example.org"}`)
			aiSend(t, gateway, session.AccessToken, `{"event_id":"hit","content":"hours"}`)
			store.captureMu.Lock()
			defer store.captureMu.Unlock()
			if enabled && (len(store.questions) != 1 || store.questions[0] != "help [已脱敏]") || !enabled && len(store.questions) != 0 {
				t.Fatal("capture opt-in or redaction failed")
			}
			if len(model.snapshot()) != 2 {
				t.Fatal("capture failure blocked local mock replies")
			}
		})
	}
}

func TestManagedFeedbackFlag(t *testing.T) {
	env := validEnvironment()
	c, err := resolveConfig(mapLookup(env), "")
	if err != nil || c.KnowledgeFeedback {
		t.Fatal("not default off")
	}
	for _, raw := range []string{"true", "yes", "1", "TRUE"} {
		env["WEBCHAT_KNOWLEDGE_FEEDBACK"] = raw
		if _, err = resolveConfig(mapLookup(env), ""); err == nil {
			t.Fatal("non-managed or malformed flag accepted")
		}
	}
	model := newLocalChatModel(t, nil)
	env = aiEnvironment(t, model)
	delete(env, "KNOWLEDGE_FAQ_PATH")
	env["WEBCHAT_KNOWLEDGE_SOURCE"] = "managed"
	env["WEBCHAT_KNOWLEDGE_FEEDBACK"] = "true"
	c, err = resolveConfig(mapLookup(env), "")
	if err != nil || !c.KnowledgeFeedback {
		t.Fatal(err)
	}
}
