// Command knowledge-eval is offline by default. Semantic evaluation requires
// both a backend flag and explicit permission to call the embedding endpoint.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/evaluation"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/faq"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/semantic"
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
	casesPath := flags.String("cases", "examples/regression/knowledge/eval_cases.json", "human-labelled relevance cases")
	topK := flags.Int("top-k", customerservice.MaxKnowledgeSnippets, "maximum references per query (1-3)")
	format := flags.String("format", "text", "report format: text or json")
	backend := flags.String("retriever", "keyword", "retriever: keyword or semantic")
	allowEmbedding := flags.Bool("allow-embedding-api", false, "allow sending FAQ and queries to EMBEDDING_BASE_URL; may incur charges")
	diagnostics := flags.Bool("diagnostics", false, "include all candidate cosine scores and selection decisions (semantic JSON only; at most 20 cases)")
	timeout := flags.Duration("timeout", evaluation.DefaultTimeout, "evaluation timeout, excluding index initialization")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || (*format != "text" && *format != "json") || *topK < 1 || *topK > customerservice.MaxKnowledgeSnippets {
		return fmt.Errorf("expected named flags, format text/json and top-k between 1 and %d", customerservice.MaxKnowledgeSnippets)
	}
	if (*backend != "keyword" && *backend != "semantic") || *timeout <= 0 || *timeout > 30*time.Minute {
		return fmt.Errorf("retriever must be keyword/semantic and timeout must be positive, at most 30m")
	}
	if *diagnostics && (*backend != "semantic" || *format != "json") {
		return fmt.Errorf("diagnostics requires -retriever semantic and -format json")
	}
	if *backend == "semantic" && !*allowEmbedding {
		return fmt.Errorf("semantic evaluation requires -allow-embedding-api; FAQ and queries will be sent to the configured embedding service")
	}
	retriever, err := faq.Load(*faqPath)
	if err != nil {
		return err
	}
	cases, err := evaluation.Load(*casesPath)
	if err != nil {
		return err
	}
	if *diagnostics && len(cases) > maxDiagnosticCases {
		return fmt.Errorf("diagnostics supports at most %d cases per run", maxDiagnosticCases)
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
	var selected customerservice.KnowledgeRetriever = retriever
	var diagnostic *diagnosticRetriever
	queryTimeout := customerservice.DefaultKnowledgeRetrievalTimeout
	info := &evaluation.RetrievalInfo{Backend: *backend, CorpusSHA256: fingerprint(retriever.Snippets()), CasesSHA256: fingerprint(cases)}
	if *backend == "semantic" {
		// The command-line backend choice is authoritative; an environment file
		// cannot silently make a default keyword evaluation access the network.
		lookup := func(name string) (string, bool) {
			switch name {
			case "KNOWLEDGE_RETRIEVER":
				return "semantic", true
			case "KNOWLEDGE_FAQ_PATH":
				return *faqPath, true
			default:
				return os.LookupEnv(name)
			}
		}
		config, err := bootstrap.ResolveKnowledgeConfig(lookup)
		if err != nil {
			return err
		}
		index, err := bootstrap.NewSemanticKnowledgeRetriever(ctx, config, retriever.Snippets())
		if err != nil {
			return err
		}
		selected = index
		if *diagnostics {
			diagnostic = &diagnosticRetriever{index: index}
			selected = diagnostic
		}
		queryTimeout = config.RetrievalTimeout
		info.EmbeddingModel = config.Embedding.Model
		info.EmbeddingBatchSize = config.Embedding.BatchSize
		info.MinScore = &config.MinScore
		info.Dimensions = index.Dimensions()
		info.DocumentFormat = semantic.DocumentFormat
	}
	info.QueryTimeout = queryTimeout.String()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	report, err := evaluation.RunWithTimeout(ctx, selected, cases, *topK, queryTimeout)
	if err != nil {
		return err
	}
	report.Retrieval = info
	if *format == "json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if diagnostic != nil {
			if len(diagnostic.snapshots) != len(report.Results) {
				return fmt.Errorf("diagnostic observation count differs from evaluated cases")
			}
			output := diagnosticReport{Report: report, Diagnostics: make([]caseDiagnostics, len(report.Results))}
			for i, result := range report.Results {
				output.Diagnostics[i] = caseDiagnostics{CaseID: result.ID, Candidates: diagnostic.snapshots[i]}
			}
			return encoder.Encode(output)
		}
		return encoder.Encode(report)
	}
	return writeText(stdout, report)
}

func fingerprint(value any) string {
	data, _ := json.Marshal(value) // Only JSON-serializable reference/case structs.
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func writeText(w io.Writer, report evaluation.Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "FAQ 检索评测：%d 条，Top K = %d（仅检索，不评价模型回答）\n", report.Summary.Cases, report.TopK)
	if info := report.Retrieval; info != nil {
		fmt.Fprintf(&b, "检索器：%s；查询超时：%s\n资料快照：%s\n标注快照：%s\n", info.Backend, info.QueryTimeout, info.CorpusSHA256, info.CasesSHA256)
		if info.MinScore != nil {
			fmt.Fprintf(&b, "Embedding 模型：%q；索引批量：%d；维度：%d；阈值：%.4f；文档格式：%s\n", info.EmbeddingModel, info.EmbeddingBatchSize, info.Dimensions, *info.MinScore, info.DocumentFormat)
		}
	}
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
