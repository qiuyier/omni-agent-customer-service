package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
)

const localDemoURL = "postgres://webchat_demo:webchat_demo_local@127.0.0.1:15433/omni_agent_webchat_demo?sslmode=disable"

func TestDemoStorageIsExplicitAndSeparate(t *testing.T) {
	config, err := resolveDemoConfig("127.0.0.1:0", "memory", func(string) (string, bool) {
		t.Fatal("memory mode must not read database or deployment settings")
		return "", false
	})
	if err != nil || config.repository.Driver != bootstrap.StorageDriverMemory {
		t.Fatal("bad memory default")
	}
	config, err = resolveDemoConfig("127.0.0.1:0", "postgres", func(key string) (string, bool) {
		if key != "WEBCHAT_DEMO_DATABASE_URL" {
			t.Fatal("read deployment database environment")
		}
		return localDemoURL, true
	})
	if err != nil || config.repository.Driver != bootstrap.StorageDriverPostgres || config.repository.DatabaseURL != localDemoURL {
		t.Fatalf("bad opt-in: %v", err)
	}
	for _, driver := range []string{"", "invalid", "postgresql"} {
		if _, err := resolveDemoConfig("127.0.0.1:0", driver, nil); err == nil {
			t.Fatal("accepted unsupported storage")
		}
	}
	if _, err := resolveDemoConfig("127.0.0.1:0", "postgres", func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("missing database accepted")
	}
}

func TestDemoDatabaseRejectsUnsafeTargetsAndOverrides(t *testing.T) {
	for _, raw := range []string{
		"", "host=127.0.0.1 user=webchat_demo dbname=omni_agent_webchat_demo", "private-connection-secret",
		strings.Replace(localDemoURL, "127.0.0.1", "localhost", 1), strings.Replace(localDemoURL, "127.0.0.1", "192.168.1.8", 1),
		strings.Replace(localDemoURL, ":15433", "", 1), strings.Replace(localDemoURL, "omni_agent_webchat_demo", "production", 1),
		strings.Replace(localDemoURL, ":webchat_demo_local", "", 1), localDemoURL + "&host=127.0.0.2", localDemoURL + "&dbname=production",
		localDemoURL + "&sslmode=disable", localDemoURL + "#fragment", strings.Replace(localDemoURL, "sslmode=disable", "sslmode=prefer", 1),
	} {
		err := validateDemoDatabaseURL(raw)
		if err == nil {
			t.Fatalf("accepted unsafe URL")
		}
		if strings.Contains(err.Error(), "private-connection-secret") || strings.Contains(err.Error(), "webchat_demo_local") {
			t.Fatal("error echoed secret")
		}
	}
	// Explicit URL identity wins even if unrelated libpq defaults are present.
	t.Setenv("PGHOST", "192.168.1.8")
	t.Setenv("PGDATABASE", "production")
	t.Setenv("PGUSER", "production")
	t.Setenv("PGPASSWORD", "private-unrelated-password")
	if err := validateDemoDatabaseURL(localDemoURL); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresUnavailableDoesNotListenOrFallBack(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	raw := "postgres://demo:private-demo-password@" + address + "/omni_agent_webchat_demo?sslmode=disable"
	config, err := resolveDemoConfig("127.0.0.1:0", "postgres", func(string) (string, bool) { return raw, true })
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = runDemoWithConfig(context.Background(), config, &output)
	if err == nil || output.Len() != 0 || !strings.Contains(err.Error(), "no memory fallback") || strings.Contains(err.Error(), "private-demo-password") {
		t.Fatal("storage failure was not safe/fail-closed")
	}
}

func TestHostStorageMetadataIsServerOwned(t *testing.T) {
	for _, storage := range []string{"memory", "postgres"} {
		f := newHostFixture(t, nil)
		f.host.storage = storage
		me := serveHost(f.host, hostRequest(http.MethodGet, "/host/me", "", nil, ""))
		if !strings.Contains(me.Body.String(), `"storage":"`+storage+`"`) {
			t.Fatal("missing anonymous metadata")
		}
		cookie := f.login(t, "lin", nil)
		boot := serveHost(f.host, hostRequest(http.MethodPost, "/host/chat-session", `{}`, cookie, ""))
		if boot.Code != 200 || !strings.Contains(boot.Body.String(), `"storage":"`+storage+`"`) {
			t.Fatalf("missing bootstrap metadata: %d", boot.Code)
		}
		if got := serveHost(f.host, hostRequest(http.MethodPost, "/host/chat-session", `{"storage":"postgres"}`, cookie, "")).Code; got != 400 {
			t.Fatal("browser changed storage mode")
		}
	}
}
