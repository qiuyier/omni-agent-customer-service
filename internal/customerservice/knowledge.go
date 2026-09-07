package customerservice

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultKnowledgeRetrievalTimeout = 2 * time.Second
	MaxKnowledgeQueryRunes           = 512
	MaxKnowledgeSnippets             = 3
	MaxKnowledgeContextRunes         = 6000
)

// KnowledgeQuery is independent of a search engine, embedding model or tenant
// identity. This increment uses one trusted, instance-wide FAQ dataset. A future
// multi-tenant retriever must add an authenticated scope, not infer it from text.
type KnowledgeQuery struct {
	Text  string
	Limit int
}

// KnowledgeSnippet is reference data, never a system instruction or chat turn.
// ID and Source describe provenance; this increment does not persist citations.
type KnowledgeSnippet struct {
	ID      string
	Title   string
	Source  string
	Content string
}

// KnowledgeRetriever searches a read-only knowledge source. Implementations must
// be concurrency-safe, respect ctx cancellation and avoid logging query/content.
// A successful empty result means no matches; a backend failure must be an error.
type KnowledgeRetriever interface {
	Retrieve(context.Context, KnowledgeQuery) ([]KnowledgeSnippet, error)
}

type KnowledgeRetrieverFunc func(context.Context, KnowledgeQuery) ([]KnowledgeSnippet, error)

func (f KnowledgeRetrieverFunc) Retrieve(ctx context.Context, query KnowledgeQuery) ([]KnowledgeSnippet, error) {
	return f(ctx, query)
}

// ReplyKnowledge distinguishes disabled retrieval (nil ReplyRequest.Knowledge)
// from a successful search with no usable evidence (non-nil, empty Snippets).
type ReplyKnowledge struct {
	Snippets []KnowledgeSnippet
}

func WithKnowledgeRetriever(retriever KnowledgeRetriever) Option {
	return func(service *Service) error {
		if retriever == nil {
			return fmt.Errorf("%w: knowledge retriever is nil", ErrInvalidDependency)
		}
		service.knowledgeRetriever = retriever
		return nil
	}
}

// WithKnowledgeRetrievalTimeout bounds knowledge preparation separately from
// model generation. It does not force-stop a retriever that ignores its context.
func WithKnowledgeRetrievalTimeout(timeout time.Duration) Option {
	return func(service *Service) error {
		if timeout <= 0 {
			return fmt.Errorf("%w: knowledge retrieval timeout must be positive", ErrInvalidDependency)
		}
		service.knowledgeTimeout = timeout
		return nil
	}
}

// prepareReplyKnowledge runs after the conversation snapshot transaction and
// duplicate fast path, but before acquiring a model slot or emitting AI events.
// The original snapshot remains the authority for post-generation validation.
func (s *Service) prepareReplyKnowledge(ctx context.Context, request ReplyRequest) (ReplyRequest, error) {
	if s.knowledgeRetriever == nil {
		return request, nil
	}
	// History may end with a system/tool turn; buildReplyHistory has already
	// verified that its newest customer turn is the requested trigger.
	var query string
	for index := len(request.History) - 1; index >= 0; index-- {
		if request.History[index].Role == ReplyRoleUser {
			query = strings.TrimSpace(request.History[index].Content)
			break
		}
	}
	query = knowledgeQueryPrefix(query)
	if query == "" {
		return ReplyRequest{}, fmt.Errorf("%w: trigger query is empty", ErrKnowledgeRetrievalFailed)
	}
	retrievalContext, cancel := context.WithTimeout(ctx, s.knowledgeTimeout)
	defer cancel()
	if err := retrievalContext.Err(); err != nil {
		return ReplyRequest{}, fmt.Errorf("%w: %w", ErrKnowledgeRetrievalFailed, err)
	}
	snippets, err := s.knowledgeRetriever.Retrieve(retrievalContext, KnowledgeQuery{Text: query, Limit: MaxKnowledgeSnippets})
	if contextErr := retrievalContext.Err(); contextErr != nil {
		err = contextErr
	}
	if err != nil {
		return ReplyRequest{}, fmt.Errorf("%w: %w", ErrKnowledgeRetrievalFailed, err)
	}
	bounded, err := boundedKnowledgeSnippets(snippets)
	if err != nil {
		return ReplyRequest{}, err
	}
	if err := retrievalContext.Err(); err != nil {
		return ReplyRequest{}, fmt.Errorf("%w: %w", ErrKnowledgeRetrievalFailed, err)
	}
	request.Knowledge = &ReplyKnowledge{Snippets: bounded}
	return request, nil
}

func knowledgeQueryPrefix(query string) string {
	// Slice at a rune boundary without allocating a copy of an unbounded query.
	count := 0
	for offset := range query {
		if count == MaxKnowledgeQueryRunes {
			return query[:offset]
		}
		count++
	}
	return query
}

func boundedKnowledgeSnippets(snippets []KnowledgeSnippet) ([]KnowledgeSnippet, error) {
	bounded := make([]KnowledgeSnippet, 0, min(len(snippets), MaxKnowledgeSnippets))
	seen := make(map[string]struct{}, MaxKnowledgeSnippets)
	remaining := MaxKnowledgeContextRunes
	// The port is asked for top K. Do not scan an arbitrarily large result from
	// a faulty adapter; enforce that same boundary again on the receiving side.
	for _, snippet := range snippets[:min(len(snippets), MaxKnowledgeSnippets)] {
		snippet.ID = strings.TrimSpace(snippet.ID)
		snippet.Title = strings.TrimSpace(snippet.Title)
		snippet.Source = strings.TrimSpace(snippet.Source)
		snippet.Content = strings.TrimSpace(snippet.Content)
		if !validKnowledgeField(snippet.ID, 128) || !validKnowledgeField(snippet.Title, 128) ||
			!validKnowledgeField(snippet.Source, 256) || !validKnowledgeField(snippet.Content, MaxKnowledgeContextRunes) {
			return nil, fmt.Errorf("%w: invalid knowledge snippet", ErrKnowledgeRetrievalFailed)
		}
		if _, duplicate := seen[snippet.ID]; duplicate {
			continue
		}
		seen[snippet.ID] = struct{}{}
		size := utf8.RuneCountInString(snippet.ID) + utf8.RuneCountInString(snippet.Title) +
			utf8.RuneCountInString(snippet.Source) + utf8.RuneCountInString(snippet.Content)
		if size > remaining {
			// Never cut a policy mid-sentence and accidentally lose its exceptions.
			// If an entire snippet cannot fit, omit it instead of truncating it.
			continue
		}
		bounded = append(bounded, snippet)
		remaining -= size
	}
	return bounded, nil
}

func validKnowledgeField(value string, maxRunes int) bool {
	return value != "" && len(value) <= maxRunes*utf8.UTFMax &&
		utf8.ValidString(value) && utf8.RuneCountInString(value) <= maxRunes
}
