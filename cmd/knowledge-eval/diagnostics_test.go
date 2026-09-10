package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/evaluation"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/semantic"
)

func TestRunDiagnosticsBoundedSameCallsAndSameReport(t *testing.T) {
	var calls atomic.Int32
	var failQueries atomic.Bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if failQueries.Load() && n > 2 {
			http.Error(w, "private provider payload", http.StatusBadRequest)
			return
		}
		var body struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		data := make([]map[string]any, len(body.Input))
		for i := range data {
			data[i] = map[string]any{"index": i, "embedding": []float64{1, 0}}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"data": data}); err != nil {
			t.Error(err)
		}
	}))
	defer s.Close()
	t.Setenv("EMBEDDING_BASE_URL", s.URL+"/v1")
	t.Setenv("EMBEDDING_API_KEY", "private-diagnostic-key")
	t.Setenv("EMBEDDING_MODEL", "test-vectors")
	t.Setenv("EMBEDDING_BATCH_SIZE", "2")
	t.Setenv("KNOWLEDGE_MIN_SCORE", "0.8")
	base := append(exampleArgs(), "-retriever", "semantic", "-format", "json", "-allow-embedding-api", "-top-k", "1")
	for _, suffix := range [][]string{
		{"-diagnostics", "-retriever", "keyword"},
		{"-diagnostics", "-format", "text"},
		{"-diagnostics", "-allow-embedding-api=false"},
	} {
		var out bytes.Buffer
		if err := run(context.Background(), append(append([]string{}, base...), suffix...), &out, io.Discard); err == nil || calls.Load() != 0 || out.Len() != 0 {
			t.Fatalf("invalid flags reached network/report: %v", suffix)
		}
	}
	tooMany := make([]evaluation.Case, maxDiagnosticCases+1)
	for i := range tooMany {
		tooMany[i] = evaluation.Case{ID: fmt.Sprintf("case-%d", i), Query: "question", ExpectedIDs: []string{}}
	}
	data, err := json.Marshal(tooMany)
	if err != nil {
		t.Fatal(err)
	}
	casePath := filepath.Join(t.TempDir(), "cases.json")
	if err := os.WriteFile(casePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := run(context.Background(), append(append([]string{}, base...), "-diagnostics", "-cases", casePath), &out, io.Discard); err == nil || !strings.Contains(err.Error(), "at most 20") || calls.Load() != 0 || out.Len() != 0 {
		t.Fatal("case cap not enforced before network/report", err)
	}
	if err := run(context.Background(), base, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var plain evaluation.Report
	if err := json.Unmarshal(out.Bytes(), &plain); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out.Bytes(), []byte(`"diagnostics"`)) || calls.Load() != 22 {
		t.Fatal("default report/call count changed", calls.Load())
	}
	out.Reset()
	args := append(append([]string{}, base...), "-diagnostics")
	if err := run(context.Background(), args, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var report diagnosticReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Report, plain) || calls.Load() != 44 || len(report.Diagnostics) != 20 {
		t.Fatal("diagnostics altered results or issued additional query embeddings", calls.Load())
	}
	for i, d := range report.Diagnostics {
		if d.CaseID != report.Results[i].ID || len(d.Candidates) != 3 {
			t.Fatalf("case alignment: %+v", d)
		}
		for j, c := range d.Candidates {
			want := semantic.OutsideTopK
			if j == 0 {
				want = semantic.Selected
			}
			if c.Score != 1 || c.Rank != j+1 || c.Decision != want {
				t.Fatalf("candidate=%+v", c)
			}
		}
		if d.Candidates[0].ReferenceID != report.Results[i].ActualIDs[0] {
			t.Fatal("selected candidate differs from actual result")
		}
	}
	for _, private := range []string{"private-diagnostic-key", s.URL, `"embedding":`, `"content":`, `"vector":`} {
		if strings.Contains(out.String(), private) {
			t.Fatal("diagnostic output leaks extra data")
		}
	}
	if err := run(context.Background(), args, failingWriter{}, io.Discard); !errors.Is(err, errOutput) {
		t.Fatal("output failure lost", err)
	}
	calls.Store(0)
	failQueries.Store(true)
	out.Reset()
	err = run(context.Background(), args, &out, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "case 1 retrieval failed") || strings.Contains(err.Error(), "private") || out.Len() != 0 {
		t.Fatal("failed retrieval produced diagnostics or private errors", err)
	}
}
