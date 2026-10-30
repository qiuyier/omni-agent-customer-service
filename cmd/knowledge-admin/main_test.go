package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/adminauth"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/manage"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigurationIsOfflineAndPrivate(t *testing.T) {
	hash, e := adminauth.HashPassword([]byte("fictional-password-only"))
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal([]adminauth.Account{{ID: "admin", PasswordHash: hash, Permissions: []manage.Permission{manage.Read, manage.Edit, manage.Publish}}})
	path := filepath.Join(t.TempDir(), "accounts.json")
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
	env := map[string]string{"KNOWLEDGE_ADMIN_ACCOUNTS_FILE": path, "DATABASE_URL": "postgres://unused:unused@127.0.0.1:1/unused"}
	lookup := func(k string) string { return env[k] }
	var out bytes.Buffer
	if e = run(context.Background(), []string{"-check-config"}, lookup, strings.NewReader(""), &out); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(out.String(), hash) || strings.Contains(out.String(), path) || !strings.Contains(out.String(), "not checked") {
		t.Fatal("unsafe status")
	}
	os.Chmod(path, 0644)
	if e = run(context.Background(), []string{"-check-config"}, lookup, strings.NewReader(""), &out); e == nil {
		t.Fatal("public account file accepted")
	}
	os.Chmod(path, 0600)
	env["KNOWLEDGE_ADMIN_ADDRESS"] = "0.0.0.0:8094"
	if e = run(context.Background(), []string{"-check-config"}, lookup, strings.NewReader(""), &out); e == nil {
		t.Fatal("public listener accepted")
	}
}
func TestHashModeNeverReadsEnvironment(t *testing.T) {
	var out bytes.Buffer
	e := run(context.Background(), []string{"-hash-password"}, func(string) string { t.Fatal("environment read"); return "" }, strings.NewReader("fictional-password-only\n"), &out)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.HasPrefix(out.String(), "scrypt-v1$") || strings.Contains(out.String(), "fictional") {
		t.Fatal("bad hash output")
	}
}
