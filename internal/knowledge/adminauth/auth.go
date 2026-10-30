// Package adminauth provides single-process knowledge-admin sessions, separate
// from customer/staff identities. It does not implement public registration.
package adminauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/managehttp"
	"golang.org/x/crypto/scrypt"
)

var ErrConfiguration = errors.New("invalid knowledge administrator configuration")

type Account struct {
	ID           string              `json:"id"`
	PasswordHash string              `json:"password_hash"`
	Permissions  []manage.Permission `json:"permissions"`
}
type proof struct {
	salt, key   []byte
	permissions map[manage.Permission]bool
	disabled    bool
}
type session struct {
	account string
	expires time.Time
	version string
}
type window struct {
	at    time.Time
	count int
}
type Directory struct {
	mu                   sync.Mutex
	accounts             map[string]*proof
	tickets              map[[32]byte]session
	attempts             map[string]window
	now                  func() time.Time
	origin, host, cookie string
	secure               bool
	loginGate            chan struct{}
	dummy                *proof
}

func validID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// HashPassword uses a fixed, versioned cost. No caller-supplied work factor is
// accepted. The caller owns obtaining the password without command-line logs.
func HashPassword(password []byte) (string, error) {
	if len(password) < 16 || len(password) > 512 || !utf8.Valid(password) || strings.ContainsFunc(string(password), unicode.IsControl) {
		return "", ErrConfiguration
	}
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", ErrConfiguration
	}
	key, e := scrypt.Key(password, salt, 32768, 8, 1, 32)
	if e != nil {
		return "", ErrConfiguration
	}
	return "scrypt-v1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}
func parseHash(s string) (*proof, error) {
	p := strings.Split(s, "$")
	if len(p) != 3 || p[0] != "scrypt-v1" {
		return nil, ErrConfiguration
	}
	salt, e := base64.RawStdEncoding.DecodeString(p[1])
	if e != nil || len(salt) != 16 {
		return nil, ErrConfiguration
	}
	key, e := base64.RawStdEncoding.DecodeString(p[2])
	if e != nil || len(key) != 32 {
		return nil, ErrConfiguration
	}
	return &proof{salt: salt, key: key}, nil
}
func New(accounts []Account, origin string) (*Directory, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Scheme != "https" && !(u.Scheme == "http" && net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback()) || len(accounts) == 0 || len(accounts) > 32 {
		return nil, ErrConfiguration
	}
	d := &Directory{accounts: map[string]*proof{}, tickets: map[[32]byte]session{}, attempts: map[string]window{}, now: time.Now, origin: origin, host: u.Host, secure: u.Scheme == "https", cookie: "omni_knowledge_admin_local", loginGate: make(chan struct{}, 2)}
	if d.secure {
		d.cookie = "__Host-omni_knowledge_admin"
	}
	for _, a := range accounts {
		if !validID(a.ID) || d.accounts[a.ID] != nil || len(a.Permissions) == 0 {
			return nil, ErrConfiguration
		}
		p, e := parseHash(a.PasswordHash)
		if e != nil {
			return nil, e
		}
		p.permissions = map[manage.Permission]bool{}
		for _, v := range a.Permissions {
			if v != manage.Read && v != manage.Edit && v != manage.Publish || p.permissions[v] {
				return nil, ErrConfiguration
			}
			p.permissions[v] = true
		}
		d.accounts[a.ID] = p
	}
	// Unknown accounts pay the same derivation cost as configured accounts.
	salt := make([]byte, 16)
	key := make([]byte, 32)
	if _, e = rand.Read(salt); e != nil {
		return nil, ErrConfiguration
	}
	if _, e = rand.Read(key); e != nil {
		return nil, ErrConfiguration
	}
	d.dummy = &proof{salt: salt, key: key}
	return d, nil
}
func (d *Directory) presented(r *http.Request) ([32]byte, session, bool) {
	var zero [32]byte
	found := ""
	n := 0
	for _, c := range r.Cookies() {
		if c.Name == d.cookie {
			found = c.Value
			n++
		}
	}
	if n != 1 || len(found) != 43 {
		return zero, session{}, false
	}
	b, e := base64.RawURLEncoding.DecodeString(found)
	if e != nil || len(b) != 32 {
		return zero, session{}, false
	}
	key := sha256.Sum256([]byte(found))
	s, ok := d.tickets[key]
	a := d.accounts[s.account]
	if !ok || a == nil || a.disabled || !d.now().Before(s.expires) {
		delete(d.tickets, key)
		return zero, session{}, false
	}
	return key, s, true
}
func (d *Directory) Authorize(ctx context.Context, r *http.Request, p manage.Permission) (string, error) {
	if ctx == nil || ctx.Err() != nil {
		return "", manage.ErrDenied
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, s, ok := d.presented(r)
	if !ok || !d.accounts[s.account].permissions[p] {
		return "", manage.ErrDenied
	}
	return s.account, nil
}

// Revoke disables one configured account and every outstanding session.
// No HTTP route exposes this operation; deployment config changes need restart.
func (d *Directory) Revoke(account string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if a := d.accounts[account]; a != nil {
		a.disabled = true
	}
	for k, s := range d.tickets {
		if s.account == account {
			delete(d.tickets, k)
		}
	}
}
func (d *Directory) cookieValue(value string, age int) *http.Cookie {
	return &http.Cookie{Name: d.cookie, Value: value, Path: "/", HttpOnly: true, Secure: d.secure, SameSite: http.SameSiteStrictMode, MaxAge: age}
}
func reply(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}
func denied(w http.ResponseWriter, status int) {
	reply(w, status, map[string]string{"code": "login_required_or_not_allowed"})
}
func (d *Directory) Handler(repo manage.Repository) (http.Handler, error) {
	admin, e := managehttp.New(repo, d.Authorize, d.origin)
	if e != nil {
		return nil, e
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Host != d.host || len(r.Header.Values("Origin")) > 1 || r.Header.Get("Sec-Fetch-Site") == "cross-site" || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != d.origin {
			denied(w, 403)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/internal/knowledge/") {
			admin.ServeHTTP(w, r)
			return
		}
		if r.URL.RawQuery != "" || r.URL.RawPath != "" || r.URL.ForceQuery {
			denied(w, 400)
			return
		}
		switch r.URL.Path {
		case "/auth/me":
			if r.Method != "GET" {
				denied(w, 405)
				return
			}
			d.mu.Lock()
			_, s, ok := d.presented(r)
			if !ok {
				d.mu.Unlock()
				denied(w, 401)
				return
			}
			perms := []manage.Permission{}
			for _, p := range []manage.Permission{manage.Read, manage.Edit, manage.Publish} {
				if d.accounts[s.account].permissions[p] {
					perms = append(perms, p)
				}
			}
			d.mu.Unlock()
			reply(w, 200, map[string]any{"id": s.account, "permissions": perms, "version": s.version})
			return
		case "/auth/login", "/auth/logout":
		default:
			denied(w, 404)
			return
		}
		if r.Method != "POST" {
			denied(w, 405)
			return
		}
		if r.Header.Get("Origin") != d.origin {
			denied(w, 403)
			return
		}
		media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if e != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 {
			denied(w, 415)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		defer r.Body.Close()
		var body struct {
			Account  string `json:"account"`
			Password string `json:"password"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
			denied(w, 400)
			return
		}
		if r.URL.Path == "/auth/logout" {
			if body.Account != "" || body.Password != "" {
				denied(w, 400)
				return
			}
			d.mu.Lock()
			key, _, ok := d.presented(r)
			if ok {
				delete(d.tickets, key)
			}
			d.mu.Unlock()
			http.SetCookie(w, d.cookieValue("", -1))
			reply(w, 204, nil)
			return
		}
		d.login(w, r, body.Account, body.Password)
	}), nil
}
func (d *Directory) login(w http.ResponseWriter, r *http.Request, account, password string) {
	if !validID(account) || len(password) > 512 || strings.ContainsFunc(password, unicode.IsControl) {
		denied(w, 401)
		return
	}
	select {
	case d.loginGate <- struct{}{}:
		defer func() { <-d.loginGate }()
	default:
		denied(w, 429)
		return
	}
	d.mu.Lock()
	a := d.accounts[account]
	bucket := account
	if a == nil {
		bucket = "unknown"
		a = d.dummy
	}
	limited := false
	now := d.now()
	for _, k := range []string{"global", "account:" + bucket} {
		v := d.attempts[k]
		if now.Sub(v.at) >= time.Minute {
			v = window{at: now}
		}
		v.count++
		d.attempts[k] = v
		max := 10
		if k == "global" {
			max = 60
		}
		if v.count > max {
			limited = true
		}
	}
	d.mu.Unlock()
	if limited {
		denied(w, 429)
		return
	}
	key, e := scrypt.Key([]byte(password), a.salt, 32768, 8, 1, 32)
	if e != nil || subtle.ConstantTimeCompare(key, a.key) != 1 {
		denied(w, 401)
		return
	}
	raw := make([]byte, 32)
	nonce := make([]byte, 16)
	if _, e = rand.Read(raw); e != nil {
		denied(w, 503)
		return
	}
	if _, e = rand.Read(nonce); e != nil {
		denied(w, 503)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	d.mu.Lock()
	defer d.mu.Unlock()
	if r.Context().Err() != nil || d.accounts[account] == nil || d.accounts[account].disabled {
		denied(w, 401)
		return
	}
	for k, s := range d.tickets {
		if !d.now().Before(s.expires) {
			delete(d.tickets, k)
		}
	}
	old, _, had := d.presented(r)
	count := len(d.tickets)
	if had {
		count--
	}
	if count >= 128 {
		denied(w, 429)
		return
	}
	if had {
		delete(d.tickets, old)
	}
	d.tickets[sha256.Sum256([]byte(token))] = session{account: account, expires: d.now().Add(30 * time.Minute), version: base64.RawURLEncoding.EncodeToString(nonce)}
	http.SetCookie(w, d.cookieValue(token, 1800))
	reply(w, 200, map[string]bool{"authenticated": true})
}
