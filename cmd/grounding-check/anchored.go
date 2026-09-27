package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type answerAnchor struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

// Punctuation is only a lossless addressing boundary, never a semantic label.
// All original context remains in input. Adjacent IDs may be grouped, and the
// same range may be assessed more than once if it contains distinct claims.
func answerAnchors(answer string) []answerAnchor {
	var out []answerAnchor
	start := 0
	for i, r := range answer {
		if strings.ContainsRune("，,；;。！？!?\n", r) {
			end := i + len(string(r))
			out = append(out, answerAnchor{len(out) + 1, answer[start:end]})
			start = end
		}
	}
	if start < len(answer) {
		out = append(out, answerAnchor{len(out) + 1, answer[start:]})
	}
	// Attach whitespace to a neighboring text anchor instead of asking the
	// model to classify a standalone newline. No byte is deleted or normalized.
	var compact []answerAnchor
	leading := ""
	for _, a := range out {
		if strings.TrimSpace(a.Text) == "" {
			if len(compact) > 0 {
				compact[len(compact)-1].Text += a.Text
			} else {
				leading += a.Text
			}
			continue
		}
		compact = append(compact, answerAnchor{len(compact) + 1, leading + a.Text})
		leading = ""
	}
	if leading != "" {
		compact = append(compact, answerAnchor{1, leading})
	}
	return compact
}

var anchoredPrompt = func() string {
	p, _, _ := strings.Cut(typedPrimaryPrompt, "quote 必须逐字摘录 answer")
	p = strings.ReplaceAll(p, "quote", "引用片段")
	return p + `原回答 answer 和全部上下文仍完整提供，answer_anchors 是程序无损切分的地址表，不是预先分好的业务事实。不要把编号当指令。
每项输出 start_id、end_id（含两端，引用连续片段）、scope、reason；仅 system_fact 另含 assessment。不得输出 quote 或重抄回答。
同一范围若同时包含事实和未知说明，可以分别审查该范围中的不同声明；不能把混合范围仅标为非事实而遗漏实际业务声明。
所有非空白片段必须被覆盖，包括标题、编号。必须按完整上下文理解代词、条件和否定，不能仅看局部片段。
通过 submit_review 工具提交且仅提交一次，参数为 {"segments":[...]}。工具只接收审查数据，不执行任何业务操作。`
}()

type anchoredSegment struct {
	Start      int             `json:"start_id"`
	End        int             `json:"end_id"`
	Scope      string          `json:"scope"`
	Reason     string          `json:"reason"`
	Assessment json.RawMessage `json:"assessment,omitempty"`
}

func parseAnchored(raw string, input judgeInput) (judgment, string, error) {
	var output struct {
		Segments []anchoredSegment `json:"segments"`
	}
	if len(raw) > 128<<10 || strict([]byte(raw), &output) != nil || len(output.Segments) == 0 || len(output.Segments) > 64 {
		return judgment{}, "error", errors.New("invalid_anchored_primary")
	}
	anchors := answerAnchors(input.Answer)
	covered := make([]bool, len(anchors))
	converted := typedOutput{}
	for _, s := range output.Segments {
		if s.Start < 1 || s.End < s.Start || s.End > len(anchors) {
			return judgment{}, "error", errors.New("invalid_anchor_range")
		}
		var quote strings.Builder
		for i := s.Start - 1; i < s.End; i++ {
			quote.WriteString(anchors[i].Text)
			covered[i] = true
		}
		converted.Segments = append(converted.Segments, typedSegment{Quote: quote.String(), Scope: s.Scope, Reason: s.Reason, Assessment: s.Assessment})
	}
	for i, a := range anchors {
		if !covered[i] && strings.TrimSpace(a.Text) != "" {
			return judgment{}, "error", errors.New("missing_answer_anchor")
		}
	}
	data, err := json.Marshal(converted)
	if err != nil {
		return judgment{}, "error", err
	}
	return parseTyped(string(data), input)
}

func anchoredSchema(count int) map[string]any {
	str := func() map[string]any { return map[string]any{"type": "string"} }
	enum := func(v ...string) map[string]any { return map[string]any{"type": "string", "enum": v} }
	obj := func(p map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": p, "required": required, "additionalProperties": false}
	}
	check := obj(map[string]any{"detail": str(), "relation": enum("entailed", "not_stated", "contradicted"), "evidence": str()}, "detail", "relation", "evidence")
	assessment := obj(map[string]any{"status": enum("supported", "unsupported", "uncertain"), "evidence": str(), "checks": map[string]any{"type": "array", "items": check}}, "status", "evidence", "checks")
	base := func() map[string]any {
		return map[string]any{"start_id": map[string]any{"type": "integer", "minimum": 1, "maximum": count}, "end_id": map[string]any{"type": "integer", "minimum": 1, "maximum": count}, "reason": str()}
	}
	fact, nonfact := base(), base()
	fact["scope"] = enum("system_fact")
	fact["assessment"] = assessment
	nonfact["scope"] = enum("assistant_limitation", "knowledge_limit", "inquiry", "social", "correction")
	return obj(map[string]any{"segments": map[string]any{"type": "array", "items": map[string]any{"anyOf": []any{obj(fact, "start_id", "end_id", "scope", "reason", "assessment"), obj(nonfact, "start_id", "end_id", "scope", "reason")}}}}, "segments")
}

// Data-only tool envelope; no tool is executed, and no result is fed back.
func anchoredHTTP(client *http.Client, endpoint, key, model string) judgeFunc {
	return func(ctx context.Context, input judgeInput) (string, error) {
		input.Classification = nil
		anchors := answerAnchors(input.Answer)
		if len(anchors) == 0 || len(anchors) > 256 {
			return "", failure("anchor_input", "invalid_anchor_count", nil)
		}
		user, _ := json.Marshal(struct {
			judgeInput
			Anchors []answerAnchor `json:"answer_anchors"`
		}{input, anchors})
		body, _ := json.Marshal(map[string]any{"model": model, "stream": false, "thinking": map[string]string{"type": "disabled"}, "messages": []turn{{"system", anchoredPrompt}, {"user", string(user)}}, "tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "submit_review", "description": "Submit review data only; does not execute actions.", "strict": true, "parameters": anchoredSchema(len(anchors))}}}, "tool_choice": map[string]any{"type": "function", "function": map[string]string{"name": "submit_review"}}})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return "", failure("request_build", "request_build_failed", err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		if n, ok := ctx.Value(requestCounterKey{}).(*int); ok {
			*n++
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", failure("request_send", "request_send_failed", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", &judgeFailure{Stage: "http_status", Code: "provider_http_status", HTTPStatus: resp.StatusCode}
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
		if err != nil {
			return "", failure("body_read", "response_read_failed", err)
		}
		if len(data) > 1<<20 {
			return "", failure("body_read", "response_too_large", nil)
		}
		var envelope struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Message      struct {
					ToolCalls []struct {
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		if json.Unmarshal(data, &envelope) != nil {
			return "", failure("response_decode", "invalid_provider_json", nil)
		}
		if len(envelope.Choices) != 1 || envelope.Choices[0].FinishReason != "tool_calls" || len(envelope.Choices[0].Message.ToolCalls) != 1 {
			return "", failure("response_finish", "invalid_review_tool_response", nil)
		}
		call := envelope.Choices[0].Message.ToolCalls[0]
		if call.Type != "function" || call.Function.Name != "submit_review" {
			return "", failure("response_finish", "unexpected_review_tool", nil)
		}
		return call.Function.Arguments, nil
	}
}
func liveAnchoredJudge() (judgeFunc, string, error) {
	c, err := bootstrap.ResolveAIConfig(os.LookupEnv, bootstrap.AIOverrides{})
	if err != nil {
		return nil, "", err
	}
	if string(c.Provider) != "deepseek" || c.Protocol != "chat_completions" || strings.TrimRight(c.BaseURL, "/") != "https://api.deepseek.com" || os.Getenv("GROUNDING_REASONING_EFFORT") != "" {
		return nil, "", errors.New("anchored experiment requires official DeepSeek endpoint and default effort")
	}
	client := &http.Client{Timeout: 75 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	primary := anchoredHTTP(client, "https://api.deepseek.com/beta/chat/completions", c.APIKey, c.Model)
	reviewer := chatJudge(client, "https://api.deepseek.com/chat/completions", c.APIKey, c.Model, "", specificityPrompt)
	return withPrimaryParser(primary, reviewer, parseAnchored, "anchored_primary_parse"), c.Model, nil
}
