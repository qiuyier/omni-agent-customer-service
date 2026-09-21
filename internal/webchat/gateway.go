// Package webchat is a small browser-facing channel adapter. Host credentials
// attest customer identity; opaque browser credentials can only read/send in
// that identity's single support conversation. It does not add tenant auth to
// the existing service-wide /v1 API or partition knowledge/model configuration.
package webchat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

const (
	SessionPath     = "/internal/chat/sessions"
	MessagesPath    = "/chat/v1/messages"
	MaxContentBytes = 4000
	maxBodyBytes    = 32 << 10 // Allows JSON escaping of a 4 KB message.
)

// Application is the existing channel-independent use-case boundary.
type Application interface {
	ReceiveMessage(context.Context, customerservice.ReceiveMessageCommand) (customerservice.ReceiveMessageResult, error)
	GenerateBotReply(context.Context, customerservice.GenerateBotReplyCommand) (customerservice.GenerateBotReplyResult, error)
}

// Host is trusted server-side configuration, never browser input. Secret must
// be an independent, randomly generated credential, not the /v1 service token.
type Host struct{ SourceID, Secret string }

// Config limits process-local capabilities and request work. Zero values use
// conservative demo defaults. This is not a distributed session/rate store.
type Config struct {
	Hosts          []Host
	SessionTTL     time.Duration
	MaxSessions    int
	MaxPrincipals  int
	MaxConcurrent  int
	RequestTimeout time.Duration
}

type Gateway struct {
	repo       conversation.Repository
	app        Application
	config     Config
	now        func() time.Time
	mu         sync.Mutex
	hosts      []trustedHost
	sessions   map[[32]byte]session
	principals map[string]*principal
	active     int
}

func New(repo conversation.Repository, app Application, config Config) (*Gateway, error) {
	if repo == nil || app == nil {
		return nil, errors.New("webchat: repository and application required")
	}
	if config.SessionTTL == 0 {
		config.SessionTTL = 5 * time.Minute
	}
	if config.MaxSessions == 0 {
		config.MaxSessions = 1024
	}
	if config.MaxPrincipals == 0 {
		config.MaxPrincipals = 1024
	}
	if config.MaxConcurrent == 0 {
		config.MaxConcurrent = 16
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 20 * time.Second
	}
	if config.SessionTTL < time.Second || config.SessionTTL > 15*time.Minute ||
		config.MaxSessions < 1 || config.MaxSessions > 100000 ||
		config.MaxPrincipals < 1 || config.MaxPrincipals > 100000 ||
		config.MaxConcurrent < 1 || config.MaxConcurrent > 1024 ||
		config.RequestTimeout < time.Second || config.RequestTimeout > 2*time.Minute {
		return nil, errors.New("webchat: invalid limits")
	}
	hosts, err := configureHosts(config.Hosts)
	if err != nil {
		return nil, err
	}
	config.Hosts = nil // Do not retain plaintext credentials.
	return &Gateway{repo: repo, app: app, config: config, now: time.Now, hosts: hosts,
		sessions: make(map[[32]byte]session), principals: make(map[string]*principal)}, nil
}

// ServeHTTP deliberately has no arbitrary conversation, customer or role route.
// Deploy behind TLS and a same-origin host proxy; never proxy SessionPath to a
// browser. The demo binds this listener to loopback only.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	defer func() {
		if recover() != nil {
			problem(w, 500, "unavailable")
		}
	}()
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		problem(w, 400, "invalid_request")
		return
	}
	switch r.URL.Path {
	case SessionPath:
		if r.Method != http.MethodPost {
			methodNotAllowed(w, "POST")
			return
		}
		g.exchange(w, r)
	case MessagesPath:
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			methodNotAllowed(w, "GET, POST")
			return
		}
		claims, release, code := g.acquire(r)
		if code != "" {
			status := http.StatusTooManyRequests
			if code == "unauthorized" {
				status = http.StatusUnauthorized
			} else {
				w.Header().Set("Retry-After", "1")
			}
			problem(w, status, code)
			return
		}
		defer release()
		ctx, cancel := context.WithTimeout(r.Context(), g.config.RequestTimeout)
		defer cancel()
		if r.Method == http.MethodGet {
			g.history(w, r.WithContext(ctx), claims)
		} else {
			g.send(w, r.WithContext(ctx), claims)
		}
	default:
		problem(w, 404, "not_found")
	}
}

func decode(w http.ResponseWriter, r *http.Request, value any, fields ...string) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if len(r.Header.Values("Content-Type")) != 1 || err != nil || mediaType != "application/json" {
		problem(w, 400, "invalid_request")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil || !utf8.Valid(data) || !strictObject(data, fields) || json.Unmarshal(data, value) != nil {
		problem(w, 400, "invalid_request")
		return false
	}
	return true
}

// encoding/json alone permits duplicate and case-insensitive field names.
// Reject both so the browser, host proxy and gateway agree on one request.
func strictObject(data []byte, fields []string) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return false
	}
	seen := make(map[string]bool, len(fields))
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return false
		}
		key, ok := token.(string)
		allowed := false
		for _, field := range fields {
			if key == field {
				allowed = true
				break
			}
		}
		if !ok || !allowed || seen[key] {
			return false
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return false
		}
	}
	end, err := d.Token()
	return err == nil && end == json.Delim('}') && d.Decode(new(any)) == io.EOF
}

func validID(value string) bool {
	return value != "" && len(value) <= 128 && utf8.ValidString(value) && strings.TrimSpace(value) == value &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

func bearer(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || len(values[0]) > 256 {
		return "", false
	}
	parts := strings.Split(values[0], " ")
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func problem(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{code})
}
func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	problem(w, 405, "invalid_request")
}

func applicationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, customerservice.ErrIdempotencyConflict):
		problem(w, 409, "event_conflict")
	case errors.Is(err, customerservice.ErrConversationMismatch), errors.Is(err, conversation.ErrConversationNotFound):
		problem(w, 404, "not_found")
	case errors.Is(err, conversation.ErrConversationClosed):
		problem(w, 409, "conversation_closed")
	case errors.Is(err, customerservice.ErrInvalidInput):
		problem(w, 400, "invalid_request")
	case errors.Is(err, customerservice.ErrReplyCapacityExceeded):
		w.Header().Set("Retry-After", "1")
		problem(w, 429, "busy")
	default:
		problem(w, 503, "unavailable")
	}
}

func ownership(current *conversation.Conversation, claims identity) error {
	if current.ID() != claims.conversation || current.CustomerID() != claims.customer || current.Channel() != claims.channel {
		return fmt.Errorf("%w: webchat ownership", customerservice.ErrConversationMismatch)
	}
	return nil
}
