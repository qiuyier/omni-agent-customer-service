package main

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
)

const demoDatabaseName = "omni_agent_webchat_demo"

type demoConfig struct {
	address    string
	repository bootstrap.RepositoryConfig
	reply      bootstrap.WebchatReplyConfig
}

// The simulated-account executable never consumes the real deployment's
// DATABASE_URL. PostgreSQL is opt-in and restricted to one local demo database.
func resolveDemoConfig(address, driver string, lookup func(string) (string, bool)) (demoConfig, error) {
	return resolveDemoConfigWithReply(address, driver, "fake", lookup)
}

func resolveDemoConfigWithReply(address, driver, mode string, lookup func(string) (string, bool)) (demoConfig, error) {
	config := demoConfig{address: address}
	if err := validateDemoAddress(address); err != nil {
		return config, err
	}
	var err error
	config.reply, err = bootstrap.ResolveWebchatReplyConfig(mode, lookup)
	if err != nil {
		return config, err
	}
	switch driver {
	case "memory":
		config.repository.Driver = bootstrap.StorageDriverMemory
		return config, nil
	case "postgres":
		if lookup == nil {
			return config, errors.New("demo environment reader is required")
		}
		raw, _ := lookup("WEBCHAT_DEMO_DATABASE_URL")
		if err := validateDemoDatabaseURL(raw); err != nil {
			return config, err
		}
		config.repository = bootstrap.RepositoryConfig{Driver: bootstrap.StorageDriverPostgres, DatabaseURL: raw}
		return config, nil
	default:
		return config, errors.New("demo storage must be memory or postgres")
	}
}

func validateDemoDatabaseURL(raw string) error {
	failure := errors.New("WEBCHAT_DEMO_DATABASE_URL must explicitly name a literal loopback host, port, user, password and omni_agent_webchat_demo database with sslmode=disable; no other query options")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Opaque != "" || u.Fragment != "" || u.User == nil || strings.TrimSpace(raw) != raw {
		return failure
	}
	password, hasPassword := u.User.Password()
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	ip := net.ParseIP(u.Hostname())
	if err != nil || port == 0 || ip == nil || !ip.IsLoopback() || u.User.Username() == "" || !hasPassword || password == "" || u.Path != "/"+demoDatabaseName {
		return failure
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query) != 1 || len(query["sslmode"]) != 1 || query.Get("sslmode") != "disable" {
		return failure
	}
	// Validate the effective pgx target, too: defaults/environment must never
	// redirect this simulated-account process to another host or database.
	parsed, err := pgx.ParseConfig(raw)
	if err != nil || parsed.Host != u.Hostname() || parsed.Port != uint16(port) || parsed.Database != demoDatabaseName || parsed.User != u.User.Username() || parsed.Password != password || parsed.TLSConfig != nil {
		return failure
	}
	for _, fallback := range parsed.Fallbacks {
		if fallback.Host != parsed.Host || fallback.Port != parsed.Port || fallback.TLSConfig != nil {
			return failure
		}
	}
	return nil
}
