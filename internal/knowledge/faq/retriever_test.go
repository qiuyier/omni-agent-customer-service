package faq

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

func testEntry(id string, keywords ...string) Entry {
	return Entry{
		ID: id, Title: "退货说明", Source: "policy/returns", Content: "这是测试政策，不代表真实企业承诺。",
		Keywords: keywords,
	}
}

func TestRetrieveKeywordRanking(t *testing.T) {
	entries := []Entry{
		testEntry("first", "退货", " RETURN ", "return"),
		testEntry("second", "退货", "条件", "Return"),
		testEntry("third", "退货", "Return"),
		testEntry("fourth", "物流"),
	}
	retriever, err := New(entries)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		text  string
		limit int
		want  []string
	}{
		{"Chinese and English score", "请问退货条件是什么，RETURN POLICY?", 3, []string{"second", "first", "third"}},
		{"stable tie order", "退货", 3, []string{"first", "second", "third"}},
		{"case insensitive", "rEtUrN", 2, []string{"first", "second"}},
		{"one result", "退货条件", 1, []string{"second"}},
		{"separate topic", "我的物流到哪了", 3, []string{"fourth"}},
		{"no matches", "优惠券怎么使用", 3, []string{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			results, err := retriever.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: test.text, Limit: test.limit})
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(results))
			for _, result := range results {
				got = append(got, result.ID)
				if result.Title != entries[0].Title || result.Source != entries[0].Source || result.Content != entries[0].Content {
					t.Fatalf("lost reference fields: %+v", result)
				}
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("IDs = %v; want %v", got, test.want)
			}
		})
	}
}

func TestNewValidatesDataset(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Entry)
	}{
		{"blank ID", func(e *Entry) { e.ID = " " }},
		{"blank title", func(e *Entry) { e.Title = "\t" }},
		{"blank source", func(e *Entry) { e.Source = "" }},
		{"blank content", func(e *Entry) { e.Content = "\n" }},
		{"long ID", func(e *Entry) { e.ID = strings.Repeat("中", maxIDRunes+1) }},
		{"long title", func(e *Entry) { e.Title = strings.Repeat("中", maxTitleRunes+1) }},
		{"long source", func(e *Entry) { e.Source = strings.Repeat("中", maxSourceRunes+1) }},
		{"long content", func(e *Entry) { e.Content = strings.Repeat("中", customerservice.MaxKnowledgeContextRunes+1) }},
		{"invalid UTF8", func(e *Entry) { e.Content = string([]byte{0xff}) }},
		{"control in content", func(e *Entry) { e.Content = "line\x00next" }},
		{"multiline metadata", func(e *Entry) { e.Title = "line\nnext" }},
		{"no keywords", func(e *Entry) { e.Keywords = nil }},
		{"blank keyword", func(e *Entry) { e.Keywords = []string{"退货", " "} }},
		{"invalid keyword", func(e *Entry) { e.Keywords = []string{string([]byte{0xff})} }},
		{"long keyword", func(e *Entry) { e.Keywords = []string{strings.Repeat("中", maxKeywordRunes+1)} }},
		{"too many keywords", func(e *Entry) { e.Keywords = make([]string, maxKeywords+1) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := testEntry("entry", "退货")
			test.edit(&entry)
			if _, err := New([]Entry{entry}); !errors.Is(err, ErrInvalidData) {
				t.Fatalf("New error = %v; want ErrInvalidData", err)
			}
		})
	}
	for _, entries := range [][]Entry{nil, {}, {testEntry("same", "退货"), testEntry(" same ", "物流")}} {
		if _, err := New(entries); !errors.Is(err, ErrInvalidData) {
			t.Fatalf("New error = %v; want empty/duplicate data rejection", err)
		}
	}
	entries := make([]Entry, maxEntries+1)
	for i := range entries {
		entries[i] = testEntry(fmt.Sprintf("entry-%d", i), "退货")
	}
	if _, err := New(entries); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("too many entries error = %v", err)
	}
	if _, err := New(entries[:maxEntries]); err != nil {
		t.Fatalf("max entries must be accepted: %v", err)
	}
}

func TestNewAcceptsLimitsAndTrimsFields(t *testing.T) {
	entry := testEntry(" id ", " 退货 ")
	entry.Title = strings.Repeat("中", maxTitleRunes)
	entry.Source = strings.Repeat("中", maxSourceRunes)
	// Trimmed ID has 2 runes; all four snippet fields together fill exactly
	// the 6000-rune application budget, including these three content runes.
	entry.Content = strings.Repeat("中", customerservice.MaxKnowledgeContextRunes-2-maxTitleRunes-maxSourceRunes-3) + "\n\t中"
	retriever, err := New([]Entry{entry})
	if err != nil {
		t.Fatal(err)
	}
	results, err := retriever.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: " 退货 ", Limit: 1})
	if err != nil || len(results) != 1 || results[0].ID != "id" || results[0].Content != entry.Content {
		t.Fatalf("results = %+v, error = %v", results, err)
	}
	entry.Content += "中"
	if _, err := New([]Entry{entry}); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("6001-rune combined snippet error = %v; want ErrInvalidData", err)
	}
}

func TestRetrieveValidatesQuery(t *testing.T) {
	retriever, err := New([]Entry{testEntry("entry", "退货")})
	if err != nil {
		t.Fatal(err)
	}
	queries := []customerservice.KnowledgeQuery{
		{Text: " ", Limit: 1},
		{Text: strings.Repeat("中", customerservice.MaxKnowledgeQueryRunes+1), Limit: 1},
		{Text: string([]byte{0xff}), Limit: 1},
		{Text: "退货\x00", Limit: 1},
		{Text: "退货", Limit: 0},
		{Text: "退货", Limit: -1},
		{Text: "退货", Limit: customerservice.MaxKnowledgeSnippets + 1},
	}
	for _, query := range queries {
		if _, err := retriever.Retrieve(context.Background(), query); !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("query validation error = %v", err)
		}
	}
	query := customerservice.KnowledgeQuery{Text: "退货", Limit: 1}
	if _, err := retriever.Retrieve(nil, query); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("nil context error = %v", err)
	}
	for _, uninitialized := range []*Retriever{nil, {}} {
		if _, err := uninitialized.Retrieve(context.Background(), query); !errors.Is(err, ErrInvalidData) {
			t.Fatalf("uninitialized retriever error = %v", err)
		}
	}
	query.Text = strings.Repeat("中", customerservice.MaxKnowledgeQueryRunes)
	if _, err := retriever.Retrieve(context.Background(), query); err != nil {
		t.Fatalf("max query length rejected: %v", err)
	}
}

func TestRetrieveCancellation(t *testing.T) {
	retriever, err := New([]Entry{testEntry("one", "退货"), testEntry("two", "退货")})
	if err != nil {
		t.Fatal(err)
	}
	query := customerservice.KnowledgeQuery{Text: "退货", Limit: 1}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := retriever.Retrieve(canceled, query); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if _, err := retriever.Retrieve(expired, query); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired error = %v", err)
	}
	duringScan := &cancelOnCheckContext{Context: context.Background(), remaining: 3}
	if results, err := retriever.Retrieve(duringScan, query); !errors.Is(err, context.Canceled) || results != nil {
		t.Fatalf("scan cancellation results = %v, error = %v", results, err)
	}
}

// cancelOnCheckContext deterministically cancels between entries, avoiding
// scheduler-dependent timing or sleeps in cancellation coverage.
type cancelOnCheckContext struct {
	context.Context
	remaining int
}

func (c *cancelOnCheckContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestRetrieverOwnsSnapshotAndReturnedValues(t *testing.T) {
	entries := []Entry{testEntry("entry", "退货")}
	retriever, err := New(entries)
	if err != nil {
		t.Fatal(err)
	}
	query := customerservice.KnowledgeQuery{Text: "退货", Limit: 1}
	wantContent := entries[0].Content
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 50 {
				results, err := retriever.Retrieve(context.Background(), query)
				if err != nil || len(results) != 1 || results[0].ID != "entry" || results[0].Content != wantContent {
					t.Errorf("immutable results = %+v, error = %v", results, err)
					return
				}
				results[0].ID = "modified-result"
				results[0].Content = "modified-result"
			}
		})
	}
	for range 100 {
		entries[0].ID = "modified-source"
		entries[0].Content = "modified-source"
		entries[0].Keywords[0] = "物流"
	}
	workers.Wait()
}
