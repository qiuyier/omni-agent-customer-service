// Package semantic implements a small, immutable in-memory cosine index.
// It knows neither HTTP nor Eino nor a particular embedding provider.
package semantic

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

const BatchSize = 16
const MaxDimensions = 16384
const DocumentFormat = "title-newline-content-v1"

var (
	ErrInvalidConfig   = errors.New("semantic: invalid configuration")
	ErrInvalidData     = errors.New("semantic: invalid documents or vectors")
	ErrInvalidQuery    = errors.New("semantic: invalid query")
	ErrEmbeddingFailed = errors.New("semantic: embedding failed")
)

// Embed returns one vector per input in input order, using one fixed model.
// Implementations must respect cancellation. Calls are serialized per index.
type Embedder interface {
	Embed(context.Context, []string) ([][]float64, error)
}
type Config struct{ MinScore float64 }
type Retriever struct {
	snippets   []customerservice.KnowledgeSnippet
	vectors    [][]float64
	embedder   Embedder
	gate       chan struct{}
	minScore   float64
	dimensions int
}

func DocumentText(s customerservice.KnowledgeSnippet) string { return s.Title + "\n" + s.Content }

func New(ctx context.Context, snippets []customerservice.KnowledgeSnippet, embedder Embedder, config Config) (*Retriever, error) {
	if ctx == nil || embedder == nil || !finite(config.MinScore) || config.MinScore < -1 || config.MinScore > 1 {
		return nil, ErrInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(snippets) < 1 || len(snippets) > 1000 {
		return nil, ErrInvalidData
	}
	r := &Retriever{snippets: append([]customerservice.KnowledgeSnippet{}, snippets...), embedder: embedder, minScore: config.MinScore, gate: make(chan struct{}, 1)}
	seen := map[string]bool{}
	for i, s := range r.snippets {
		s.ID, s.Title, s.Source, s.Content = strings.TrimSpace(s.ID), strings.TrimSpace(s.Title), strings.TrimSpace(s.Source), strings.TrimSpace(s.Content)
		if !validText(s.ID, 128, false) || !validText(s.Title, 128, false) || !validText(s.Source, 256, false) || !validText(s.Content, 6000, true) ||
			utf8.RuneCountInString(s.ID+s.Title+s.Source+s.Content) > customerservice.MaxKnowledgeContextRunes || seen[s.ID] {
			return nil, ErrInvalidData
		}
		seen[s.ID] = true
		r.snippets[i] = s
	}
	for start := 0; start < len(r.snippets); start += BatchSize {
		end := min(start+BatchSize, len(r.snippets))
		texts := make([]string, end-start)
		for i, s := range r.snippets[start:end] {
			texts[i] = DocumentText(s)
		}
		vectors, err := r.embed(ctx, texts, r.dimensions)
		if err != nil {
			return nil, err
		}
		if r.dimensions == 0 {
			r.dimensions = len(vectors[0])
		}
		r.vectors = append(r.vectors, vectors...)
	}
	return r, nil
}

func (r *Retriever) Dimensions() int { return r.dimensions }

func (r *Retriever) Retrieve(ctx context.Context, q customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
	if r == nil || r.embedder == nil || len(r.vectors) == 0 {
		return nil, ErrInvalidConfig
	}
	if ctx == nil || !validText(q.Text, customerservice.MaxKnowledgeQueryRunes, true) || q.Limit < 1 || q.Limit > customerservice.MaxKnowledgeSnippets {
		return nil, ErrInvalidQuery
	}
	vectors, err := r.embed(ctx, []string{strings.TrimSpace(q.Text)}, r.dimensions)
	if err != nil {
		return nil, err
	}
	type match struct {
		index int
		score float64
	}
	matches := make([]match, 0, len(r.vectors))
	for i, v := range r.vectors {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var score float64
		for j, x := range v {
			score += x * vectors[0][j]
		}
		score = max(-1, min(1, score))
		if score >= r.minScore {
			matches = append(matches, match{i, score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	result := make([]customerservice.KnowledgeSnippet, 0, min(q.Limit, len(matches)))
	for _, m := range matches[:min(q.Limit, len(matches))] {
		result = append(result, r.snippets[m.index])
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *Retriever) embed(ctx context.Context, texts []string, dimensions int) ([][]float64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case r.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-r.gate }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := r.embedder.Embed(ctx, texts)
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("%w: %w", ErrEmbeddingFailed, context.Canceled)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: %w", ErrEmbeddingFailed, context.DeadlineExceeded)
		}
		return nil, ErrEmbeddingFailed
	}
	if len(raw) != len(texts) {
		return nil, ErrInvalidData
	}
	result := make([][]float64, len(raw))
	for i, v := range raw {
		if dimensions == 0 {
			dimensions = len(v)
		}
		if len(v) == 0 || len(v) > MaxDimensions || len(v) != dimensions {
			return nil, ErrInvalidData
		}
		// Scale first to handle huge or subnormal finite vectors without overflow.
		var scale float64
		for _, x := range v {
			if !finite(x) {
				return nil, ErrInvalidData
			}
			scale = max(scale, math.Abs(x))
		}
		if scale == 0 {
			return nil, ErrInvalidData
		}
		var norm float64
		for _, x := range v {
			norm = math.Hypot(norm, x/scale)
		}
		result[i] = make([]float64, len(v))
		for j, x := range v {
			result[i][j] = (x / scale) / norm
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
func validText(s string, n int, multiline bool) bool {
	if strings.TrimSpace(s) == "" || !utf8.ValidString(s) || utf8.RuneCountInString(s) > n {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\t' || r == '\r')) {
			return false
		}
	}
	return true
}

var _ customerservice.KnowledgeRetriever = (*Retriever)(nil)
