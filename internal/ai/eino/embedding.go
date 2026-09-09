package einoadapter

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

var ErrEmbeddingRequest = errors.New("embedding: request or response failed")

// EmbeddingConfig Embedding settings are deliberately independent of chat provider settings.
type EmbeddingConfig struct {
	APIKey, Model, BaseURL string
	Timeout                time.Duration
}
type EmbeddingAdapter struct{ model embedding.Embedder }

func NewEmbeddingAdapter(model embedding.Embedder) (*EmbeddingAdapter, error) {
	if model == nil {
		return nil, ErrInvalidConfiguration
	}
	return &EmbeddingAdapter{model: model}, nil
}

func (e *EmbeddingAdapter) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if ctx == nil || e == nil || e.model == nil {
		return nil, ErrInvalidConfiguration
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	v, err := e.model.EmbedStrings(ctx, append([]string{}, texts...))
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, ErrEmbeddingRequest
	}
	return v, nil
}

// ValidateEmbeddingConfig performs no network requests and never prints secrets.
func ValidateEmbeddingConfig(c EmbeddingConfig) error {
	if strings.TrimSpace(c.APIKey) == "" || strings.TrimSpace(c.Model) == "" || c.Timeout <= 0 {
		return ErrInvalidConfiguration
	}
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if err := validateProviderBaseURL(base, false); err != nil {
		return ErrInvalidConfiguration
	}
	if strings.HasSuffix(base, "/embeddings") {
		return ErrInvalidConfiguration
	}
	return nil
}

// NewCompatibleEmbedder implements Eino's Embedder component with the existing
// SDK. The response index is validated and restored to input order; a gateway
// returning vectors out of order must not silently corrupt the search index.
func NewCompatibleEmbedder(ctx context.Context, c EmbeddingConfig) (*EmbeddingAdapter, error) {
	if ctx == nil {
		return nil, ErrInvalidConfiguration
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ValidateEmbeddingConfig(c); err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: c.Timeout, Transport: boundedEmbeddingTransport{}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// Unlike NewClient, this service constructor does not read OPENAI_* env vars.
	service := openai.NewEmbeddingService(option.WithAPIKey(strings.TrimSpace(c.APIKey)), option.WithBaseURL(strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")+"/"), option.WithHTTPClient(client), option.WithMaxRetries(0))
	return NewEmbeddingAdapter(&compatibleEmbedding{service: service, model: strings.TrimSpace(c.Model)})
}

type compatibleEmbedding struct {
	service openai.EmbeddingService
	model   string
}

var _ embedding.Embedder = (*compatibleEmbedding)(nil)

func (e *compatibleEmbedding) EmbedStrings(ctx context.Context, texts []string, opts ...embedding.Option) ([][]float64, error) {
	if ctx == nil || len(texts) < 1 || len(texts) > 16 {
		return nil, ErrInvalidConfiguration
	}
	selected := embedding.GetCommonOptions(nil, opts...)
	if selected.Model != nil && *selected.Model != e.model {
		return nil, ErrInvalidConfiguration
	}
	for _, s := range texts {
		if !utf8.ValidString(s) || strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > 6001 {
			return nil, ErrInvalidConfiguration
		}
	}
	response, err := e.service.New(ctx, openai.EmbeddingNewParams{Model: e.model, Input: openai.EmbeddingNewParamsInputUnion{OfArrayOfStrings: texts}, EncodingFormat: openai.EmbeddingNewParamsEncodingFormatFloat})
	if err != nil {
		return nil, err
	} // The outer adapter strips provider payloads.
	if response == nil || len(response.Data) != len(texts) {
		return nil, ErrEmbeddingRequest
	}
	result := make([][]float64, len(texts))
	dimensions := 0
	for _, item := range response.Data {
		if !item.JSON.Index.Valid() || item.Index < 0 || item.Index >= int64(len(texts)) || result[item.Index] != nil {
			return nil, ErrEmbeddingRequest
		}
		v := item.Embedding
		if dimensions == 0 {
			dimensions = len(v)
		}
		if len(v) == 0 || len(v) > 16384 || len(v) != dimensions {
			return nil, ErrEmbeddingRequest
		}
		nonzero := false
		for _, x := range v {
			if math.IsNaN(x) || math.IsInf(x, 0) {
				return nil, ErrEmbeddingRequest
			}
			nonzero = nonzero || x != 0
		}
		if !nonzero {
			return nil, ErrEmbeddingRequest
		}
		result[item.Index] = append([]float64{}, v...)
	}
	return result, nil
}

// Bound JSON/error payloads before the SDK allocates decoded vectors or errors.
type boundedEmbeddingTransport struct{}

func (boundedEmbeddingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	const maxBytes = 8 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxBytes {
		return nil, ErrEmbeddingRequest
	}
	resp.Body = io.NopCloser(bytes.NewReader(data))
	return resp, nil
}
