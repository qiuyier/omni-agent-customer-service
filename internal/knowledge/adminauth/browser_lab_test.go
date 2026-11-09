//go:build browserlab

package adminauth

import (
	"fmt"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Manual browser fixture only. Never compiled into the production executable.
// No database, external model, persistent account or production configuration.
func TestBrowserLab(t *testing.T) {
	if os.Getenv("OMNI_ADMIN_UI_LAB") != "1" {
		t.Skip("opt-in manual browser fixture")
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	origin := "http://" + listener.Addr().String()
	hash, e := HashPassword([]byte(password))
	if e != nil {
		t.Fatal(e)
	}
	d, e := New([]Account{{"admin", hash, []manage.Permission{manage.Read, manage.Edit, manage.Publish}}, {"editor", hash, []manage.Permission{manage.Read, manage.Edit}}}, origin)
	if e != nil {
		t.Fatal(e)
	}
	h, e := d.Handler(newFeedbackLab())
	if e != nil {
		t.Fatal(e)
	}
	// Fault switches exist only in this opt-in test binary, never production.
	ttl := time.Duration(0)
	if value := os.Getenv("OMNI_ADMIN_UI_LAB_TTL_SECONDS"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 5 || n > 300 {
			t.Fatal("lab TTL must be 5..300 seconds")
		}
		ttl = time.Duration(n) * time.Second
	}
	gate := &browserFaultGate{directory: d, next: h, drop: os.Getenv("OMNI_ADMIN_UI_LAB_DROP_RESPONSE") == "1", ttl: ttl}
	server := &http.Server{Handler: gate, ReadHeaderTimeout: 5 * time.Second}
	defer server.Close()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	fmt.Println("Browser fixture:", origin, "account admin/editor; password is the fictional fixture constant in auth_test.go")
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case <-stop:
	case e := <-done:
		if e != http.ErrServerClosed {
			t.Fatal(e)
		}
	}
}

// Buffer a real successful mutation, then drop its HTTP connection. The store
// has committed but the browser receives no acknowledgement. Only the first
// successful mutation is affected; reads and rejected writes remain intact.
type browserFaultGate struct {
	directory *Directory
	next      http.Handler
	mu        sync.Mutex
	drop      bool
	ttl       time.Duration
}

func TestBrowserFaultCommittedResponseLoss(t *testing.T) {
	d, h, repo := setup(t)
	cookie := login(t, h, "admin", "")
	server := httptest.NewServer(&browserFaultGate{directory: d, next: h, drop: true})
	defer server.Close()
	send := func() *http.Response {
		r, e := http.NewRequest("POST", server.URL+"/internal/knowledge/draft", strings.NewReader(`{"revision":0,"entries":[]}`))
		if e != nil {
			t.Fatal(e)
		}
		r.Host = "admin.example"
		r.Header.Set("Origin", "https://admin.example")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Cookie", cookie)
		response, e := server.Client().Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return response
	}
	response := send()
	_, e := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || e == nil {
		t.Fatal("expected interrupted success response", response.StatusCode, e)
	}
	state, e := repo.Load(t.Context())
	if e != nil || state.Revision != 1 || len(state.Events) != 1 {
		t.Fatal("mutation did not commit exactly once")
	}
	response = send()
	defer response.Body.Close()
	if response.StatusCode != 409 {
		t.Fatal("duplicate write was not rejected")
	}
	state, _ = repo.Load(t.Context())
	if state.Revision != 1 || len(state.Events) != 1 {
		t.Fatal("replay changed state")
	}
}

func TestBrowserFaultShortTTL(t *testing.T) {
	d, h, _ := setup(t)
	gate := &browserFaultGate{directory: d, next: h, ttl: 45 * time.Second}
	cookie := login(t, gate, "admin", "")
	if request(gate, "/auth/me", "", cookie).Code != 200 {
		t.Fatal("fresh session rejected")
	}
	now := time.Now().Add(46 * time.Second)
	d.now = func() time.Time { return now }
	if request(gate, "/auth/me", "", cookie).Code != 401 {
		t.Fatal("test expiry did not occur")
	}
}

func (g *browserFaultGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	capture := httptest.NewRecorder()
	g.next.ServeHTTP(capture, r)
	if g.ttl > 0 && r.URL.Path == "/auth/login" && capture.Code == 200 {
		g.directory.mu.Lock()
		for _, c := range capture.Result().Cookies() {
			probe := httptest.NewRequest("GET", "/", nil)
			probe.AddCookie(c)
			key, s, ok := g.directory.presented(probe)
			if ok {
				s.expires = g.directory.now().Add(g.ttl)
				g.directory.tickets[key] = s
			}
		}
		g.directory.mu.Unlock()
	}
	g.mu.Lock()
	drop := g.drop && r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/internal/knowledge/") && capture.Code == 200
	if drop {
		g.drop = false
	}
	g.mu.Unlock()
	if drop {
		fmt.Println("LAB: committed mutation; deliberately dropped response")
		// Deliver headers and an incomplete body before closing, so transport
		// retries of a connection closed before headers cannot mask this case.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"revision":`))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if h, ok := w.(http.Hijacker); ok {
			connection, _, err := h.Hijack()
			if err == nil {
				connection.Close()
				return
			}
		}
		panic(http.ErrAbortHandler)
	}
	for key, values := range capture.Header() {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(capture.Code)
	_, _ = w.Write(capture.Body.Bytes())
}
