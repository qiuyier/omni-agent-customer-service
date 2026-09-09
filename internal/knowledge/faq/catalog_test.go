package faq

import "testing"

func TestReferenceIDsOwnsCopy(t *testing.T) {
	r, err := New([]Entry{testEntry("one", "first"), testEntry("two", "second")})
	if err != nil {
		t.Fatal(err)
	}
	ids := r.ReferenceIDs()
	if len(ids) != 2 || ids[0] != "one" || ids[1] != "two" {
		t.Fatalf("IDs = %v", ids)
	}
	ids[0] = "changed"
	if r.ReferenceIDs()[0] != "one" {
		t.Fatal("returned IDs mutate retriever")
	}
	refs := r.Snippets()
	refs[0].Content = "changed"
	if r.Snippets()[0].Content == "changed" {
		t.Fatal("returned snippets mutate retriever")
	}
}
