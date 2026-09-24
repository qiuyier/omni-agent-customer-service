package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

type replayHint struct {
	Input          judgeInput   `json:"input"`
	Classification speechOutput `json:"classification"`
}

func replayKey(input judgeInput) string {
	input.Classification = nil
	data, _ := json.Marshal(input)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
func loadReplayHints(path string, cases []sample) (map[string]speechOutput, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		return nil, "", errors.New("invalid_replay_size")
	}
	var entries []replayHint
	if strict(data, &entries) != nil || len(entries) != len(cases) {
		return nil, "", errors.New("invalid_replay_hints")
	}
	hints := map[string]speechOutput{}
	for _, entry := range entries {
		if len(entry.Input.Classification) != 0 {
			return nil, "", errors.New("unexpected_input_hints")
		}
		key := replayKey(entry.Input)
		if _, exists := hints[key]; exists {
			return nil, "", errors.New("duplicate_replay_input")
		}
		if _, err := speechLabels(entry.Input.Answer, entry.Classification.Segments); err != nil {
			return nil, "", errors.New("invalid_replay_classification")
		}
		hints[key] = entry.Classification
	}
	for _, c := range cases {
		if _, exists := hints[replayKey(judgeInput{Reference: c.Reference, History: c.History, Answer: c.Answer})]; !exists {
			return nil, "", errors.New("replay_input_mismatch")
		}
	}
	return hints, fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

// Neither arm reclassifies. Both use identical primary/review prompts; only
// classification_hint in the primary user payload differs. Review is unhinted.
func withReplayedHints(primary, reviewer judgeFunc, hints map[string]speechOutput, omit bool) judgeFunc {
	return func(ctx context.Context, input judgeInput) (string, error) {
		input.Classification = nil
		h, ok := hints[replayKey(input)]
		if !ok {
			return "", inPhase("classification", failure("replay_lookup", "replay_input_mismatch", nil))
		}
		if trace, ok := ctx.Value(classificationTraceKey{}).(*classificationTrace); ok {
			trace.Output = &h
		}
		enriched := input
		if !omit {
			enriched.Classification = h.Segments
		}
		first := func(ctx context.Context, _ judgeInput) (string, error) { return primary(ctx, enriched) }
		return withSpecificityReview(first, reviewer)(ctx, input)
	}
}
func liveReplayJudge(hints map[string]speechOutput, omit bool) (judgeFunc, string, error) {
	primary, model, err := liveJudgeForPrompt(assistedPrompt)
	if err != nil {
		return nil, "", err
	}
	reviewer, other, err := liveJudgeForPrompt(specificityPrompt)
	if err != nil {
		return nil, "", err
	}
	if model != other {
		return nil, "", errors.New("inconsistent model configuration")
	}
	return withReplayedHints(primary, reviewer, hints, omit), model, nil
}
