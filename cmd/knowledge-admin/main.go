// Command knowledge-admin is an explicitly configured, loopback-only admin API.
// It does not migrate, enable AI, or serve the customer/staff login.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/adminauth"
	pg "github.com/qiuyier/omni-agent-customer-service/internal/persistence/postgres"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if e := run(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stdout); e != nil {
		fmt.Fprintln(os.Stderr, "knowledge-admin:", e)
		os.Exit(1)
	}
}
func accounts(path string) ([]adminauth.Account, error) {
	info, e := os.Stat(path)
	if e != nil || !info.Mode().IsRegular() {
		return nil, errors.New("administrator configuration must be a regular file")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, errors.New("cannot open administrator configuration")
	}
	defer f.Close()
	info, e = f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 65536 {
		return nil, errors.New("administrator configuration must be a private regular file (0600, at most 64 KiB)")
	}
	b, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil || len(b) > 65536 {
		return nil, errors.New("cannot read administrator configuration")
	}
	var a []adminauth.Account
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&a) != nil || d.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid administrator configuration JSON")
	}
	return a, nil
}
func run(ctx context.Context, args []string, getenv func(string) string, input io.Reader, out io.Writer) error {
	flags := flag.NewFlagSet("knowledge-admin", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	hash := flags.Bool("hash-password", false, "hash password from stdin")
	check := flags.Bool("check-config", false, "check configuration without connecting")
	if e := flags.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			_, e = fmt.Fprintln(out, "knowledge-admin [-check-config | -hash-password]\nRequires DATABASE_URL and private KNOWLEDGE_ADMIN_ACCOUNTS_FILE. Optional KNOWLEDGE_ADMIN_ADDRESS (default 127.0.0.1:8094), KNOWLEDGE_ADMIN_ORIGIN (default http://127.0.0.1:8094). API only; no UI, automatic migrations or models. -hash-password reads 16..512 bytes from stdin, removing one final newline; never pass passwords as arguments.")
			return e
		}
		return errors.New("invalid flags")
	}
	if flags.NArg() != 0 || *hash && *check {
		return errors.New("invalid flags")
	}
	if *hash {
		if f, ok := input.(*os.File); ok {
			info, e := f.Stat()
			if e != nil || info.Mode()&os.ModeCharDevice != 0 {
				return errors.New("pipe password through stdin; interactive terminal echo is not supported")
			}
		}
		b, e := io.ReadAll(io.LimitReader(input, 515))
		if e != nil || len(b) > 514 {
			return errors.New("invalid password input")
		}
		b = bytes.TrimSuffix(b, []byte("\n"))
		b = bytes.TrimSuffix(b, []byte("\r"))
		v, e := adminauth.HashPassword(b)
		if e != nil {
			return e
		}
		_, e = fmt.Fprintln(out, v)
		return e
	}
	address := getenv("KNOWLEDGE_ADMIN_ADDRESS")
	if address == "" {
		address = "127.0.0.1:8094"
	}
	host, port, e := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	n, pe := strconv.Atoi(port)
	if e != nil || pe != nil || ip == nil || !ip.IsLoopback() || n < 1 || n > 65535 {
		return errors.New("administrator listen address must be literal loopback with a valid port")
	}
	origin := getenv("KNOWLEDGE_ADMIN_ORIGIN")
	if origin == "" {
		origin = "http://127.0.0.1:8094"
	}
	list, e := accounts(getenv("KNOWLEDGE_ADMIN_ACCOUNTS_FILE"))
	if e != nil {
		return e
	}
	directory, e := adminauth.New(list, origin)
	if e != nil {
		return e
	}
	dsn := getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	if _, e := pgxpool.ParseConfig(dsn); e != nil {
		return errors.New("invalid DATABASE_URL")
	}
	if *check {
		_, e = fmt.Fprintln(out, "Administrator configuration is valid. Database/schema, listener and real login were not checked.")
		return e
	}
	probe, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	repository, e := pg.Open(probe, pg.Config{DatabaseURL: dsn})
	if e != nil {
		return errors.New("administrator database unavailable; verify connectivity and schema migrations")
	}
	defer repository.Close()
	store, e := repository.KnowledgeStore()
	if e != nil {
		return errors.New("administrator store unavailable")
	}
	handler, e := directory.Handler(store)
	if e != nil {
		return e
	}
	listener, e := net.Listen("tcp", address)
	if e != nil {
		return errors.New("administrator listener unavailable")
	}
	defer listener.Close()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if server.Shutdown(c) != nil {
				_ = server.Close()
			}
		case <-done:
		}
	}()
	if _, e = fmt.Fprintln(out, "Knowledge administrator API started on configured loopback listener. No UI or model enabled."); e != nil {
		return errors.New("cannot write startup status")
	}
	if e = server.Serve(listener); e != nil && !errors.Is(e, http.ErrServerClosed) {
		return errors.New("administrator server stopped unexpectedly")
	}
	return nil
}
