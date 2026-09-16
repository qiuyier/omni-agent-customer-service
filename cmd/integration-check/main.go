// integration-check is a local acceptance harness, not a production connector.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/cloudwego/hertz/pkg/common/hlog"
)

func main() {
	worker := flag.Bool("worker", false, "internal child mode; JSON configuration on stdin")
	output := flag.String("output-dir", "", "new private directory for fictional state and report (must not exist)")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(2)
	}
	if *worker {
		if err := workerMain(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "worker failed:", err)
			os.Exit(1)
		}
		return
	}
	hlog.SetOutput(os.Stderr)
	hlog.SetLevel(hlog.LevelError)
	root := *output
	var err error
	if root == "" {
		root, err = os.MkdirTemp("", "integration-check-")
	} else {
		root, err = filepath.Abs(root)
		if err == nil {
			err = os.Mkdir(root, 0700)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot create new acceptance directory:", err)
		os.Exit(1)
	}
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 90*time.Second)
	defer cancel()
	report, runErr := runAcceptance(ctx, root, workerProgram{Path: executable, Args: []string{"-worker"}})
	report.ReportPath = filepath.Join(root, "report.json")
	if runErr != nil {
		report.Failure = runErr.Error()
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err == nil {
		err = writeNewReport(report.ReportPath, append(encoded, '\n'))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot save report:", err)
		os.Exit(1)
	}
	if _, err = os.Stdout.Write(append(encoded, '\n')); err != nil {
		os.Exit(1)
	}
	if runErr != nil {
		os.Exit(1)
	}
}

func writeNewReport(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func workerMain(input io.Reader, output io.Writer) error {
	var cfg workerConfig
	decoder := json.NewDecoder(io.LimitReader(input, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return fmt.Errorf("invalid child input")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing child input")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := processWorker(ctx, cfg)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}
