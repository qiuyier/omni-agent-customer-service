package bootstrap_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

func semanticSettings(base string) map[string]string {
	return map[string]string{
		"KNOWLEDGE_FAQ_PATH": "../../examples/knowledge/faq.json", "KNOWLEDGE_RETRIEVER": "semantic", "EMBEDDING_API_KEY": "local-test-key", "EMBEDDING_MODEL": "test-vectors", "EMBEDDING_BASE_URL": base, "KNOWLEDGE_MIN_SCORE": "0.8",
	}
}

func TestSemanticKnowledgeConfiguration(t *testing.T) {
	config, err := bootstrap.ResolveKnowledgeConfig(environment(semanticSettings("https://example.test/v1")))
	if err != nil {
		t.Fatal(err)
	}
	if config.RetrievalTimeout != 10*time.Second || config.IndexTimeout != 2*time.Minute || config.MinScore != 0.8 || config.Embedding.Model != "test-vectors" {
		t.Fatal("defaults")
	}
	for _, item := range [][2]string{{"EMBEDDING_API_KEY", ""}, {"EMBEDDING_MODEL", ""}, {"EMBEDDING_BASE_URL", "http://example.test"}, {"KNOWLEDGE_MIN_SCORE", "NaN"}, {"KNOWLEDGE_MIN_SCORE", "2"}, {"KNOWLEDGE_RETRIEVAL_TIMEOUT", "0s"}, {"KNOWLEDGE_INDEX_TIMEOUT", "bad"}, {"KNOWLEDGE_FAQ_PATH", ""}} {
		values := semanticSettings("https://example.test/v1")
		values[item[0]] = item[1]
		values["AI_API_KEY"] = "must-not-fallback"
		values["OPENAI_API_KEY"] = "must-not-fallback"
		if _, err := bootstrap.ResolveKnowledgeConfig(environment(values)); !errors.Is(err, bootstrap.ErrInvalidKnowledgeConfiguration) || strings.Contains(err.Error(), "must-not-fallback") {
			t.Fatal(item[0], err)
		}
	}
	values := semanticSettings("bad")
	values["KNOWLEDGE_RETRIEVER"] = "keyword"
	values["KNOWLEDGE_MIN_SCORE"] = "NaN"
	if _, err := bootstrap.ResolveKnowledgeConfig(environment(values)); err != nil {
		t.Fatal("keyword must ignore embedding settings", err)
	}
}

func TestSemanticKnowledgeLocalEndpoint(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		data := make([]map[string]any, len(body.Input))
		for i, text := range body.Input {
			v := []float64{1, 0, 0}
			if strings.Contains(text, "重置登录密码") || text == "口令忘了" {
				v = []float64{0, 1, 0}
			} else if strings.Contains(text, "查询开票入口") {
				v = []float64{0, 0, 1}
			}
			data[i] = map[string]any{"index": i, "embedding": v}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer s.Close()
	c, err := bootstrap.ResolveKnowledgeConfig(environment(semanticSettings(s.URL + "/v1")))
	if err != nil {
		t.Fatal(err)
	}
	r, err := bootstrap.NewKnowledgeRetrieverContext(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("index must be built once", calls.Load())
	}
	got, err := r.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: "口令忘了", Limit: 3})
	if err != nil || len(got) != 1 || got[0].ID != "demo-password-reset" {
		t.Fatal(got, err)
	}
	if calls.Load() != 2 {
		t.Fatal("query should only embed itself")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bootstrap.NewKnowledgeRetrieverContext(ctx, c); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
