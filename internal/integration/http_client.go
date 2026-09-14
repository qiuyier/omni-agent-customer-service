package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
)

// HTTPConfig is trusted startup configuration, not data from an inbound event.
// Plain HTTP requires explicit opt-in and is intended for local/private tests.
type HTTPConfig struct {
	BaseURL           string
	BearerToken       string
	AllowInsecureHTTP bool
}
type HTTPClient struct {
	base      string
	token     string
	client    *http.Client
	transport *http.Transport
}

func NewHTTPClient(cfg HTTPConfig) (*HTTPClient, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Hostname() == "" || base.Opaque != "" || base.User != nil ||
		base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || (base.Scheme != "https" && !(base.Scheme == "http" && cfg.AllowInsecureHTTP)) ||
		cfg.BearerToken == "" || strings.TrimSpace(cfg.BearerToken) != cfg.BearerToken ||
		strings.ContainsAny(cfg.BearerToken, "\r\n\t ") || len(cfg.BearerToken) > 4096 {
		return nil, ErrInvalidInput
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	client := &http.Client{Transport: transport, Timeout: 90 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &HTTPClient{base: strings.TrimRight(base.String(), "/"), token: cfg.BearerToken, client: client, transport: transport}, nil
}
func (c *HTTPClient) Close() { c.transport.CloseIdleConnections() }

func (c *HTTPClient) ReceiveMessage(ctx context.Context, command customerservice.ReceiveMessageCommand) (customerservice.ReceiveMessageResult, error) {
	payload := struct {
		CustomerID        string `json:"customer_id"`
		Channel           string `json:"channel"`
		ExternalMessageID string `json:"external_message_id"`
		Content           string `json:"content"`
	}{command.CustomerID, command.Channel, command.ExternalMessageID, command.Content}
	var result struct {
		ConversationID string                     `json:"conversation_id"`
		MessageID      string                     `json:"message_id"`
		Status         conversation.Status        `json:"status"`
		NextAction     customerservice.NextAction `json:"next_action"`
		Duplicate      *bool                      `json:"duplicate"`
	}
	status, err := c.post(ctx, "/v1/conversations/"+url.PathEscape(command.ConversationID)+"/messages", payload, &result)
	if err != nil {
		return customerservice.ReceiveMessageResult{}, err
	}
	if result.Duplicate == nil || (status == http.StatusOK) != *result.Duplicate {
		return customerservice.ReceiveMessageResult{}, &CallError{Code: FailureInvalidResponse}
	}
	return customerservice.ReceiveMessageResult{ConversationID: result.ConversationID, MessageID: result.MessageID, Status: result.Status, NextAction: result.NextAction, Duplicate: *result.Duplicate}, nil
}
func (c *HTTPClient) GenerateBotReply(ctx context.Context, command customerservice.GenerateBotReplyCommand) (customerservice.GenerateBotReplyResult, error) {
	var result struct {
		ConversationID   string `json:"conversation_id"`
		TriggerMessageID string `json:"trigger_message_id"`
		MessageID        string `json:"message_id"`
		Content          string `json:"content"`
		Duplicate        *bool  `json:"duplicate"`
	}
	status, err := c.post(ctx, "/v1/conversations/"+url.PathEscape(command.ConversationID)+"/messages/"+url.PathEscape(command.TriggerMessageID)+"/reply", nil, &result)
	if err != nil {
		return customerservice.GenerateBotReplyResult{}, err
	}
	if result.Duplicate == nil || (status == http.StatusOK) != *result.Duplicate {
		return customerservice.GenerateBotReplyResult{}, &CallError{Code: FailureInvalidResponse}
	}
	return customerservice.GenerateBotReplyResult{ConversationID: result.ConversationID, TriggerMessageID: result.TriggerMessageID, MessageID: result.MessageID, Content: result.Content, Duplicate: *result.Duplicate}, nil
}
func (c *HTTPClient) post(ctx context.Context, path string, payload any, result any) (int, error) {
	if ctx == nil {
		return 0, ErrInvalidInput
	}
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return 0, &CallError{Code: FailureInvalidRequest}
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return 0, &CallError{Code: FailureInvalidRequest}
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		code := FailureTransport
		if ctx.Err() == context.Canceled {
			code = FailureCancelled
		}
		if ctx.Err() == context.DeadlineExceeded {
			code = FailureTimeout
		}
		return 0, &CallError{Code: code, Retryable: true}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return response.StatusCode, httpFailure(response)
	}
	const maxResponse = 512 << 10
	encoded, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil {
		return 0, &CallError{Code: FailureTransport, Retryable: true}
	}
	if len(encoded) > maxResponse || json.Unmarshal(encoded, result) != nil {
		return 0, &CallError{Code: FailureInvalidResponse}
	}
	return response.StatusCode, nil
}
func httpFailure(response *http.Response) error {
	failure := &CallError{Code: FailureInvalidResponse}
	switch response.StatusCode {
	case 400:
		failure.Code = FailureInvalidRequest
	case 401, 403:
		failure.Code = FailureUnauthorized
	case 404:
		failure.Code = FailureNotFound
	case 409:
		failure.Code = FailureConflict
	case 412:
		failure.Code = FailurePrecondition
	case 408, 504:
		failure.Code, failure.Retryable = FailureTimeout, true
	case 429:
		failure.Code, failure.Retryable = FailureBusy, true
	case 500, 502, 503:
		failure.Code, failure.Retryable = FailureUnavailable, true
	}
	if failure.Retryable {
		value := response.Header.Get("Retry-After")
		if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
			if seconds > int64(maxRetryDelay/time.Second) {
				failure.Retryable = false
			} else {
				failure.RetryAfter = time.Duration(seconds) * time.Second
			}
		} else if retryAt, err := http.ParseTime(value); err == nil {
			delay := time.Until(retryAt)
			if delay > maxRetryDelay {
				failure.Retryable = false
			} else if delay > 0 {
				failure.RetryAfter = delay
			}
		}
	}
	// Error bodies are deliberately not used as executable policy or diagnostics.
	return failure
}

var _ Client = (*HTTPClient)(nil)
