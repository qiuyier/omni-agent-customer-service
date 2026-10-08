package evaluation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadStrictCases(t *testing.T) {
	valid := `[{"id":"one","query":"question","expected_ids":[]}]`
	for name, input := range map[string]string{
		"valid":           valid,
		"empty":           `[]`,
		"null":            `null`,
		"object":          `{}`,
		"unknown field":   `[{"id":"one","query":"q","expected_ids":[],"typo":1}]`,
		"missing label":   `[{"id":"one","query":"q"}]`,
		"null label":      `[{"id":"one","query":"q","expected_ids":null}]`,
		"duplicate field": `[{"id":"one","query":"q","expected_ids":["a"],"expected_ids":[]}]`,
		"duplicate label": `[{"id":"one","query":"q","expected_ids":["a","a"]}]`,
		"blank label":     `[{"id":"one","query":"q","expected_ids":[" "]}]`,
		"blank query":     `[{"id":"one","query":" ","expected_ids":[]}]`,
		"control query":   `[{"id":"one","query":"\u001b[31m","expected_ids":[]}]`,
		"long query":      `[{"id":"one","query":"` + strings.Repeat("中", 513) + `","expected_ids":[]}]`,
		"long note":       `[{"id":"one","query":"q","expected_ids":[],"note":"` + strings.Repeat("a", 1025) + `"}]`,
		"duplicate case":  `[{"id":"one","query":"q","expected_ids":[]},{"id":"one","query":"q","expected_ids":[]}]`,
		"trailing data":   valid + `{}`,
		"invalid UTF8":    string([]byte{'[', 0xff, ']'}),
		"oversized":       strings.Repeat(" ", (1<<20)+1),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cases.json")
			if err := os.WriteFile(path, []byte(input), 0600); err != nil {
				t.Fatal(err)
			}
			cases, err := Load(path)
			if name == "valid" {
				if err != nil || len(cases) != 1 || cases[0].ExpectedIDs == nil {
					t.Fatalf("cases = %+v, err = %v", cases, err)
				}
			} else if err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	for _, path := range []string{"", t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		if _, err := Load(path); err == nil {
			t.Fatal("invalid path accepted")
		}
	}
}

// Labels must reference the paired FAQ, but do not pin today's quality scores:
// better retrieval should improve the evaluation, not break the unit tests.
func TestExampleLabelsReferenceExistingFAQ(t *testing.T) {
	cases, err := Load("../../../examples/regression/knowledge/eval_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../../examples/knowledge/faq.json")
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, e := range entries {
		known[e.ID] = true
	}
	positive, negative := 0, 0
	for _, c := range cases {
		if len(c.ExpectedIDs) == 0 {
			negative++
		} else {
			positive++
		}
		for _, id := range c.ExpectedIDs {
			if !known[id] {
				t.Fatalf("case %q references unknown ID %q", c.ID, id)
			}
		}
	}
	if len(cases) != 20 || positive == 0 || negative == 0 {
		t.Fatalf("expected 20 examples including positive and negative labels, got %d", len(cases))
	}
}
