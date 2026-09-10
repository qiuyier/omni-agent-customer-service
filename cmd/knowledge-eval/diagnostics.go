package main

import (
	"context"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/evaluation"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/semantic"
)

const maxDiagnosticCases = 20

type diagnosticIndex interface {
	RetrieveWithDiagnostics(context.Context, customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, []semantic.Candidate, error)
}

// evaluation.RunWithTimeout calls this wrapper sequentially. Diagnostics are
// attached to that same retrieval, never obtained by issuing a second query.
type diagnosticRetriever struct {
	index     diagnosticIndex
	snapshots [][]semantic.Candidate
}

func (r *diagnosticRetriever) Retrieve(ctx context.Context, q customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
	result, candidates, err := r.index.RetrieveWithDiagnostics(ctx, q)
	if err != nil {
		return nil, err
	}
	r.snapshots = append(r.snapshots, candidates)
	return result, nil
}

type caseDiagnostics struct {
	CaseID     string               `json:"case_id"`
	Candidates []semantic.Candidate `json:"candidates"`
}

// Keep evaluation's provider-independent report and default JSON unchanged.
type diagnosticReport struct {
	evaluation.Report
	Diagnostics []caseDiagnostics `json:"diagnostics"`
}
