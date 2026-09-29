package bootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

const reloadFAQ = `[{"id":"help","title":"帮助","source":"fixture","content":"old","keywords":["help"]}]`

func TestReloadFileUpdateFailureEmptyAndRestore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "faq.json")
	write := func(s string) {
		t.Helper()
		if e := os.WriteFile(p, []byte(s), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(reloadFAQ)
	m, e := NewReloadableKnowledge(context.Background(), KnowledgeConfig{FAQPath: p, Retriever: "keyword"})
	if e != nil {
		t.Fatal(e)
	}
	query := func() []customerservice.KnowledgeSnippet {
		t.Helper()
		r, e := m.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: "help", Limit: 3})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	old := m.Status().Version
	write(strings.Replace(reloadFAQ, "old", "new", 1))
	if e = m.Reload(context.Background()); e != nil {
		t.Fatal(e)
	}
	if query()[0].Content != "new" || m.Status().Version == old {
		t.Fatal("update not active")
	}
	good := m.Status().Version
	write("invalid private marker")
	if e = m.Reload(context.Background()); e == nil || strings.Contains(e.Error(), "private") {
		t.Fatal(e)
	}
	if query()[0].Content != "new" || m.Status().Version != good {
		t.Fatal("failed reload changed active")
	}
	write("[]")
	if e = m.Reload(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(query()) != 0 || m.Status().Entries != 0 {
		t.Fatal("withdrawal retained evidence")
	}
	write(reloadFAQ)
	if e = m.Reload(context.Background()); e != nil || query()[0].Content != "old" {
		t.Fatal("recovery", e)
	}
	for _, invalid := range []string{"null", "", "{}"} {
		write(invalid)
		if e = m.Reload(context.Background()); e == nil {
			t.Fatal("invalid input became empty source")
		}
	}
}

type statusWriter chan []byte

func (w statusWriter) Write(b []byte) (int, error) { w <- append([]byte{}, b...); return len(b), nil }
func TestSignalReloadAndStatus(t *testing.T) {
	p := filepath.Join(t.TempDir(), "faq.json")
	if e := os.WriteFile(p, []byte(reloadFAQ), 0600); e != nil {
		t.Fatal(e)
	}
	m, e := NewReloadableKnowledge(context.Background(), KnowledgeConfig{FAQPath: p})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan os.Signal, 4)
	w := make(statusWriter, 8)
	done := make(chan struct{})
	go func() { serveKnowledgeSignals(ctx, m, events, w); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("control did not stop")
		}
	}()
	next := func(want string) {
		t.Helper()
		select {
		case b := <-w:
			var x struct {
				Event string `json:"event"`
			}
			if json.Unmarshal(b, &x) != nil || x.Event != want || strings.Contains(string(b), p) {
				t.Fatal(string(b))
			}
		case <-time.After(time.Second):
			t.Fatal("no status")
		}
	}
	events <- syscall.SIGUSR1
	next("knowledge_status")
	if e = os.WriteFile(p, []byte("[]"), 0600); e != nil {
		t.Fatal(e)
	}
	events <- syscall.SIGHUP
	next("knowledge_reload_succeeded")
	if m.Status().Entries != 0 {
		t.Fatal("empty not active")
	}
	if e = os.WriteFile(p, []byte("bad"), 0600); e != nil {
		t.Fatal(e)
	}
	events <- syscall.SIGHUP
	next("knowledge_reload_failed")
}
func TestSemanticEmptyAvoidsEmbeddingAndDisabledIsDistinct(t *testing.T) {
	p := filepath.Join(t.TempDir(), "faq.json")
	if e := os.WriteFile(p, []byte("[]"), 0600); e != nil {
		t.Fatal(e)
	}
	m, e := NewReloadableKnowledge(context.Background(), KnowledgeConfig{FAQPath: p, Retriever: "semantic"})
	if e != nil || m == nil || m.Status().Entries != 0 {
		t.Fatal("empty should not embed", e)
	}
	m, e = NewReloadableKnowledge(context.Background(), KnowledgeConfig{})
	if e != nil || m != nil {
		t.Fatal("disabled is not nil", e)
	}
}
