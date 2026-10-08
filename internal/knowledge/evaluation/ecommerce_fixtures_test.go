package evaluation

import (
	"reflect"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/faq"
)

// Synthetic fixture integrity only: do not pin retrieval scores or treat canned
// model answers as evidence of real answer quality.
func TestEcommerceSimulationFixtures(t *testing.T) {
	const root = "../../../examples/regression/knowledge/ecommerce/"
	corpus, err := faq.Load("../../../examples/knowledge/ecommerce/faq.json")
	if err != nil {
		t.Fatal(err)
	}
	known := make(map[string]bool)
	for _, id := range corpus.ReferenceIDs() {
		known[id] = true
	}
	if len(known) != 16 {
		t.Fatalf("FAQ count = %d, want 16", len(known))
	}
	all := make(map[string]Case)
	queries := make(map[string]bool)
	covered := make(map[string]bool)
	for _, split := range []struct {
		file     string
		count    int
		negative int
	}{
		{"eval_cases.json", 20, 2},
		{"eval_holdout.json", 10, 3},
	} {
		cases, err := Load(root + split.file)
		if err != nil {
			t.Fatal(err)
		}
		if len(cases) != split.count {
			t.Fatalf("%s has %d cases, want %d", split.file, len(cases), split.count)
		}
		negative := 0
		for _, c := range cases {
			if _, found := all[c.ID]; found || queries[c.Query] {
				t.Fatalf("development and holdout data overlap at %q", c.ID)
			}
			if c.Note == "" || len(c.ExpectedIDs) > 3 {
				t.Fatalf("case %q needs review criteria and at most 3 references", c.ID)
			}
			all[c.ID], queries[c.Query] = c, true
			if len(c.ExpectedIDs) == 0 {
				negative++
			}
			for _, id := range c.ExpectedIDs {
				if !known[id] {
					t.Fatalf("case %q references unknown FAQ %q", c.ID, id)
				}
				covered[id] = true
			}
		}
		if negative != split.negative {
			t.Fatalf("%s has %d no-reference cases, want %d", split.file, negative, split.negative)
		}
	}
	if len(covered) != len(known) {
		t.Fatal("some FAQ entries have no labelled question")
	}
	replies, err := Load(root + "reply_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 8 {
		t.Fatalf("reply case count = %d, want 8", len(replies))
	}
	for _, c := range replies {
		if original, found := all[c.ID]; !found || !reflect.DeepEqual(c, original) {
			t.Fatalf("reply case %q diverges from its retrieval case", c.ID)
		}
	}
}
