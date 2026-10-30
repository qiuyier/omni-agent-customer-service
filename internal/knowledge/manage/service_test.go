package manage

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

type pinKey struct{}
type pinnedRepository struct {
	Repository
	entered, release chan struct{}
}

func (p *pinnedRepository) Load(ctx context.Context) (State, error) {
	state, err := p.Repository.Load(ctx)
	if ctx.Value(pinKey{}) == true {
		close(p.entered)
		<-p.release
	}
	return state, err
}
func TestRetrievalPinsPublishedSnapshot(t *testing.T) {
	s, r := fixture(t)
	ctx := context.Background()
	s.SaveDraft(ctx, 0, data("old"))
	s.PublishDraft(ctx, 1, false)
	p := &pinnedRepository{Repository: r, entered: make(chan struct{}), release: make(chan struct{})}
	s.repo = p
	result := make(chan string, 1)
	go func() {
		v, e := s.Retrieve(context.WithValue(ctx, pinKey{}, true), customerservice.KnowledgeQuery{Text: "hours", Limit: 1})
		if e != nil || len(v) != 1 {
			result <- "failed"
			return
		}
		result <- v[0].Content
	}()
	<-p.entered
	s.SaveDraft(ctx, 2, data("new"))
	if _, e := s.PublishDraft(ctx, 3, false); e != nil {
		t.Fatal(e)
	}
	close(p.release)
	if got := <-result; got != "old" {
		t.Fatal("in-flight retrieval changed", got)
	}
	answer(t, s, "new")
}

// Test-only transactional repository; not a production persistence adapter.
type memory struct {
	mu    sync.Mutex
	state State
	fail  bool
}

func copyState(s State) State {
	b, _ := json.Marshal(s)
	var c State
	_ = json.Unmarshal(b, &c)
	return c
}
func (m *memory) Load(ctx context.Context) (State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return State{}, err
	}
	return copyState(m.state), nil
}
func (m *memory) CompareAndSwap(ctx context.Context, expected uint64, s State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.fail {
		return errors.New("private repository diagnostic")
	}
	if m.state.Revision != expected {
		return ErrConflict
	}
	m.state = copyState(s)
	return nil
}
func fixture(t *testing.T) (*Service, *memory) {
	t.Helper()
	r := &memory{}
	s, e := New(r, func(context.Context, Permission) (string, error) { return "admin-1", nil })
	if e != nil {
		t.Fatal(e)
	}
	return s, r
}
func data(content string) []byte {
	return []byte(`[{"id":"hours","title":"Hours","source":"fictional","content":"` + content + `","keywords":["hours"]}]`)
}
func read(t *testing.T, s *Service) State {
	t.Helper()
	v, e := s.View(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func answer(t *testing.T, s *Service, want string) {
	t.Helper()
	v, e := s.Retrieve(context.Background(), customerservice.KnowledgeQuery{Text: "hours", Limit: 1})
	if e != nil || len(v) != 1 || v[0].Content != want {
		t.Fatalf("unexpected retrieval: %v, %v", v, e)
	}
}

func TestDraftPublishRollbackAndAudit(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	if _, e := s.SaveDraft(ctx, 0, data("old")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Retrieve(ctx, customerservice.KnowledgeQuery{Text: "hours", Limit: 1}); !errors.Is(e, ErrNotFound) {
		t.Fatal("draft became active", e)
	}
	old := read(t, s).Draft.ID
	if _, e := s.PublishDraft(ctx, 1, false); e != nil {
		t.Fatal(e)
	}
	answer(t, s, "old")
	if _, e := s.SaveDraft(ctx, 2, data("new")); e != nil {
		t.Fatal(e)
	}
	answer(t, s, "old")
	if _, e := s.PublishDraft(ctx, 3, false); e != nil {
		t.Fatal(e)
	}
	answer(t, s, "new")
	if _, e := s.Rollback(ctx, 4, old, false); e != nil {
		t.Fatal(e)
	}
	answer(t, s, "old")
	v := read(t, s)
	if v.Revision != 5 || len(v.Versions) != 2 || len(v.Events) != 5 || v.Draft.ID == old {
		t.Fatalf("bad lifecycle %+v", v)
	}
	actions := []string{"save_draft", "publish", "save_draft", "publish", "rollback"}
	for i, e := range v.Events {
		if e.Actor != "admin-1" || e.Action != actions[i] || e.Revision != uint64(i+1) || e.At.IsZero() {
			t.Fatal(e)
		}
	}
	if v.Events[4].Previous != v.Draft.ID || v.Events[4].Version != old {
		t.Fatal("rollback audit incomplete")
	}
	// A newly composed service sees the repository's active version.
	other, _ := New(s.repo, s.authorize)
	answer(t, other, "old")
}
func TestFailureConflictAndInvalidInputsPreserveState(t *testing.T) {
	s, r := fixture(t)
	ctx := context.Background()
	s.SaveDraft(ctx, 0, data("old"))
	s.PublishDraft(ctx, 1, false)
	before := read(t, s)
	for _, bad := range [][]byte{nil, []byte("null"), []byte("[null]"), []byte("[{\"secret\":true}]"), []byte(strings.Repeat(" ", (1<<20)+1))} {
		if _, e := s.SaveDraft(ctx, 2, bad); !errors.Is(e, ErrInvalid) {
			t.Fatal(e)
		}
	}
	if _, e := s.SaveDraft(ctx, 0, data("new")); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	r.fail = true
	if _, e := s.SaveDraft(ctx, 2, data("new")); e != ErrUnavailable {
		t.Fatal(e)
	}
	r.fail = false
	if !reflect.DeepEqual(before, read(t, s)) {
		t.Fatal("failed operations changed state")
	}
	s.SaveDraft(ctx, 2, data("new"))
	before = read(t, s)
	r.fail = true
	if _, e := s.PublishDraft(ctx, 3, false); e != ErrUnavailable {
		t.Fatal(e)
	}
	r.fail = false
	answer(t, s, "old")
	if !reflect.DeepEqual(before, read(t, s)) {
		t.Fatal("failed publish changed audit or active")
	}
	if _, e := s.Rollback(ctx, 3, strings.Repeat("a", 64), false); e != ErrNotFound {
		t.Fatal(e)
	}
}
func TestEmptyPublicationRequiresExplicitConfirmation(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	s.SaveDraft(ctx, 0, []byte("[]"))
	empty := read(t, s).Draft.ID
	if _, e := s.PublishDraft(ctx, 1, false); e != ErrEmpty {
		t.Fatal(e)
	}
	if _, e := s.PublishDraft(ctx, 1, true); e != nil {
		t.Fatal(e)
	}
	got, e := s.Retrieve(ctx, customerservice.KnowledgeQuery{Text: "hours", Limit: 1})
	if e != nil || len(got) != 0 {
		t.Fatal(got, e)
	}
	s.SaveDraft(ctx, 2, data("new"))
	s.PublishDraft(ctx, 3, false)
	if _, e := s.Rollback(ctx, 4, empty, false); e != ErrEmpty {
		t.Fatal(e)
	}
	answer(t, s, "new")
	if _, e := s.Rollback(ctx, 4, empty, true); e != nil {
		t.Fatal(e)
	}
}
func TestPermissionsRevocationAndCancellation(t *testing.T) {
	s, r := fixture(t)
	ctx := context.Background()
	s.SaveDraft(ctx, 0, data("old"))
	before := read(t, s)
	var permissions []Permission
	s.authorize = func(_ context.Context, p Permission) (string, error) {
		permissions = append(permissions, p)
		return "", errors.New("private credential")
	}
	if _, e := s.View(ctx); e != ErrDenied {
		t.Fatal(e)
	}
	if _, e := s.SaveDraft(ctx, 1, data("new")); e != ErrDenied {
		t.Fatal(e)
	}
	if _, e := s.PublishDraft(ctx, 1, false); e != ErrDenied {
		t.Fatal(e)
	}
	if _, e := s.Rollback(ctx, 1, before.Draft.ID, false); e != ErrDenied {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(permissions, []Permission{Read, Edit, Publish, Publish}) {
		t.Fatal(permissions)
	}
	if !reflect.DeepEqual(before, r.state) {
		t.Fatal("unauthorized operation mutated state")
	}
	// Being able to edit does not grant publication rights.
	s.authorize = func(_ context.Context, p Permission) (string, error) {
		if p == Edit {
			return "editor", nil
		}
		return "", ErrDenied
	}
	if _, e := s.SaveDraft(ctx, 1, data("new")); e != nil {
		t.Fatal(e)
	}
	if _, e := s.PublishDraft(ctx, 2, false); e != ErrDenied {
		t.Fatal(e)
	}
	c, cancel := context.WithCancel(ctx)
	s.authorize = func(context.Context, Permission) (string, error) { cancel(); return "admin", nil }
	if _, e := s.SaveDraft(c, 2, data("cancelled")); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if r.state.Revision != 2 {
		t.Fatal("cancelled operation committed")
	}
}
func TestConcurrentEditsAndPublicationsHaveSingleWinner(t *testing.T) {
	for _, publishing := range []bool{false, true} {
		s, _ := fixture(t)
		ctx := context.Background()
		expected := uint64(0)
		if publishing {
			s.SaveDraft(ctx, 0, data("first"))
			expected = 1
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() {
				<-start
				var e error
				if publishing {
					_, e = s.PublishDraft(ctx, expected, false)
				} else {
					_, e = s.SaveDraft(ctx, expected, data("concurrent"))
				}
				results <- e
			}()
		}
		close(start)
		success, conflict := 0, 0
		for i := 0; i < 2; i++ {
			e := <-results
			if e == nil {
				success++
			} else if errors.Is(e, ErrConflict) {
				conflict++
			} else {
				t.Fatal(e)
			}
		}
		if success != 1 || conflict != 1 || read(t, s).Revision != expected+1 {
			t.Fatal("lost update")
		}
	}
}
func TestOwnedBuffersAndCorruptActiveFailClosed(t *testing.T) {
	s, r := fixture(t)
	ctx := context.Background()
	input := data("original")
	s.SaveDraft(ctx, 0, input)
	input[0] = '!'
	v := read(t, s)
	v.Draft.Data[0] = '!'
	if _, e := s.PublishDraft(ctx, 1, false); e != nil {
		t.Fatal(e)
	}
	answer(t, s, "original")
	r.state.Versions[0].Data = data("tampered")
	if _, e := s.Retrieve(ctx, customerservice.KnowledgeQuery{Text: "hours", Limit: 1}); e != ErrUnavailable {
		t.Fatal(e)
	}
}
func TestHistoryLimitsNeverSilentlyDropAudit(t *testing.T) {
	s, r := fixture(t)
	r.state.Events = make([]Event, 1000)
	if _, e := s.SaveDraft(context.Background(), 0, data("new")); e != ErrLimit {
		t.Fatal(e)
	}
	if r.state.Revision != 0 || r.state.Draft != nil {
		t.Fatal("limit mutated state")
	}
}

func TestWhitespaceSurvivesRepositorySerialization(t *testing.T) {
	s, _ := fixture(t)
	input := append([]byte(" \n"), data("original")...)
	input = append(input, []byte("\n ")...)
	if _, err := s.SaveDraft(context.Background(), 0, input); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(read(t, s).Draft.Data, input) {
		t.Fatal("persisted bytes changed")
	}
	if _, err := s.PublishDraft(context.Background(), 1, false); err != nil {
		t.Fatal(err)
	}
	answer(t, s, "original")
}
