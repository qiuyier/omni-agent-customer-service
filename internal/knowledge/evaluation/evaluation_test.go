package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

type retrieveFunc func(context.Context, customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error)

func (f retrieveFunc) Retrieve(ctx context.Context, q customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
	return f(ctx, q)
}

func snippets(ids ...string) []customerservice.KnowledgeSnippet {
	result := make([]customerservice.KnowledgeSnippet, len(ids))
	for i, id := range ids {
		result[i].ID = id
	}
	return result
}

func TestRunMetricsAndDifferences(t *testing.T) {
	cases := []Case{
		{ID: "hit", Query: "hit", ExpectedIDs: []string{"a"}},
		{ID: "rank", Query: "rank", ExpectedIDs: []string{"b"}},
		{ID: "miss", Query: "miss", ExpectedIDs: []string{"c"}},
		{ID: "false-positive", Query: "false-positive", ExpectedIDs: []string{}},
		{ID: "empty", Query: "empty", ExpectedIDs: []string{}},
	}
	answers := map[string][]customerservice.KnowledgeSnippet{
		"hit": snippets("a"), "rank": snippets("a", "b"), "false-positive": snippets("a"),
	}
	calls := 0
	r := retrieveFunc(func(ctx context.Context, q customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
		if q.Limit != 3 || q.Text != cases[calls].Query {
			t.Fatalf("query = %+v", q)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing query deadline")
		}
		calls++
		return answers[q.Text], nil
	})
	report, err := Run(context.Background(), r, cases, 3)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 1 || report.TopK != 3 || calls != 5 || report.Summary.Cases != 5 {
		t.Fatalf("report metadata = %+v, calls = %d", report.Summary, calls)
	}
	assertMetric(t, report.Summary.Top1, 1, 3)
	assertMetric(t, report.Summary.NoMatch, 1, 2)
	assertMetric(t, report.Summary.ExactSet, 2, 5)
	if report.Summary.MissingCases != 1 || report.Summary.UnexpectedCases != 2 {
		t.Fatalf("summary = %+v", report.Summary)
	}
	if !reflect.DeepEqual(report.Results[1].ActualIDs, []string{"a", "b"}) ||
		!reflect.DeepEqual(report.Results[1].UnexpectedIDs, []string{"a"}) ||
		!reflect.DeepEqual(report.Results[2].MissingIDs, []string{"c"}) ||
		report.Results[3].Top1Hit != nil || *report.Results[1].Top1Hit {
		t.Fatalf("wrong per-case differences: %+v", report.Results)
	}
	for _, result := range report.Results {
		if result.ExpectedIDs == nil || result.ActualIDs == nil || result.MissingIDs == nil || result.UnexpectedIDs == nil {
			t.Fatal("empty collections must serialize as [], not null")
		}
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report, decoded) {
		t.Fatal("JSON round trip lost report fields, label arrays or metric values")
	}
	report.Results[0].ExpectedIDs[0] = "changed"
	if cases[0].ExpectedIDs[0] != "a" {
		t.Fatal("report aliases input labels")
	}
}

func assertMetric(t *testing.T, got Metric, count, total int) {
	t.Helper()
	if got.Count != count || got.Total != total {
		t.Fatalf("metric = %+v, want %d/%d", got, count, total)
	}
	if total == 0 {
		if got.Rate != nil {
			t.Fatal("zero denominator must have null rate")
		}
	} else if got.Rate == nil || *got.Rate != float64(count)/float64(total) {
		t.Fatalf("rate = %v", got.Rate)
	}
}

func TestRunEmptyCategoriesAndUnorderedLabels(t *testing.T) {
	r := retrieveFunc(func(context.Context, customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
		return snippets("b", "a"), nil
	})
	report, err := Run(context.Background(), r, []Case{{ID: "multi", Query: "multi", ExpectedIDs: []string{"a", "b"}}}, 3)
	if err != nil {
		t.Fatal(err)
	}
	assertMetric(t, report.Summary.Top1, 1, 1)
	assertMetric(t, report.Summary.NoMatch, 0, 0)
	assertMetric(t, report.Summary.ExactSet, 1, 1)
	r = func(context.Context, customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
		return nil, nil
	}
	report, err = Run(context.Background(), r, []Case{{ID: "empty", Query: "empty", ExpectedIDs: []string{}}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	assertMetric(t, report.Summary.Top1, 0, 0)
	assertMetric(t, report.Summary.NoMatch, 1, 1)
}

func TestRunFailuresAreNotMisses(t *testing.T) {
	sentinel := errors.New("retriever failure")
	cases := []Case{{ID: "one", Query: "question", ExpectedIDs: []string{"a"}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := retrieveFunc(func(context.Context, customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
		return nil, sentinel
	})
	for _, test := range []struct {
		name string
		ctx  context.Context
		r    customerservice.KnowledgeRetriever
		k    int
		want error
	}{
		{"retrieval", context.Background(), r, 3, sentinel},
		{"cancelled", ctx, r, 3, context.Canceled},
		{"nil context", nil, r, 3, nil},
		{"nil retriever", context.Background(), nil, 3, nil},
		{"zero k", context.Background(), r, 0, nil},
		{"large k", context.Background(), r, 4, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			report, err := Run(test.ctx, test.r, cases, test.k)
			if err == nil || (test.want != nil && !errors.Is(err, test.want)) || report.Summary.Cases != 0 {
				t.Fatalf("report = %+v, error = %v", report, err)
			}
		})
	}
	for _, ids := range [][]string{{"a", "a"}, {""}, {" a "}, {"a", "b", "c", "d"}} {
		r := retrieveFunc(func(context.Context, customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
			return snippets(ids...), nil
		})
		if _, err := Run(context.Background(), r, cases, 3); err == nil {
			t.Fatalf("invalid retrieval IDs accepted: %q", ids)
		}
	}
	ctx, cancel = context.WithCancel(context.Background())
	r = func(context.Context, customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
		cancel()
		return snippets("a"), nil
	}
	if _, err := Run(ctx, r, cases, 3); !errors.Is(err, context.Canceled) {
		t.Fatalf("late cancellation = %v", err)
	}
}

func TestRunTopKBudgetAndOverlappingDifferences(t *testing.T) {
	cases := []Case{{ID: "multi", Query: "multi", ExpectedIDs: []string{"a", "b"}}}
	for _, test := range []struct {
		id         string
		top1       bool
		unexpected int
	}{
		{"a", true, 0},
		{"irrelevant", false, 1},
	} {
		r := retrieveFunc(func(context.Context, customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
			return snippets(test.id), nil
		})
		report, err := Run(context.Background(), r, cases, 1)
		if err != nil {
			t.Fatal(err)
		}
		if *report.Results[0].Top1Hit != test.top1 || report.Summary.MissingCases != 1 || report.Summary.UnexpectedCases != test.unexpected || report.Summary.ExactSet.Count != 0 {
			t.Fatalf("top-k summary = %+v", report.Summary)
		}
	}
}
