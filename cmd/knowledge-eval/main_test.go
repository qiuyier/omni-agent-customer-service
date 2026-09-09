package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/evaluation"
)

func exampleArgs() []string {
	return []string{"-faq", "../../examples/knowledge/faq.json", "-cases", "../../examples/knowledge/eval_cases.json"}
}

func TestRunOfflineReport(t *testing.T) {
	// Even deliberately invalid model configuration must be irrelevant here.
	t.Setenv("AI_PROVIDER", "not-a-provider")
	t.Setenv("AI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("KNOWLEDGE_RETRIEVER", "semantic")
	t.Setenv("EMBEDDING_BASE_URL", "http://invalid.invalid")
	t.Setenv("EMBEDDING_BATCH_SIZE", "not-a-number")
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			var out, stderr bytes.Buffer
			if err := run(context.Background(), append(exampleArgs(), "-format", format), &out, &stderr); err != nil {
				t.Fatal(err)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %s", stderr.String())
			}
			if format == "json" {
				var report evaluation.Report
				if err := json.Unmarshal(out.Bytes(), &report); err != nil {
					t.Fatal(err)
				}
				if report.TopK != 3 || report.Summary.Cases != 20 || len(report.Results) != 20 {
					t.Fatalf("report = %+v", report.Summary)
				}
			} else if !strings.Contains(out.String(), "首条命中率") || !strings.Contains(out.String(), "paraphrase_password") {
				t.Fatalf("incomplete text report: %s", out.String())
			}
		})
	}
}

func TestSemanticEvaluationRequiresOptInAndRecordsConfiguration(t *testing.T) {
	var calls atomic.Int32
	batches := make(chan int, 32)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		select {
		case batches <- len(body.Input):
		default:
			t.Error("unexpected extra embedding request")
		}
		data := make([]map[string]any, len(body.Input))
		for i := range data {
			data[i] = map[string]any{"index": i, "embedding": []float64{1, 0}}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer s.Close()
	t.Setenv("EMBEDDING_BASE_URL", s.URL+"/v1")
	t.Setenv("EMBEDDING_API_KEY", "private-test-key")
	t.Setenv("EMBEDDING_MODEL", "test-vectors")
	t.Setenv("EMBEDDING_BATCH_SIZE", "2")
	t.Setenv("KNOWLEDGE_MIN_SCORE", "0.8")
	args := append(exampleArgs(), "-retriever", "semantic", "-format", "json")
	var out bytes.Buffer
	if err := run(context.Background(), args, &out, io.Discard); err == nil || calls.Load() != 0 || out.Len() != 0 {
		t.Fatal("semantic call without opt-in")
	}
	t.Setenv("EMBEDDING_BATCH_SIZE", "0")
	if err := run(context.Background(), append(args, "-allow-embedding-api"), &out, io.Discard); err == nil || calls.Load() != 0 || out.Len() != 0 {
		t.Fatal("invalid batch size must fail before network or reporting")
	}
	t.Setenv("EMBEDDING_BATCH_SIZE", "2")
	if err := run(context.Background(), append(args, "-allow-embedding-api"), &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var report evaluation.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	info := report.Retrieval
	if info == nil || info.Backend != "semantic" || info.EmbeddingModel != "test-vectors" || info.EmbeddingBatchSize != 2 || info.Dimensions != 2 || info.MinScore == nil || *info.MinScore != 0.8 || len(info.CorpusSHA256) != 64 || len(info.CasesSHA256) != 64 {
		t.Fatalf("metadata=%+v", info)
	}
	if calls.Load() != 22 {
		t.Fatalf("calls=%d, want two index batches and 20 queries", calls.Load())
	}
	for i := 0; i < 22; i++ {
		want := 1
		if i == 0 {
			want = 2
		}
		if got := <-batches; got != want {
			t.Fatalf("request %d sent %d texts, want %d", i, got, want)
		}
	}
	var formatted bytes.Buffer
	if err := writeText(&formatted, report); err != nil || !strings.Contains(formatted.String(), "索引批量：2") {
		t.Fatal("text report missing batch configuration", err)
	}
	if strings.Contains(out.String(), "private-test-key") || strings.Contains(out.String(), s.URL) {
		t.Fatal("report leaks endpoint or credentials")
	}
	// This is a wiring test using deliberately uninformative vectors, not an
	// assertion that semantic retrieval improves the real FAQ quality baseline.
}

func TestRunAllQualityMissesStillSucceeds(t *testing.T) {
	dir := t.TempDir()
	faqPath, casesPath := filepath.Join(dir, "faq.json"), filepath.Join(dir, "cases.json")
	for path, data := range map[string]string{
		faqPath:   `[{"id":"known","title":"demo","source":"demo/source","content":"fictional","keywords":["needle"]}]`,
		casesPath: `[{"id":"one","query":"different wording","expected_ids":["known"]}]`,
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	args := []string{"-faq", faqPath, "-cases", casesPath, "-format", "json"}
	if err := run(context.Background(), args, &out, io.Discard); err != nil {
		t.Fatal(err)
	}
	var report evaluation.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Summary.Top1.Count != 0 || report.Summary.MissingCases != 1 || report.Summary.NoMatch.Rate != nil {
		t.Fatalf("summary = %+v", report.Summary)
	}
	var text bytes.Buffer
	if err := writeText(&text, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "N/A（0/0）") {
		t.Fatal("empty category not marked N/A")
	}
	if err := os.WriteFile(casesPath, []byte(`[{"id":"one","query":"q","expected_ids":["typo"]}]`), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(context.Background(), args, &out, io.Discard); err == nil || out.Len() != 0 {
		t.Fatal("unknown label must fail before reporting scores")
	}
}

type failingWriter struct{}

var errOutput = errors.New("output failed")

func (failingWriter) Write([]byte) (int, error) { return 0, errOutput }

type shortWriter struct{}

func (shortWriter) Write([]byte) (int, error) { return 0, nil }

func TestRunErrorsAndHelp(t *testing.T) {
	for _, suffix := range [][]string{
		{"-top-k", "0"}, {"-top-k", "4"}, {"-format", "yaml"}, {"unexpected"}, {"-unknown"}, {"-faq", "missing-file"}, {"-cases", "missing-file"},
	} {
		var out bytes.Buffer
		if err := run(context.Background(), append(exampleArgs(), suffix...), &out, io.Discard); err == nil || out.Len() != 0 {
			t.Fatalf("invalid flags accepted: %v", suffix)
		}
	}
	for _, format := range []string{"text", "json"} {
		if err := run(context.Background(), append(exampleArgs(), "-format", format), failingWriter{}, io.Discard); !errors.Is(err, errOutput) {
			t.Fatalf("writer error = %v", err)
		}
	}
	if err := writeText(shortWriter{}, evaluation.Report{}); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, exampleArgs(), io.Discard, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	var usage bytes.Buffer
	if err := run(context.Background(), []string{"-help"}, io.Discard, &usage); err != nil || usage.Len() == 0 {
		t.Fatalf("help = %q, err = %v", usage.String(), err)
	}
}
