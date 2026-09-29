package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ptr(v int) *int { return &v }
func sample() snapshot {
	return snapshot{Schema: 1, Source: "test", Complete: true, Count: 1, Exported: time.Now().UTC(), Articles: []article{{ID: 1, CategoryID: 1, Title: "帮助", Content: "&lt;p&gt;已知说明&lt;/p&gt;", Status: ptr(1), Hide: ptr(0), CategoryStatus: ptr(1), CategoryHidden: ptr(0), CategoryDeleted: ptr(0)}}}
}
func encode(t *testing.T, s snapshot) []byte {
	t.Helper()
	b, e := json.Marshal(s)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func apply(t *testing.T, root string, s snapshot, marker string, empty bool) error {
	t.Helper()
	es, m, e := transform(encode(t, s), "test", marker, nil, time.Now().UTC())
	if e != nil {
		return e
	}
	_, e = publish(root, es, m, empty)
	return e
}
func current(t *testing.T, r string) []byte {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(r, "current", "faq.json"))
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestAddUpdateWithdrawAndRecovery(t *testing.T) {
	r := t.TempDir()
	s := sample()
	if e := apply(t, r, s, "", false); e != nil {
		t.Fatal(e)
	}
	first := current(t, r)
	if !bytes.Contains(first, []byte("已知说明")) {
		t.Fatal("missing addition")
	}
	s.Exported = s.Exported.Add(time.Second)
	s.Articles[0].Content = "新的说明"
	if e := apply(t, r, s, "", false); e != nil {
		t.Fatal(e)
	}
	second := current(t, r)
	if bytes.Equal(first, second) {
		t.Fatal("update not published")
	}
	broken := s
	broken.Complete = false
	if e := apply(t, r, broken, "", false); e == nil {
		t.Fatal("accepted incomplete")
	}
	if !bytes.Equal(second, current(t, r)) {
		t.Fatal("failure changed current")
	}
	s.Exported = s.Exported.Add(time.Second)
	s.Articles[0].Hide = ptr(1)
	if e := apply(t, r, s, "", false); e == nil {
		t.Fatal("empty withdrawal needs opt-in")
	}
	if e := apply(t, r, s, "", true); e != nil {
		t.Fatal(e)
	}
	if strings.TrimSpace(string(current(t, r))) != "[]" {
		t.Fatal("hidden article remained")
	}
	s.Exported = s.Exported.Add(time.Second)
	s.Articles[0].Hide = ptr(0)
	if e := apply(t, r, s, "", false); e != nil {
		t.Fatal(e)
	}
	s.Exported = s.Exported.Add(time.Second)
	s.Articles = []article{}
	s.Count = 0
	if e := apply(t, r, s, "", true); e != nil {
		t.Fatal(e)
	}
	if strings.TrimSpace(string(current(t, r))) != "[]" {
		t.Fatal("deleted article remained")
	}
}
func TestFiltersAndExplicitFixtures(t *testing.T) {
	for _, field := range []string{"status", "hide", "category_status", "category_hidden", "category_deleted", "fixture"} {
		t.Run(field, func(t *testing.T) {
			s := sample()
			switch field {
			case "status":
				s.Articles[0].Status = ptr(0)
			case "hide":
				s.Articles[0].Hide = ptr(1)
			case "category_status":
				s.Articles[0].CategoryStatus = ptr(0)
			case "category_hidden":
				s.Articles[0].CategoryHidden = ptr(1)
			case "category_deleted":
				s.Articles[0].CategoryDeleted = ptr(1)
			case "fixture":
				s.Articles[0].Marker = "omni-fixture-test"
			}
			es, _, e := transform(encode(t, s), "test", "", nil, time.Now())
			if e != nil || len(es) != 0 {
				t.Fatal("filter", e)
			}
		})
	}
	s := sample()
	s.Articles[0].Hide = ptr(1)
	s.Articles[0].Marker = "omni-fixture-test"
	r := t.TempDir()
	if e := apply(t, r, s, "omni-fixture-test", false); e != nil {
		t.Fatal(e)
	}
	if e := apply(t, r, s, "", true); e == nil {
		t.Fatal("mixed test/normal output")
	}
}
func TestRejectMalformedAndOldSnapshots(t *testing.T) {
	for _, f := range []func(*snapshot){func(s *snapshot) { s.Source = "other" }, func(s *snapshot) { s.Count = 0 }, func(s *snapshot) { s.Articles[0].Hide = nil }, func(s *snapshot) { s.Articles[0].Status = ptr(3) }, func(s *snapshot) { s.Articles = append(s.Articles, s.Articles[0]); s.Count = 2 }, func(s *snapshot) { s.Exported = time.Now().Add(-25 * time.Hour) }, func(s *snapshot) { s.Articles[0].Content = "<script>bad</script>" }} {
		s := sample()
		f(&s)
		if _, _, e := transform(encode(t, s), "test", "", nil, time.Now()); e == nil {
			t.Fatal("accepted invalid snapshot")
		}
	}
	r := t.TempDir()
	s := sample()
	if e := apply(t, r, s, "", false); e != nil {
		t.Fatal(e)
	}
	s.Exported = s.Exported.Add(-time.Minute)
	if e := apply(t, r, s, "", false); e == nil {
		t.Fatal("accepted older generation")
	}
}
func TestHTMLAndLock(t *testing.T) {
	got, e := plain(`&lt;p&gt;说明&lt;/p&gt;&lt;script&gt;隐藏指令&lt;/script&gt;<style>x</style><a href="https://invalid.example">链接文本</a>`)
	if e != nil || got != "说明 链接文本" {
		t.Fatal(got, e)
	}
	r := t.TempDir()
	s := sample()
	if e = apply(t, r, s, "", false); e != nil {
		t.Fatal(e)
	}
	before := current(t, r)
	if e = os.Mkdir(filepath.Join(r, ".sync-lock"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = apply(t, r, s, "", false); e == nil {
		t.Fatal("ignored lock")
	}
	if !bytes.Equal(before, current(t, r)) {
		t.Fatal("lock changed output")
	}
}
func TestCLI(t *testing.T) {
	r := t.TempDir()
	input := filepath.Join(r, "input.json")
	if e := os.WriteFile(input, encode(t, sample()), 0600); e != nil {
		t.Fatal(e)
	}
	var out bytes.Buffer
	if e := run([]string{"-input", input, "-source-id", "test", "-output", filepath.Join(r, "out")}, &out); e != nil {
		t.Fatal(e)
	}
	var m metadata
	if e := json.Unmarshal(out.Bytes(), &m); e != nil || m.Count != 1 || m.InputHash == "" || m.FAQHash == "" {
		t.Fatal("missing receipt", e)
	}
}
