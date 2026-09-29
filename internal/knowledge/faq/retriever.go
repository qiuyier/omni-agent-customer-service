// Package faq provides deterministic, local keyword retrieval. It intentionally
// does not provide vector search or semantic similarity.
package faq

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

const (
	maxEntries      = 1000
	maxIDRunes      = 128
	maxTitleRunes   = 128
	maxSourceRunes  = 256
	maxKeywords     = 20
	maxKeywordRunes = 64
)

var (
	ErrInvalidData  = errors.New("faq: invalid knowledge data")
	ErrInvalidQuery = errors.New("faq: invalid query")
)

// Entry is one curated FAQ reference. Keywords are explicit substring matches,
// not instructions to the model. Source is a reference identifier, not a URL to
// fetch: retrieval never performs network or file I/O.
type Entry struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Source   string   `json:"source"`
	Content  string   `json:"content"`
	Keywords []string `json:"keywords"`
}

type indexedEntry struct {
	snippet  customerservice.KnowledgeSnippet
	keywords []string
}

// Retriever owns an immutable copy of its entries and is safe for concurrent
// use. Construct a replacement retriever to change the dataset.
type Retriever struct {
	entries []indexedEntry
}

// New validates and copies a nonempty collection of entries. Duplicate keywords
// within one entry are normalized away, so they cannot inflate its score.
func New(entries []Entry) (*Retriever, error) {
	if len(entries) == 0 || len(entries) > maxEntries {
		return nil, fmt.Errorf("%w: entry count must be between 1 and %d", ErrInvalidData, maxEntries)
	}
	indexed := make([]indexedEntry, 0, len(entries))
	seenIDs := make(map[string]struct{}, len(entries))
	for index, entry := range entries {
		fields := []struct {
			name  string
			value string
			limit int
		}{
			{"id", entry.ID, maxIDRunes},
			{"title", entry.Title, maxTitleRunes},
			{"source", entry.Source, maxSourceRunes},
			{"content", entry.Content, customerservice.MaxKnowledgeContextRunes},
		}
		snippetRunes := 0
		for _, field := range fields {
			if !validText(field.value, field.limit, field.name == "content") {
				return nil, fmt.Errorf("%w: entry %d has invalid %s", ErrInvalidData, index+1, field.name)
			}
			snippetRunes += utf8.RuneCountInString(strings.TrimSpace(field.value))
		}
		if snippetRunes > customerservice.MaxKnowledgeContextRunes {
			// Every accepted entry must be usable as the sole search result. The
			// application uses this same combined budget, including provenance.
			return nil, fmt.Errorf("%w: entry %d exceeds the complete snippet budget", ErrInvalidData, index+1)
		}
		id := strings.TrimSpace(entry.ID)
		if _, duplicate := seenIDs[id]; duplicate {
			return nil, fmt.Errorf("%w: entry %d has a duplicate id", ErrInvalidData, index+1)
		}
		seenIDs[id] = struct{}{}
		if len(entry.Keywords) == 0 || len(entry.Keywords) > maxKeywords {
			return nil, fmt.Errorf("%w: entry %d keyword count must be between 1 and %d", ErrInvalidData, index+1, maxKeywords)
		}
		keywords := make([]string, 0, len(entry.Keywords))
		seenKeywords := make(map[string]struct{}, len(entry.Keywords))
		for _, keyword := range entry.Keywords {
			if !validText(keyword, maxKeywordRunes, false) {
				return nil, fmt.Errorf("%w: entry %d has an invalid keyword", ErrInvalidData, index+1)
			}
			normalized := strings.ToLower(strings.TrimSpace(keyword))
			if _, duplicate := seenKeywords[normalized]; duplicate {
				continue
			}
			seenKeywords[normalized] = struct{}{}
			keywords = append(keywords, normalized)
		}
		indexed = append(indexed, indexedEntry{
			snippet: customerservice.KnowledgeSnippet{
				ID:      id,
				Title:   strings.TrimSpace(entry.Title),
				Source:  strings.TrimSpace(entry.Source),
				Content: strings.TrimSpace(entry.Content),
			},
			keywords: keywords,
		})
	}
	return &Retriever{entries: indexed}, nil
}

// Retrieve ranks entries by the number of distinct matching keywords. Ties keep
// their original dataset order. Matches are case-insensitive substrings; this
// supports Chinese text without claiming language-aware tokenization. A miss is
// a successful empty result, not an error.
func (r *Retriever) Retrieve(ctx context.Context, query customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidQuery)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r == nil || r.entries == nil {
		return nil, fmt.Errorf("%w: retriever is not initialized", ErrInvalidData)
	}
	if !validText(query.Text, customerservice.MaxKnowledgeQueryRunes, true) {
		return nil, fmt.Errorf("%w: text is blank, invalid, or too long", ErrInvalidQuery)
	}
	if query.Limit < 1 || query.Limit > customerservice.MaxKnowledgeSnippets {
		return nil, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidQuery, customerservice.MaxKnowledgeSnippets)
	}
	text := strings.ToLower(strings.TrimSpace(query.Text))
	type match struct {
		index int
		score int
	}
	matches := make([]match, 0, len(r.entries))
	for index, entry := range r.entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		score := 0
		for _, keyword := range entry.keywords {
			if strings.Contains(text, keyword) {
				score++
			}
		}
		if score > 0 {
			matches = append(matches, match{index: index, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })
	count := min(query.Limit, len(matches))
	results := make([]customerservice.KnowledgeSnippet, 0, count)
	for _, match := range matches[:count] {
		results = append(results, r.entries[match.index].snippet)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func validText(value string, limit int, multiline bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit || strings.TrimSpace(value) == "" {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) && !(multiline && (char == '\n' || char == '\r' || char == '\t')) {
			return false
		}
	}
	return true
}

var _ customerservice.KnowledgeRetriever = (*Retriever)(nil)
