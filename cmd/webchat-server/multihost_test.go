package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/bootstrap"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/webchat"
)

func multiEnvironment() map[string]string {
	env := validEnvironment()
	delete(env, "WEBCHAT_HOST_ID")
	delete(env, "WEBCHAT_HOST_SECRET")
	env["WEBCHAT_HOSTS_JSON"] = `[{"source_id":"business-a","secret":"` + strings.Repeat("a", 40) + `"},{"source_id":"business-b","secret":"` + strings.Repeat("b", 40) + `"}]`
	env["WEBCHAT_AGENTS_JSON"] = `[{"source_id":"business-a","id":"staff-1","secret":"` + strings.Repeat("c", 40) + `"},{"source_id":"business-b","id":"staff-1","secret":"` + strings.Repeat("d", 40) + `"}]`
	return env
}

func TestMultiHostConfiguration(t *testing.T) {
	env := multiEnvironment()
	cfg, err := resolveConfig(mapLookup(env), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.trustedHosts()) != 2 || len(cfg.Agents) != 2 {
		t.Fatal("lost source configuration")
	}
	for name, mutate := range map[string]func(map[string]string){
		"mixed":        func(e map[string]string) { e["WEBCHAT_HOST_ID"] = "legacy" },
		"mixed secret": func(e map[string]string) { e["WEBCHAT_HOST_SECRET"] = strings.Repeat("x", 40) },
		"empty":        func(e map[string]string) { e["WEBCHAT_HOSTS_JSON"] = "[]" },
		"null":         func(e map[string]string) { e["WEBCHAT_HOSTS_JSON"] = "null" },
		"unknown": func(e map[string]string) {
			e["WEBCHAT_HOSTS_JSON"] = `[{"source_id":"a","secret":"private","extra":1}]`
		},
		"trailing": func(e map[string]string) { e["WEBCHAT_HOSTS_JSON"] += " {}" },
		"duplicate source": func(e map[string]string) {
			e["WEBCHAT_HOSTS_JSON"] = strings.ReplaceAll(e["WEBCHAT_HOSTS_JSON"], "business-b", "business-a")
		},
		"duplicate secret": func(e map[string]string) {
			e["WEBCHAT_HOSTS_JSON"] = strings.ReplaceAll(e["WEBCHAT_HOSTS_JSON"], strings.Repeat("b", 40), strings.Repeat("a", 40))
		},
		"oversize": func(e map[string]string) { e["WEBCHAT_HOSTS_JSON"] = strings.Repeat(" ", 16385) },
		"too many": func(e map[string]string) {
			entries := []map[string]string{}
			for i := 0; i < 33; i++ {
				entries = append(entries, map[string]string{"source_id": fmt.Sprint(i), "secret": fmt.Sprintf("%040d", i)})
			}
			b, _ := json.Marshal(entries)
			e["WEBCHAT_HOSTS_JSON"] = string(b)
		},
		"unknown agent source": func(e map[string]string) {
			e["WEBCHAT_AGENTS_JSON"] = strings.ReplaceAll(e["WEBCHAT_AGENTS_JSON"], "business-b", "unknown")
		},
		"shared host agent credential": func(e map[string]string) {
			e["WEBCHAT_AGENTS_JSON"] = strings.ReplaceAll(e["WEBCHAT_AGENTS_JSON"], strings.Repeat("c", 40), strings.Repeat("b", 40))
		},
		"duplicate agent credential": func(e map[string]string) {
			e["WEBCHAT_AGENTS_JSON"] = strings.ReplaceAll(e["WEBCHAT_AGENTS_JSON"], strings.Repeat("d", 40), strings.Repeat("c", 40))
		},
		"duplicate agent id in source": func(e map[string]string) {
			e["WEBCHAT_AGENTS_JSON"] = strings.ReplaceAll(e["WEBCHAT_AGENTS_JSON"], "business-b", "business-a")
		},
		"ai": func(e map[string]string) { e["WEBCHAT_REPLY_MODE"] = "ai" },
	} {
		t.Run(name, func(t *testing.T) {
			e := multiEnvironment()
			mutate(e)
			err := runCommand(context.Background(), []string{"-check-config"}, mapLookup(e), io.Discard, serverDependencies{})
			if err == nil {
				t.Fatal("accepted invalid configuration")
			}
			for _, v := range []string{strings.Repeat("a", 40), strings.Repeat("b", 40), "private", e["DATABASE_URL"]} {
				if strings.Contains(err.Error(), v) {
					t.Fatal("configuration leaked")
				}
			}
		})
	}
	if err := runCommand(context.Background(), []string{"-check-config"}, mapLookup(env), io.Discard, serverDependencies{}); err != nil {
		t.Fatal(err)
	}
}

// Exercise the real command/config/listener/gateway path. Only PostgreSQL is
// replaced with a test repository; this is not a persistence acceptance test.
func TestMultiHostCommandHTTPIsolation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	address := make(chan string, 1)
	done := make(chan error, 1)
	deps := defaultDependencies()
	deps.open = func(context.Context, bootstrap.RepositoryConfig) (*repositoryHandle, error) {
		return &repositoryHandle{repository: conversation.NewMemoryRepository(), check: func(context.Context) error { return nil }, close: func() {}}, nil
	}
	deps.listen = func(network, addr string) (net.Listener, error) {
		l, err := net.Listen(network, addr)
		if err == nil {
			address <- l.Addr().String()
		}
		return l, err
	}
	go func() {
		done <- runCommand(ctx, []string{"-address", "127.0.0.1:0"}, mapLookup(multiEnvironment()), io.Discard, deps)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("server did not stop")
		}
	})
	var origin string
	select {
	case addr := <-address:
		origin = "http://" + addr
	case <-time.After(10 * time.Second):
		t.Fatal("server did not listen")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	call := func(method, path, token string, body any, want int) map[string]any {
		t.Helper()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, origin+path, strings.NewReader(string(raw)))
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		if res.StatusCode != want {
			t.Fatalf("%s status %d want %d", path, res.StatusCode, want)
		}
		var result map[string]any
		_ = json.Unmarshal(data, &result)
		return result
	}
	sessions := []map[string]any{}
	states := []map[string]any{}
	for i := 0; i < 2; i++ {
		s := call("POST", webchat.SessionPath, strings.Repeat(string(rune('a'+i)), 40), map[string]any{"customer_id": "same-customer"}, 201)
		sessions = append(sessions, s)
		token := s["access_token"].(string)
		call("POST", webchat.MessagesPath, token, map[string]any{"event_id": "same-event", "content": fmt.Sprintf("source-%d", i)}, 200)
		states = append(states, call("POST", "/chat/v1/handoff", token, map[string]any{}, 200))
	}
	if sessions[0]["conversation_id"] == sessions[1]["conversation_id"] {
		t.Fatal("conversation collision")
	}
	for i := 0; i < 2; i++ {
		agent := strings.Repeat(string(rune('c'+i)), 40)
		own := sessions[i]["conversation_id"]
		foreign := sessions[1-i]["conversation_id"]
		queue := call("GET", "/internal/chat/desk/queue", agent, nil, 200)
		items := queue["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["conversation_id"] != own {
			t.Fatal("foreign queue item")
		}
		for _, action := range []string{"history", "claim", "reply", "read", "close"} {
			body := map[string]any{"action": action, "conversation_id": foreign}
			if action == "claim" || action == "close" {
				body["revision"] = states[1-i]["revision"]
			}
			if action == "reply" {
				body["event_id"] = "attack"
				body["content"] = "forbidden"
			}
			if action == "read" {
				body["message_ids"] = []string{"foreign"}
			}
			call("POST", "/internal/chat/desk/action", agent, body, 403)
		}
		call("POST", "/internal/chat/desk/action", agent, map[string]any{"action": "claim", "conversation_id": own, "revision": states[i]["revision"]}, 200)
		call("POST", "/internal/chat/desk/action", agent, map[string]any{"action": "reply", "conversation_id": own, "event_id": "same-reply", "content": fmt.Sprintf("staff-%d", i)}, 200)
		history := call("GET", webchat.MessagesPath, sessions[i]["access_token"].(string), nil, 200)
		raw, _ := json.Marshal(history)
		if strings.Contains(string(raw), fmt.Sprintf("source-%d", 1-i)) || strings.Contains(string(raw), fmt.Sprintf("staff-%d", 1-i)) || !strings.Contains(string(raw), fmt.Sprintf("staff-%d", i)) {
			t.Fatal("incorrect history")
		}
	}
	a := strings.Repeat("c", 40)
	history := call("POST", "/internal/chat/desk/action", a, map[string]any{"action": "history", "conversation_id": sessions[0]["conversation_id"]}, 200)
	call("POST", "/internal/chat/desk/action", a, map[string]any{"action": "close", "conversation_id": sessions[0]["conversation_id"], "revision": history["revision"]}, 200)
	if call("GET", webchat.MessagesPath, sessions[1]["access_token"].(string), nil, 200)["status"] != "human_active" {
		t.Fatal("other source changed")
	}
}
