package semantic

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

type embedFunc func(context.Context, []string) ([][]float64, error)

func (f embedFunc) Embed(c context.Context, s []string) ([][]float64, error) { return f(c, s) }
func doc(id string) customerservice.KnowledgeSnippet {
	return customerservice.KnowledgeSnippet{ID: id, Title: id, Source: "demo", Content: "fictional"}
}

func TestRankingThresholdAndImmutableIndex(t *testing.T) {
	vs := [][]float64{{1, 0}, {0, 2}, {0, 3}}
	calls := 0
	e := embedFunc(func(_ context.Context, texts []string) ([][]float64, error) {
		calls++
		if calls == 1 {
			if texts[0] != "a\nfictional" {
				t.Fatal(texts)
			}
			return vs, nil
		}
		return [][]float64{{0, 9}}, nil
	})
	docs := []customerservice.KnowledgeSnippet{doc("a"), doc("b"), doc("c")}
	r, err := New(context.Background(), docs, e, Config{MinScore: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	docs[1].Content = "changed"
	vs[1][1] = 0
	got, err := r.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: "question", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "c" || got[0].Content != "fictional" || r.Dimensions() != 2 {
		t.Fatal(got)
	}
	got[0].Content = "again"
	got, err = r.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: "question", Limit: 1})
	if err != nil || len(got) != 1 || got[0].Content != "fictional" {
		t.Fatal(got, err)
	}
	miss := embedFunc(func(context.Context, []string) ([][]float64, error) { return [][]float64{{-1, 0}}, nil })
	r.embedder = miss
	got, err = r.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: "question", Limit: 3})
	if err != nil || got == nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}

func TestVectorAndInputValidation(t *testing.T) {
	for _, v := range [][]float64{nil, {}, {0, 0}, {math.NaN(), 1}, {math.Inf(1), 1}, make([]float64, MaxDimensions+1)} {
		e := embedFunc(func(context.Context, []string) ([][]float64, error) { return [][]float64{v}, nil })
		if _, err := New(context.Background(), []customerservice.KnowledgeSnippet{doc("a")}, e, Config{}); !errors.Is(err, ErrInvalidData) {
			t.Fatalf("vector error %v", err)
		}
	}
	e := embedFunc(func(_ context.Context, texts []string) ([][]float64, error) {
		v := make([][]float64, len(texts))
		for i := range v {
			v[i] = []float64{math.MaxFloat64, math.MaxFloat64}
		}
		return v, nil
	})
	r, err := New(context.Background(), []customerservice.KnowledgeSnippet{doc("a")}, e, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: "q", Limit: 1}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Config{{MinScore: -2}, {MinScore: 2}, {MinScore: math.NaN()}} {
		if _, err := New(context.Background(), []customerservice.KnowledgeSnippet{doc("a")}, e, c); !errors.Is(err, ErrInvalidConfig) {
			t.Fatal(err)
		}
	}
	for _, docs := range [][]customerservice.KnowledgeSnippet{nil, {doc("a"), doc("a")}, {doc("")}} {
		if _, err := New(context.Background(), docs, e, Config{}); err == nil {
			t.Fatal("bad documents accepted")
		}
	}
	for _, q := range []customerservice.KnowledgeQuery{{Text: "", Limit: 1}, {Text: "q", Limit: 0}, {Text: "q", Limit: 4}, {Text: strings.Repeat("中", 513), Limit: 1}} {
		if _, err := r.Retrieve(context.Background(), q); !errors.Is(err, ErrInvalidQuery) {
			t.Fatal(err)
		}
	}
	r.embedder = embedFunc(func(context.Context, []string) ([][]float64, error) { return [][]float64{{1}}, nil })
	if _, err := r.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: "q", Limit: 1}); !errors.Is(err, ErrInvalidData) {
		t.Fatal(err)
	}
}

func TestConfigurableIndexBatches(t *testing.T) {
	for _, batchSize := range []int{0, 1, 2, 10, 16} {
		t.Run(fmt.Sprint(batchSize), func(t *testing.T) {
			docs := make([]customerservice.KnowledgeSnippet, 33)
			wantTexts := make([]string, len(docs))
			for i := range docs {
				docs[i] = doc(fmt.Sprint(i))
				wantTexts[i] = DocumentText(docs[i])
			}
			var batches []int
			var gotTexts []string
			e := embedFunc(func(_ context.Context, texts []string) ([][]float64, error) {
				batches = append(batches, len(texts))
				gotTexts = append(gotTexts, texts...)
				vectors := make([][]float64, len(texts))
				for i := range vectors {
					vectors[i] = []float64{1, 0}
				}
				return vectors, nil
			})
			r, err := New(context.Background(), docs, e, Config{BatchSize: batchSize})
			if err != nil {
				t.Fatal(err)
			}
			effective := batchSize
			if effective == 0 {
				effective = 16
			}
			var wantBatches []int
			for remaining := len(docs); remaining > 0; remaining -= effective {
				wantBatches = append(wantBatches, min(remaining, effective))
			}
			if !slices.Equal(batches, wantBatches) || !slices.Equal(gotTexts, wantTexts) {
				t.Fatalf("batches=%v want=%v, text order preserved=%t", batches, wantBatches, slices.Equal(gotTexts, wantTexts))
			}
			if _, err := r.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: "query", Limit: 1}); err != nil {
				t.Fatal(err)
			}
			if len(batches) != len(wantBatches)+1 || batches[len(batches)-1] != 1 || gotTexts[len(gotTexts)-1] != "query" {
				t.Fatal("query must embed only itself", batches)
			}
		})
	}
}

func TestInvalidBatchSizeFailsBeforeEmbedding(t *testing.T) {
	calls := 0
	e := embedFunc(func(context.Context, []string) ([][]float64, error) {
		calls++
		return nil, nil
	})
	for _, batchSize := range []int{-1, 17} {
		if _, err := New(context.Background(), []customerservice.KnowledgeSnippet{doc("a")}, e, Config{BatchSize: batchSize}); !errors.Is(err, ErrInvalidConfig) {
			t.Fatal(batchSize, err)
		}
	}
	if calls != 0 {
		t.Fatal("invalid batch size called embedder")
	}
}

func TestBatchingConcurrentCallsAndCancellation(t *testing.T) {
	docs := make([]customerservice.KnowledgeSnippet, 33)
	for i := range docs {
		docs[i] = doc(fmt.Sprint(i))
	}
	var batches []int
	e := embedFunc(func(_ context.Context, texts []string) ([][]float64, error) {
		batches = append(batches, len(texts))
		v := make([][]float64, len(texts))
		for i := range v {
			v[i] = []float64{1, 0}
		}
		return v, nil
	})
	r, err := New(context.Background(), docs, e, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(batches) != "[16 16 1]" {
		t.Fatal(batches)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: "q", Limit: 1}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Retrieve(ctx, customerservice.KnowledgeQuery{Text: "q", Limit: 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	r.embedder = embedFunc(func(context.Context, []string) ([][]float64, error) {
		return nil, errors.New("secret provider payload")
	})
	if _, err := r.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: "q", Limit: 1}); !errors.Is(err, ErrEmbeddingFailed) || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
}
