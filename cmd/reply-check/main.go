// Command reply-check explicitly opts into live model calls and checks the real
// HTTP/SSE handlers against a disposable in-memory repository. Natural-language
// answer quality is recorded for review, never asserted by keyword matching.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/hlog"
	"github.com/google/uuid"
	"go.uber.org/zap"

	einoadapter "github.com/qiuyier/omni-agent-customer-service/internal/ai/eino"
	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/evaluation"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/faq"
	hertztransport "github.com/qiuyier/omni-agent-customer-service/internal/transport/hertz"
)

type checkOptions struct {
	FAQ, Cases                 string
	AllowModel, AllowEmbedding bool
	Timeout                    time.Duration
}

type checkDependencies struct {
	Retriever    customerservice.KnowledgeRetriever
	Generator    bootstrap.AIReplyGenerator
	QueryTimeout time.Duration
	IndexMS      float64
	Metadata     map[string]any
}

type dependencyFactory func(context.Context, checkOptions, []customerservice.KnowledgeSnippet) (checkDependencies, error)

type checkReport struct {
	SchemaVersion    int            `json:"schema_version"`
	CheckedAt        string         `json:"checked_at"`
	Complete         bool           `json:"complete"`
	MechanicalPassed bool           `json:"mechanical_passed"`
	QualityReview    string         `json:"answer_quality_review"`
	Settings         map[string]any `json:"settings"`
	IndexMS          float64        `json:"index_initialization_ms"`
	Results          []checkResult  `json:"results"`
	Failure          string         `json:"failure,omitempty"`
}

type checkResult struct {
	Case              evaluation.Case `json:"case"`
	Mode              string          `json:"mode"`
	HTTPStatus        int             `json:"http_status"`
	Reply             replyPayload    `json:"reply"`
	RetrievedIDs      []string        `json:"retrieved_ids"`
	ModelReferenceIDs []string        `json:"model_reference_ids"`
	RetrievalCalls    int             `json:"retrieval_calls"`
	ModelCalls        int             `json:"model_calls"`
	ModelOutcome      string          `json:"model_outcome"`
	CleanupFailed     bool            `json:"cleanup_failed"`
	RetrievalError    string          `json:"retrieval_error,omitempty"`
	ModelError        string          `json:"model_error,omitempty"`
	ModelHTTPStatus   int             `json:"model_http_status,omitempty"`
	ModelErrorStage   string          `json:"model_error_stage,omitempty"`
	RetrievalMS       float64         `json:"retrieval_ms"`
	ModelMS           float64         `json:"model_ms"`
	PostRetrievalMS   float64         `json:"post_retrieval_total_ms"`
	HTTPMS            float64         `json:"http_total_ms"`
	FirstDeltaMS      *float64        `json:"first_delta_ms,omitempty"`
	DeltaCount        int             `json:"delta_count"`
	Events            []string        `json:"sse_events,omitempty"`
	StoredMessages    int             `json:"stored_messages"`
	Replays           []replayResult  `json:"replays"`
	MechanicalPassed  bool            `json:"mechanical_passed"`
}

type replayResult struct {
	Mode           string   `json:"mode"`
	HTTPStatus     int      `json:"http_status"`
	Duplicate      bool     `json:"duplicate"`
	ReusedReply    bool     `json:"reused_reply"`
	NoNewCalls     bool     `json:"no_new_retrieval_or_model_calls"`
	StoredMessages int      `json:"stored_messages"`
	HTTPMS         float64  `json:"http_total_ms"`
	DeltaCount     int      `json:"delta_count"`
	Events         []string `json:"sse_events,omitempty"`
}

func main() {
	hlog.SetOutput(io.Discard)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "reply-check: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return runWithFactory(ctx, args, stdout, stderr, realDependencies)
}

func runWithFactory(ctx context.Context, args []string, stdout, stderr io.Writer, factory dependencyFactory) error {
	if ctx == nil || factory == nil {
		return errors.New("context and dependency factory are required")
	}
	var opts checkOptions
	flags := flag.NewFlagSet("reply-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.FAQ, "faq", "examples/knowledge/faq.json", "FAQ file sent to the embedding service")
	flags.StringVar(&opts.Cases, "cases", "examples/knowledge/reply_cases.json", "1-10 labelled fictional or authorized cases")
	flags.BoolVar(&opts.AllowModel, "allow-model-api", false, "allow sending questions and references to the reply model; may incur charges")
	flags.BoolVar(&opts.AllowEmbedding, "allow-embedding-api", false, "allow sending FAQ and questions to the embedding service; may incur charges")
	flags.DurationVar(&opts.Timeout, "timeout", 10*time.Minute, "overall budget including index initialization (at most 30m)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || opts.Timeout <= 0 || opts.Timeout > 30*time.Minute {
		return errors.New("expected named flags and a positive timeout at most 30m")
	}
	if !opts.AllowModel || !opts.AllowEmbedding {
		return errors.New("live acceptance requires both -allow-model-api and -allow-embedding-api")
	}
	corpus, err := faq.Load(opts.FAQ)
	if err != nil {
		return errors.New("cannot load FAQ; check its path and format")
	}
	cases, err := evaluation.Load(opts.Cases)
	if err != nil {
		return err
	}
	if len(cases) > 10 {
		return errors.New("acceptance supports at most 10 cases per run")
	}
	known := corpus.ReferenceIDs()
	for _, c := range cases {
		for _, id := range c.ExpectedIDs {
			if !slices.Contains(known, id) {
				return errors.New("case references an ID absent from the FAQ")
			}
		}
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	deps, err := factory(ctx, opts, corpus.Snippets())
	if err != nil {
		return err
	}
	if deps.Metadata == nil {
		deps.Metadata = map[string]any{}
	}
	deps.Metadata["corpus_sha256"] = checkFingerprint(corpus.Snippets())
	deps.Metadata["cases_sha256"] = checkFingerprint(cases)
	deps.Metadata["storage"] = "disposable_memory"
	deps.Metadata["count_scope"] = "application_ports_not_sdk_http_attempts"
	report := checkReport{SchemaVersion: 1, CheckedAt: time.Now().UTC().Format(time.RFC3339), QualityReview: "manual_review_required", Settings: deps.Metadata, IndexMS: deps.IndexMS, Results: []checkResult{}}
	suiteErr := checkSuite(ctx, deps, cases, stderr, &report)
	if suiteErr != nil {
		report.Failure = suiteErr.Error()
	} else {
		report.Complete, report.MechanicalPassed = true, true
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return err
	}
	return suiteErr
}

func realDependencies(ctx context.Context, opts checkOptions, snippets []customerservice.KnowledgeSnippet) (checkDependencies, error) {
	ai, err := bootstrap.ResolveAIConfig(os.LookupEnv, bootstrap.AIOverrides{})
	if err != nil {
		return checkDependencies{}, errors.New("invalid reply-model configuration; check AI_* or provider credential variables")
	}
	generator, err := bootstrap.NewAIReplyGenerator(ctx, ai)
	if err != nil {
		return checkDependencies{}, errors.New("cannot initialize reply model; check protocol, model and API root")
	}
	lookup := func(key string) (string, bool) {
		switch key {
		case "KNOWLEDGE_RETRIEVER":
			return "semantic", true
		case "KNOWLEDGE_FAQ_PATH":
			return opts.FAQ, true
		default:
			return os.LookupEnv(key)
		}
	}
	config, err := bootstrap.ResolveKnowledgeConfig(lookup)
	if err != nil {
		return checkDependencies{}, err
	}
	start := time.Now()
	retriever, err := bootstrap.NewSemanticKnowledgeRetriever(ctx, config, snippets)
	if err != nil {
		return checkDependencies{}, err
	}
	model := ai.Model
	if model == "" && ai.Provider == einoadapter.ProviderOpenAI {
		model = einoadapter.DefaultOpenAIModel
	}
	protocol := string(ai.Protocol)
	if protocol == "" {
		protocol = "provider_default"
	}
	return checkDependencies{Retriever: retriever, Generator: generator, QueryTimeout: config.RetrievalTimeout, IndexMS: elapsedMS(start), Metadata: map[string]any{
		"ai_provider": string(ai.Provider), "ai_model": model, "ai_protocol_configured": protocol,
		"embedding_model": config.Embedding.Model, "embedding_batch_size": config.Embedding.BatchSize,
		"embedding_dimensions": retriever.Dimensions(), "min_score": config.MinScore, "top_k": customerservice.MaxKnowledgeSnippets,
		"query_timeout": config.RetrievalTimeout.String(), "reply_timeout": bootstrap.DefaultAIReplyTimeout.String(),
	}}, nil
}

func checkSuite(ctx context.Context, deps checkDependencies, cases []evaluation.Case, progress io.Writer, report *checkReport) error {
	if deps.Retriever == nil || deps.Generator == nil || deps.QueryTimeout <= 0 {
		return errors.New("invalid acceptance dependencies")
	}
	probe := &acceptanceProbe{}
	generator := &observedGenerator{inner: deps.Generator, probe: probe}
	repository := conversation.NewMemoryRepository()
	service, err := customerservice.New(repository,
		customerservice.MessageIDGeneratorFunc(func() (string, error) { return uuid.NewString(), nil }),
		customerservice.ClockFunc(time.Now), customerservice.WithReplyGenerator(generator),
		customerservice.WithReplyStreamGenerator(generator), customerservice.WithReplyObserver(probe),
		customerservice.WithKnowledgeRetriever(probe.retrieve(deps.Retriever)),
		customerservice.WithKnowledgeRetrievalTimeout(deps.QueryTimeout),
		customerservice.WithReplyGenerationTimeout(bootstrap.DefaultAIReplyTimeout), customerservice.WithMaxConcurrentReplies(1),
	)
	if err != nil {
		return errors.New("cannot initialize acceptance service")
	}
	base, token, closeServer, err := startCheckServer(ctx, service)
	if err != nil {
		return err
	}
	defer closeServer()
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: bootstrap.DefaultAIReplyTimeout + deps.QueryTimeout + 5*time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for i, c := range cases {
		for _, mode := range []string{"generate", "stream"} {
			fmt.Fprintf(progress, "reply-check: case %d/%d, %s\n", i+1, len(cases), mode)
			result, err := checkOne(ctx, client, base, token, repository, probe, c, mode)
			report.Results = append(report.Results, result)
			if err != nil {
				return fmt.Errorf("case %d, %s: %w", i+1, mode, err)
			}
		}
	}
	return nil
}

func checkOne(ctx context.Context, client *http.Client, base, token string, repository *conversation.MemoryRepository, probe *acceptanceProbe, c evaluation.Case, mode string) (checkResult, error) {
	result := checkResult{Case: c, Mode: mode, Replays: []replayResult{}}
	cid := "reply-check-" + uuid.NewString()
	received, err := receiveMessage(ctx, client, base, token, cid, uuid.NewString(), c.Query)
	if err != nil {
		return result, err
	}
	before := probe.snapshot()
	response, err := requestReply(ctx, client, base, token, mode, cid, received.MessageID)
	result.HTTPStatus, result.Reply = response.Status, response.Reply
	result.HTTPMS, result.FirstDeltaMS, result.DeltaCount, result.Events = response.TotalMS, response.FirstDeltaMS, response.DeltaCount, response.Events
	r, m, finished, calls := probe.observations(before)
	result.RetrievalCalls, result.ModelCalls = calls.Retrievals, calls.Models
	result.ModelOutcome, result.CleanupFailed = string(finished.Outcome), finished.CleanupFailed
	result.RetrievalError, result.ModelError, result.ModelHTTPStatus, result.ModelErrorStage = r.ErrorClass, m.ErrorClass, m.HTTPStatus, m.ErrorStage
	result.RetrievedIDs, result.ModelReferenceIDs, result.RetrievalMS = r.IDs, m.ReferenceIDs, r.DurationMS
	result.ModelMS, result.PostRetrievalMS = float64(finished.ModelDuration)/float64(time.Millisecond), float64(finished.TotalDuration)/float64(time.Millisecond)
	if messages, listErr := repository.ListMessages(ctx, cid); listErr == nil {
		result.StoredMessages = len(messages)
	}
	if err != nil {
		return result, err
	}
	if calls != (probeSnapshot{1, 1, 1}) || m.Mode != mode || !m.KnowledgeEnabled {
		return result, errors.New("first reply did not record exactly one retrieval and successful model operation")
	}
	if finished.Outcome != customerservice.ReplyOutcomeCompleted || finished.Duplicate || finished.CleanupFailed {
		return result, errors.New("reply model did not finish and clean up successfully")
	}
	if response.Reply.Duplicate || response.Reply.ConversationID != cid || response.Reply.TriggerMessageID != received.MessageID || strings.TrimSpace(response.Reply.Content) == "" || !sameIDs(r.IDs, c.ExpectedIDs) || !sameIDs(m.ReferenceIDs, c.ExpectedIDs) {
		return result, errors.New("initial reply identity or reference set did not match the case")
	}
	if err := checkStoredPair(ctx, repository, cid, received.MessageID, c.Query, response.Reply); err != nil {
		return result, err
	}
	result.StoredMessages = 2
	otherMode := "stream"
	if mode == "stream" {
		otherMode = "generate"
	}
	for _, replayMode := range []string{mode, otherMode} {
		beforeReplay := probe.snapshot()
		replayed, err := requestReply(ctx, client, base, token, replayMode, cid, received.MessageID)
		if err != nil {
			return result, err
		}
		reused := replayed.Reply.ConversationID == response.Reply.ConversationID && replayed.Reply.TriggerMessageID == response.Reply.TriggerMessageID && replayed.Reply.MessageID == response.Reply.MessageID && replayed.Reply.Content == response.Reply.Content
		noNewCalls := beforeReplay == probe.snapshot()
		if !replayed.Reply.Duplicate || !reused || !noNewCalls || replayed.DeltaCount != 0 {
			return result, errors.New("completed-reply replay changed the reply or invoked retrieval/model again")
		}
		if err := checkStoredPair(ctx, repository, cid, received.MessageID, c.Query, response.Reply); err != nil {
			return result, err
		}
		result.Replays = append(result.Replays, replayResult{Mode: replayMode, HTTPStatus: replayed.Status, Duplicate: true, ReusedReply: reused, NoNewCalls: noNewCalls, StoredMessages: 2, HTTPMS: replayed.TotalMS, DeltaCount: replayed.DeltaCount, Events: replayed.Events})
	}
	result.MechanicalPassed = true
	return result, nil
}

func checkStoredPair(ctx context.Context, repository *conversation.MemoryRepository, cid, triggerID, query string, reply replyPayload) error {
	messages, err := repository.ListMessages(ctx, cid)
	if err != nil || len(messages) != 2 {
		return errors.New("conversation did not retain exactly one customer and one bot message")
	}
	if messages[0].ID() != triggerID || messages[0].Sender() != conversation.SenderCustomer || messages[0].Content() != query || messages[1].ID() != reply.MessageID || messages[1].Sender() != conversation.SenderBot || messages[1].ReplyToMessageID() != triggerID || messages[1].Content() != reply.Content {
		return errors.New("persisted conversation differs from the authoritative reply")
	}
	return nil
}

func startCheckServer(ctx context.Context, service *customerservice.Service) (string, string, func(), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", nil, errors.New("cannot bind a loopback acceptance listener")
	}
	requestContext, cancelRequests := context.WithCancel(ctx)
	token := uuid.NewString()
	h := server.New(server.WithListener(listener), server.WithOnAccept(func(net.Conn) context.Context { return requestContext }), server.WithSenseClientDisconnection(true), server.WithExitWaitTime(2*time.Second))
	handler, _ := hertztransport.NewHandler(service)
	security, _ := hertztransport.NewSecurityMiddleware(token)
	accessLog, _ := hertztransport.NewAccessLogMiddleware(zap.NewNop())
	rateLimit, _ := hertztransport.NewRateLimitMiddleware(100, 100)
	if err := hertztransport.RegisterRoutes(h.Engine, handler, hertztransport.NewAvailability(), security, accessLog, rateLimit); err != nil {
		cancelRequests()
		_ = listener.Close()
		return "", "", nil, errors.New("cannot register acceptance routes")
	}
	done := make(chan error, 1)
	go func() { done <- h.Run() }()
	closeServer := func() {
		cancelRequests()
		_ = h.Close()
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
	// Wait for the engine itself to be running before issuing any business request.
	startup, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for !h.IsRunning() {
		select {
		case <-done:
			cancelRequests()
			_ = listener.Close()
			return "", "", nil, errors.New("acceptance HTTP server stopped during startup")
		case <-startup.Done():
			closeServer()
			return "", "", nil, errors.New("acceptance HTTP server startup deadline exceeded")
		case <-ticker.C:
		}
	}
	return "http://" + listener.Addr().String(), token, closeServer, nil
}

func sameIDs(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

func checkFingerprint(value any) string {
	data, _ := json.Marshal(value)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
