package einoadapter_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	einoadapter "github.com/qiuyier/omni-agent-customer-service/internal/ai/eino"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

func TestAgenticGeneratorMapsTurnsAndExtractsTextBlocks(t *testing.T) {
	agenticModel := &recordingAgenticModel{
		response: &schema.AgenticMessage{
			Role: schema.AgenticRoleTypeAssistant,
			ContentBlocks: []*schema.ContentBlock{
				schema.NewContentBlock(&schema.AssistantGenText{Text: "first "}),
				schema.NewContentBlock(&schema.AssistantGenText{Text: "second"}),
			},
		},
	}
	generator, err := einoadapter.NewAgenticGenerator(agenticModel, "support policy")
	if err != nil {
		t.Fatal(err)
	}

	reply, err := generator.Generate(context.Background(), customerservice.ReplyRequest{
		History: []customerservice.ReplyTurn{
			{Role: customerservice.ReplyRoleSystem, Content: "premium customer"},
			{Role: customerservice.ReplyRoleUser, Content: "hello"},
			{Role: customerservice.ReplyRoleAssistant, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if reply.Content != "first second" {
		t.Fatalf("reply content = %q", reply.Content)
	}
	if len(agenticModel.input) != 4 {
		t.Fatalf("model input length = %d, want 4", len(agenticModel.input))
	}
	wantRoles := []schema.AgenticRoleType{
		schema.AgenticRoleTypeSystem,
		schema.AgenticRoleTypeSystem,
		schema.AgenticRoleTypeUser,
		schema.AgenticRoleTypeAssistant,
	}
	wantText := []string{"support policy", "premium customer", "hello", "hi"}
	for index := range wantRoles {
		message := agenticModel.input[index]
		if message.Role != wantRoles[index] {
			t.Fatalf("input[%d] role = %q, want %q", index, message.Role, wantRoles[index])
		}
		if text := agenticMessageText(message); text != wantText[index] {
			t.Fatalf("input[%d] text = %q, want %q", index, text, wantText[index])
		}
	}
}

func TestAgenticGeneratorHandlesInvalidInputAndResponses(t *testing.T) {
	if _, err := einoadapter.NewAgenticGenerator(nil, ""); !errors.Is(err, einoadapter.ErrInvalidConfiguration) {
		t.Fatalf("NewAgenticGenerator(nil) error = %v", err)
	}

	modelErr := errors.New("responses API unavailable")
	tests := []struct {
		name     string
		history  []customerservice.ReplyTurn
		response *schema.AgenticMessage
		modelErr error
		wantErr  error
	}{
		{
			name:    "unsupported role",
			history: []customerservice.ReplyTurn{{Role: customerservice.ReplyRole("unknown")}},
			wantErr: einoadapter.ErrUnsupportedRole,
		},
		{name: "model error", modelErr: modelErr, wantErr: modelErr},
		{name: "nil response", wantErr: einoadapter.ErrInvalidResponse},
		{
			name:     "non assistant response",
			response: schema.UserAgenticMessage("unexpected"),
			wantErr:  einoadapter.ErrInvalidResponse,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			agenticModel := &recordingAgenticModel{response: test.response, err: test.modelErr}
			generator, err := einoadapter.NewAgenticGenerator(agenticModel, "")
			if err != nil {
				t.Fatal(err)
			}
			_, err = generator.Generate(context.Background(), customerservice.ReplyRequest{History: test.history})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Generate() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestNewOpenAIResponsesGeneratorValidatesConfigurationWithoutCallingAPI(t *testing.T) {
	_, err := einoadapter.NewOpenAIResponsesGenerator(context.Background(), einoadapter.OpenAIResponsesConfig{})
	if !errors.Is(err, einoadapter.ErrInvalidConfiguration) {
		t.Fatalf("missing key error = %v, want ErrInvalidConfiguration", err)
	}
	_, err = einoadapter.NewOpenAIResponsesGenerator(context.Background(), einoadapter.OpenAIResponsesConfig{
		APIKey:  "test-key",
		Timeout: -time.Second,
	})
	if !errors.Is(err, einoadapter.ErrInvalidConfiguration) {
		t.Fatalf("negative timeout error = %v, want ErrInvalidConfiguration", err)
	}

	generator, err := einoadapter.NewOpenAIResponsesGenerator(context.Background(), einoadapter.OpenAIResponsesConfig{
		APIKey: "test-key",
	})
	if err != nil || generator == nil {
		t.Fatalf("valid config generator = %v, error %v", generator, err)
	}
}

type recordingAgenticModel struct {
	input    []*schema.AgenticMessage
	response *schema.AgenticMessage
	err      error
}

func (m *recordingAgenticModel) Generate(
	_ context.Context,
	input []*schema.AgenticMessage,
	_ ...model.Option,
) (*schema.AgenticMessage, error) {
	m.input = append([]*schema.AgenticMessage(nil), input...)
	return m.response, m.err
}

func agenticMessageText(message *schema.AgenticMessage) string {
	if message == nil || len(message.ContentBlocks) != 1 || message.ContentBlocks[0] == nil {
		return ""
	}
	block := message.ContentBlocks[0]
	if block.UserInputText != nil {
		return block.UserInputText.Text
	}
	if block.AssistantGenText != nil {
		return block.AssistantGenText.Text
	}
	return ""
}
