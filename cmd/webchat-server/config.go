package main

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
)

const defaultServerAddress = "127.0.0.1:8093"

type serverConfig struct {
	Address     string
	HostID      string
	HostSecret  string
	DatabaseURL string
	ReplyMode   string
	Reply       bootstrap.WebchatReplyConfig
}

// AI and knowledge settings are read only when explicitly enabled. This entry
// never consumes CRMEB, service-token or demo-login configuration.
func resolveConfig(lookup func(string) (string, bool), addressOverride string) (serverConfig, error) {
	if lookup == nil {
		return serverConfig{}, errors.New("environment lookup is required")
	}
	value := func(name string) string { result, _ := lookup(name); return result }
	config := serverConfig{
		Address: value("WEBCHAT_ADDRESS"), HostID: value("WEBCHAT_HOST_ID"),
		HostSecret: value("WEBCHAT_HOST_SECRET"), DatabaseURL: value("DATABASE_URL"),
		ReplyMode: value("WEBCHAT_REPLY_MODE"),
	}
	if config.Address == "" {
		config.Address = defaultServerAddress
	}
	if addressOverride != "" {
		config.Address = addressOverride
	}
	if config.ReplyMode == "" {
		config.ReplyMode = "fake"
	}
	if driver, supplied := lookup("STORAGE_DRIVER"); supplied && driver != "postgres" && driver != "postgresql" {
		return serverConfig{}, errors.New("STORAGE_DRIVER must be postgres or postgresql; memory storage is not supported")
	}
	var err error
	config.Reply, err = bootstrap.ResolveWebchatReplyConfig(config.ReplyMode, lookup)
	if err != nil {
		return serverConfig{}, err
	}
	if err := config.validate(); err != nil {
		return serverConfig{}, err
	}
	return config, nil
}

func (c serverConfig) validate() error {
	if c.HostID == "" || len(c.HostID) > 128 || !utf8.ValidString(c.HostID) ||
		strings.ContainsFunc(c.HostID, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return errors.New("WEBCHAT_HOST_ID must be a stable non-whitespace identifier of 1..128 bytes")
	}
	if strings.EqualFold(c.HostID, "example-host") || strings.EqualFold(c.HostID, "webchat-demo") {
		return errors.New("WEBCHAT_HOST_ID must not use a reserved demo identifier")
	}
	if len(c.HostSecret) < 32 || len(c.HostSecret) > 128 || !utf8.ValidString(c.HostSecret) ||
		strings.ContainsFunc(c.HostSecret, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) ||
		strings.HasPrefix(c.HostSecret, "chat_") {
		return errors.New("WEBCHAT_HOST_SECRET must be an independent random 32..128 byte credential without whitespace or the chat_ prefix")
	}
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return errors.New("DATABASE_URL is required; PostgreSQL storage has no memory fallback")
	}
	if _, err := pgxpool.ParseConfig(c.DatabaseURL); err != nil {
		return errors.New("DATABASE_URL is invalid; no connection was attempted")
	}
	if (c.ReplyMode != "fake" && c.ReplyMode != "ai") || c.Reply.Mode != c.ReplyMode {
		return errors.New("WEBCHAT_REPLY_MODE must be fake or ai and match the resolved reply configuration")
	}
	if err := c.Reply.Validate(); err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(c.Address)
	if err != nil {
		return errors.New("WEBCHAT_ADDRESS must be a literal loopback IP and valid port")
	}
	ip := net.ParseIP(host)
	number, err := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || err != nil || number < 0 || number > 65535 {
		return errors.New("WEBCHAT_ADDRESS must be a literal loopback IP and valid port")
	}
	return nil
}

func (c serverConfig) repositoryConfig() bootstrap.RepositoryConfig {
	return bootstrap.RepositoryConfig{Driver: bootstrap.StorageDriverPostgres, DatabaseURL: c.DatabaseURL}
}
