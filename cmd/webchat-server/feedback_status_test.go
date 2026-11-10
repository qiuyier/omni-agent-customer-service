package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"strings"
	"testing"
	"time"
)

type countedKnowledge struct {
	knowledgeMemory
	fail bool
}

func (c *countedKnowledge) FeedbackCounts(context.Context) (int, int, error) {
	if c.fail {
		return 0, 0, errors.New("private storage error")
	}
	return 12, 3, nil
}
func TestManagedFeedbackPreflightIsReadOnly(t *testing.T) {
	model := newLocalChatModel(t, nil)
	env := aiEnvironment(t, model)
	delete(env, "KNOWLEDGE_FAQ_PATH")
	env["WEBCHAT_KNOWLEDGE_SOURCE"] = "managed"
	env["WEBCHAT_KNOWLEDGE_FEEDBACK"] = "true"
	store := &countedKnowledge{}
	service, _ := manage.New(store, func(context.Context, manage.Permission) (string, error) { return "fixture", nil })
	service.SaveDraft(context.Background(), 0, []byte("[]"))
	service.PublishDraft(context.Background(), 1, true)
	deps := serverDependencies{startupTimeout: time.Second, open: func(context.Context, bootstrap.RepositoryConfig) (*repositoryHandle, error) {
		return &repositoryHandle{repository: withKnowledge{conversation.NewMemoryRepository(), store}, check: func(context.Context) error { return nil }, close: func() {}}, nil
	}}
	var out bytes.Buffer
	if err := runCommand(context.Background(), []string{"-check-resources"}, mapLookup(env), &out, deps); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "not running-process state") || !strings.Contains(out.String(), "enabled=true; active=12/500; expired=3") || len(model.snapshot()) != 0 || store.state.Revision != 2 {
		t.Fatal("incorrect or mutating status")
	}
	store.fail = true
	out.Reset()
	if err := runCommand(context.Background(), []string{"-check-resources"}, mapLookup(env), &out, deps); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("queue failure not safe")
	}
}
