package einoadapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmbeddingWireOrderAndIndependentConfiguration(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "wrong-key")
	t.Setenv("OPENAI_BASE_URL", "http://invalid.invalid")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/embeddings" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("wrong request", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "test-embedding" || body["encoding_format"] != "float" || len(body["input"].([]any)) != 2 {
			t.Error(body)
		}
		if _, ok := body["dimensions"]; ok {
			t.Error("dimensions must be unset")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}]}`)
	}))
	defer server.Close()
	e, err := NewCompatibleEmbedder(context.Background(), EmbeddingConfig{APIKey: "test-key", Model: "test-embedding", BaseURL: server.URL + "/v1", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("constructor called network")
	}
	v, err := e.Embed(context.Background(), []string{"first", "second"})
	if err != nil || len(v) != 2 || v[0][0] != 1 || v[1][1] != 1 {
		t.Fatal(v, err)
	}
}

func TestEmbeddingInvalidResponseAndSafeError(t *testing.T) {
	for _, body := range []string{`{"data":[]}`, `{"data":[{"embedding":[1,0]}]}`, `{"data":[{"index":2,"embedding":[1,0]}]}`, `{"data":[{"index":0,"embedding":[0,0]}]}`, `{"error":{"message":"sensitive-marker"}}`, strings.Repeat("x", (8<<20)+1)} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, body)
		}))
		e, err := NewCompatibleEmbedder(context.Background(), EmbeddingConfig{APIKey: "key", Model: "model", BaseURL: s.URL, Timeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Embed(context.Background(), []string{"q"})
		s.Close()
		if err == nil || strings.Contains(err.Error(), "sensitive-marker") {
			t.Fatal(err)
		}
	}
}

func TestEmbeddingRejectsRedirectAndCancellation(t *testing.T) {
	var downstream atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { downstream.Add(1) }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	e, err := NewCompatibleEmbedder(context.Background(), EmbeddingConfig{APIKey: "key", Model: "model", BaseURL: s.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Embed(context.Background(), []string{"q"}); err == nil || downstream.Load() != 0 {
		t.Fatal("redirect followed", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Embed(ctx, []string{"q"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, base := range []string{"http://example.com/v1", "https://user:pass@example.com", "https://example.com/v1/embeddings", "https://example.com/v1?key=secret"} {
		if err := ValidateEmbeddingConfig(EmbeddingConfig{APIKey: "key", Model: "model", BaseURL: base, Timeout: time.Second}); err == nil {
			t.Fatal("accepted unsafe base")
		}
	}
}

func TestEmbeddingRejectsDuplicateIndexAndMixedDimensions(t *testing.T) {
	for _, body := range []string{
		`{"data":[{"index":0,"embedding":[1,0]},{"index":0,"embedding":[0,1]}]}`,
		`{"data":[{"index":0,"embedding":[1,0]},{"index":1,"embedding":[1]}]}`,
	} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, body)
		}))
		e, err := NewCompatibleEmbedder(context.Background(), EmbeddingConfig{APIKey: "key", Model: "model", BaseURL: s.URL, Timeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.Embed(context.Background(), []string{"a", "b"})
		s.Close()
		if !errors.Is(err, ErrEmbeddingRequest) {
			t.Fatal(err)
		}
	}
}

func TestEmbeddingHTTPTimeout(t *testing.T) {
	release := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer s.Close()
	defer close(release)
	e, err := NewCompatibleEmbedder(context.Background(), EmbeddingConfig{APIKey: "key", Model: "model", BaseURL: s.URL, Timeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Embed(context.Background(), []string{"q"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
