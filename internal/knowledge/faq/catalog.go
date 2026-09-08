package faq

// ReferenceIDs returns an owned copy in dataset order for offline label
// validation. It does not expose reference content or change retrieval ranking.
func (r *Retriever) ReferenceIDs() []string {
	ids := make([]string, len(r.entries))
	for i, entry := range r.entries {
		ids[i] = entry.snippet.ID
	}
	return ids
}
