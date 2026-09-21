package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
)

func validEnvironment() map[string]string {
	return map[string]string{
		"WEBCHAT_HOST_ID": "shop-support", "WEBCHAT_HOST_SECRET": "0123456789abcdefghijklmnopqrstuvwxyz-ABCDEF",
		"DATABASE_URL": "postgres://unit-user:unit-password@127.0.0.1:1/unit-db?sslmode=disable",
	}
}

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
}

func validConfig(t *testing.T) serverConfig {
	t.Helper()
	config, err := resolveConfig(mapLookup(validEnvironment()), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func TestConfigDefaultsArePostgresAndFakeOnly(t *testing.T) {
	allowed := map[string]bool{"WEBCHAT_ADDRESS": true, "WEBCHAT_HOST_ID": true, "WEBCHAT_HOST_SECRET": true, "DATABASE_URL": true, "WEBCHAT_REPLY_MODE": true, "STORAGE_DRIVER": true}
	values := validEnvironment()
	config, err := resolveConfig(func(key string) (string, bool) {
		if !allowed[key] {
			t.Fatalf("unexpected environment lookup %s", key)
		}
		return mapLookup(values)(key)
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if config.Address != defaultServerAddress || config.ReplyMode != "fake" {
		t.Fatalf("incorrect defaults")
	}
	storage := config.repositoryConfig()
	if storage.Driver != bootstrap.StorageDriverPostgres || storage.DatabaseURL != values["DATABASE_URL"] {
		t.Fatalf("PostgreSQL configuration not preserved")
	}
	for _, driver := range []string{"postgres", "postgresql"} {
		values["STORAGE_DRIVER"] = driver
		if _, err := resolveConfig(mapLookup(values), ""); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConfigRejectsMissingAndUnsafeValuesWithoutEcho(t *testing.T) {
	cases := []struct{ name, key, value string }{
		{"missing host", "WEBCHAT_HOST_ID", ""},
		{"demo host", "WEBCHAT_HOST_ID", "example-host"},
		{"demo command host", "WEBCHAT_HOST_ID", "webchat-demo"},
		{"case demo host", "WEBCHAT_HOST_ID", "EXAMPLE-HOST"},
		{"spaced host", "WEBCHAT_HOST_ID", "private host value"},
		{"control host", "WEBCHAT_HOST_ID", "private\x00host"},
		{"long host", "WEBCHAT_HOST_ID", strings.Repeat("privatehost", 20)},
		{"invalid utf8 host", "WEBCHAT_HOST_ID", "private\xffhost"},
		{"missing secret", "WEBCHAT_HOST_SECRET", ""},
		{"short secret", "WEBCHAT_HOST_SECRET", "private-short-secret"},
		{"long secret", "WEBCHAT_HOST_SECRET", strings.Repeat("private", 20)},
		{"space secret", "WEBCHAT_HOST_SECRET", strings.Repeat("x", 32) + " private"},
		{"unicode whitespace secret", "WEBCHAT_HOST_SECRET", strings.Repeat("x", 32) + "\u2003"},
		{"control secret", "WEBCHAT_HOST_SECRET", strings.Repeat("x", 32) + "\x00"},
		{"browser token", "WEBCHAT_HOST_SECRET", "chat_" + strings.Repeat("x", 40)},
		{"missing database", "DATABASE_URL", ""},
		{"blank database", "DATABASE_URL", "\t "},
		{"malformed database", "DATABASE_URL", "private-invalid-dsn"},
		{"memory", "STORAGE_DRIVER", "memory"},
		{"blank explicit storage", "STORAGE_DRIVER", ""},
		{"other storage", "STORAGE_DRIVER", "private-unsupported-storage"},
		{"real reply", "WEBCHAT_REPLY_MODE", "private-real-model"},
		{"hostname", "WEBCHAT_ADDRESS", "localhost:8093"},
		{"wildcard", "WEBCHAT_ADDRESS", "0.0.0.0:8093"},
		{"ipv6 wildcard", "WEBCHAT_ADDRESS", "[::]:8093"},
		{"private network", "WEBCHAT_ADDRESS", "192.168.1.2:8093"},
		{"invalid port", "WEBCHAT_ADDRESS", "127.0.0.1:65536"},
		{"negative port", "WEBCHAT_ADDRESS", "127.0.0.1:-1"},
		{"invalid address", "WEBCHAT_ADDRESS", "private-invalid-address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := validEnvironment()
			values[tc.key] = tc.value
			_, err := resolveConfig(mapLookup(values), "")
			if err == nil {
				t.Fatal("expected invalid configuration")
			}
			for _, secret := range []string{values["WEBCHAT_HOST_SECRET"], values["DATABASE_URL"], "private"} {
				if len(secret) > 4 && strings.Contains(err.Error(), secret) {
					t.Fatal("error leaked configuration values")
				}
			}
		})
	}
}

func TestConfigAddressOverrideAndIPv6Loopback(t *testing.T) {
	values := validEnvironment()
	values["WEBCHAT_ADDRESS"] = "127.0.0.1:9000"
	for _, address := range []string{"127.0.0.1:0", "[::1]:8093", "127.2.3.4:65535"} {
		config, err := resolveConfig(mapLookup(values), address)
		if err != nil || config.Address != address {
			t.Fatalf("override failed: %v", err)
		}
	}
}

func TestCheckConfigDoesNotOpenDatabaseOrListen(t *testing.T) {
	var output bytes.Buffer
	if err := runCommand(context.Background(), []string{"-check-config"}, mapLookup(validEnvironment()), &output, serverDependencies{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "were not checked") {
		t.Fatal("check-config should distinguish validation from readiness")
	}
	for _, value := range validEnvironment() {
		if strings.Contains(output.String(), value) {
			t.Fatal("check-config leaked private configuration")
		}
	}
	values := validEnvironment()
	delete(values, "DATABASE_URL")
	if err := runCommand(context.Background(), []string{"-check-config"}, mapLookup(values), &output, serverDependencies{}); err == nil {
		t.Fatal("missing database accepted")
	}
}

func TestCommandLineErrorsNeverEchoArguments(t *testing.T) {
	for _, args := range [][]string{{"-private-super-secret"}, {"private-super-secret"}, {"-check-config=private-super-secret"}} {
		var output bytes.Buffer
		err := runCommand(context.Background(), args, mapLookup(validEnvironment()), &output, serverDependencies{})
		if err == nil || strings.Contains(err.Error()+output.String(), "private-super-secret") {
			t.Fatal("unsafe command-line error")
		}
	}
}
