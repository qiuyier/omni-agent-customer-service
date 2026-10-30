package faq

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVersionMatchesFileContract(t *testing.T) {
	data := []byte(`[{"id":"hours","title":"Hours","source":"fictional","content":"test only","keywords":["hours"]}]`)
	path := filepath.Join(t.TempDir(), "faq.json")
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	r, id, e := ParseVersion(data)
	if e != nil {
		t.Fatal(e)
	}
	f, fid, e := LoadVersion(path)
	if e != nil || id != fid || len(r.ReferenceIDs()) != len(f.ReferenceIDs()) {
		t.Fatal("file/byte contract drift")
	}
	if _, _, e = ParseVersion([]byte("[]")); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{"", "null", "{}", "[null]", string(data) + " null", strings.Repeat(" ", (1<<20)+1)} {
		if _, _, e = ParseVersion([]byte(bad)); e == nil {
			t.Fatal("accepted invalid import")
		}
	}
}
