//go:build browserlab

package adminauth

import (
	"fmt"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"testing"
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
	h, e := d.Handler(&memory{})
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: h}
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
