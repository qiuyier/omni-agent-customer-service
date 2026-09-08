// Command knowledge-eval evaluates the local FAQ retriever without an AI model,
// network service, environment credentials, or database.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/evaluation"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/faq"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "knowledge-eval: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("knowledge-eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	faqPath := flags.String("faq", "examples/knowledge/faq.json", "local FAQ JSON file")
	casesPath := flags.String("cases", "examples/knowledge/eval_cases.json", "human-labelled relevance cases")
	topK := flags.Int("top-k", customerservice.MaxKnowledgeSnippets, "maximum references per query (1-3)")
	format := flags.String("format", "text", "report format: text or json")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || (*format != "text" && *format != "json") || *topK < 1 || *topK > customerservice.MaxKnowledgeSnippets {
		return fmt.Errorf("expected named flags, format text/json and top-k between 1 and %d", customerservice.MaxKnowledgeSnippets)
	}
	retriever, err := faq.Load(*faqPath)
	if err != nil {
		return err
	}
	cases, err := evaluation.Load(*casesPath)
	if err != nil {
		return err
	}
	known := make(map[string]bool)
	for _, id := range retriever.ReferenceIDs() {
		known[id] = true
	}
	for index, c := range cases {
		for _, id := range c.ExpectedIDs {
			if !known[id] {
				return fmt.Errorf("case %d references an ID absent from the FAQ", index+1)
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, evaluation.DefaultTimeout)
	defer cancel()
	report, err := evaluation.Run(ctx, retriever, cases, *topK)
	if err != nil {
		return err
	}
	if *format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	return writeText(stdout, report)
}

func writeText(w io.Writer, report evaluation.Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "FAQ 检索评测：%d 条，Top K = %d（仅检索，不评价模型回答）\n", report.Summary.Cases, report.TopK)
	for _, item := range []struct {
		name  string
		value evaluation.Metric
	}{
		{"首条命中率（有相关资料的问题）", report.Summary.Top1},
		{"正确无命中率（无相关资料的问题）", report.Summary.NoMatch},
		{"结果集合完全匹配率（不比较相关资料间的顺序）", report.Summary.ExactSet},
	} {
		if item.value.Rate == nil {
			fmt.Fprintf(&b, "%s：N/A（0/0）\n", item.name)
		} else {
			fmt.Fprintf(&b, "%s：%.1f%%（%d/%d）\n", item.name, *item.value.Rate*100, item.value.Count, item.value.Total)
		}
	}
	fmt.Fprintf(&b, "Top-K 内漏检问题数：%d；含无关命中的问题数：%d（两者可重叠）\n", report.Summary.MissingCases, report.Summary.UnexpectedCases)
	for _, result := range report.Results {
		fmt.Fprintf(&b, "\n[%s] %q\n  预期：%q\n  实际：%q\n  漏检：%q\n  无关：%q\n", result.ID, result.Query, result.ExpectedIDs, result.ActualIDs, result.MissingIDs, result.UnexpectedIDs)
		if result.Top1Hit != nil {
			fmt.Fprintf(&b, "  首条命中：%t\n", *result.Top1Hit)
		}
		if result.Note != "" {
			fmt.Fprintf(&b, "  标注说明：%q\n", result.Note)
		}
	}
	n, err := io.WriteString(w, b.String())
	if err == nil && n != b.Len() {
		return io.ErrShortWrite
	}
	return err
}
