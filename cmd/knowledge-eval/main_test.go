package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
