// Package manage defines the single-instance keyword FAQ administration core.
// It is not an HTTP endpoint, login system, or a production repository.
package manage

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/faq"
)

type Permission string

const (
	Read    Permission = "knowledge.read"
	Edit    Permission = "knowledge.edit"
	Publish Permission = "knowledge.publish"
)

var (
	ErrDenied      = errors.New("knowledge management access denied")
	ErrConflict    = errors.New("knowledge management revision conflict")
	ErrInvalid     = errors.New("invalid knowledge management request")
	ErrUnavailable = errors.New("knowledge management storage unavailable")
	ErrEmpty       = errors.New("empty knowledge publication requires confirmation")
	ErrNotFound    = errors.New("knowledge version not found")
	ErrLimit       = errors.New("knowledge management history limit reached")
)

// Authorizer verifies a trusted session on EVERY call, including revocation and
// the requested permission. Return a stable non-secret actor ID, never a token.
// Customer/staff identity alone must not imply any of these permissions.
type Authorizer func(context.Context, Permission) (string, error)
type Version struct {
	ID string `json:"id"`
	// Bytes encode as base64 in JSON snapshots: RawMessage would compact JSON
	// and change the original-byte version hash during persistence.
	Data []byte `json:"data"`
}
type Event struct {
	Revision uint64    `json:"revision"`
	Action   string    `json:"action"`
	Actor    string    `json:"actor"`
	Version  string    `json:"version"`
	Previous string    `json:"previous"`
	At       time.Time `json:"at"`
}
type State struct {
	Revision uint64    `json:"revision"`
	Draft    *Version  `json:"draft,omitempty"`
	Active   string    `json:"active"`
	Versions []Version `json:"versions"`
	Events   []Event   `json:"events"`
}

// Repository must return owned snapshots and atomically persist draft, active
// version, history AND audit under a compare-and-swap revision check. On failure
// nothing may change. A production adapter must provide durable transactions;
// a file write followed by an independent reload/audit append does NOT satisfy
// this contract. An absent repository starts with zero State.
type Repository interface {
	Load(context.Context) (State, error)
	CompareAndSwap(context.Context, uint64, State) error
}
type Service struct {
	repo      Repository
	authorize Authorizer
	now       func() time.Time
}

func New(repo Repository, authorize Authorizer) (*Service, error) {
	if repo == nil || authorize == nil {
		return nil, ErrInvalid
	}
	return &Service{repo: repo, authorize: authorize, now: time.Now}, nil
}
func (s *Service) actor(ctx context.Context, permission Permission) (string, error) {
	if ctx == nil {
		return "", ErrDenied
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	id, err := s.authorize(ctx, permission)
	if err != nil || id == "" || id != strings.TrimSpace(id) || len(id) > 128 || !utf8.ValidString(id) || strings.IndexFunc(id, unicode.IsControl) >= 0 {
		return "", ErrDenied
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return id, nil
}
func (s *Service) load(ctx context.Context) (State, error) {
	state, err := s.repo.Load(ctx)
	if err != nil {
		return State{}, ErrUnavailable
	}
	return state, nil
}
func (s *Service) View(ctx context.Context) (State, error) {
	if _, err := s.actor(ctx, Read); err != nil {
		return State{}, err
	}
	return s.load(ctx)
}
func (s *Service) SaveDraft(ctx context.Context, expected uint64, data []byte) (uint64, error) {
	actor, err := s.actor(ctx, Edit)
	if err != nil {
		return 0, err
	}
	// Own the buffer before validation so the persisted version and hash agree.
	if len(data) > 1<<20 {
		return 0, ErrInvalid
	}
	owned := append([]byte(nil), data...)
	_, id, err := faq.ParseVersion(owned)
	if err != nil {
		return 0, ErrInvalid
	}
	state, err := s.load(ctx)
	if err != nil {
		return 0, err
	}
	if state.Revision != expected {
		return 0, ErrConflict
	}
	state.Draft = &Version{ID: id, Data: owned}
	return s.commit(ctx, expected, state, Event{Action: "save_draft", Actor: actor, Version: id, Previous: state.Active})
}
func (s *Service) PublishDraft(ctx context.Context, expected uint64, allowEmpty bool) (uint64, error) {
	return s.activate(ctx, expected, "", allowEmpty)
}
func (s *Service) Rollback(ctx context.Context, expected uint64, target string, allowEmpty bool) (uint64, error) {
	if len(target) != 64 {
		return 0, ErrInvalid
	}
	return s.activate(ctx, expected, target, allowEmpty)
}
func (s *Service) activate(ctx context.Context, expected uint64, target string, allowEmpty bool) (uint64, error) {
	actor, err := s.actor(ctx, Publish)
	if err != nil {
		return 0, err
	}
	state, err := s.load(ctx)
	if err != nil {
		return 0, err
	}
	if state.Revision != expected {
		return 0, ErrConflict
	}
	selected := state.Draft
	action := "publish"
	if target != "" {
		action = "rollback"
		selected = nil
		for i := range state.Versions {
			if state.Versions[i].ID == target {
				selected = &state.Versions[i]
				break
			}
		}
	}
	if selected == nil {
		return 0, ErrNotFound
	}
	retriever, id, err := faq.ParseVersion(selected.Data)
	if err != nil || id != selected.ID {
		return 0, ErrInvalid
	}
	if len(retriever.ReferenceIDs()) == 0 && !allowEmpty {
		return 0, ErrEmpty
	}
	exists := false
	for _, v := range state.Versions {
		if v.ID == id {
			exists = true
			break
		}
	}
	if !exists {
		if len(state.Versions) >= 32 {
			return 0, ErrLimit
		}
		state.Versions = append(state.Versions, Version{ID: id, Data: append([]byte(nil), selected.Data...)})
	}
	previous := state.Active
	state.Active = id
	// Publishing and rollback never overwrite a saved draft.
	return s.commit(ctx, expected, state, Event{Action: action, Actor: actor, Version: id, Previous: previous})
}
func (s *Service) commit(ctx context.Context, expected uint64, state State, event Event) (uint64, error) {
	if len(state.Events) >= 1000 || expected == ^uint64(0) {
		return 0, ErrLimit
	}
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	state.Revision = expected + 1
	event.Revision = state.Revision
	event.At = s.now().UTC()
	state.Events = append(state.Events, event)
	if err := s.repo.CompareAndSwap(ctx, expected, state); err != nil {
		if errors.Is(err, ErrConflict) {
			return 0, ErrConflict
		}
		return 0, ErrUnavailable
	}
	return state.Revision, nil
}

// Retrieve is the internal application port, NOT an unauthenticated admin route.
// Each call pins one repository snapshot, never a draft; unavailable/corrupt
// storage fails closed instead of silently inventing an empty knowledge source.
func (s *Service) Retrieve(ctx context.Context, q customerservice.KnowledgeQuery) ([]customerservice.KnowledgeSnippet, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	state, err := s.load(ctx)
	if err != nil {
		return nil, err
	}
	if state.Active == "" {
		return nil, ErrNotFound
	}
	for _, v := range state.Versions {
		if v.ID == state.Active {
			r, id, err := faq.ParseVersion(v.Data)
			if err != nil || id != v.ID {
				return nil, ErrUnavailable
			}
			return r.Retrieve(ctx, q)
		}
	}
	return nil, ErrUnavailable
}
