package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	einoadapter "github.com/qiuyier/omni-agent-customer-service/internal/ai/eino"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/faq"
)

var ErrInvalidKnowledgeConfiguration = errors.New("bootstrap: invalid knowledge configuration")

// KnowledgeConfig selects the optional startup-loaded FAQ adapter. A relative
// FAQPath is resolved against the server process's working directory, not the
// source directory or the directory containing an environment file.
type KnowledgeConfig struct {
	FAQPath          string
	Retriever        string
	MinScore         float64
	RetrievalTimeout time.Duration
	IndexTimeout     time.Duration
	Embedding        einoadapter.EmbeddingConfig
}

// ResolveKnowledgeConfig leaves retrieval disabled unless KNOWLEDGE_FAQ_PATH is
// nonblank. The file is validated when the adapter is constructed at startup.
func ResolveKnowledgeConfig(lookup LookupEnv) (KnowledgeConfig, error) {
	if lookup == nil {
		return KnowledgeConfig{}, fmt.Errorf(
			"%w: environment lookup is nil",
			ErrInvalidKnowledgeConfiguration,
		)
	}

	return resolveKnowledgeSettings(lookup)
}

// NewKnowledgeRetriever loads and validates the FAQ once at startup. Requests
// use the in-memory adapter; changing the file requires restarting the server.
// A nil retriever with no error means knowledge retrieval is disabled.
func NewKnowledgeRetriever(config KnowledgeConfig) (customerservice.KnowledgeRetriever, error) {
	return NewKnowledgeRetrieverContext(context.Background(), config)
}

// NewKnowledgeRetrieverContext also supports cancellable semantic index creation.
func NewKnowledgeRetrieverContext(ctx context.Context, config KnowledgeConfig) (customerservice.KnowledgeRetriever, error) {
	if ctx == nil {
		return nil, ErrInvalidKnowledgeConfiguration
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mode := config.Retriever
	if mode == "" {
		mode = "keyword"
	}
	if mode != "keyword" && mode != "semantic" {
		return nil, ErrInvalidKnowledgeConfiguration
	}
	path := strings.TrimSpace(config.FAQPath)
	if path == "" {
		if mode == "semantic" {
			return nil, ErrInvalidKnowledgeConfiguration
		}
		return nil, nil
	}

	retriever, err := faq.Load(path)
	if err != nil {
		// Loader/parser errors may contain a sensitive path or FAQ content. Keep
		// only stable, actionable metadata at the startup logging boundary.
		return nil, fmt.Errorf(
			"%w: cannot load KNOWLEDGE_FAQ_PATH; check the file and FAQ format",
			ErrInvalidKnowledgeConfiguration,
		)
	}
	if mode == "keyword" {
		return retriever, nil
	}
	return NewSemanticKnowledgeRetriever(ctx, config, retriever.Snippets())
}
