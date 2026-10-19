package webchat

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

type trustedHost struct {
	source string
	digest [32]byte
	limit  *rate.Limiter
}
type identity struct{ conversation, customer, channel string }
type session struct {
	identity
	expires time.Time
}
type principal struct {
	limit   *rate.Limiter
	expires time.Time
	busy    bool
}

// SessionResponse is the only credential material intended for the browser.
// It must not be cached, logged, put in a URL or persisted in browser storage.
type SessionResponse struct {
	AccessToken    string    `json:"access_token"`
	ExpiresAt      time.Time `json:"expires_at"`
	ConversationID string    `json:"conversation_id"`
}

func configureHosts(config []Host) ([]trustedHost, error) {
	if len(config) == 0 || len(config) > 32 {
		return nil, errors.New("webchat: configure 1..32 hosts")
	}
	hosts := make([]trustedHost, 0, len(config))
	for _, host := range config {
		if !validID(host.SourceID) || len(host.Secret) < 32 || len(host.Secret) > 128 || strings.ContainsAny(host.Secret, " \r\n\t") || strings.HasPrefix(host.Secret, "chat_") {
			return nil, errors.New("webchat: invalid host configuration")
		}
		digest := sha256.Sum256([]byte(host.Secret))
		for _, existing := range hosts {
			if existing.source == host.SourceID || existing.digest == digest {
				return nil, errors.New("webchat: duplicate host configuration")
			}
		}
		hosts = append(hosts, trustedHost{host.SourceID, digest, rate.NewLimiter(2, 20)})
	}
	return hosts, nil
}

// Versioned tuple encoding avoids delimiter collisions. Raw host/customer IDs
// never become public core identifiers. The first round retains its stable ID.
func mapped(kind string, values ...string) string {
	parts := append([]string{"browser-chat/v1", kind}, values...)
	encoded, _ := json.Marshal(parts)
	digest := sha256.Sum256(encoded)
	return "wbc_" + hex.EncodeToString(digest[:])
}
func identify(source, customer string) identity {
	return identity{mapped("conversation", source, customer), mapped("customer", source, customer), mapped("channel", source)}
}

func (g *Gateway) exchange(w http.ResponseWriter, r *http.Request) {
	token, ok := bearer(r)
	if !ok {
		problem(w, 401, "unauthorized")
		return
	}
	digest := sha256.Sum256([]byte(token))
	index := -1
	for i := range g.hosts {
		if subtle.ConstantTimeCompare(digest[:], g.hosts[i].digest[:]) == 1 {
			index = i
		}
	}
	if index == -1 {
		problem(w, 401, "unauthorized")
		return
	}
	host := g.hosts[index]
	if !host.limit.AllowN(g.now(), 1) {
		w.Header().Set("Retry-After", "1")
		problem(w, 429, "rate_limited")
		return
	}
	var request struct {
		CustomerID string `json:"customer_id"`
	}
	if !decode(w, r, &request, "customer_id") {
		return
	}
	if !validID(request.CustomerID) {
		problem(w, 400, "invalid_request")
		return
	}
	claims := identify(host.source, request.CustomerID)
	if rounds, ok := g.repo.(conversation.Rounds); ok {
		latest, err := rounds.FindLatestRound(r.Context(), claims.customer, claims.channel, time.Time{})
		if err == nil {
			claims.conversation = latest.ID()
		} else if !errors.Is(err, conversation.ErrConversationNotFound) {
			applicationError(w, err)
			return
		}
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		problem(w, 503, "unavailable")
		return
	}
	accessToken := "chat_" + base64.RawURLEncoding.EncodeToString(random[:])
	key := sha256.Sum256([]byte(accessToken))
	now := g.now()
	expires := now.Add(g.config.SessionTTL)
	g.mu.Lock()
	g.prune(now)
	count := 0
	for _, current := range g.sessions {
		if current.conversation == claims.conversation {
			count++
		}
	}
	p := g.principals[claims.conversation]
	if len(g.sessions) >= g.config.MaxSessions || count >= 8 || (p == nil && len(g.principals) >= g.config.MaxPrincipals) {
		g.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		problem(w, 429, "busy")
		return
	}
	if p == nil {
		p = &principal{limit: rate.NewLimiter(2, 20)}
		g.principals[claims.conversation] = p
	}
	p.expires = expires.Add(time.Minute) // Reissuing a token must not reset rate/busy state.
	g.sessions[key] = session{claims, expires}
	g.mu.Unlock()
	writeJSON(w, 201, SessionResponse{accessToken, expires, claims.conversation})
}

// prune is called under mu. Active requests keep their principal even if their
// credential expires; expiration is checked at admission, not halfway through
// an already accepted, timeout-bounded message.
func (g *Gateway) prune(now time.Time) {
	for key, s := range g.sessions {
		if !now.Before(s.expires) {
			delete(g.sessions, key)
		}
	}
	for key, p := range g.principals {
		if !p.busy && !now.Before(p.expires) {
			delete(g.principals, key)
		}
	}
}

func (g *Gateway) acquire(r *http.Request) (identity, func(), string) {
	token, ok := bearer(r)
	if !ok || len(token) != 48 || !strings.HasPrefix(token, "chat_") {
		return identity{}, nil, "unauthorized"
	}
	key := sha256.Sum256([]byte(token))
	g.mu.Lock()
	now := g.now()
	g.prune(now)
	s, ok := g.sessions[key]
	if !ok {
		g.mu.Unlock()
		return identity{}, nil, "unauthorized"
	}
	p := g.principals[s.conversation]
	if p == nil {
		g.mu.Unlock()
		return identity{}, nil, "unauthorized"
	}
	// Read-only current history may run alongside generation. Authentication,
	// ownership, rate limits and the global concurrency cap still apply.
	exclusive := r.URL.Path != "/chat/v1/handoff" && r.URL.Path != MessageReceiptPath && !(r.URL.Path == MessagesPath && r.Method == http.MethodGet)
	if (exclusive && p.busy) || g.active >= g.config.MaxConcurrent {
		g.mu.Unlock()
		return identity{}, nil, "busy"
	}
	if !p.limit.AllowN(now, 1) {
		g.mu.Unlock()
		return identity{}, nil, "rate_limited"
	}
	if exclusive {
		p.busy = true
	}
	g.active++
	g.mu.Unlock()
	return s.identity, func() {
		g.mu.Lock()
		if exclusive {
			p.busy = false
		}
		g.active--
		g.mu.Unlock()
	}, ""
}
