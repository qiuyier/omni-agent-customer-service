package main

import (
	"strings"
	"testing"
)

func TestMultiChildEnvironmentHasOnlyExplicitPostgresAndFakeConfiguration(t *testing.T) {
	t.Setenv("AI_API_KEY", "private-do-not-inherit")
	t.Setenv("PGSERVICE", "private-do-not-inherit")
	env := multiChildEnvironment(options{databaseURL: testDatabaseURL}, "host-json", "agent-json")
	got := map[string]string{}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		got[key] = value
		if strings.Contains(entry, "private-do-not-inherit") {
			t.Fatal("inherited ambient configuration")
		}
	}
	if len(got) != 9 || got["WEBCHAT_HOSTS_JSON"] != "host-json" || got["WEBCHAT_AGENTS_JSON"] != "agent-json" || got["DATABASE_URL"] != testDatabaseURL || got["WEBCHAT_REPLY_MODE"] != "fake" || got["STORAGE_DRIVER"] != "postgres" {
		t.Fatal("incorrect multi-host child configuration")
	}
	if _, ok := got["WEBCHAT_HOST_SECRET"]; ok {
		t.Fatal("mixed legacy credential")
	}
}
