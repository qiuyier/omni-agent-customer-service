// Command webchat-restart-check verifies the web chat process boundary against
// an explicitly supplied, already migrated local acceptance database. It never
// creates, migrates, truncates or drops a database, and never calls a real model.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const databaseEnvironment = "WEBCHAT_CHECK_DATABASE_URL"

type options struct {
	serverBinary string
	demoBinary   string
	outputDir    string
	databaseURL  string
}

type checkResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

type processResult struct {
	Role   string `json:"role"`
	PID    int    `json:"pid"`
	Stop   string `json:"stop"`
	Waited bool   `json:"waited"`
}

type report struct {
	Version    int             `json:"version"`
	Passed     bool            `json:"passed"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	Demo       bool            `json:"demo_checked"`
	Checks     []checkResult   `json:"checks"`
	Processes  []processResult `json:"processes"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.LookupEnv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "webchat-restart-check:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, lookup func(string) (string, bool), output io.Writer) error {
	opts, err := parseOptions(args, lookup)
	if err != nil {
		return err
	}
	if ctx == nil || output == nil {
		return errors.New("context and output are required")
	}
	if err := ctx.Err(); err != nil {
		return errors.New("acceptance was canceled")
	}
	// Never reuse or overwrite an existing report directory, including a symlink.
	if err := os.Mkdir(opts.outputDir, 0700); err != nil {
		return errors.New("output directory must be a new directory with an existing parent")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	h := newHarness(ctx, opts)
	defer h.client.CloseIdleConnections()
	h.report.StartedAt = time.Now().UTC()
	err = h.accept()
	cleanupErr := h.cleanup()
	h.report.Checks = append(h.report.Checks, checkResult{Name: "all_child_processes_reaped", Passed: cleanupErr == nil})
	if cleanupErr != nil {
		err = errors.New("child cleanup did not complete successfully")
	}
	h.report.Passed = err == nil
	h.report.FinishedAt = time.Now().UTC()
	for _, child := range h.children {
		h.report.Processes = append(h.report.Processes, child.result)
	}
	path := filepath.Join(opts.outputDir, "report.json")
	if writeErr := writeReport(path, h.report); writeErr != nil {
		return writeErr
	}
	if err != nil {
		return fmt.Errorf("acceptance failed; sanitized report: %s", path)
	}
	if _, err := fmt.Fprintf(output, "Web chat restart acceptance passed: %s\n", path); err != nil {
		return errors.New("cannot display acceptance report path")
	}
	return nil
}

func parseOptions(args []string, lookup func(string) (string, bool)) (options, error) {
	if lookup == nil {
		return options{}, errors.New("environment lookup is required")
	}
	var opts options
	flags := flag.NewFlagSet("webchat-restart-check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.serverBinary, "server-binary", "", "absolute path to a built webchat-server binary")
	flags.StringVar(&opts.demoBinary, "demo-binary", "", "optional absolute path to a built webchat-demo binary")
	flags.StringVar(&opts.outputDir, "output-dir", "", "new absolute directory for a sanitized report")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return options{}, errors.New("expected -server-binary /absolute/binary [-demo-binary /absolute/binary] -output-dir /absolute/new-directory")
	}
	if !validBinary(opts.serverBinary) || (opts.demoBinary != "" && !validBinary(opts.demoBinary)) {
		return options{}, errors.New("binary paths must name executable regular files using absolute paths, not symlinks")
	}
	if !filepath.IsAbs(opts.outputDir) || filepath.Clean(opts.outputDir) != opts.outputDir || opts.outputDir == string(filepath.Separator) {
		return options{}, errors.New("output directory must be a clean absolute path other than the filesystem root")
	}
	value, _ := lookup(databaseEnvironment)
	databaseURL, err := validateDatabaseURL(value)
	if err != nil {
		return options{}, err
	}
	opts.databaseURL = databaseURL
	if opts.demoBinary != "" {
		parsed, _ := url.Parse(databaseURL)
		if parsed.Path != "/omni_agent_webchat_demo" {
			return options{}, errors.New("demo acceptance requires the dedicated omni_agent_webchat_demo database")
		}
	}
	return opts, nil
}

func validBinary(path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

// The child's pgx parser sees only this fully explicit URL and a fresh minimal
// environment. Reject alternative libpq syntax, hosts, services, fallbacks,
// socket paths and query overrides before pgx can interpret them. The checker
// itself never parses pgx configuration or reads PG environment/config files.
func validateDatabaseURL(raw string) (string, error) {
	invalid := errors.New("WEBCHAT_CHECK_DATABASE_URL must explicitly name a loopback PostgreSQL acceptance database, user, password, port and sslmode=disable")
	if raw == "" || strings.TrimSpace(raw) != raw || len(raw) > 4096 {
		return "", invalid
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.Opaque != "" || u.Fragment != "" || u.RawPath != "" {
		return "", invalid
	}
	password, hasPassword := u.User.Password()
	if u.User.Username() == "" || !hasPassword || password == "" || strings.ContainsAny(u.User.Username()+password, "\x00\r\n") {
		return "", invalid
	}
	if u.Hostname() != "127.0.0.1" && u.Hostname() != "::1" {
		return "", invalid
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || u.Host != net.JoinHostPort(u.Hostname(), u.Port()) {
		return "", invalid
	}
	if u.Path != "/omni_agent_webchat_demo" && u.Path != "/omni_agent_webchat_check" {
		return "", invalid
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query) != 1 || len(query["sslmode"]) != 1 || query.Get("sslmode") != "disable" {
		return "", invalid
	}
	return u.String(), nil
}

func randomID() (string, error) {
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", errors.New("cannot initialize acceptance identifiers")
	}
	return hex.EncodeToString(value[:]), nil
}

func writeReport(path string, value report) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("cannot create a new acceptance report")
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encodeErr := encoder.Encode(value)
	syncErr := file.Sync()
	closeErr := file.Close()
	if encodeErr != nil || syncErr != nil || closeErr != nil {
		return errors.New("cannot persist the acceptance report")
	}
	return nil
}
