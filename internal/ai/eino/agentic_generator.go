package einoadapter

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

// AgenticModel is the narrow subset of model.AgenticModel required by the
// provider-independent reply adapter.
type AgenticModel interface {
	Generate(
		ctx context.Context,
		input []*schema.AgenticMessage,
		opts ...model.Option,
	) (*schema.AgenticMessage, error)
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

	capacity := len(request.History)
	if strings.TrimSpace(g.systemPrompt) != "" {
		capacity++
	}
	messages := make([]*schema.AgenticMessage, 0, capacity)
	if strings.TrimSpace(g.systemPrompt) != "" {
		messages = append(messages, schema.SystemAgenticMessage(g.systemPrompt))
	}
	for index, turn := range request.History {
		message, err := agenticMessage(turn)
		if err != nil {
			return customerservice.Reply{}, fmt.Errorf("history turn %d: %w", index, err)
		}
		messages = append(messages, message)
	}

	response, err := g.model.Generate(ctx, messages)
	if err != nil {
		return customerservice.Reply{}, fmt.Errorf("generate with Eino agentic model: %w", err)
	}
	if response == nil {
		return customerservice.Reply{}, fmt.Errorf("%w: model returned nil agentic message", ErrInvalidResponse)
	}
	if response.Role != schema.AgenticRoleTypeAssistant {
		return customerservice.Reply{}, fmt.Errorf(
			"%w: model returned role %q instead of assistant",
			ErrInvalidResponse,
			response.Role,
		)
	}

	var content strings.Builder
	for _, block := range response.ContentBlocks {
		if block != nil && block.AssistantGenText != nil {
			content.WriteString(block.AssistantGenText.Text)
		}
	}
	return customerservice.Reply{Content: content.String()}, nil
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
