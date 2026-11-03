package faq

import (
	"context"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"strings"
)

type ExplainedMatch struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Content  string   `json:"content"`
	Source   string   `json:"source"`
	Keywords []string `json:"keywords"`
}

// Explain preserves Retrieve ranking and adds distinct normalized matched keywords.
func (r *Retriever) Explain(ctx context.Context, q customerservice.KnowledgeQuery) ([]ExplainedMatch, error) {
	snippets, err := r.Retrieve(ctx, q)
	if err != nil {
		return nil, err
	}
	results := make([]ExplainedMatch, 0, len(snippets))
	text := strings.ToLower(strings.TrimSpace(q.Text))
	for _, s := range snippets {
		words := []string{}
		for _, entry := range r.entries {
			if entry.snippet.ID == s.ID {
				for _, word := range entry.keywords {
					if strings.Contains(text, word) {
						words = append(words, word)
					}
				}
				break
			}
		}
		results = append(results, ExplainedMatch{s.ID, s.Title, s.Content, s.Source, words})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}
