package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	einoadapter "github.com/qiuyier/omni-agent-customer-service/internal/ai/eino"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/semantic"
)

func resolveKnowledgeSettings(lookup LookupEnv) (KnowledgeConfig, error) {
	c := KnowledgeConfig{FAQPath: strings.TrimSpace(environmentValue(lookup, "KNOWLEDGE_FAQ_PATH")), Retriever: strings.TrimSpace(environmentValue(lookup, "KNOWLEDGE_RETRIEVER")), RetrievalTimeout: customerservice.DefaultKnowledgeRetrievalTimeout, IndexTimeout: 2 * time.Minute, MinScore: 0.7}
	if c.Retriever == "" {
		c.Retriever = "keyword"
	}
	if c.Retriever != "keyword" && c.Retriever != "semantic" {
		return KnowledgeConfig{}, fmt.Errorf("%w: KNOWLEDGE_RETRIEVER must be keyword or semantic", ErrInvalidKnowledgeConfiguration)
	}
	if c.FAQPath == "" {
		if c.Retriever == "semantic" {
			return KnowledgeConfig{}, fmt.Errorf("%w: semantic mode requires KNOWLEDGE_FAQ_PATH", ErrInvalidKnowledgeConfiguration)
		}
		return c, nil
	}
	if c.Retriever == "semantic" {
		c.RetrievalTimeout = 10 * time.Second
	}
	var err error
	if c.RetrievalTimeout, err = knowledgeDuration(lookup, "KNOWLEDGE_RETRIEVAL_TIMEOUT", c.RetrievalTimeout); err != nil {
		return KnowledgeConfig{}, err
	}
	if c.Retriever == "keyword" {
		return c, nil
	} // Never inspect embedding credentials in keyword mode.
	if c.IndexTimeout, err = knowledgeDuration(lookup, "KNOWLEDGE_INDEX_TIMEOUT", c.IndexTimeout); err != nil {
		return KnowledgeConfig{}, err
	}
	if value := strings.TrimSpace(environmentValue(lookup, "KNOWLEDGE_MIN_SCORE")); value != "" {
		c.MinScore, err = strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(c.MinScore) || math.IsInf(c.MinScore, 0) || c.MinScore < -1 || c.MinScore > 1 {
			return KnowledgeConfig{}, fmt.Errorf("%w: KNOWLEDGE_MIN_SCORE must be finite and between -1 and 1", ErrInvalidKnowledgeConfiguration)
		}
	}
	c.Embedding = einoadapter.EmbeddingConfig{APIKey: strings.TrimSpace(environmentValue(lookup, "EMBEDDING_API_KEY")), Model: strings.TrimSpace(environmentValue(lookup, "EMBEDDING_MODEL")), BaseURL: strings.TrimSpace(environmentValue(lookup, "EMBEDDING_BASE_URL")), Timeout: c.RetrievalTimeout}
	if err := einoadapter.ValidateEmbeddingConfig(c.Embedding); err != nil {
		return KnowledgeConfig{}, fmt.Errorf("%w: semantic mode requires EMBEDDING_API_KEY, EMBEDDING_MODEL and a safe EMBEDDING_BASE_URL API root", ErrInvalidKnowledgeConfiguration)
	}
	return c, nil
}

func knowledgeDuration(lookup LookupEnv, name string, fallback time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(environmentValue(lookup, name))
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 || d > 10*time.Minute {
		return 0, fmt.Errorf("%w: %s must be a positive duration at most 10m", ErrInvalidKnowledgeConfiguration, name)
	}
	return d, nil
}

// NewSemanticKnowledgeRetriever uses a prevalidated FAQ snapshot, so evaluation
// label validation and indexing can use the exact same documents without rereads.
func NewSemanticKnowledgeRetriever(ctx context.Context, c KnowledgeConfig, snippets []customerservice.KnowledgeSnippet) (*semantic.Retriever, error) {
	if ctx == nil || c.IndexTimeout <= 0 || c.RetrievalTimeout <= 0 {
		return nil, ErrInvalidKnowledgeConfiguration
	}
	ctx, cancel := context.WithTimeout(ctx, c.IndexTimeout)
	defer cancel()
	embedder, err := einoadapter.NewCompatibleEmbedder(ctx, c.Embedding)
	if err == nil {
		var r *semantic.Retriever
		r, err = semantic.New(ctx, snippets, embedder, semantic.Config{MinScore: c.MinScore})
		if err == nil {
			return r, nil
		}
	}
	// Keep startup diagnostics actionable without leaking a provider response,
	// source text, local file path, URL credentials or query.
	if errors.Is(err, context.Canceled) {
		return nil, fmt.Errorf("%w: %w", ErrInvalidKnowledgeConfiguration, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("%w: %w", ErrInvalidKnowledgeConfiguration, context.DeadlineExceeded)
	}
	return nil, fmt.Errorf("%w: semantic index initialization failed; check embedding configuration and document limits", ErrInvalidKnowledgeConfiguration)
}
