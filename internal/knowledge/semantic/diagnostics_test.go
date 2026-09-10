package semantic

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

func TestDiagnosticsPreserveSelectionAndUseOneQueryEmbedding(t *testing.T) {
	for _, tt := range []struct {
		name      string
		threshold float64
		limit     int
		decisions []string
	}{
		{"top-k-and-threshold", 0, 1, []string{Selected, OutsideTopK, OutsideTopK, BelowMinScore}},
		{"inclusive-zero", 0, 3, []string{Selected, Selected, Selected, BelowMinScore}},
		{"inclusive-one", 1, 3, []string{Selected, Selected, BelowMinScore, BelowMinScore}},
		{"all-below", 0.5, 3, []string{BelowMinScore, BelowMinScore, BelowMinScore, BelowMinScore}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			e := embedFunc(func(_ context.Context, texts []string) ([][]float64, error) {
				calls.Add(1)
				if len(texts) == 4 {
					return [][]float64{{0, 1}, {1, 0}, {2, 0}, {-1, 0}}, nil
				}
				if len(texts) != 1 || texts[0] != "question" {
					t.Errorf("query inputs = %q", texts)
				}
				if tt.name == "all-below" {
					return [][]float64{{0, -1}}, nil
				}
				return [][]float64{{1, 0}}, nil
			})
			r, err := New(context.Background(), []customerservice.KnowledgeSnippet{doc("zero"), doc("tie-first"), doc("tie-second"), doc("negative")}, e, Config{MinScore: tt.threshold})
			if err != nil {
				t.Fatal(err)
			}
			q := customerservice.KnowledgeQuery{Text: "  question  ", Limit: tt.limit}
			plain, err := r.Retrieve(context.Background(), q)
			if err != nil {
				t.Fatal(err)
			}
			got, candidates, err := r.RetrieveWithDiagnostics(context.Background(), q)
			if err != nil || !reflect.DeepEqual(got, plain) || calls.Load() != 3 {
				t.Fatalf("plain=%v diagnostic=%v calls=%d err=%v", plain, got, calls.Load(), err)
			}
			ids := []string{"tie-first", "tie-second", "zero", "negative"}
			scores := []float64{1, 1, 0, -1}
			if tt.name == "all-below" {
				ids = []string{"tie-first", "tie-second", "negative", "zero"}
				scores = []float64{0, 0, 0, -1}
			}
			if len(candidates) != len(ids) {
				t.Fatal(candidates)
			}
			var selected []string
			for i, c := range candidates {
				want := Candidate{ReferenceID: ids[i], Score: scores[i], Rank: i + 1, Decision: tt.decisions[i]}
				if c != want {
					t.Fatalf("candidate %d = %+v, want %+v", i, c, want)
				}
				if c.Decision == Selected {
					selected = append(selected, c.ReferenceID)
				}
			}
			if len(selected) != len(got) {
				t.Fatal("diagnostic selection count differs")
			}
			for i, id := range selected {
				if id != got[i].ID {
					t.Fatal("diagnostic selection differs")
				}
			}
			wantCandidates := append([]Candidate(nil), candidates...)
			candidates[0].ReferenceID = "changed"
			if len(got) > 0 {
				got[0].Content = "changed"
			}
			// All snapshots are per-call owned; shared index state stays immutable.
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					result, snapshot, err := r.RetrieveWithDiagnostics(context.Background(), q)
					if err != nil || !reflect.DeepEqual(result, plain) || !reflect.DeepEqual(snapshot, wantCandidates) {
						t.Errorf("unstable diagnostic snapshot: %v %v %v", result, snapshot, err)
					}
				}()
			}
			wg.Wait()
			if calls.Load() != 11 {
				t.Fatalf("calls=%d; want one index plus ten query calls", calls.Load())
			}
		})
	}
}

func TestDiagnosticsErrorsHaveNoPartialSnapshot(t *testing.T) {
	q := customerservice.KnowledgeQuery{Text: "question", Limit: 1}
	check := func(r *Retriever, ctx context.Context, query customerservice.KnowledgeQuery, want error) {
		t.Helper()
		result, candidates, err := r.RetrieveWithDiagnostics(ctx, query)
		if !errors.Is(err, want) || result != nil || candidates != nil {
			t.Fatalf("result=%v candidates=%v err=%v, want %v", result, candidates, err, want)
		}
		if strings.Contains(err.Error(), "private") {
			t.Fatal("provider details escaped")
		}
	}
	check(nil, context.Background(), q, ErrInvalidConfig)
	r, err := New(context.Background(), []customerservice.KnowledgeSnippet{doc("a")}, embedFunc(func(context.Context, []string) ([][]float64, error) { return [][]float64{{1, 0}}, nil }), Config{})
	if err != nil {
		t.Fatal(err)
	}
	check(r, nil, q, ErrInvalidQuery)
	check(r, context.Background(), customerservice.KnowledgeQuery{Text: "q", Limit: 0}, ErrInvalidQuery)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	check(r, ctx, q, context.Canceled)
	r.embedder = embedFunc(func(context.Context, []string) ([][]float64, error) {
		return nil, errors.New("private provider payload")
	})
	check(r, context.Background(), q, ErrEmbeddingFailed)
	r.embedder = embedFunc(func(context.Context, []string) ([][]float64, error) { return [][]float64{{1}}, nil })
	check(r, context.Background(), q, ErrInvalidData)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	r.embedder = embedFunc(func(context.Context, []string) ([][]float64, error) { cancel(); return [][]float64{{1, 0}}, nil })
	check(r, ctx, q, context.Canceled)
}
