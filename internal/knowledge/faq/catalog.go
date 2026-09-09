package faq

import "github.com/qiuyier/omni-agent-customer-service/internal/customerservice"

// ReferenceIDs returns an owned copy in dataset order for offline label
// validation. It does not expose reference content or change retrieval ranking.
func (r *Retriever) ReferenceIDs() []string {
	ids := make([]string, len(r.entries))
	for i, entry := range r.entries {
		ids[i] = entry.snippet.ID
	}
	return ids
}

// Snippets returns an owned copy of validated references, without search keywords.
func (r *Retriever) Snippets() []customerservice.KnowledgeSnippet {
	result := make([]customerservice.KnowledgeSnippet, len(r.entries))
	for i, entry := range r.entries {
		result[i] = entry.snippet
	}
	return result
}
