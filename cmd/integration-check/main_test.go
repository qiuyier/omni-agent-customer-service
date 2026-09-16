package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/common/hlog"
)

// Only selected in a subprocess; os.Exit avoids test-runner output on stdout.
func TestAcceptanceWorker(t *testing.T) {
	if os.Getenv("INTEGRATION_ACCEPTANCE_WORKER") != "1" {
		t.Skip("subprocess helper")
	}
	if err := workerMain(os.Stdin, os.Stdout); err != nil {
		os.Stderr.WriteString(err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}
func TestHTTPProcessAcceptance(t *testing.T) {
	hlog.SetOutput(os.Stderr)
	hlog.SetLevel(hlog.LevelError)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	report, err := runAcceptance(ctx, t.TempDir(), workerProgram{Path: os.Args[0], Args: []string{"-test.run=^TestAcceptanceWorker$"}})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Passed || len(report.Cases) != 8 {
		t.Fatalf("%+v", report)
	}
	killed := 0
	for _, c := range report.Cases {
		if !c.Passed || len(c.WorkerPIDs) < 2 {
			t.Fatalf("%+v", c)
		}
		if c.KilledPID != 0 {
			killed++
		}
	}
	if killed != 3 {
		t.Fatalf("actual process kills=%d", killed)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "Bearer") || strings.Contains(string(encoded), "Token") {
		t.Fatal("credential in report")
	}
}
func TestWorkerRejectsNonlocalEndpoints(t *testing.T) {
	for _, address := range []string{"https://127.0.0.1:1", "http://example.com:80", "http://127.0.0.1:1?", "http://user:pass@127.0.0.1:1", "http://127.0.0.1:1/path", "http://127.0.0.1"} {
		if localURL(address) {
			t.Errorf("accepted %s", address)
		}
	}
	if !localURL("http://127.0.0.1:12345") || !localURL("http://[::1]:12345") {
		t.Fatal("valid loopback rejected")
	}
	if _, _, err := openWorker(workerConfig{APIURL: "http://example.com:80", SinkURL: "http://127.0.0.1:12345", Token: "synthetic"}); err == nil {
		t.Fatal("remote endpoint accepted")
	}
}

func TestPrematureChildExitCannotProducePassingReport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// A real child exits successfully without ever reaching the commit barrier.
	result, err := runAcceptance(ctx, t.TempDir(), workerProgram{Path: os.Args[0], Args: []string{"-test.run=^$"}})
	if err == nil || result.Passed || len(result.Cases) != 1 || result.Cases[0].Passed {
		t.Fatalf("false success: %+v %v", result, err)
	}
}
func TestReportCannotOverwriteExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if err := writeNewReport(path, []byte("original")); err != nil {
		t.Fatal(err)
	}
	if err := writeNewReport(path, []byte("replacement")); err == nil {
		t.Fatal("overwrote report")
	}
	content, _ := os.ReadFile(path)
	if string(content) != "original" {
		t.Fatal(string(content))
	}
}
