package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/qiuyier/omni-agent-customer-service/internal/knowledge/faq"
)

// Deliberately does not construct a gateway, provider or semantic index.
// Reuse startup's strict FAQ parser and PostgreSQL readiness contract only.
func checkResources(ctx context.Context, config serverConfig, output io.Writer, deps serverDependencies) error {
	if err := config.validate(); err != nil {
		return err
	}
	if ctx == nil || output == nil || deps.open == nil || deps.startupTimeout <= 0 {
		return errors.New("invalid resource check dependency")
	}
	if ctx.Err() != nil {
		return errors.New("resource check was canceled")
	}
	knowledge := "disabled (not inspected)"
	if config.ReplyMode == "ai" && strings.TrimSpace(config.Reply.Knowledge.FAQPath) != "" {
		snapshot, _, err := faq.LoadVersion(config.Reply.Knowledge.FAQPath)
		if err != nil {
			return errors.New("knowledge file check failed; verify KNOWLEDGE_FAQ_PATH, UTF-8 JSON FAQ format and 1 MiB limit; no fake fallback")
		}
		knowledge = fmt.Sprintf("valid local FAQ (%d entries; semantic index not built)", len(snapshot.ReferenceIDs()))
	}
	probeContext, cancel := context.WithTimeout(ctx, deps.startupTimeout)
	defer cancel()
	repository, err := deps.open(probeContext, config.repositoryConfig())
	if err != nil {
		return errors.New("PostgreSQL resource check failed; verify DATABASE_URL, connectivity and schema migrations (cmd/migrate); no memory fallback")
	}
	if repository == nil || repository.check == nil || repository.close == nil {
		if repository != nil && repository.close != nil {
			repository.close()
		}
		return errors.New("PostgreSQL resource check is unavailable")
	}
	defer repository.close()
	if err := repository.check(probeContext); err != nil || probeContext.Err() != nil {
		return errors.New("PostgreSQL readiness check failed; verify connectivity and schema migrations (cmd/migrate)")
	}
	if _, err := fmt.Fprintf(output, "Resource checks passed. Reply mode: %s. PostgreSQL connectivity/schema: passed. Knowledge: %s.\nNo model or embedding requests, migrations, messages or listener were created. Model credentials/availability, semantic indexing, port availability and host authentication/proxy integration remain unverified.\n", config.ReplyMode, knowledge); err != nil {
		return errors.New("cannot display resource check status")
	}
	return nil
}
