package bootstrap_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	einoadapter "github.com/qiuyier/omni-agent-customer-service/internal/ai/eino"
	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

func webchatAIValues() map[string]string {
	return map[string]string{"AI_PROVIDER": "custom", "AI_PROTOCOL": "chat_completions", "AI_MODEL": "local-test", "AI_BASE_URL": "http://127.0.0.1:1/v1", "AI_API_KEY": "unit-only"}
}

func TestWebchatFakeNeverReadsAIOrKnowledge(t *testing.T) {
	for _, mode := range []string{"", "fake"} {
		c, err := bootstrap.ResolveWebchatReplyConfig(mode, func(string) (string, bool) {
			t.Fatal("fake mode read environment")
			return "", false
		})
		if err != nil || c.Mode != "fake" || c.RequestTimeout() != 20*time.Second {
			t.Fatalf("invalid fake default: %v", err)
		}
		// Even an accidentally attached invalid AI config cannot cause fake I/O.
		c.AI.BaseURL = "private-invalid-endpoint"
		c.Knowledge.FAQPath = "/missing/private-faq"
		_, err = bootstrap.NewWebchatReplyOptions(context.Background(), c, customerservice.ReplyGeneratorFunc(func(context.Context, customerservice.ReplyRequest) (customerservice.Reply, error) {
			return customerservice.Reply{Content: "fake"}, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestWebchatAIConfigurationIsPureAndKeywordIgnoresEmbedding(t *testing.T) {
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer endpoint.Close()
	values := webchatAIValues()
	values["AI_BASE_URL"] = endpoint.URL + "/v1"
	values["KNOWLEDGE_FAQ_PATH"] = filepath.Join(t.TempDir(), "not-created.json")
	c, err := bootstrap.ResolveWebchatReplyConfig("ai", func(key string) (string, bool) {
		if strings.HasPrefix(key, "EMBEDDING_") {
			t.Fatal("keyword read embedding settings")
		}
		return environment(values)(key)
	})
	if err != nil || c.Validate() != nil || calls.Load() != 0 {
		t.Fatalf("configuration must be pure: %v", err)
	}
	if c.RequestTimeout() != bootstrap.WebchatAIRequestTimeout || c.RequestTimeout() <= bootstrap.DefaultAIReplyTimeout+20*time.Second {
		t.Fatal("request deadline does not leave retrieval and persistence headroom")
	}
	if _, err := bootstrap.NewWebchatReplyOptions(context.Background(), c, nil); err == nil || strings.Contains(err.Error(), values["KNOWLEDGE_FAQ_PATH"]) || !strings.Contains(err.Error(), "no fake fallback") {
		t.Fatal("missing FAQ must fail startup safely")
	}
	if calls.Load() != 0 {
		t.Fatal("failed FAQ initialization called provider")
	}
}

func TestWebchatAIRejectsUnsafeConfigurationWithoutEcho(t *testing.T) {
	for key, value := range map[string]string{
		"AI_PROVIDER": "private-provider-marker", "AI_PROTOCOL": "private-protocol-marker",
		"AI_BASE_URL": "https://private:secret@example.invalid/v1", "AI_MAX_CONCURRENT_REPLIES": "private-limit-marker",
		"KNOWLEDGE_RETRIEVER": "private-retriever-marker", "KNOWLEDGE_RETRIEVAL_TIMEOUT": "21s",
	} {
		t.Run(key, func(t *testing.T) {
			values := webchatAIValues()
			values["KNOWLEDGE_FAQ_PATH"] = "/not-read/private-faq"
			values[key] = value
			_, err := bootstrap.ResolveWebchatReplyConfig("ai", environment(values))
			if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
				t.Fatal("invalid configuration accepted or echoed")
			}
		})
	}
	if _, err := bootstrap.ResolveWebchatReplyConfig("ai", nil); err == nil {
		t.Fatal("nil lookup accepted")
	}
	if _, err := bootstrap.ResolveWebchatReplyConfig("private-mode", nil); err == nil || strings.Contains(err.Error(), "private-mode") {
		t.Fatal("unsafe mode diagnostic")
	}
}

func TestWebchatInitializationHonorsCancellationAndValidation(t *testing.T) {
	c, err := bootstrap.ResolveWebchatReplyConfig("ai", environment(webchatAIValues()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bootstrap.NewWebchatReplyOptions(ctx, c, nil); err == nil {
		t.Fatal("canceled initialization succeeded")
	}
	if _, err := bootstrap.NewWebchatReplyOptions(nil, c, nil); err == nil {
		t.Fatal("nil context accepted")
	}
	c.Runtime.MaxConcurrentReplies = 0
	if c.Validate() == nil {
		t.Fatal("unlimited model concurrency accepted")
	}
	if _, err := bootstrap.NewWebchatReplyOptions(context.Background(), bootstrap.WebchatReplyConfig{Mode: "fake"}, nil); err == nil {
		t.Fatal("missing fake generator accepted")
	}
}

func TestProviderValidationHasNoNetworkAndSanitizesFailure(t *testing.T) {
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer endpoint.Close()
	c := einoadapter.ProviderConfig{Provider: einoadapter.ProviderCustom, Protocol: einoadapter.ProtocolChatCompletions, Model: "local-test", BaseURL: endpoint.URL + "/v1"}
	if err := einoadapter.ValidateProviderConfig(c); err != nil {
		t.Fatal(err)
	}
	c.Provider = "private-provider-marker"
	err := einoadapter.ValidateProviderConfig(c)
	if !errors.Is(err, einoadapter.ErrInvalidConfiguration) || strings.Contains(err.Error(), "private") || calls.Load() != 0 {
		t.Fatal("validation was not pure and sanitized")
	}
}
