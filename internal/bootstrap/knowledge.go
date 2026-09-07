package bootstrap

import (
	"errors"
	"fmt"
	"strings"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/faq"
)

var ErrInvalidKnowledgeConfiguration = errors.New("bootstrap: invalid knowledge configuration")

// KnowledgeConfig selects the optional startup-loaded FAQ adapter. A relative
// FAQPath is resolved against the server process's working directory, not the
// source directory or the directory containing an environment file.
type KnowledgeConfig struct {
	FAQPath string
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

	return KnowledgeConfig{
		FAQPath: strings.TrimSpace(environmentValue(lookup, "KNOWLEDGE_FAQ_PATH")),
	}, nil
}

// NewKnowledgeRetriever loads and validates the FAQ once at startup. Requests
// use the in-memory adapter; changing the file requires restarting the server.
// A nil retriever with no error means knowledge retrieval is disabled.
func NewKnowledgeRetriever(config KnowledgeConfig) (customerservice.KnowledgeRetriever, error) {
	path := strings.TrimSpace(config.FAQPath)
	if path == "" {
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
	return retriever, nil
}
