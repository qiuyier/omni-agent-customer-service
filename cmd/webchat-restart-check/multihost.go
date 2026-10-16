package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

func multiChildEnvironment(opts options, hosts, agents string) []string {
	return []string{"PATH=/usr/bin:/bin", "LANG=C", "TZ=UTC", "WEBCHAT_ADDRESS=127.0.0.1:0", "DATABASE_URL=" + opts.databaseURL, "STORAGE_DRIVER=postgres", "WEBCHAT_REPLY_MODE=fake", "WEBCHAT_HOSTS_JSON=" + hosts, "WEBCHAT_AGENTS_JSON=" + agents}
}

type multiDesk struct {
	ID       string    `json:"conversation_id"`
	Status   string    `json:"status"`
	Revision uint64    `json:"revision"`
	Messages []message `json:"messages"`
}

func (h *harness) acceptMultiHost() error {
	runID, err := randomID()
	if err != nil {
		return err
	}
	hosts, agents := []map[string]string{}, []map[string]string{}
	for i := 0; i < 2; i++ {
		hostKey, err := randomID()
		if err != nil {
			return err
		}
		agentKey, err := randomID()
		if err != nil {
			return err
		}
		source := fmt.Sprintf("multi-check-%s-%d", runID, i)
		hosts = append(hosts, map[string]string{"source_id": source, "secret": hostKey})
		agents = append(agents, map[string]string{"source_id": source, "id": "staff-1", "secret": agentKey})
	}
	hostJSON, _ := json.Marshal(hosts)
	agentJSON, _ := json.Marshal(agents)
	start := func(role string) (*childProcess, error) {
		return h.start(role, "", "", false, string(hostJSON), string(agentJSON))
	}
	var first, second, third *childProcess
	old := make([]session, 2)
	renewed := make([]session, 2)
	before := make([]history, 2)
	desk := func(child *childProcess, i int, body map[string]any, status int) (multiDesk, error) {
		var result multiDesk
		err := h.expect(child.origin, "POST", "/internal/chat/desk/action", agents[i]["secret"], body, nil, status, &result)
		return result, err
	}
	foreignDenied := func(child *childProcess, sessions []session) error {
		for i := 0; i < 2; i++ {
			for _, action := range []string{"history", "claim", "reply", "read", "close"} {
				body := map[string]any{"action": action, "conversation_id": sessions[1-i].ConversationID}
				if action == "claim" || action == "close" {
					body["revision"] = 1
				}
				if action == "reply" {
					body["event_id"] = "foreign"
					body["content"] = "must not persist"
				}
				if action == "read" {
					body["message_ids"] = []string{"foreign"}
				}
				if _, err := desk(child, i, body, 403); err != nil {
					return err
				}
			}
		}
		return nil
	}
	queues := func(child *childProcess, sessions []session) error {
		for i := 0; i < 2; i++ {
			var result struct {
				Items []multiDesk `json:"items"`
			}
			if err := h.expect(child.origin, "GET", "/internal/chat/desk/queue", agents[i]["secret"], nil, nil, 200, &result); err != nil {
				return err
			}
			if len(result.Items) != 1 || result.Items[0].ID != sessions[i].ConversationID || result.Items[0].Status != "human_active" {
				return errors.New("persistent queue crossed sources or lost ownership")
			}
		}
		return nil
	}
	if err := h.check("multi_host_same_database_send_claim_reply_isolation", func() error {
		var err error
		first, err = start("multi_before_restart")
		if err != nil {
			return err
		}
		for i := 0; i < 2; i++ {
			old[i], err = h.mint(first, hosts[i]["secret"], "same-customer")
			if err != nil {
				return err
			}
			empty, err := h.history(first, old[i], nil)
			if err != nil {
				return err
			}
			if len(empty.Messages) != 0 {
				return errors.New("new multi-host identity was not empty")
			}
			if _, err = h.send(first, old[i], nil, "same-event", fmt.Sprintf("multi-source-%d", i)); err != nil {
				return err
			}
			var waiting multiDesk
			if err = h.expect(first.origin, "POST", "/chat/v1/handoff", old[i].AccessToken, map[string]any{}, nil, 200, &waiting); err != nil {
				return err
			}
			if _, err = desk(first, i, map[string]any{"action": "claim", "conversation_id": old[i].ConversationID, "revision": waiting.Revision}, 200); err != nil {
				return err
			}
			if _, err = desk(first, i, map[string]any{"action": "reply", "conversation_id": old[i].ConversationID, "event_id": "same-staff-event", "content": fmt.Sprintf("multi-staff-%d", i)}, 200); err != nil {
				return err
			}
			before[i], err = h.history(first, old[i], nil)
			if err != nil {
				return err
			}
			if len(before[i].Messages) != 3 || before[i].Status != "human_active" || before[i].Messages[0].Content != fmt.Sprintf("multi-source-%d", i) || before[i].Messages[2].Content != fmt.Sprintf("multi-staff-%d", i) {
				return errors.New("multi-host history content mismatch")
			}
		}
		if old[0].ConversationID == old[1].ConversationID {
			return errors.New("multi-host conversation collision")
		}
		if err := queues(first, old); err != nil {
			return err
		}
		return foreignDenied(first, old)
	}); err != nil {
		return err
	}
	if err := h.check("multi_host_kill_reap_restart_and_expire_old_tokens", func() error {
		if err := first.stop(true); err != nil {
			return err
		}
		var err error
		second, err = start("multi_after_restart")
		if err != nil {
			return err
		}
		if first.result.PID == second.result.PID {
			return errors.New("multi-host restart reused process")
		}
		for i := 0; i < 2; i++ {
			if err = h.expect(second.origin, "GET", messagesPath, old[i].AccessToken, nil, nil, 401, nil); err != nil {
				return err
			}
			renewed[i], err = h.mint(second, hosts[i]["secret"], "same-customer")
			if err != nil {
				return err
			}
			if renewed[i].ConversationID != old[i].ConversationID || renewed[i].AccessToken == old[i].AccessToken {
				return errors.New("multi-host identity or token renewal mismatch")
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := h.check("multi_host_history_ownership_and_denials_survive_restart", func() error {
		for i := 0; i < 2; i++ {
			after, err := h.history(second, renewed[i], nil)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(before[i], after) {
				return errors.New("multi-host persisted history changed")
			}
			// No new claim: the same source-scoped staff must retain authority.
			body := map[string]any{"action": "reply", "conversation_id": renewed[i].ConversationID, "event_id": "after-restart", "content": fmt.Sprintf("restored-staff-%d", i)}
			for n := 0; n < 2; n++ {
				if _, err = desk(second, i, body, 200); err != nil {
					return err
				}
			}
			after, err = h.history(second, renewed[i], nil)
			if err != nil {
				return err
			}
			if len(after.Messages) != 4 || after.Messages[3].Content != fmt.Sprintf("restored-staff-%d", i) {
				return errors.New("restored ownership reply or replay mismatch")
			}
		}
		if err := queues(second, renewed); err != nil {
			return err
		}
		return foreignDenied(second, renewed)
	}); err != nil {
		return err
	}
	var closedHistory, otherHistory history
	if err := h.check("multi_host_close_does_not_change_other_source", func() error {
		var err error
		otherHistory, err = h.history(second, renewed[1], nil)
		if err != nil {
			return err
		}
		state, err := desk(second, 0, map[string]any{"action": "history", "conversation_id": renewed[0].ConversationID}, 200)
		if err != nil {
			return err
		}
		if _, err = desk(second, 0, map[string]any{"action": "close", "conversation_id": renewed[0].ConversationID, "revision": state.Revision}, 200); err != nil {
			return err
		}
		closedHistory, err = h.history(second, renewed[0], nil)
		if err != nil {
			return err
		}
		if closedHistory.Status != "closed" {
			return errors.New("close was not persisted")
		}
		other, err := h.history(second, renewed[1], nil)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(otherHistory, other) {
			return errors.New("close changed other source")
		}
		return second.stop(false)
	}); err != nil {
		return err
	}
	if err := h.check("multi_host_closed_and_active_states_survive_second_restart", func() error {
		var err error
		third, err = start("multi_verify_closed_restart")
		if err != nil {
			return err
		}
		for i, want := range []history{closedHistory, otherHistory} {
			next, err := h.mint(third, hosts[i]["secret"], "same-customer")
			if err != nil {
				return err
			}
			actual, err := h.history(third, next, nil)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(want, actual) {
				return errors.New("second restart changed persistent state")
			}
			renewed[i] = next
		}
		if err := foreignDenied(third, renewed); err != nil {
			return err
		}
		return third.stop(false)
	}); err != nil {
		return err
	}
	return nil
}
