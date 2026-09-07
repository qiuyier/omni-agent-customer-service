package einoadapter_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	einoadapter "github.com/qiuyier/omni-agent-customer-service/internal/ai/eino"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

// Exercise the real Eino/SDK serialization against a local endpoint, not a
// mocked AgenticModel. No credentials or external model access are required.
func TestDefaultResponsesWireContract(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "generate"
		if streaming {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			requests := make(chan recordedRequest, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- recordRequest(r)
				if !streaming {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"resp_test","object":"response","status":"completed","model":"responses-test-model","output":[{"id":"msg_test","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello","annotations":[]}]}]}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, event := range []string{
					`{"type":"response.created","response":{"id":"resp_test","model":"responses-test-model","status":"in_progress"}}`,
					`{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_test","type":"message","role":"assistant","status":"in_progress","content":[]}}`,
					`{"type":"response.content_part.added","item_id":"msg_test","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`,
					`{"type":"response.output_text.delta","item_id":"msg_test","output_index":0,"content_index":0,"delta":"Hel"}`,
					`{"type":"response.output_text.delta","item_id":"msg_test","output_index":0,"content_index":0,"delta":"lo"}`,
					`{"type":"response.completed","response":{"id":"resp_test","model":"responses-test-model","status":"completed","output":[]}}`,
				} {
					_, _ = io.WriteString(w, "data: "+event+"\n\n")
					w.(http.Flusher).Flush()
				}
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			generator, err := einoadapter.NewOpenAIResponsesGenerator(ctx, einoadapter.OpenAIResponsesConfig{
				APIKey: "local-test-only", BaseURL: server.URL + "/v1",
			})
			if err != nil {
				t.Fatal(err)
			}
			input := customerservice.ReplyRequest{History: []customerservice.ReplyTurn{
				{Role: customerservice.ReplyRoleUser, Content: "hello"},
			}}
			var text strings.Builder
			if streaming {
				reader, err := generator.Stream(ctx, input)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				for {
					delta, err := reader.Recv()
					if errors.Is(err, io.EOF) {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					text.WriteString(delta.Content)
				}
			} else {
				reply, err := generator.Generate(ctx, input)
				if err != nil {
					t.Fatal(err)
				}
				text.WriteString(reply.Content)
			}
			if text.String() != "Hello" {
				t.Fatalf("reply = %q", text.String())
			}
			var request recordedRequest
			select {
			case request = <-requests:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if request.err != nil {
				t.Fatal(request.err)
			}
			if request.method != http.MethodPost || request.path != "/v1/responses" {
				t.Fatalf("request = %s %s", request.method, request.path)
			}
			if request.body["model"] != einoadapter.DefaultOpenAIModel || request.body["store"] != false {
				t.Fatalf("unexpected model/store: %#v", request.body)
			}
			if streaming && request.body["stream"] != true {
				t.Fatal("stream flag missing")
			}
		})
	}
}

func TestParseProvider(t *testing.T) {
	tests := map[string]einoadapter.Provider{
		"":             einoadapter.ProviderOpenAI,
		" OPENAI ":     einoadapter.ProviderOpenAI,
		"deepseek":     einoadapter.ProviderDeepSeek,
		"zhipu":        einoadapter.ProviderGLM,
		"bigmodel":     einoadapter.ProviderGLM,
		"moonshot":     einoadapter.ProviderKimi,
		"dashscope":    einoadapter.ProviderQwen,
		"bailian":      einoadapter.ProviderQwen,
		"open-router":  einoadapter.ProviderOpenRouter,
		"silicon_flow": einoadapter.ProviderSiliconFlow,
		"custom":       einoadapter.ProviderCustom,
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			got, err := einoadapter.ParseProvider(input)
			if err != nil {
				t.Fatalf("ParseProvider(%q) error = %v", input, err)
			}
			if got != want {
				t.Fatalf("ParseProvider(%q) = %q, want %q", input, got, want)
			}
		})
	}
	if _, err := einoadapter.ParseProvider("unknown"); !errors.Is(err, einoadapter.ErrUnknownProvider) {
		t.Fatalf("unknown provider error = %v", err)
	}
}

func TestParseProtocol(t *testing.T) {
	tests := map[string]einoadapter.Protocol{
		"responses":         einoadapter.ProtocolResponses,
		" response ":        einoadapter.ProtocolResponses,
		"chat":              einoadapter.ProtocolChatCompletions,
		"chat-completions":  einoadapter.ProtocolChatCompletions,
		"openai-compatible": einoadapter.ProtocolChatCompletions,
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			got, err := einoadapter.ParseProtocol(input)
			if err != nil {
				t.Fatalf("ParseProtocol(%q) error = %v", input, err)
			}
			if got != want {
				t.Fatalf("ParseProtocol(%q) = %q, want %q", input, got, want)
			}
		})
	}
	if _, err := einoadapter.ParseProtocol("completions"); !errors.Is(err, einoadapter.ErrUnsupportedProtocol) {
		t.Fatalf("unsupported protocol error = %v", err)
	}
}

func TestProviderAPIKeyEnvironmentVariable(t *testing.T) {
	tests := map[einoadapter.Provider]string{
		einoadapter.ProviderOpenAI:      "OPENAI_API_KEY",
		einoadapter.ProviderDeepSeek:    "DEEPSEEK_API_KEY",
		einoadapter.ProviderGLM:         "ZHIPUAI_API_KEY",
		einoadapter.ProviderKimi:        "MOONSHOT_API_KEY",
		einoadapter.ProviderQwen:        "DASHSCOPE_API_KEY",
		einoadapter.ProviderOpenRouter:  "OPENROUTER_API_KEY",
		einoadapter.ProviderSiliconFlow: "SILICONFLOW_API_KEY",
		einoadapter.ProviderCustom:      "",
	}
	for provider, want := range tests {
		got, err := einoadapter.ProviderAPIKeyEnvironmentVariable(provider)
		if err != nil {
			t.Fatalf("ProviderAPIKeyEnvironmentVariable(%q) error = %v", provider, err)
		}
		if got != want {
			t.Fatalf("ProviderAPIKeyEnvironmentVariable(%q) = %q, want %q", provider, got, want)
		}
	}
}

func TestProviderPresetsBuildWithoutNetwork(t *testing.T) {
	providers := []einoadapter.Provider{
		einoadapter.ProviderOpenAI,
		einoadapter.ProviderDeepSeek,
		einoadapter.ProviderGLM,
		einoadapter.ProviderKimi,
		einoadapter.ProviderQwen,
		einoadapter.ProviderOpenRouter,
		einoadapter.ProviderSiliconFlow,
	}
	for _, provider := range providers {
		t.Run(string(provider), func(t *testing.T) {
			config := einoadapter.ProviderConfig{
				Provider: provider,
				APIKey:   "test-key",
				Model:    "test-model",
			}
			if provider == einoadapter.ProviderOpenAI {
				config.Model = ""
			}
			generator, err := einoadapter.NewProviderGenerator(context.Background(), config)
			if err != nil || generator == nil {
				t.Fatalf("NewProviderGenerator() = %v, error %v", generator, err)
			}
		})
	}

	generator, err := einoadapter.NewProviderGenerator(context.Background(), einoadapter.ProviderConfig{
		Provider: einoadapter.ProviderCustom,
		Model:    "local-model",
		BaseURL:  "http://127.0.0.1:11434/v1",
	})
	if err != nil || generator == nil {
		t.Fatalf("custom provider without API key = %v, error %v", generator, err)
	}

	generator, err = einoadapter.NewProviderGenerator(context.Background(), einoadapter.ProviderConfig{
		Provider:          einoadapter.ProviderCustom,
		Model:             "internal-model",
		BaseURL:           "http://models.internal/v1",
		AllowInsecureHTTP: true,
	})
	if err != nil || generator == nil {
		t.Fatalf("explicit insecure internal HTTP provider = %v, error %v", generator, err)
	}
}

func TestProviderGeneratorUsesChatCompletionsCompatibilityProtocol(t *testing.T) {
	requests := make(chan recordedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- recordRequest(request)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":1,
			"model":"compatible-model",
			"choices":[{
				"index":0,
				"message":{"role":"assistant","content":"compatible reply"},
				"finish_reason":"stop"
			}],
			"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}
		}`)
	}))
	defer server.Close()

	generator, err := einoadapter.NewProviderGenerator(context.Background(), einoadapter.ProviderConfig{
		Provider:     einoadapter.ProviderDeepSeek,
		APIKey:       "chat-test-key",
		Model:        "compatible-model",
		BaseURL:      server.URL + "/v1",
		SystemPrompt: "support safely",
		Headers:      map[string]string{"X-Test-Header": "header-value"},
		ExtraFields:  map[string]any{"enable_thinking": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := generator.Generate(context.Background(), customerservice.ReplyRequest{
		History: []customerservice.ReplyTurn{{
			Role:    customerservice.ReplyRoleUser,
			Content: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if reply.Content != "compatible reply" {
		t.Fatalf("reply content = %q", reply.Content)
	}

	request := <-requests
	if request.err != nil {
		t.Fatalf("decode request: %v", request.err)
	}
	if request.method != http.MethodPost || request.path != "/v1/chat/completions" {
		t.Fatalf("request = %s %s", request.method, request.path)
	}
	if request.authorization != "Bearer chat-test-key" {
		t.Fatalf("Authorization = %q", request.authorization)
	}
	if request.headers.Get("X-Test-Header") != "header-value" {
		t.Fatalf("X-Test-Header = %q", request.headers.Get("X-Test-Header"))
	}
	if request.body["model"] != "compatible-model" {
		t.Fatalf("request model = %#v", request.body["model"])
	}
	if request.body["enable_thinking"] != true {
		t.Fatalf("enable_thinking = %#v", request.body["enable_thinking"])
	}
	messages, ok := request.body["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %#v", request.body["messages"])
	}
}

func TestProviderGeneratorUsesResponsesProtocolWithoutStorage(t *testing.T) {
	requests := make(chan recordedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- recordRequest(request)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
			"id":"resp_test",
			"object":"response",
			"created_at":1,
			"status":"completed",
			"model":"responses-model",
			"output":[{
				"id":"msg_test",
				"type":"message",
				"status":"completed",
				"role":"assistant",
				"content":[{"type":"output_text","text":"responses reply","annotations":[]}]
			}],
			"usage":{
				"input_tokens":1,
				"input_tokens_details":{"cached_tokens":0},
				"output_tokens":1,
				"output_tokens_details":{"reasoning_tokens":0},
				"total_tokens":2
			}
		}`)
	}))
	defer server.Close()

	generator, err := einoadapter.NewProviderGenerator(context.Background(), einoadapter.ProviderConfig{
		Provider: einoadapter.ProviderOpenAI,
		APIKey:   "responses-test-key",
		Model:    "responses-model",
		BaseURL:  server.URL + "/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := generator.Generate(context.Background(), customerservice.ReplyRequest{
		History: []customerservice.ReplyTurn{{
			Role:    customerservice.ReplyRoleUser,
			Content: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if reply.Content != "responses reply" {
		t.Fatalf("reply content = %q", reply.Content)
	}

	request := <-requests
	if request.err != nil {
		t.Fatalf("decode request: %v", request.err)
	}
	if request.method != http.MethodPost || request.path != "/v1/responses" {
		t.Fatalf("request = %s %s", request.method, request.path)
	}
	if request.authorization != "Bearer responses-test-key" {
		t.Fatalf("Authorization = %q", request.authorization)
	}
	if request.body["model"] != "responses-model" {
		t.Fatalf("request model = %#v", request.body["model"])
	}
	if request.body["store"] != false {
		t.Fatalf("store = %#v, want false", request.body["store"])
	}
	if _, ok := request.body["input"].([]any); !ok {
		t.Fatalf("input = %#v", request.body["input"])
	}
}

func TestCustomProviderSupportsCustomAuthorization(t *testing.T) {
	requests := make(chan recordedRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- recordRequest(request)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{
			"id":"chatcmpl-local",
			"object":"chat.completion",
			"created":1,
			"model":"local-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"local reply"},"finish_reason":"stop"}]
		}`)
	}))
	defer server.Close()

	generator, err := einoadapter.NewProviderGenerator(context.Background(), einoadapter.ProviderConfig{
		Provider: einoadapter.ProviderCustom,
		Model:    "local-model",
		BaseURL:  server.URL + "/v1",
		Headers:  map[string]string{"Authorization": "Basic custom-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := generator.Generate(context.Background(), customerservice.ReplyRequest{
		History: []customerservice.ReplyTurn{{Role: customerservice.ReplyRoleUser, Content: "hello"}},
	}); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	request := <-requests
	if request.authorization != "Basic custom-token" {
		t.Fatalf("Authorization = %q", request.authorization)
	}
}

func TestNewProviderGeneratorRejectsInvalidConfiguration(t *testing.T) {
	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name    string
		ctx     context.Context
		config  einoadapter.ProviderConfig
		wantErr error
	}{
		{
			name:    "nil context",
			config:  einoadapter.ProviderConfig{},
			wantErr: einoadapter.ErrInvalidConfiguration,
		},
		{
			name:    "canceled context",
			ctx:     canceledContext,
			config:  einoadapter.ProviderConfig{},
			wantErr: context.Canceled,
		},
		{
			name:    "unknown provider",
			ctx:     context.Background(),
			config:  einoadapter.ProviderConfig{Provider: "unknown"},
			wantErr: einoadapter.ErrUnknownProvider,
		},
		{
			name: "unsupported protocol",
			ctx:  context.Background(),
			config: einoadapter.ProviderConfig{
				Provider: einoadapter.ProviderOpenAI,
				Protocol: "completions",
			},
			wantErr: einoadapter.ErrUnsupportedProtocol,
		},
		{
			name:    "missing API key",
			ctx:     context.Background(),
			config:  einoadapter.ProviderConfig{Provider: einoadapter.ProviderDeepSeek, Model: "model"},
			wantErr: einoadapter.ErrInvalidConfiguration,
		},
		{
			name:    "missing non OpenAI model",
			ctx:     context.Background(),
			config:  einoadapter.ProviderConfig{Provider: einoadapter.ProviderDeepSeek, APIKey: "key"},
			wantErr: einoadapter.ErrInvalidConfiguration,
		},
		{
			name:    "missing custom base URL",
			ctx:     context.Background(),
			config:  einoadapter.ProviderConfig{Provider: einoadapter.ProviderCustom, Model: "model"},
			wantErr: einoadapter.ErrInvalidConfiguration,
		},
		{
			name: "negative timeout",
			ctx:  context.Background(),
			config: einoadapter.ProviderConfig{
				Provider: einoadapter.ProviderOpenAI,
				APIKey:   "key",
				Timeout:  -time.Second,
			},
			wantErr: einoadapter.ErrInvalidConfiguration,
		},
		{
			name: "API key and Authorization conflict",
			ctx:  context.Background(),
			config: einoadapter.ProviderConfig{
				Provider: einoadapter.ProviderCustom,
				APIKey:   "key",
				Model:    "model",
				BaseURL:  "https://example.com/v1",
				Headers:  map[string]string{"authorization": "Basic token"},
			},
			wantErr: einoadapter.ErrInvalidConfiguration,
		},
		{
			name: "keyless non loopback HTTP without opt in",
			ctx:  context.Background(),
			config: einoadapter.ProviderConfig{
				Provider: einoadapter.ProviderCustom,
				Model:    "model",
				BaseURL:  "http://models.internal/v1",
			},
			wantErr: einoadapter.ErrInvalidConfiguration,
		},
		{
			name: "custom Authorization over non loopback HTTP",
			ctx:  context.Background(),
			config: einoadapter.ProviderConfig{
				Provider: einoadapter.ProviderCustom,
				Model:    "model",
				BaseURL:  "http://models.internal/v1",
				Headers:  map[string]string{"Authorization": "Basic secret"},
			},
			wantErr: einoadapter.ErrInvalidConfiguration,
		},
		{
			name: "blank header name",
			ctx:  context.Background(),
			config: einoadapter.ProviderConfig{
				Provider: einoadapter.ProviderCustom,
				Model:    "model",
				BaseURL:  "https://example.com/v1",
				Headers:  map[string]string{" ": "value"},
			},
			wantErr: einoadapter.ErrInvalidConfiguration,
		},
		{
			name: "managed chat extra field",
			ctx:  context.Background(),
			config: einoadapter.ProviderConfig{
				Provider:    einoadapter.ProviderCustom,
				Model:       "model",
				BaseURL:     "https://example.com/v1",
				ExtraFields: map[string]any{"messages": []any{}},
			},
			wantErr: einoadapter.ErrInvalidConfiguration,
		},
		{
			name: "Responses extra field unsupported by Eino version",
			ctx:  context.Background(),
			config: einoadapter.ProviderConfig{
				Provider:    einoadapter.ProviderOpenAI,
				APIKey:      "key",
				ExtraFields: map[string]any{"service_tier": "default"},
			},
			wantErr: einoadapter.ErrInvalidConfiguration,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := einoadapter.NewProviderGenerator(test.ctx, test.config)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("NewProviderGenerator() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestNewProviderGeneratorRejectsUnsafeBaseURLsWithoutLeakingAPIKey(t *testing.T) {
	urls := []string{
		"relative/v1",
		"ftp://example.com/v1",
		"http://example.com/v1",
		"https://user:password@example.com/v1",
		"https://example.com/v1?",
		"https://example.com/v1#",
		"https://example.com/v1?region=cn",
		"https://example.com/v1/chat/completions",
		"https://example.com/v1/responses",
	}
	const apiKey = "secret-key-must-not-appear"
	for _, baseURL := range urls {
		t.Run(baseURL, func(t *testing.T) {
			_, err := einoadapter.NewProviderGenerator(context.Background(), einoadapter.ProviderConfig{
				Provider: einoadapter.ProviderOpenAI,
				APIKey:   apiKey,
				BaseURL:  baseURL,
			})
			if !errors.Is(err, einoadapter.ErrInvalidConfiguration) {
				t.Fatalf("base URL %q error = %v", baseURL, err)
			}
			if strings.Contains(err.Error(), apiKey) {
				t.Fatalf("error leaked API key: %v", err)
			}
		})
	}
}

type recordedRequest struct {
	method        string
	path          string
	authorization string
	headers       http.Header
	body          map[string]any
	err           error
}

func recordRequest(request *http.Request) recordedRequest {
	body := make(map[string]any)
	err := json.NewDecoder(request.Body).Decode(&body)
	return recordedRequest{
		method:        request.Method,
		path:          request.URL.Path,
		authorization: request.Header.Get("Authorization"),
		headers:       request.Header.Clone(),
		body:          body,
		err:           err,
	}
}
