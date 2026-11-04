package managehttp

import (
	"context"
	"encoding/json"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func batchBody(revision int, cases any) string {
	b, _ := json.Marshal(map[string]any{"revision": revision, "cases": cases})
	return string(b)
}
func bc(id, query string, expected any) map[string]any {
	return map[string]any{"id": id, "query": query, "expected_id": expected}
}
func TestBatchComparisonNoWrites(t *testing.T) {
	h, repo := trialFixture(t)
	before := copyState(repo.state)
	w := call(h, "/internal/knowledge/batch", batchBody(7, []any{bc("regression", "hours", "old"), bc("improvement", "hours", "new"), bc("miss", "unrelated", nil)}), "https://admin.example", "reader")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var report struct {
		Revision uint64
		Rows     []batchRow
	}
	if e := json.Unmarshal(w.Body.Bytes(), &report); e != nil {
		t.Fatal(e)
	}
	if report.Revision != 7 || len(report.Rows) != 3 || !report.Rows[0].Regression || report.Rows[1].Regression || report.Rows[1].Draft.Status != "pass" || report.Rows[2].Draft.Status != "pass" {
		t.Fatal(report)
	}
	if !reflect.DeepEqual(repo.state, before) {
		t.Fatal("batch wrote state")
	}
	if strings.Contains(w.Body.String(), "unrelated") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("query echoed or cached")
	}
}
func TestBatchRejectsInvalidAndStale(t *testing.T) {
	h, repo := trialFixture(t)
	before := copyState(repo.state)
	tooMany := []any{}
	for i := 0; i < 51; i++ {
		tooMany = append(tooMany, bc("a", "hours", nil))
	}
	invalid := []any{[]any{}, []any{nil}, []any{map[string]any{"id": "a", "query": "hours"}}, []any{bc("a", "hours", 12)}, []any{bc("a", "hours", "")}, []any{bc("a", "hours", nil), bc("a", "hours", nil)}, tooMany, []any{bc("a", strings.Repeat("问", 513), nil)}}
	for _, c := range invalid {
		if w := call(h, "/internal/knowledge/batch", batchBody(7, c), "https://admin.example", "reader"); w.Code != 400 {
			t.Fatal(w.Code, w.Body)
		}
	}
	if w := call(h, "/internal/knowledge/batch", strings.Repeat(" ", 133<<10), "https://admin.example", "reader"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	body := batchBody(6, []any{bc("a", "hours", nil)})
	if w := call(h, "/internal/knowledge/batch", body, "https://admin.example", "reader"); w.Code != 409 {
		t.Fatal(w.Code)
	}
	for _, pair := range [][2]string{{"", "reader"}, {"https://foreign.example", "reader"}, {"https://admin.example", "customer"}} {
		if w := call(h, "/internal/knowledge/batch", body, pair[0], pair[1]); w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	if !reflect.DeepEqual(repo.state, before) {
		t.Fatal("invalid batch wrote state")
	}
}
func TestBatchAbsentVersionAndRevocation(t *testing.T) {
	h, repo := trialFixture(t)
	repo.state = manage.State{}
	body := batchBody(0, []any{bc("a", "hours", nil)})
	w := call(h, "/internal/knowledge/batch", body, "https://admin.example", "reader")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var report struct{ Rows []batchRow }
	json.Unmarshal(w.Body.Bytes(), &report)
	if report.Rows[0].Draft.Status != "unavailable" || report.Rows[0].Regression {
		t.Fatal(report)
	}
	n := 0
	h.auth = func(context.Context, *http.Request, manage.Permission) (string, error) {
		n++
		if n == 3 {
			return "", manage.ErrDenied
		}
		return "reader", nil
	}
	if w = call(h, "/internal/knowledge/batch", body, "https://admin.example", "reader"); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
