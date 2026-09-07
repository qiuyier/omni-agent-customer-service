package bootstrap_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

func TestResolveKnowledgeConfigDisablesRetrievalWithoutPath(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
	}{
		{name: "missing path", values: map[string]string{}},
		{name: "empty path", values: map[string]string{"KNOWLEDGE_FAQ_PATH": ""}},
		{name: "blank path", values: map[string]string{"KNOWLEDGE_FAQ_PATH": " \t\n "}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config, err := bootstrap.ResolveKnowledgeConfig(environment(test.values))
			if err != nil {
				t.Fatalf("ResolveKnowledgeConfig() error = %v", err)
			}
			if config.FAQPath != "" {
				t.Fatalf("FAQPath = %q, want empty", config.FAQPath)
			}
		})
	}
}

func TestResolveKnowledgeConfigTrimsPath(t *testing.T) {
	config, err := bootstrap.ResolveKnowledgeConfig(environment(map[string]string{
		"KNOWLEDGE_FAQ_PATH": " \t./data/local FAQ.json\n ",
	}))
	if err != nil {
		t.Fatalf("ResolveKnowledgeConfig() error = %v", err)
	}
	if config.FAQPath != "./data/local FAQ.json" {
		t.Fatalf("FAQPath = %q, want trimmed path with internal space preserved", config.FAQPath)
	}
}

func TestResolveKnowledgeConfigRejectsNilLookup(t *testing.T) {
	config, err := bootstrap.ResolveKnowledgeConfig(nil)
	if !errors.Is(err, bootstrap.ErrInvalidKnowledgeConfiguration) {
		t.Fatalf("ResolveKnowledgeConfig() error = %v, want %v", err, bootstrap.ErrInvalidKnowledgeConfiguration)
	}
	if config.FAQPath != "" {
		t.Fatal("invalid configuration must not return a FAQ path")
	}
}

func TestNewKnowledgeRetrieverDisabled(t *testing.T) {
	for _, path := range []string{"", " \t\n "} {
		retriever, err := bootstrap.NewKnowledgeRetriever(bootstrap.KnowledgeConfig{FAQPath: path})
		if err != nil {
			t.Fatalf("NewKnowledgeRetriever() error = %v", err)
		}
		if retriever != nil {
			t.Fatal("disabled retrieval must return an untyped nil interface")
		}
	}
}

func TestNewKnowledgeRetrieverLoadsFAQAtStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "faq.json")
	content := `[{"id":"returns","title":"Return policy","source":"policies/returns","content":"Contact support before returning an item.","keywords":["return"]}]`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	retriever, err := bootstrap.NewKnowledgeRetriever(bootstrap.KnowledgeConfig{FAQPath: " \t" + path + "\n "})
	if err != nil {
		t.Fatalf("NewKnowledgeRetriever() error = %v", err)
	}
	if retriever == nil {
		t.Fatal("a valid FAQ file must enable retrieval")
	}
	// Remove only this test's temporary fixture: retrieval must depend on the
	// startup snapshot, not on a file read during each request.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	snippets, err := retriever.Retrieve(context.Background(), customerservice.KnowledgeQuery{
		Text:  "Can I return this item?",
		Limit: customerservice.MaxKnowledgeSnippets,
	})
	if err != nil {
		t.Fatalf("Retrieve() after removing FAQ file: %v", err)
	}
	if len(snippets) != 1 || snippets[0].ID != "returns" {
		t.Fatalf("Retrieve() = %#v, want the startup-loaded return policy", snippets)
	}
}

func TestNewKnowledgeRetrieverRejectsInvalidFAQWithoutLeakingData(t *testing.T) {
	const secret = "sensitive-faq-marker"
	tests := []struct {
		name    string
		content string
		missing bool
	}{
		{name: "missing file", missing: true},
		{name: "invalid JSON", content: `{"` + secret + `":`},
		{name: "invalid FAQ format", content: `[{"id":"` + secret + `"}]`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), secret+".json")
			if !test.missing {
				if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			retriever, err := bootstrap.NewKnowledgeRetriever(bootstrap.KnowledgeConfig{FAQPath: path})
			if !errors.Is(err, bootstrap.ErrInvalidKnowledgeConfiguration) {
				t.Fatalf("NewKnowledgeRetriever() error = %v, want %v", err, bootstrap.ErrInvalidKnowledgeConfiguration)
			}
			if retriever != nil {
				t.Fatal("invalid FAQ configuration must not return a retriever")
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), path) {
				t.Fatal("configuration error leaked a file path or FAQ content")
			}
		})
	}
}
