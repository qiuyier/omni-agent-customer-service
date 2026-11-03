package faq

import (
	"context"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"reflect"
	"testing"
)

func TestExplainPreservesRanking(t *testing.T) {
	r, e := New([]Entry{{"a", "A", "manual", "A", []string{"Help", "HELP"}}, {"b", "B", "manual", "B", []string{"help", "时间"}}, {"c", "C", "manual", "C", []string{"时间"}}, {"d", "D", "manual", "D", []string{"help"}}})
	if e != nil {
		t.Fatal(e)
	}
	q := customerservice.KnowledgeQuery{Text: "HELP 时间", Limit: 3}
	plain, _ := r.Retrieve(context.Background(), q)
	explained, e := r.Explain(context.Background(), q)
	if e != nil {
		t.Fatal(e)
	}
	if len(explained) != 3 {
		t.Fatal(explained)
	}
	for i, m := range explained {
		if m.ID != plain[i].ID || m.Content != plain[i].Content {
			t.Fatal("ranking drift")
		}
	}
	if !reflect.DeepEqual(explained[0].Keywords, []string{"help", "时间"}) || !reflect.DeepEqual(explained[1].Keywords, []string{"help"}) {
		t.Fatal(explained)
	}
	miss, e := r.Explain(context.Background(), customerservice.KnowledgeQuery{Text: "unmatched", Limit: 3})
	if e != nil || len(miss) != 0 {
		t.Fatal(miss, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = r.Explain(ctx, q); e == nil {
		t.Fatal("cancel ignored")
	}
}
