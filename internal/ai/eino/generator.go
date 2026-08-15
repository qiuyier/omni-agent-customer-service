// Package einoadapter translates the customer-service reply port to Eino's
// chat-model message format.
package einoadapter

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

var (
	// ErrInvalidConfiguration indicates a missing adapter dependency.
	ErrInvalidConfiguration = errors.New("eino reply adapter: invalid configuration")

	// ErrInvalidResponse indicates that the model returned an unusable value.
	ErrInvalidResponse = errors.New("eino reply adapter: invalid response")

	// ErrUnsupportedRole indicates that the application supplied an unknown
	// provider-independent role.
	ErrUnsupportedRole = errors.New("eino reply adapter: unsupported role")
)

// ChatModel is the narrow subset of model.BaseChatModel needed in phase three.
// A real Eino model satisfies it, while tests only need to fake Generate.
type ChatModel interface {
	Generate(
		ctx context.Context,
		input []*schema.Message,
		opts ...model.Option,
	) (*schema.Message, error)
}

// Generator implements customerservice.ReplyGenerator with an Eino chat model.
type Generator struct {
	model        ChatModel
	systemPrompt string
}

// NewGenerator creates an Eino reply adapter. The model is injected so OpenAI,
// a local model, or another Eino provider can be selected at composition time.
func NewGenerator(chatModel ChatModel, systemPrompt string) (*Generator, error) {
	if chatModel == nil {
		return nil, fmt.Errorf("%w: chat model is nil", ErrInvalidConfiguration)
	}
	return &Generator{model: chatModel, systemPrompt: systemPrompt}, nil
}

// Generate maps provider-independent turns to Eino and performs one
// non-streaming model call.
func (g *Generator) Generate(
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
	messages := make([]*schema.Message, 0, capacity)
	if strings.TrimSpace(g.systemPrompt) != "" {
		messages = append(messages, &schema.Message{
			Role:    schema.System,
			Content: g.systemPrompt,
		})
	}
	for index, turn := range request.History {
		role, err := einoRole(turn.Role)
		if err != nil {
			return customerservice.Reply{}, fmt.Errorf("history turn %d: %w", index, err)
		}
		messages = append(messages, &schema.Message{Role: role, Content: turn.Content})
	}

	response, err := g.model.Generate(ctx, messages)
	if err != nil {
		return customerservice.Reply{}, fmt.Errorf("generate with Eino chat model: %w", err)
	}
	if response == nil {
		return customerservice.Reply{}, fmt.Errorf("%w: model returned nil message", ErrInvalidResponse)
	}
	if response.Role != schema.Assistant {
		return customerservice.Reply{}, fmt.Errorf(
			"%w: model returned role %q instead of assistant",
			ErrInvalidResponse,
			response.Role,
		)
	}
	return customerservice.Reply{Content: response.Content}, nil
}

func einoRole(role customerservice.ReplyRole) (schema.RoleType, error) {
	switch role {
	case customerservice.ReplyRoleSystem:
		return schema.System, nil
	case customerservice.ReplyRoleUser:
		return schema.User, nil
	case customerservice.ReplyRoleAssistant:
		return schema.Assistant, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedRole, role)
	}
}

var _ customerservice.ReplyGenerator = (*Generator)(nil)
