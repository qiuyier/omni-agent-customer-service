package feedback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

type countFixture struct {
	active, expired int
	err             error
}

func (c countFixture) FeedbackCounts(context.Context) (int, int, error) {
	return c.active, c.expired, c.err
}
func TestStatusPrivacyAndCategories(t *testing.T) {
	m := NewMonitor("enabled")
	m.record(nil)
	m.record(ErrFull)
	m.record(errors.New("secret fictional@example.org postgres://secret"))
	m.skip()
	m.clean(errors.New("private question"))
	s := m.Snapshot(context.Background(), countFixture{active: 500, expired: 2})
	if s.Recorded != 1 || s.Full != 1 || s.Failed != 1 || s.Skipped != 1 || s.Cleanup != "failed" || s.Queue != "full" || s.CleanupAt == nil {
		t.Fatal("status categories")
	}
	raw, _ := json.Marshal(s)
	for _, secret := range []string{"secret", "fictional@example.org", "postgres://", "private question"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("sensitive log")
		}
	}
	m.clean(nil)
	s = m.Snapshot(context.Background(), countFixture{err: errors.New("secret")})
	if s.Queue != "unavailable" || s.Active != nil || s.Expired != nil || s.Cleanup != "ok" {
		t.Fatal("unavailable must not appear empty")
	}
	if NewMonitor("admin").Snapshot(context.Background(), countFixture{}).Capture != "not_owned" {
		t.Fatal("admin cannot infer gateway configuration")
	}
}
func TestMonitorConcurrencyAndShutdown(t *testing.T) {
	m := NewMonitor("disabled")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				m.record(nil)
				m.Snapshot(context.Background(), countFixture{})
			}
		}()
	}
	wg.Wait()
	var output bytes.Buffer
	stop := m.StartReporter(context.Background(), countFixture{}, &output)
	stop()
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) < 1 {
		t.Fatal("no exit status")
	}
	var s Status
	if json.Unmarshal([]byte(lines[len(lines)-1]), &s) != nil || s.Recorded != 1000 || s.Capture != "disabled" {
		t.Fatal("counter lost")
	}
	if MonitorFrom(WithMonitor(context.Background(), m)) != m {
		t.Fatal("missing context monitor")
	}
}
