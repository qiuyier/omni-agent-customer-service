package managehttp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"net/http"
	"testing"
)

func TestPublishedSnapshotAndPreviewConflict(t *testing.T) {
	repo := &memory{}
	h, _ := New(repo, func(_ context.Context, r *http.Request, p manage.Permission) (string, error) {
		if r.Header.Get("X-Test-Session") == "admin" || r.Header.Get("X-Test-Session") == "reader" && p == manage.Read {
			return "fixture", nil
		}
		return "", manage.ErrDenied
	}, "https://admin.example")
	invoke := func(path, body string, code int) {
		t.Helper()
		w := call(h, path, body, "https://admin.example", "admin")
		if w.Code != code {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	snapshot := func() map[string]json.RawMessage {
		t.Helper()
		w := call(h, "/internal/knowledge/state", "", "https://admin.example", "reader")
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		var s map[string]json.RawMessage
		if e := json.Unmarshal(w.Body.Bytes(), &s); e != nil {
			t.Fatal(e)
		}
		return s
	}
	if string(snapshot()["published"]) != "null" {
		t.Fatal("initial publication must be null")
	}
	entries := `[{"id":"hours","title":"Hours","content":"old","source":"fixture","keywords":["hours"]}]`
	invoke("/internal/knowledge/draft", `{"revision":0,"entries":`+entries+`}`, 200)
	invoke("/internal/knowledge/publish", `{"revision":1}`, 200)
	invoke("/internal/knowledge/draft", `{"revision":2,"entries":[]}`, 200)
	s := snapshot()
	var published struct {
		Version string          `json:"version"`
		Entries json.RawMessage `json:"entries"`
	}
	if e := json.Unmarshal(s["published"], &published); e != nil {
		t.Fatal(e)
	}
	var active string
	json.Unmarshal(s["active"], &active)
	if published.Version != active || string(published.Entries) != entries {
		t.Fatalf("published mismatch: %+v", published)
	}
	var revision uint64
	json.Unmarshal(s["revision"], &revision)
	// Preview reads do not write audit or revision. A later save invalidates it.
	snapshot()
	if repo.state.Revision != revision {
		t.Fatal("preview wrote state")
	}
	invoke("/internal/knowledge/draft", fmt.Sprintf(`{"revision":%d,"entries":%s}`, revision, entries), 200)
	invoke("/internal/knowledge/publish", fmt.Sprintf(`{"revision":%d,"allow_empty":true}`, revision), 409)
	if repo.state.Active != active || repo.state.Revision != revision+1 {
		t.Fatal("stale preview changed publication")
	}
	invoke("/internal/knowledge/draft", fmt.Sprintf(`{"revision":%d,"entries":[]}`, revision+1), 200)
	invoke("/internal/knowledge/publish", fmt.Sprintf(`{"revision":%d}`, revision+2), 422)
	invoke("/internal/knowledge/publish", fmt.Sprintf(`{"revision":%d,"allow_empty":true}`, revision+2), 200)
	if w := call(h, "/internal/knowledge/state", "", "https://admin.example", "customer"); w.Code != 403 {
		t.Fatal("customer read publication")
	}
}

func TestMissingActiveSnapshotFailsClosed(t *testing.T) {
	h, _ := New(&memory{state: manage.State{Active: "missing"}}, func(context.Context, *http.Request, manage.Permission) (string, error) { return "admin", nil }, "https://admin.example")
	if w := call(h, "/internal/knowledge/state", "", "https://admin.example", "admin"); w.Code != 503 {
		t.Fatal(w.Code, w.Body)
	}
}
