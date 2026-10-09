package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
)

func TestResourceCheckKnowledgeAndSafeModes(t *testing.T) {
	for _, tc := range []struct {
		name, mode, data   string
		missing, wantError bool
	}{
		{name: "fake ignores missing file", missing: true},
		{name: "AI valid", mode: "ai", data: `[{"id":"test","title":"Test","source":"fixture","content":"Fixture","keywords":["test"]}]`},
		{name: "AI empty snapshot", mode: "ai", data: `[]`},
		{name: "AI missing", mode: "ai", missing: true, wantError: true},
		{name: "AI malformed", mode: "ai", data: `secret-content-not-json`, wantError: true},
		{name: "AI unknown field", mode: "ai", data: `[{"id":"test","private":"secret-content"}]`, wantError: true},
		{name: "AI invalid mode", mode: "secret-mode", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := validEnvironment()
			env["WEBCHAT_REPLY_MODE"] = tc.mode
			env["AI_PROVIDER"] = "custom"
			env["AI_PROTOCOL"] = "chat_completions"
			env["AI_MODEL"] = "fixture"
			env["AI_API_KEY"] = "secret-api-key"
			env["AI_BASE_URL"] = "https://model.invalid/v1"
			path := filepath.Join(t.TempDir(), "secret-faq.json")
			env["KNOWLEDGE_FAQ_PATH"] = path
			if !tc.missing {
				if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			opened, checked, closed := 0, 0, 0
			deps := serverDependencies{startupTimeout: time.Second, open: func(ctx context.Context, _ bootstrap.RepositoryConfig) (*repositoryHandle, error) {
				opened++
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("missing database timeout")
				}
				return &repositoryHandle{check: func(context.Context) error { checked++; return nil }, close: func() { closed++ }}, nil
			}} // nil listener: preflight must never listen.
			var out bytes.Buffer
			err := runCommand(context.Background(), []string{"-check-resources"}, mapLookup(env), &out, deps)
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected result: %v", err)
			}
			if tc.wantError {
				if opened != 0 {
					t.Fatal("invalid knowledge/config reached database")
				}
				if out.Len() != 0 {
					t.Fatal("failure reported success")
				}
			} else if opened != 1 || checked != 1 || closed != 1 {
				t.Fatal("readiness or cleanup not completed")
			}
			text := out.String()
			if err != nil {
				text += err.Error()
			}
			for _, secret := range []string{path, "secret-content", "secret-mode", env["AI_API_KEY"], env["AI_BASE_URL"], env["DATABASE_URL"], env["WEBCHAT_HOST_SECRET"]} {
				if strings.Contains(text, secret) {
					t.Fatal("private value exposed")
				}
			}
			if !tc.wantError && !strings.Contains(text, "remain unverified") {
				t.Fatal("missing check limitations")
			}
		})
	}
}

func TestResourceCheckFailuresAndCleanup(t *testing.T) {
	for _, stage := range []string{"open", "schema", "deadline", "canceled", "output"} {
		t.Run(stage, func(t *testing.T) {
			closed := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "canceled" {
				cancel()
			}
			deps := serverDependencies{startupTimeout: time.Millisecond, open: func(ctx context.Context, _ bootstrap.RepositoryConfig) (*repositoryHandle, error) {
				if stage == "canceled" {
					t.Fatal("opened after cancellation")
				}
				if stage == "open" {
					return nil, errors.New("secret-driver-error")
				}
				return &repositoryHandle{close: func() { closed++ }, check: func(ctx context.Context) error {
					if stage == "schema" {
						return errors.New("secret-schema-error")
					}
					if stage == "deadline" {
						<-ctx.Done()
					}
					return nil
				}}, nil
			}}
			var out bytes.Buffer
			var writer io.Writer = &out
			if stage == "output" {
				writer = preflightFailWriter{}
			}
			err := runCommand(ctx, []string{"-check-resources"}, mapLookup(validEnvironment()), writer, deps)
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe failure: %v", err)
			}
			want := 1
			if stage == "open" || stage == "canceled" {
				want = 0
			}
			if closed != want {
				t.Fatal("resource not closed")
			}
		})
	}
	if err := runCommand(context.Background(), []string{"-check-config", "-check-resources"}, nil, io.Discard, serverDependencies{}); err == nil {
		t.Fatal("conflicting flags accepted")
	}
}

type preflightFailWriter struct{}

func TestResourceCheckSemanticNeverCallsModelOrEmbedding(t *testing.T) {
	model := newLocalChatModel(t, nil)
	var calls atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer endpoint.Close()
	env := aiEnvironment(t, model)
	env["KNOWLEDGE_RETRIEVER"] = "semantic"
	env["EMBEDDING_API_KEY"] = "test-embedding-key"
	env["EMBEDDING_MODEL"] = "test-embedding"
	env["EMBEDDING_BASE_URL"] = endpoint.URL + "/v1"
	deps := serverDependencies{startupTimeout: time.Second, open: func(context.Context, bootstrap.RepositoryConfig) (*repositoryHandle, error) {
		return &repositoryHandle{check: func(context.Context) error { return nil }, close: func() {}}, nil
	}}
	var out bytes.Buffer
	if err := runCommand(context.Background(), []string{"-check-resources"}, mapLookup(env), &out, deps); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || len(model.snapshot()) != 0 {
		t.Fatal("preflight contacted a provider")
	}
	if !strings.Contains(out.String(), "semantic index not built") {
		t.Fatal("missing semantic scope")
	}
}

func (preflightFailWriter) Write([]byte) (int, error) { return 0, errors.New("secret-output-error") }
