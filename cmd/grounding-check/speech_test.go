package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

const speechCorpus = "../../examples/knowledge/webchat/speech_classification_cases.json"

func TestSpeechLongCorpusPreservesOriginalAnswers(t *testing.T) {
	long, _, err := loadSpeech("../../examples/knowledge/webchat/speech_long_holdout_cases.json")
	if err != nil || len(long) != 9 {
		t.Fatalf("long corpus: %v", err)
	}
	original, _, err := load("../../examples/knowledge/webchat/grounding_advice_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range long[:3] {
		found := false
		for _, old := range original {
			if old.ID != c.ID {
				continue
			}
			found = true
			a, _ := json.Marshal(old.History)
			b, _ := json.Marshal(c.History)
			if c.Answer != old.Answer || c.Reference != old.Reference || !bytes.Equal(a, b) {
				t.Fatalf("modified original %s", c.ID)
			}
		}
		if !found {
			t.Fatalf("missing original %s", c.ID)
		}
	}
}

func TestSpeechCorpusAndOffline(t *testing.T) {
	cases, _, err := loadSpeech(speechCorpus)
	if err != nil || len(cases) != 12 {
		t.Fatalf("corpus: %v", err)
	}
	var out bytes.Buffer
	err = runSpeech(context.Background(), speechCorpus, false, time.Minute, &out, io.Discard, func() (judgeFunc, string, error) { t.Fatal("offline used model"); return nil, "", nil })
	var rep speechReport
	if err != nil || json.Unmarshal(out.Bytes(), &rep) != nil || rep.Calls != 0 || !rep.Complete {
		t.Fatalf("offline: %v", err)
	}
}
func TestSpeechStructureAndGranularity(t *testing.T) {
	answer := "无法确认。请咨询。"
	good := []speechSegment{{"无法确认。", "uncertainty", "未知"}, {"请咨询。", "information_seeking", "求证"}}
	split := []speechSegment{{"无法", "uncertainty", "未知"}, {"确认", "uncertainty", "未知"}, {"请咨询", "information_seeking", "求证"}}
	if !speechMatches(answer, good, split) {
		t.Fatal("equivalent splitting rejected")
	}
	wrong := append([]speechSegment(nil), good...)
	wrong[0].Act = "assertion"
	if speechMatches(answer, good, wrong) {
		t.Fatal("incorrect act accepted")
	}
	for _, raw := range []string{
		`{"segments":[{"quote":"无法确认。","act":"uncertainty","reason":"未知"}]}`,
		`{"segments":[{"quote":"无法确认。请咨询。","act":"unknown","reason":"未知"}]}`,
		`{"segments":[{"quote":"无法确认。请咨询。","act":"uncertainty","reason":"未知","checks":[]}]}`,
		`{"segments":[{"quote":"不存在","act":"uncertainty","reason":"未知"}]}`,
		`{"segments":[]}`, `null`, `{"segments":[]} {}`,
	} {
		if _, err := parseSpeech(raw, answer); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	conflict := append(append([]speechSegment(nil), good...), speechSegment{"无法确认。", "assertion", "冲突"})
	if _, err := speechLabels(answer, conflict); err == nil {
		t.Fatal("conflict accepted")
	}
}
func TestSpeechScoresWithoutLabelsLeaking(t *testing.T) {
	cases, _, _ := loadSpeech(speechCorpus)
	calls := 0
	var out bytes.Buffer
	err := runSpeech(context.Background(), speechCorpus, true, time.Minute, &out, io.Discard, func() (judgeFunc, string, error) {
		return func(_ context.Context, input judgeInput) (string, error) {
			data, _ := json.Marshal(input)
			if strings.Contains(string(data), "expected_segments") || strings.Contains(string(data), cases[calls].ID) {
				t.Fatal("labels leaked")
			}
			expected := append([]speechSegment(nil), cases[calls].Expected...)
			if calls == 0 {
				expected[0].Act = "assertion"
			}
			calls++
			if calls == 2 {
				return "invalid JSON", nil
			}
			raw, _ := json.Marshal(speechOutput{expected})
			return string(raw), nil
		}, "fixture", nil
	})
	var r speechReport
	if err != nil || json.Unmarshal(out.Bytes(), &r) != nil || calls != 12 || !r.Complete || r.Counts["matched"] != 10 || r.Counts["mismatched"] != 1 || r.Counts["error"] != 1 {
		t.Fatalf("bad scores: %s %v", out.String(), err)
	}
}
func TestSpeechCallFailureStopsAndRedacts(t *testing.T) {
	calls := 0
	var out bytes.Buffer
	err := runSpeech(context.Background(), speechCorpus, true, time.Minute, &out, io.Discard, func() (judgeFunc, string, error) {
		return func(context.Context, judgeInput) (string, error) {
			calls++
			return "secret raw", errors.New("secret URL")
		}, "fixture", nil
	})
	var r speechReport
	if err == nil || json.Unmarshal(out.Bytes(), &r) != nil || r.Complete || calls != 1 || r.Counts["error"] != 1 || strings.Contains(out.String(), "secret") {
		t.Fatalf("bad failure: %s", out.String())
	}
	if r.Results[0].Failure.Phase != "classification" {
		t.Fatal("missing phase")
	}
}
