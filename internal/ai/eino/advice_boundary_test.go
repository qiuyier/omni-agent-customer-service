package einoadapter_test

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

// These tests verify prompt wiring and preservation of evidence/history, not
// whether a live model follows the rules. Live answers require separate review.
func TestAdviceBoundaryRegressionWiring(t *testing.T) {
	data, err := os.ReadFile("../../../examples/regression/knowledge/webchat/advice_boundary_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		ID        string                      `json:"id"`
		Reference string                      `json:"reference"`
		History   []customerservice.ReplyTurn `json:"history"`
		Observed  string                      `json:"observed_answer"`
		Criterion string                      `json:"criterion"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 5 {
		t.Fatal("expected three archived failures/risks and two positive controls")
	}
	for _, fragment := range []string{"未知渠道不是兜底答案", "无法说明补充信息如何改变基于现有资料的回答，就不要追问"} {
		if !strings.Contains(bootstrap.DefaultAISystemPrompt, fragment) {
			t.Fatalf("missing default rule: %s", fragment)
		}
	}
	for _, fixture := range fixtures {
		if fixture.ID == "" || fixture.Criterion == "" || len(fixture.History) == 0 {
			t.Fatal("incomplete fixture")
		}
		for _, path := range []string{"chat_generate", "agentic_generate", "agentic_stream"} {
			t.Run(fixture.ID+"/"+path, func(t *testing.T) {
				knowledge := &customerservice.ReplyKnowledge{Snippets: []customerservice.KnowledgeSnippet{}}
				if fixture.Reference != "" {
					knowledge.Snippets = append(knowledge.Snippets, customerservice.KnowledgeSnippet{ID: fixture.ID, Content: fixture.Reference})
				}
				prompt := recordedReplyPrompt(t, path, bootstrap.DefaultAISystemPrompt, customerservice.ReplyRequest{History: fixture.History, Knowledge: knowledge})
				if len(prompt) != len(fixture.History)+3 || !reflect.DeepEqual(prompt[3:], fixture.History) {
					t.Fatal("history altered")
				}
				for _, fragment := range []string{"Unknown channels are not a fallback answer", "If you cannot identify how the requested detail changes an answer supported by current references, do not ask"} {
					if !strings.Contains(prompt[1].Content, fragment) {
						t.Fatalf("missing shared rule: %s", fragment)
					}
				}
				var envelope struct {
					Snippets []customerservice.KnowledgeSnippet `json:"snippets"`
				}
				if err := json.Unmarshal([]byte(referenceJSON(t, prompt[2].Content)), &envelope); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(envelope.Snippets, knowledge.Snippets) {
					t.Fatal("evidence changed")
				}
				for _, turn := range prompt {
					if strings.Contains(turn.Content, fixture.Criterion) || (fixture.Observed != "" && strings.Contains(turn.Content, fixture.Observed)) {
						t.Fatal("review annotations leaked into model input")
					}
				}
			})
		}
	}
}
