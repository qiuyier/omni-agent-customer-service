package einoadapter_test

import (
	"context"
	"errors"
	"io"
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

func TestAgenticGeneratorStreamsAssistantText(t *testing.T) {
	agenticModel := &recordingAgenticModel{
		stream: schema.StreamReaderFromArray([]*schema.AgenticMessage{
			{Role: schema.AgenticRoleTypeAssistant},
			{
				Role: schema.AgenticRoleTypeAssistant,
				ContentBlocks: []*schema.ContentBlock{
					schema.NewContentBlock(&schema.AssistantGenText{Text: "first "}),
					schema.NewContentBlock(&schema.AssistantGenText{Text: "part"}),
				},
			},
			{
				Role: schema.AgenticRoleTypeAssistant,
				ContentBlocks: []*schema.ContentBlock{
					schema.NewContentBlock(&schema.AssistantGenText{Text: " second"}),
				},
			},
		}),
	}
	generator, err := einoadapter.NewAgenticGenerator(agenticModel, "support policy")
	if err != nil {
		t.Fatal(err)
	}

	reader, err := generator.Stream(context.Background(), customerservice.ReplyRequest{
		History: []customerservice.ReplyTurn{
			{Role: customerservice.ReplyRoleUser, Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	first, err := reader.Recv()
	if err != nil || first.Content != "first part" {
		t.Fatalf("first Recv() = %#v, %v", first, err)
	}
	second, err := reader.Recv()
	if err != nil || second.Content != " second" {
		t.Fatalf("second Recv() = %#v, %v", second, err)
	}
	if _, err := reader.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("final Recv() error = %v, want EOF", err)
	}
	if agenticModel.streamCalls != 1 {
		t.Fatalf("model Stream() calls = %d, want 1", agenticModel.streamCalls)
	}
	if len(agenticModel.input) != 2 {
		t.Fatalf("model input length = %d, want 2", len(agenticModel.input))
	}
	if got := agenticMessageText(agenticModel.input[0]); got != "support policy" {
		t.Fatalf("system prompt = %q", got)
	}
	if got := agenticMessageText(agenticModel.input[1]); got != "hello" {
		t.Fatalf("customer message = %q", got)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestAgenticGeneratorHandlesStreamErrors(t *testing.T) {
	streamErr := errors.New("stream unavailable")
	tests := []struct {
		name      string
		history   []customerservice.ReplyTurn
		stream    *schema.StreamReader[*schema.AgenticMessage]
		streamErr error
		wantErr   error
		anyError  bool
	}{
		{
			name:    "unsupported role before model call",
			history: []customerservice.ReplyTurn{{Role: customerservice.ReplyRole("unknown")}},
			wantErr: einoadapter.ErrUnsupportedRole,
		},
		{name: "model stream creation error", streamErr: streamErr, wantErr: streamErr},
		{name: "nil model stream", anyError: true},
		{
			name:    "nil chunk",
			stream:  schema.StreamReaderFromArray([]*schema.AgenticMessage{nil}),
			wantErr: einoadapter.ErrInvalidResponse,
		},
		{
			name: "non assistant chunk",
			stream: schema.StreamReaderFromArray([]*schema.AgenticMessage{
				schema.UserAgenticMessage("unexpected"),
			}),
			wantErr: einoadapter.ErrInvalidResponse,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			agenticModel := &recordingAgenticModel{stream: test.stream, streamErr: test.streamErr}
			generator, err := einoadapter.NewAgenticGenerator(agenticModel, "")
			if err != nil {
				t.Fatal(err)
			}
			reader, err := generator.Stream(context.Background(), customerservice.ReplyRequest{
				History: test.history,
			})
			if err != nil {
				if test.anyError {
					return
				}
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("Stream() error = %v, want %v", err, test.wantErr)
				}
				return
			}
			t.Cleanup(func() { _ = reader.Close() })
			_, err = reader.Recv()
			if test.anyError && err != nil {
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Recv() error = %v, want %v", err, test.wantErr)
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
	input       []*schema.AgenticMessage
	response    *schema.AgenticMessage
	err         error
	stream      *schema.StreamReader[*schema.AgenticMessage]
	streamErr   error
	streamCalls int
}

func (m *recordingAgenticModel) Stream(
	_ context.Context,
	input []*schema.AgenticMessage,
	_ ...model.Option,
) (*schema.StreamReader[*schema.AgenticMessage], error) {
	m.input = append([]*schema.AgenticMessage(nil), input...)
	m.streamCalls++
	return m.stream, m.streamErr
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
