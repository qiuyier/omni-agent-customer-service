package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testDatabaseURL = "postgres://check_user:check_password@127.0.0.1:15432/omni_agent_webchat_check?sslmode=disable"

func TestValidateDatabaseURLIsExplicitAndLocal(t *testing.T) {
	for _, raw := range []string{
		testDatabaseURL,
		"postgresql://check:pass@[::1]:5432/omni_agent_webchat_demo?sslmode=disable",
	} {
		if _, err := validateDatabaseURL(raw); err != nil {
			t.Fatalf("safe explicit URL rejected: %v", err)
		}
	}
	for _, raw := range []string{
		"", " " + testDatabaseURL, "host=127.0.0.1 dbname=omni_agent_webchat_check",
		"postgres:///omni_agent_webchat_check?sslmode=disable",
		"postgres://check:pass@localhost:5432/omni_agent_webchat_check?sslmode=disable",
		"postgres://check:pass@192.0.2.1:5432/omni_agent_webchat_check?sslmode=disable",
		"postgres://check:pass@127.0.0.2:5432/omni_agent_webchat_check?sslmode=disable",
		"postgres://check:pass@127.0.0.1/omni_agent_webchat_check?sslmode=disable",
		"postgres://check:pass@127.0.0.1:0/omni_agent_webchat_check?sslmode=disable",
		"postgres://check:pass@127.0.0.1:65536/omni_agent_webchat_check?sslmode=disable",
		"postgres://check@127.0.0.1:5432/omni_agent_webchat_check?sslmode=disable",
		"postgres://check:@127.0.0.1:5432/omni_agent_webchat_check?sslmode=disable",
		"postgres://:pass@127.0.0.1:5432/omni_agent_webchat_check?sslmode=disable",
		"postgres://check:pass@127.0.0.1:5432/production?sslmode=disable",
		"postgres://check:pass@127.0.0.1:5432/another_test?sslmode=disable",
		"postgres://check:pass@127.0.0.1:5432/%6fmni_agent_webchat_check?sslmode=disable",
		"postgres://check:pass@127.0.0.1:5432/omni_agent_webchat_check",
		testDatabaseURL + "&host=remote.invalid",
		testDatabaseURL + "&hostaddr=192.0.2.1",
		testDatabaseURL + "&service=production",
		testDatabaseURL + "&dbname=production",
		testDatabaseURL + "&sslmode=verify-full",
		testDatabaseURL + "#fragment",
		strings.Replace(testDatabaseURL, "sslmode=disable", "sslmode=require", 1),
	} {
		if _, err := validateDatabaseURL(raw); err == nil {
			t.Errorf("accepted an unsafe or ambiguous database URL")
		} else if strings.Contains(err.Error(), "check_password") || strings.Contains(err.Error(), raw) && raw != "" {
			t.Errorf("database rejection echoed supplied configuration")
		}
	}
}

func fixtureBinary(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func lookupDatabase(name string) (string, bool) {
	if name == databaseEnvironment {
		return testDatabaseURL, true
	}
	return "", false
}

func TestOptionsRequireExactBinaryAndNewOutputPaths(t *testing.T) {
	binary := fixtureBinary(t, "exit 0\n")
	dir := filepath.Join(t.TempDir(), "new-output")
	base := []string{"-server-binary", binary, "-output-dir", dir}
	if _, err := parseOptions(base, lookupDatabase); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"-server-binary", "relative", "-output-dir", dir},
		{"-server-binary", binary, "-output-dir", "relative"},
		{"-server-binary", binary, "-output-dir", "/"},
		{"-server-binary", binary, "-output-dir", dir, "extra"},
		{"-server-binary", binary, "-output-dir", dir, "-unknown-secret-value"},
	} {
		if _, err := parseOptions(args, lookupDatabase); err == nil {
			t.Fatal("invalid command arguments accepted")
		}
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(binary, link); err != nil {
		t.Fatal(err)
	}
	if validBinary(link) {
		t.Fatal("symlink binary accepted")
	}
	if _, err := parseOptions(base, func(string) (string, bool) { return "", false }); err == nil {
		t.Fatal("missing scoped database variable accepted")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), base, lookupDatabase, io.Discard); err == nil {
		t.Fatal("existing output directory reused")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("existing output directory was modified")
	}
}

func TestChildEnvironmentDoesNotInheritDeploymentOrPGSettings(t *testing.T) {
	for _, key := range []string{"PGHOST", "PGSERVICE", "PGPASSFILE", "HTTP_PROXY", "HTTPS_PROXY", "OPENAI_API_KEY", "DATABASE_URL", "HOME"} {
		t.Setenv(key, "must-not-be-inherited")
	}
	opts := options{databaseURL: testDatabaseURL}
	for _, demo := range []bool{false, true} {
		values := childEnvironment(opts, "check-host", "independent-check-secret", demo)
		for _, value := range values {
			if strings.Contains(value, "must-not-be-inherited") || strings.HasPrefix(value, "PG") || strings.HasPrefix(value, "HOME=") {
				t.Fatal("child inherited ambient settings")
			}
		}
		joined := strings.Join(values, "\n")
		if demo && (strings.Contains(joined, "WEBCHAT_HOST_SECRET=") || !strings.Contains(joined, "WEBCHAT_DEMO_DATABASE_URL=")) {
			t.Fatal("demo inherited gateway credentials")
		}
		if !demo && (!strings.Contains(joined, "WEBCHAT_REPLY_MODE=fake") || !strings.Contains(joined, "DATABASE_URL=")) {
			t.Fatal("gateway environment is incomplete")
		}
	}
}

func TestStartupOriginRejectsNonLoopbackAndAdditionalData(t *testing.T) {
	prefix := "Web chat server: "
	for _, value := range []string{"http://127.0.0.1:1234", "http://[::1]:1234"} {
		if got, err := startupOrigin(prefix+value+"\n", prefix); err != nil || got != value {
			t.Fatal("valid origin rejected")
		}
	}
	for _, line := range []string{
		"", prefix + "http://localhost:1234\n", prefix + "http://192.0.2.1:1234\n",
		prefix + "https://127.0.0.1:1234\n", prefix + "http://user:pass@127.0.0.1:1234\n",
		prefix + "http://127.0.0.1:1234/\n", prefix + "http://127.0.0.1:1234?secret=value\n",
		prefix + "http://127.0.0.1:1234#secret\n", prefix + "http://127.0.0.1:0\n",
		prefix + "http://127.0.0.1:1234\nsecret\n", prefix + "http://127.0.0.1:1234",
	} {
		if _, err := startupOrigin(line, prefix); err == nil {
			t.Fatal("unsafe startup output accepted")
		}
	}
}

func TestFailedChildCannotProducePassingReportOrLeakOutput(t *testing.T) {
	binary := fixtureBinary(t, "printf 'raw-private-child-output\\n'\nprintf 'raw-private-child-error\\n' >&2\nexit 19\n")
	dir := filepath.Join(t.TempDir(), "result")
	err := run(context.Background(), []string{"-server-binary", binary, "-output-dir", dir}, lookupDatabase, io.Discard)
	if err == nil {
		t.Fatal("failed child passed acceptance")
	}
	data, readErr := os.ReadFile(filepath.Join(dir, "report.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, value := range []string{"raw-private", "check_password", "DATABASE_URL", "access_token"} {
		if strings.Contains(string(data), value) || strings.Contains(err.Error(), value) {
			t.Fatal("report or error leaked private inputs")
		}
	}
	var result report
	if json.Unmarshal(data, &result) != nil || result.Passed || len(result.Checks) == 0 || result.Checks[0].Passed {
		t.Fatal("failed child produced an invalid passing report")
	}
	if len(result.Processes) != 1 || !result.Processes[0].Waited || result.Processes[0].PID <= 0 {
		t.Fatal("failed child was not reaped and recorded")
	}
}

func TestCrashWaitsForExactChild(t *testing.T) {
	binary := fixtureBinary(t, "printf 'Web chat server: http://127.0.0.1:1234\\n'\nexec /bin/sleep 30\n")
	h := newHarness(context.Background(), options{serverBinary: binary, outputDir: t.TempDir(), databaseURL: testDatabaseURL})
	defer h.client.CloseIdleConnections()
	defer h.cleanup()
	child, err := h.start("fixture", "check-host", "independent-check-secret", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.stop(true); err != nil {
		t.Fatal(err)
	}
	if !child.result.Waited || child.result.Stop != "kill" {
		t.Fatal("crash was not reaped")
	}
	if err := child.stop(true); err == nil {
		t.Fatal("an already exited child was accepted as another crash")
	}
}

func TestWithheldAcknowledgementNeverReachesCaller(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h := newHarness(ctx, options{})
	defer h.client.CloseIdleConnections()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"conversation_id":"conversation","message_id":"message","reply_message_id":"reply","outcome":"replied"}`)
	}))
	defer upstream.Close()
	sent, release, err := h.holdAcknowledgement(&childProcess{origin: upstream.URL}, session{ConversationID: "conversation", AccessToken: "restricted-token"}, "event", "content")
	if err != nil {
		t.Fatal(err)
	}
	if sent.MessageID != "message" || sent.ReplyMessageID != "reply" {
		t.Fatal("proxy did not observe the committed acknowledgement")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestReportIsExclusiveAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if err := writeReport(path, report{Version: 1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("report mode is not private")
	}
	if err := writeReport(path, report{Passed: true}); err == nil {
		t.Fatal("report was overwritten")
	}
}
