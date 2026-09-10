package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxReplyResponseBytes = 1 << 20

type receivedMessage struct {
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
	Duplicate      bool   `json:"duplicate"`
}

type replyPayload struct {
	ConversationID   string `json:"conversation_id"`
	TriggerMessageID string `json:"trigger_message_id"`
	MessageID        string `json:"message_id"`
	Content          string `json:"content"`
	Duplicate        bool   `json:"duplicate"`
}

type replyHTTPResult struct {
	Status       int
	Reply        replyPayload
	DeltaCount   int
	DeltaText    string
	Events       []string
	FirstDeltaMS *float64
	TotalMS      float64
}

func receiveMessage(ctx context.Context, client *http.Client, baseURL, token, conversationID, externalID, query string) (receivedMessage, error) {
	payload := struct {
		CustomerID        string `json:"customer_id"`
		Channel           string `json:"channel"`
		ExternalMessageID string `json:"external_message_id"`
		Content           string `json:"content"`
	}{"acceptance-customer", "web", externalID, query}
	data, err := json.Marshal(payload)
	if err != nil {
		return receivedMessage{}, errors.New("encode customer message failed")
	}
	path := "/v1/conversations/" + url.PathEscape(conversationID) + "/messages"
	response, err := acceptancePOST(ctx, client, baseURL, token, path, "application/json", data)
	if err != nil {
		return receivedMessage{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return receivedMessage{}, fmt.Errorf("receive message: unexpected HTTP status %d", response.StatusCode)
	}
	if !responseMediaType(response, "application/json") {
		return receivedMessage{}, errors.New("receive message: expected JSON response")
	}
	var received receivedMessage
	if err := decodeBoundedJSON(response.Body, &received); err != nil {
		return receivedMessage{}, safeAcceptanceError(ctx, "receive message: invalid JSON response")
	}
	if received.ConversationID != conversationID || strings.TrimSpace(received.MessageID) == "" || received.Duplicate {
		return receivedMessage{}, errors.New("receive message: unexpected message identity or duplicate result")
	}
	return received, nil
}

func requestReply(ctx context.Context, client *http.Client, baseURL, token, mode, conversationID, triggerID string) (result replyHTTPResult, err error) {
	started := time.Now()
	defer func() { result.TotalMS = elapsedMilliseconds(started) }()
	if mode != "generate" && mode != "stream" {
		return result, errors.New("reply mode must be generate or stream")
	}
	path := "/v1/conversations/" + url.PathEscape(conversationID) + "/messages/" + url.PathEscape(triggerID) + "/reply"
	accept := "application/json"
	if mode == "stream" {
		path += "/stream"
		accept = "text/event-stream"
	}
	response, err := acceptancePOST(ctx, client, baseURL, token, path, accept, nil)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	result.Status = response.StatusCode
	if mode == "generate" {
		if result.Status != http.StatusOK && result.Status != http.StatusCreated {
			return result, fmt.Errorf("generate reply: unexpected HTTP status %d", result.Status)
		}
		if !responseMediaType(response, "application/json") {
			return result, errors.New("generate reply: expected JSON response")
		}
		if err := decodeBoundedJSON(response.Body, &result.Reply); err != nil {
			return result, safeAcceptanceError(ctx, "generate reply: invalid JSON response")
		}
		if !validReplyPayload(result.Reply, conversationID, triggerID) || result.Reply.Duplicate != (result.Status == http.StatusOK) {
			return result, errors.New("generate reply: unexpected reply identity, content or duplicate result")
		}
		return result, nil
	}
	if result.Status != http.StatusOK {
		return result, fmt.Errorf("stream reply: unexpected HTTP status %d", result.Status)
	}
	if !responseMediaType(response, "text/event-stream") {
		return result, errors.New("stream reply: expected SSE response")
	}
	if err := readReplySSE(response.Body, conversationID, triggerID, started, &result); err != nil {
		if ctx != nil && ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, err
	}
	return result, nil
}

func acceptancePOST(ctx context.Context, client *http.Client, baseURL, token, path, accept string, body []byte) (*http.Response, error) {
	if ctx == nil || client == nil {
		return nil, errors.New("HTTP acceptance client and context are required")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, safeAcceptanceError(ctx, "construct acceptance request failed")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", accept)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		// net/http errors can include the URL, provider data or redirect details.
		// Keep only cancellation identity; never print request or response data.
		return nil, safeAcceptanceError(ctx, "acceptance HTTP request failed")
	}
	return response, nil
}

func safeAcceptanceError(ctx context.Context, message string) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New(message)
}

func responseMediaType(response *http.Response, want string) bool {
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	return err == nil && strings.EqualFold(mediaType, want)
}

func decodeBoundedJSON(reader io.Reader, target any) error {
	data, err := io.ReadAll(io.LimitReader(reader, maxReplyResponseBytes+1))
	if err != nil || len(data) > maxReplyResponseBytes {
		return errors.New("invalid or oversized JSON response")
	}
	// Unmarshal rejects extra JSON values and trailing garbage while allowing
	// the endpoint's other documented fields (such as status and next_action).
	if err := json.Unmarshal(data, target); err != nil {
		return errors.New("invalid JSON response")
	}
	return nil
}

func validReplyPayload(reply replyPayload, conversationID, triggerID string) bool {
	return reply.ConversationID == conversationID && reply.TriggerMessageID == triggerID &&
		strings.TrimSpace(reply.MessageID) != "" && strings.TrimSpace(reply.Content) != ""
}

func readReplySSE(reader io.Reader, conversationID, triggerID string, started time.Time, result *replyHTTPResult) error {
	limited := &io.LimitedReader{R: reader, N: maxReplyResponseBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), maxReplyResponseBytes+1)
	var eventType string
	var dataLines []string
	var deltas strings.Builder
	completed := false
	dispatch := func() error {
		if eventType == "" && len(dataLines) == 0 {
			return nil
		}
		if completed {
			return errors.New("stream reply: event after completion")
		}
		if len(dataLines) == 0 {
			return errors.New("stream reply: event has no data")
		}
		data := []byte(strings.Join(dataLines, "\n"))
		switch eventType {
		case "reply.delta":
			var delta struct {
				Delta string `json:"delta"`
			}
			if json.Unmarshal(data, &delta) != nil || delta.Delta == "" {
				return errors.New("stream reply: invalid delta payload")
			}
			if result.FirstDeltaMS == nil {
				elapsed := elapsedMilliseconds(started)
				result.FirstDeltaMS = &elapsed
			}
			result.DeltaCount++
			deltas.WriteString(delta.Delta)
		case "reply.completed":
			if json.Unmarshal(data, &result.Reply) != nil || !validReplyPayload(result.Reply, conversationID, triggerID) {
				return errors.New("stream reply: invalid completed payload")
			}
			completed = true
		case "error", "reply.superseded":
			return errors.New("stream reply: unsuccessful terminal event")
		default:
			return errors.New("stream reply: unexpected event type")
		}
		result.Events = append(result.Events, eventType)
		eventType = ""
		dataLines = nil
		return nil
	}
	firstLine := true
	for scanner.Scan() {
		if limited.N == 0 {
			return errors.New("stream reply: response too large")
		}
		line := scanner.Text()
		if firstLine {
			line = strings.TrimPrefix(line, "\uFEFF")
			firstLine = false
		}
		if line == "" {
			if err := dispatch(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventType = value
		case "data":
			dataLines = append(dataLines, value)
		}
	}
	if scanner.Err() != nil || limited.N == 0 {
		return errors.New("stream reply: incomplete or oversized response")
	}
	if eventType != "" || len(dataLines) != 0 {
		return errors.New("stream reply: unterminated event")
	}
	if !completed {
		return errors.New("stream reply: missing completed event")
	}
	result.DeltaText = deltas.String()
	if result.Reply.Duplicate {
		if result.DeltaCount != 0 {
			return errors.New("stream reply: sequential duplicate unexpectedly generated deltas")
		}
	} else if result.DeltaCount == 0 || result.DeltaText != result.Reply.Content {
		return errors.New("stream reply: completed content does not match generated deltas")
	}
	return nil
}

func elapsedMilliseconds(started time.Time) float64 {
	return float64(time.Since(started).Nanoseconds()) / 1e6
}
