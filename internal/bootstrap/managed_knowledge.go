package bootstrap

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
)

func ValidateManagedKnowledge(c WebchatReplyConfig) error {
	if c.Mode != "ai" || strings.TrimSpace(c.Knowledge.FAQPath) != "" || c.Knowledge.Retriever != "" && c.Knowledge.Retriever != "keyword" || c.Knowledge.RetrievalTimeout <= 0 || c.Knowledge.RetrievalTimeout > 20*time.Second {
		return errors.New("managed knowledge requires AI, keyword retrieval and no FAQ file")
	}
	return c.Validate()
}

// ManagedKnowledgeRetriever exposes only the retrieval port, not admin rights.
// Startup/readiness requires a valid published version (explicit [] is valid).
func ManagedKnowledgeRetriever(ctx context.Context, repo manage.Repository) (customerservice.KnowledgeRetriever, error) {
	s, e := manage.New(repo, func(context.Context, manage.Permission) (string, error) { return "", manage.ErrDenied })
	if e == nil {
		_, e = s.Retrieve(ctx, customerservice.KnowledgeQuery{Text: "readiness", Limit: 1})
	}
	if e != nil {
		return nil, errors.New("managed knowledge unavailable; publish a valid version before starting")
	}
	return s, nil
}
func NewManagedWebchatReplyOptions(ctx context.Context, c WebchatReplyConfig, repo manage.Repository) ([]customerservice.Option, error) {
	if e := ValidateManagedKnowledge(c); e != nil {
		return nil, e
	}
	if ctx == nil {
		return nil, errors.New("managed knowledge requires a context")
	}
	probe, cancel := context.WithTimeout(ctx, c.Knowledge.RetrievalTimeout)
	defer cancel()
	retriever, e := ManagedKnowledgeRetriever(probe, repo)
	if e != nil {
		return nil, e
	}
	options, e := NewWebchatReplyOptions(ctx, c, nil)
	if e != nil {
		return nil, e
	}
	return append(options, customerservice.WithKnowledgeRetriever(retriever), customerservice.WithKnowledgeRetrievalTimeout(c.Knowledge.RetrievalTimeout)), nil
}
