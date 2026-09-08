// Package evaluation measures retrieval against human-labelled relevance sets.
// It evaluates the retrieval port only, not prompt budgets or generated answers.
package evaluation

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

// Case labels relevant references, not references guaranteed to fully answer a
// query. ExpectedIDs must be explicitly [] when no reference is relevant.
type Case struct {
	ID          string   `json:"id"`
	Query       string   `json:"query"`
	ExpectedIDs []string `json:"expected_ids"`
	Note        string   `json:"note,omitempty"`
}

// Metric exposes its denominator. Rate is null when Total is zero, not a
// misleading zero-percent success rate for a category with no examples.
type Metric struct {
	Count int      `json:"count"`
	Total int      `json:"total"`
	Rate  *float64 `json:"rate"`
}

type Summary struct {
	Cases           int    `json:"cases"`
	Top1            Metric `json:"top1"`
	NoMatch         Metric `json:"no_match"`
	ExactSet        Metric `json:"exact_set"`
	MissingCases    int    `json:"missing_cases"`
	UnexpectedCases int    `json:"unexpected_cases"`
}

type Result struct {
	Case
	ActualIDs     []string `json:"actual_ids"`
	MissingIDs    []string `json:"missing_ids"`
	UnexpectedIDs []string `json:"unexpected_ids"`
	Top1Hit       *bool    `json:"top1_hit"`
	ExactSetMatch bool     `json:"exact_set_match"`
}

type Report struct {
	SchemaVersion int      `json:"schema_version"`
	TopK          int      `json:"top_k"`
	Summary       Summary  `json:"summary"`
	Results       []Result `json:"results"`
}

// Run is sequential and deterministic: case order and retriever ranking are
// preserved. Retrieval failures abort the run; they are never scored as misses.
func Run(ctx context.Context, retriever customerservice.KnowledgeRetriever, cases []Case, topK int) (Report, error) {
	if ctx == nil || retriever == nil {
		return Report{}, fmt.Errorf("evaluation: context and retriever are required")
	}
	if err := validateCases(cases); err != nil {
		return Report{}, err
	}
	if topK < 1 || topK > customerservice.MaxKnowledgeSnippets {
		return Report{}, fmt.Errorf("evaluation: top-k must be between 1 and %d", customerservice.MaxKnowledgeSnippets)
	}
	report := Report{SchemaVersion: 1, TopK: topK, Results: make([]Result, 0, len(cases))}
	for index, c := range cases {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		queryCtx, cancel := context.WithTimeout(ctx, customerservice.DefaultKnowledgeRetrievalTimeout)
		snippets, err := retriever.Retrieve(queryCtx, customerservice.KnowledgeQuery{Text: c.Query, Limit: topK})
		if contextErr := queryCtx.Err(); contextErr != nil {
			err = contextErr
		}
		cancel()
		if err != nil {
			return Report{}, fmt.Errorf("evaluation: case %d retrieval failed: %w", index+1, err)
		}
		if len(snippets) > topK {
			return Report{}, fmt.Errorf("evaluation: case %d returned more than top-k results", index+1)
		}
		// Own the label slice so report consumers cannot mutate the input data.
		c.ExpectedIDs = append([]string{}, c.ExpectedIDs...)
		result := Result{Case: c, ActualIDs: []string{}, MissingIDs: []string{}, UnexpectedIDs: []string{}}
		expected := make(map[string]bool, len(c.ExpectedIDs))
		for _, id := range c.ExpectedIDs {
			expected[id] = true
		}
		actual := make(map[string]bool, len(snippets))
		for _, snippet := range snippets {
			if !validText(snippet.ID, 128, false) || snippet.ID != strings.TrimSpace(snippet.ID) || actual[snippet.ID] {
				return Report{}, fmt.Errorf("evaluation: case %d returned an invalid or duplicate reference ID", index+1)
			}
			actual[snippet.ID] = true
			result.ActualIDs = append(result.ActualIDs, snippet.ID)
			if !expected[snippet.ID] {
				result.UnexpectedIDs = append(result.UnexpectedIDs, snippet.ID)
			}
		}
		for _, id := range c.ExpectedIDs {
			if !actual[id] {
				result.MissingIDs = append(result.MissingIDs, id)
			}
		}
		if len(expected) > 0 {
			hit := len(result.ActualIDs) > 0 && expected[result.ActualIDs[0]]
			result.Top1Hit = &hit
			report.Summary.Top1.Total++
			if hit {
				report.Summary.Top1.Count++
			}
		} else {
			report.Summary.NoMatch.Total++
			if len(actual) == 0 {
				report.Summary.NoMatch.Count++
			}
		}
		if len(result.MissingIDs) > 0 {
			report.Summary.MissingCases++
		}
		if len(result.UnexpectedIDs) > 0 {
			report.Summary.UnexpectedCases++
		}
		result.ExactSetMatch = len(result.MissingIDs) == 0 && len(result.UnexpectedIDs) == 0
		if result.ExactSetMatch {
			report.Summary.ExactSet.Count++
		}
		report.Results = append(report.Results, result)
	}
	report.Summary.Cases = len(cases)
	report.Summary.ExactSet.Total = len(cases)
	for _, metric := range []*Metric{&report.Summary.Top1, &report.Summary.NoMatch, &report.Summary.ExactSet} {
		if metric.Total > 0 {
			rate := float64(metric.Count) / float64(metric.Total)
			metric.Rate = &rate
		}
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	return report, nil
}

func validateCases(cases []Case) error {
	if len(cases) < 1 || len(cases) > 1000 {
		return fmt.Errorf("evaluation: cases must contain between 1 and 1000 items")
	}
	seen := make(map[string]bool, len(cases))
	for index, c := range cases {
		if !validText(c.ID, 128, false) || c.ID != strings.TrimSpace(c.ID) || seen[c.ID] ||
			!validText(c.Query, customerservice.MaxKnowledgeQueryRunes, true) ||
			(c.Note != "" && !validText(c.Note, 1024, true)) || c.ExpectedIDs == nil || len(c.ExpectedIDs) > 1000 {
			return fmt.Errorf("evaluation: case %d has invalid fields or duplicate ID", index+1)
		}
		seen[c.ID] = true
		labels := make(map[string]bool, len(c.ExpectedIDs))
		for _, id := range c.ExpectedIDs {
			if !validText(id, 128, false) || id != strings.TrimSpace(id) || labels[id] {
				return fmt.Errorf("evaluation: case %d has invalid or duplicate expected IDs", index+1)
			}
			labels[id] = true
		}
	}
	return nil
}

func validText(value string, limit int, multiline bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > limit || strings.TrimSpace(value) == "" {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\r' || r == '\t')) {
			return false
		}
	}
	return true
}

// DefaultTimeout bounds an entire local evaluation, independent of model timeouts.
const DefaultTimeout = 30 * time.Second
