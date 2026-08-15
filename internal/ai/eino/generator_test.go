package einoadapter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	einoadapter "github.com/qiuyier/omni-agent-customer-service/internal/ai/eino"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

func TestGeneratorMapsReplyHistoryToEinoMessages(t *testing.T) {
	chatModel := &recordingChatModel{
		response: &schema.Message{Role: schema.Assistant, Content: "model answer"},
	}
	generator, err := einoadapter.NewGenerator(chatModel, "You are a support assistant.")
	if err != nil {
		t.Fatal(err)
	}

	reply, err := generator.Generate(context.Background(), customerservice.ReplyRequest{
		ConversationID:   "conv-1",
		TriggerMessageID: "message-3",
		History: []customerservice.ReplyTurn{
			{Role: customerservice.ReplyRoleSystem, Content: "customer is premium"},
			{Role: customerservice.ReplyRoleUser, Content: "Where is my order?"},
			{Role: customerservice.ReplyRoleAssistant, Content: "Let me check."},
		},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if reply.Content != "model answer" {
		t.Fatalf("reply content = %q", reply.Content)
	}

	want := []*schema.Message{
		{Role: schema.System, Content: "You are a support assistant."},
		{Role: schema.System, Content: "customer is premium"},
		{Role: schema.User, Content: "Where is my order?"},
		{Role: schema.Assistant, Content: "Let me check."},
	}
	if len(chatModel.input) != len(want) {
		t.Fatalf("model input length = %d, want %d", len(chatModel.input), len(want))
	}
	for index := range want {
		if chatModel.input[index].Role != want[index].Role ||
			chatModel.input[index].Content != want[index].Content {
			t.Fatalf("model input[%d] = %#v, want %#v", index, chatModel.input[index], want[index])
		}
	}
}

func TestGeneratorRejectsInvalidConfigurationAndHistory(t *testing.T) {
	if _, err := einoadapter.NewGenerator(nil, ""); !errors.Is(err, einoadapter.ErrInvalidConfiguration) {
		t.Fatalf("NewGenerator(nil) error = %v, want ErrInvalidConfiguration", err)
	}

	chatModel := &recordingChatModel{
		response: &schema.Message{Role: schema.Assistant, Content: "answer"},
	}
	generator, err := einoadapter.NewGenerator(chatModel, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = generator.Generate(context.Background(), customerservice.ReplyRequest{
		History: []customerservice.ReplyTurn{{Role: customerservice.ReplyRole("unknown"), Content: "test"}},
	})
	if !errors.Is(err, einoadapter.ErrUnsupportedRole) {
		t.Fatalf("Generate() error = %v, want ErrUnsupportedRole", err)
	}
	if chatModel.calls != 0 {
		t.Fatalf("chat model calls = %d, want 0", chatModel.calls)
	}
}

func TestGeneratorHandlesModelFailuresAndInvalidResponses(t *testing.T) {
	modelErr := errors.New("provider unavailable")
	tests := []struct {
		name     string
		response *schema.Message
		modelErr error
		wantErr  error
	}{
		{name: "model error", modelErr: modelErr, wantErr: modelErr},
		{name: "nil response", wantErr: einoadapter.ErrInvalidResponse},
		{
			name:     "non assistant response",
			response: &schema.Message{Role: schema.User, Content: "unexpected"},
			wantErr:  einoadapter.ErrInvalidResponse,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chatModel := &recordingChatModel{response: test.response, err: test.modelErr}
			generator, err := einoadapter.NewGenerator(chatModel, "")
			if err != nil {
				t.Fatal(err)
			}
			_, err = generator.Generate(context.Background(), customerservice.ReplyRequest{
				History: []customerservice.ReplyTurn{{Role: customerservice.ReplyRoleUser, Content: "hello"}},
			})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Generate() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

type recordingChatModel struct {
	input    []*schema.Message
	response *schema.Message
	err      error
	calls    int
}

func (m *recordingChatModel) Generate(
	_ context.Context,
	input []*schema.Message,
	_ ...model.Option,
) (*schema.Message, error) {
	m.calls++
	m.input = append([]*schema.Message(nil), input...)
	return m.response, m.err
}
