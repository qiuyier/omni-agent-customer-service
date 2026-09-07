package einoadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	einostream "github.com/qiuyier/omni-agent-kit/adapter/eino/stream"
	kitstream "github.com/qiuyier/omni-agent-kit/stream"
)

// AgenticModel is the narrow subset of model.AgenticModel required by the
// provider-independent reply adapter.
type AgenticModel interface {
	Generate(
		ctx context.Context,
		input []*schema.AgenticMessage,
		opts ...model.Option,
	) (*schema.AgenticMessage, error)
	Stream(
		ctx context.Context,
		input []*schema.AgenticMessage,
		opts ...model.Option,
	) (*schema.StreamReader[*schema.AgenticMessage], error)
}

// AgenticGenerator implements the reply port with Eino's AgenticMessage model.
type AgenticGenerator struct {
	model        AgenticModel
	systemPrompt string
}

// NewAgenticGenerator creates an adapter for Eino agentic models, including
// Responses and OpenAI-compatible Chat Completions implementations.
func NewAgenticGenerator(agenticModel AgenticModel, systemPrompt string) (*AgenticGenerator, error) {
	if agenticModel == nil {
		return nil, fmt.Errorf("%w: agentic model is nil", ErrInvalidConfiguration)
	}
	return &AgenticGenerator{model: agenticModel, systemPrompt: systemPrompt}, nil
}

// Generate maps application turns to AgenticMessage values and extracts all
// assistant text blocks from the model response.
func (g *AgenticGenerator) Generate(
	ctx context.Context,
	request customerservice.ReplyRequest,
) (customerservice.Reply, error) {
	if ctx == nil {
		return customerservice.Reply{}, fmt.Errorf("%w: context is nil", ErrInvalidConfiguration)
	}
	if err := ctx.Err(); err != nil {
		return customerservice.Reply{}, err
	}

	messages, err := g.agenticMessages(request)
	if err != nil {
		return customerservice.Reply{}, err
	}

	response, err := g.model.Generate(ctx, messages)
	if err != nil {
		return customerservice.Reply{}, fmt.Errorf("generate with Eino agentic model: %w", err)
	}
	content, err := assistantText(response)
	if err != nil {
		return customerservice.Reply{}, err
	}
	return customerservice.Reply{Content: content}, nil
}

// Stream maps application turns to AgenticMessage values and exposes only
// assistant text fragments through the provider-independent stream port.
func (g *AgenticGenerator) Stream(
	ctx context.Context,
	request customerservice.ReplyRequest,
) (kitstream.Reader[customerservice.ReplyDelta], error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is nil", ErrInvalidConfiguration)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	messages, err := g.agenticMessages(request)
	if err != nil {
		return nil, err
	}
	source, err := g.model.Stream(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("stream with Eino agentic model: %w", err)
	}
	reader, err := einostream.Wrap(source)
	if err != nil {
		return nil, fmt.Errorf("adapt Eino agentic stream: %w", err)
	}

	return kitstream.NewReader(func() (customerservice.ReplyDelta, error) {
		for {
			chunk, recvErr := reader.Recv()
			if recvErr != nil {
				if errors.Is(recvErr, io.EOF) {
					return customerservice.ReplyDelta{}, io.EOF
				}
				return customerservice.ReplyDelta{}, fmt.Errorf(
					"receive Eino agentic stream: %w",
					recvErr,
				)
			}
			text, extractErr := assistantText(chunk)
			if extractErr != nil {
				return customerservice.ReplyDelta{}, extractErr
			}
			if text == "" {
				continue
			}
			return customerservice.ReplyDelta{Content: text}, nil
		}
	}, reader.Close), nil
}

func (g *AgenticGenerator) agenticMessages(
	request customerservice.ReplyRequest,
) ([]*schema.AgenticMessage, error) {
	turns, err := replyPromptTurns(g.systemPrompt, request)
	if err != nil {
		return nil, err
	}
	messages := make([]*schema.AgenticMessage, 0, len(turns))
	for _, turn := range turns {
		message, err := agenticMessage(turn)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func assistantText(message *schema.AgenticMessage) (string, error) {
	if message == nil {
		return "", fmt.Errorf("%w: model returned nil agentic message", ErrInvalidResponse)
	}
	if message.Role != schema.AgenticRoleTypeAssistant {
		return "", fmt.Errorf(
			"%w: model returned role %q instead of assistant",
			ErrInvalidResponse,
			message.Role,
		)
	}

	var content strings.Builder
	for _, block := range message.ContentBlocks {
		if block != nil && block.AssistantGenText != nil {
			content.WriteString(block.AssistantGenText.Text)
		}
	}
	return content.String(), nil
}

func agenticMessage(turn customerservice.ReplyTurn) (*schema.AgenticMessage, error) {
	switch turn.Role {
	case customerservice.ReplyRoleSystem:
		return schema.SystemAgenticMessage(turn.Content), nil
	case customerservice.ReplyRoleUser:
		return schema.UserAgenticMessage(turn.Content), nil
	case customerservice.ReplyRoleAssistant:
		return &schema.AgenticMessage{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.AssistantGenText{Text: turn.Content}),
			},
		}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedRole, turn.Role)
	}
}

var _ customerservice.ReplyGenerator = (*AgenticGenerator)(nil)
var _ customerservice.ReplyStreamGenerator = (*AgenticGenerator)(nil)
