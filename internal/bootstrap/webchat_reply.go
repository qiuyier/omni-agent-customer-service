package bootstrap

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	einoadapter "github.com/qiuyier/omni-agent-customer-service/internal/ai/eino"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

const (
	// The AI request budget includes knowledge retrieval (at most 20 seconds),
	// generation (75 seconds), and time to receive/persist the messages.
	WebchatAIRequestTimeout   = 110 * time.Second
	WebchatFakeRequestTimeout = 20 * time.Second
)

// WebchatReplyConfig is trusted, instance-wide server configuration. It is not
// browser input and must never be serialized into a session or public response.
// Knowledge and provider settings are not partitioned by browser identity.
type WebchatReplyConfig struct {
	Mode      string
	AI        einoadapter.ProviderConfig
	Runtime   AIReplyRuntimeConfig
	Knowledge KnowledgeConfig
}

// ResolveWebchatReplyConfig makes real model use an explicit opt-in. Fake mode
// never consults the environment, including inherited model or embedding keys.
func ResolveWebchatReplyConfig(mode string, lookup LookupEnv) (WebchatReplyConfig, error) {
	if mode == "" {
		mode = "fake"
	}
	c := WebchatReplyConfig{Mode: mode}
	if mode == "fake" {
		return c, nil
	}
	if mode != "ai" {
		return WebchatReplyConfig{}, errors.New("webchat: reply mode must be fake or ai")
	}
	var err error
	if c.AI, err = ResolveAIConfig(lookup, AIOverrides{}); err != nil {
		return WebchatReplyConfig{}, errors.New("webchat: invalid AI configuration; check AI provider, protocol, model, endpoint and server-side credential settings")
	}
	if c.Runtime, err = ResolveAIReplyRuntimeConfig(lookup); err != nil {
		return WebchatReplyConfig{}, errors.New("webchat: AI_MAX_CONCURRENT_REPLIES must be a positive integer")
	}
	if c.Knowledge, err = ResolveKnowledgeConfig(lookup); err != nil {
		return WebchatReplyConfig{}, errors.New("webchat: invalid knowledge configuration; check KNOWLEDGE and EMBEDDING settings")
	}
	if err := c.Validate(); err != nil {
		return WebchatReplyConfig{}, err
	}
	return c, nil
}

// Validate performs no network requests and does not read the FAQ file. File
// contents and provider reachability are deliberately startup/runtime checks,
// not promises made by a check-config command.
func (c WebchatReplyConfig) Validate() error {
	if c.Mode == "" || c.Mode == "fake" {
		return nil
	}
	if c.Mode != "ai" {
		return errors.New("webchat: reply mode must be fake or ai")
	}
	if einoadapter.ValidateProviderConfig(c.AI) != nil {
		return errors.New("webchat: invalid AI configuration; check AI provider, protocol, model, endpoint and server-side credential settings")
	}
	if c.Runtime.MaxConcurrentReplies <= 0 {
		return errors.New("webchat: AI_MAX_CONCURRENT_REPLIES must be a positive integer")
	}
	k := c.Knowledge
	if k.Retriever != "" && k.Retriever != "keyword" && k.Retriever != "semantic" {
		return errors.New("webchat: KNOWLEDGE_RETRIEVER must be keyword or semantic")
	}
	if strings.TrimSpace(k.FAQPath) == "" {
		if k.Retriever == "semantic" {
			return errors.New("webchat: semantic retrieval requires KNOWLEDGE_FAQ_PATH")
		}
		return nil
	}
	if k.RetrievalTimeout <= 0 || k.RetrievalTimeout > 20*time.Second {
		return errors.New("webchat: enabled KNOWLEDGE_RETRIEVAL_TIMEOUT must be positive and at most 20s")
	}
	if k.Retriever == "semantic" {
		if k.IndexTimeout <= 0 || k.IndexTimeout > 10*time.Minute ||
			math.IsNaN(k.MinScore) || math.IsInf(k.MinScore, 0) || k.MinScore < -1 || k.MinScore > 1 ||
			einoadapter.ValidateEmbeddingConfig(k.Embedding) != nil {
			return errors.New("webchat: invalid semantic knowledge configuration; check index timeout, score and EMBEDDING settings")
		}
	}
	return nil
}

// RequestTimeout returns the complete gateway budget. Call Validate before
// using a manually assembled configuration; unknown modes are not accepted.
func (c WebchatReplyConfig) RequestTimeout() time.Duration {
	if c.Mode == "ai" {
		return WebchatAIRequestTimeout
	}
	return WebchatFakeRequestTimeout
}

// NewWebchatReplyOptions composes the existing provider and retrieval adapters.
// Pass the process stop/startup context, not a completed database probe context:
// semantic index construction gets its own bounded initialization lifetime.
// Errors never expose provider payloads, credentials, FAQ contents or paths.
func NewWebchatReplyOptions(ctx context.Context, c WebchatReplyConfig, fake customerservice.ReplyGenerator) ([]customerservice.Option, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, errors.New("webchat: reply initialization requires a context")
	}
	if ctx.Err() != nil {
		return nil, errors.New("webchat: reply initialization was canceled")
	}
	if c.Mode == "" || c.Mode == "fake" {
		if fake == nil {
			return nil, errors.New("webchat: fake reply generator is required")
		}
		return []customerservice.Option{
			customerservice.WithReplyGenerator(fake),
			customerservice.WithReplyGenerationTimeout(3 * time.Second),
			customerservice.WithMaxConcurrentReplies(DefaultAIMaxConcurrentReplies),
		}, nil
	}
	initializationTimeout := 10 * time.Second
	if c.Knowledge.Retriever == "semantic" {
		initializationTimeout += c.Knowledge.IndexTimeout
	}
	initializationContext, cancel := context.WithTimeout(ctx, initializationTimeout)
	defer cancel()
	retriever, err := NewKnowledgeRetrieverContext(initializationContext, c.Knowledge)
	if err != nil {
		return nil, errors.New("webchat: knowledge initialization failed; check FAQ format and, for semantic retrieval, embedding settings and connectivity; no fake fallback")
	}
	providerConfig := c.AI
	providerConfig.Timeout = DefaultAIReplyTimeout
	generator, err := NewAIReplyGenerator(initializationContext, providerConfig)
	if err != nil || initializationContext.Err() != nil {
		return nil, errors.New("webchat: AI initialization failed; check provider settings; no fake fallback")
	}
	options := []customerservice.Option{
		customerservice.WithReplyGenerator(generator),
		customerservice.WithReplyGenerationTimeout(DefaultAIReplyTimeout),
		customerservice.WithMaxConcurrentReplies(c.Runtime.MaxConcurrentReplies),
	}
	if retriever != nil {
		options = append(options, customerservice.WithKnowledgeRetriever(retriever),
			customerservice.WithKnowledgeRetrievalTimeout(c.Knowledge.RetrievalTimeout))
	}
	return options, nil
}
