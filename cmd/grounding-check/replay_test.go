package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
)

const replayCorpus = "../../examples/regression/knowledge/webchat/grounding_hint_comparison_cases.json"
const replayFixture = "../../examples/regression/knowledge/webchat/grounding_hint_comparison_hints.json"

func TestReplayFixturesAndOffline(t *testing.T) {
	cases, _, err := load(replayCorpus)
	if err != nil || len(cases) != 5 {
		t.Fatalf("cases: %v", err)
	}
	hints, digest, err := loadReplayHints(replayFixture, cases)
	if err != nil || len(hints) != 5 || len(digest) != 64 {
		t.Fatalf("hints: %v", err)
	}
	cases[0].Reference += "different"
	if _, _, err := loadReplayHints(replayFixture, cases); err == nil {
		t.Fatal("changed reference accepted")
	}
	for _, omit := range []bool{false, true} {
		var out bytes.Buffer
		args := []string{"-cases", replayCorpus, "-replay-hints", replayFixture}
		if omit {
			args = append(args, "-omit-replayed-hints")
		}
		if err := run(context.Background(), args, &out, io.Discard, func() (judgeFunc, string, error) { t.Fatal("offline constructed live judge"); return nil, "", nil }); err != nil {
			t.Fatal(err)
		}
		var r report
		if json.Unmarshal(out.Bytes(), &r) != nil || r.SchemaVersion != 7 || r.Prompt != assistedPrompt || r.ReplayHintsSHA256 != digest || r.Calls != 0 {
			t.Fatal("bad plan")
		}
		if (r.HintDelivery == "omitted") != omit {
			t.Fatal("incorrect hint arm")
		}
	}
}
func TestReplayOnlyChangesHintAndNeverHintsReviewer(t *testing.T) {
	input := judgeInput{Reference: "资料", Answer: "回答", History: []turn{{Role: "user", Content: "问题"}}}
	hints := map[string]speechOutput{replayKey(input): {Segments: []speechSegment{{Quote: "回答", Act: "uncertainty", Reason: "固定提示"}}}}
	for _, omit := range []bool{false, true} {
		calls := 0
		primary := func(_ context.Context, got judgeInput) (string, error) {
			calls++
			hasHints := len(got.Classification) > 0
			if hasHints == omit {
				t.Fatal("wrong arm")
			}
			got.Classification = nil
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(input)
			if !bytes.Equal(a, b) {
				t.Fatal("original data changed")
			}
			return `{"claims":[],"advice":[{"quote":"回答","advice_kind":"none","reason":"未知"}]}`, nil
		}
		review := func(_ context.Context, got judgeInput) (string, error) {
			calls++
			if len(got.Classification) > 0 || got.Answer != input.Answer {
				t.Fatal("review contaminated")
			}
			return `{"findings":[],"uncertain":false}`, nil
		}
		trace := &classificationTrace{}
		ctx := context.WithValue(context.Background(), classificationTraceKey{}, trace)
		raw, err := withReplayedHints(primary, review, hints, omit)(ctx, input)
		if _, v, e := parse(raw, input); err != nil || e != nil || v != "clear" || calls != 2 || trace.Output == nil {
			t.Fatalf("%s %v %v", v, err, e)
		}
	}
}
func TestReplayConflictingFlags(t *testing.T) {
	for _, args := range [][]string{{"-omit-replayed-hints"}, {"-classify-only", "-replay-hints", "x"}, {"-with-classification", "-replay-hints", "x"}} {
		if err := run(context.Background(), args, io.Discard, io.Discard, nil); err == nil {
			t.Fatal("invalid flags accepted")
		}
	}
}
