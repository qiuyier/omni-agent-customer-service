package faq

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

func TestLoadReadsOnceAndAcceptsExactFileLimit(t *testing.T) {
	data, err := json.Marshal([]Entry{testEntry("returns", "退货")})
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte(strings.Repeat(" ", maxFileBytes-len(data)))...)
	path := filepath.Join(t.TempDir(), "faq.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	retriever, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	results, err := retriever.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: "退货", Limit: 1})
	if err != nil || len(results) != 1 || results[0].ID != "returns" {
		t.Fatalf("loaded retrieval results = %+v, error = %v", results, err)
	}
}

func TestDemoFAQFixtureLoads(t *testing.T) {
	retriever, err := Load(filepath.Join("..", "..", "..", "examples", "knowledge", "faq.json"))
	if err != nil {
		t.Fatal(err)
	}
	results, err := retriever.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: "人工客服几点上班？", Limit: 3})
	if err != nil || len(results) != 1 || results[0].ID != "demo-support-hours" {
		t.Fatalf("demo results = %#v, error = %v", results, err)
	}
	if !strings.Contains(results[0].Content, "虚构") {
		t.Fatal("demo reference must identify itself as fictional")
	}
}

func TestLoadRejectsInvalidFilesWithoutLeakingData(t *testing.T) {
	valid := `[{"id":"returns","title":"退货","source":"faq","content":"仅供测试","keywords":["退货"]}]`
	tests := []struct {
		name string
		data string
	}{
		{"empty file", ""},
		{"empty array", "[]"},
		{"null", "null"},
		{"object", "{}"},
		{"malformed JSON", `[{"id":private-data-marker}]`},
		{"unknown field", strings.Replace(valid, `"id":`, `"private-data-marker":true,"id":`, 1)},
		{"invalid entry", strings.Replace(valid, `"title":"退货"`, `"title":""`, 1)},
		{"null entry", "[null]"},
		{"invalid UTF8", string([]byte{'[', '"', 0xff, '"', ']'})},
		{"trailing array", valid + " []"},
		{"trailing token", valid + " private-data-marker"},
		{"oversized", strings.Repeat(" ", maxFileBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private-path-marker.json")
			if err := os.WriteFile(path, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if !errors.Is(err, ErrInvalidData) {
				t.Fatalf("Load error = %v; want ErrInvalidData", err)
			}
			if strings.Contains(err.Error(), "private-data-marker") || strings.Contains(err.Error(), "private-path-marker") {
				t.Fatalf("Load error leaked source details: %v", err)
			}
		})
	}
	for _, path := range []string{" ", t.TempDir(), filepath.Join(t.TempDir(), "private-path-marker.json")} {
		_, err := Load(path)
		if !errors.Is(err, ErrInvalidData) || strings.Contains(err.Error(), "private-path-marker") {
			t.Fatalf("invalid path error = %v", err)
		}
	}
}
