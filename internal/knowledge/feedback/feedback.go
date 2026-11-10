// Package feedback collects only sanitized, unmatched knowledge queries.
package feedback

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

const Retention = 7 * 24 * time.Hour

// Sink must not log text or keep it beyond the configured retention period.
type Sink interface {
	RecordFeedback(context.Context, string) error
}

var patterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:https?://|www\.)[^\s<>]+`),
	regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`),
	regexp.MustCompile(`(?i)(?:bearer\s+|sk-|api[_-]?key\s*[:=]\s*|password\s*[:=]\s*|token\s*[:=]\s*)[^\s,;，；]+`),
	regexp.MustCompile(`(?:密码|口令|密钥|令牌)\s*[:：=]?\s*[^\s,;，；]+`),
	regexp.MustCompile(`[0-9][0-9 ()+\-]{5,}[0-9Xx]?`),
}

// Sanitize is a best-effort rule filter, not a guarantee of anonymization.
// Names, addresses and other free-form secrets may remain. Operators must
// explicitly opt in and disclose that risk before enabling collection.
func Sanitize(text string) string {
	if !utf8.ValidString(text) || len(text) > 8192 || strings.ContainsFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) {
		return ""
	}
	for _, p := range patterns {
		text = p.ReplaceAllString(text, "[已脱敏]")
	}
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > 512 {
		text = string(runes[:512])
	}
	return text
}

// Wrap records only a successful miss. Sink failure must not turn a successful
// retrieval into an error. Callers install it AFTER startup readiness checks;
// administrator trial/batch search must use the original retriever.
func Wrap(next customerservice.KnowledgeRetriever, sink Sink) customerservice.KnowledgeRetriever {
	return WrapMonitored(next, sink, nil)
}
func WrapMonitored(next customerservice.KnowledgeRetriever, sink Sink, m *Monitor) customerservice.KnowledgeRetriever {
	return customerservice.KnowledgeRetrieverFunc(func(ctx context.Context, q customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
		hits, err := next.Retrieve(ctx, q)
		// Keep a full capture budget plus headroom for the caller's post-retrieval
		// deadline check. Near-deadline misses are deliberately not collected.
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < 200*time.Millisecond {
			if err == nil && len(hits) == 0 {
				m.skip()
			}
			return hits, err
		}
		if err == nil && len(hits) == 0 && sink != nil && ctx.Err() == nil {
			if text := Sanitize(q.Text); text != "" {
				bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
				m.record(sink.RecordFeedback(bounded, text))
				cancel()
			} else {
				m.skip()
			}
		}
		return hits, err
	})
}
