package hertz

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-kit/fault"
)

func TestKnowledgeFailureIsSafeProblemBeforeSSEStarts(t *testing.T) {
	for _, path := range []string{
		"/v1/conversations/conversation-1/messages/message-1/reply",
		"/v1/conversations/conversation-1/messages/message-1/reply/stream",
	} {
		for _, test := range []struct {
			name    string
			cause   error
			status  int
			code    fault.Code
			message string
		}{
			{name: "backend", cause: errors.New("private document: api_key=secret"), status: http.StatusServiceUnavailable, code: fault.CodeUnavailable, message: "reply service unavailable"},
			{name: "nested fault", cause: fault.New(fault.CodeInvalidArgument, "private query must not escape"), status: http.StatusServiceUnavailable, code: fault.CodeUnavailable, message: "reply service unavailable"},
			{name: "timeout", cause: context.DeadlineExceeded, status: http.StatusGatewayTimeout, code: fault.CodeDeadlineExceeded, message: "deadline exceeded"},
			{name: "canceled", cause: context.Canceled, status: http.StatusRequestTimeout, code: fault.CodeCancelled, message: "request cancelled"},
		} {
			t.Run(path+"/"+test.name, func(t *testing.T) {
				err := fmt.Errorf("%w: %w", customerservice.ErrKnowledgeRetrievalFailed, test.cause)
				engine := newTestEngine(t, &receiverStub{replyErr: err, streamErr: err})
				response := performAuthorizedRequest(engine, http.MethodPost, path, nil)
				assertProblem(t, response, test.status, test.code, test.message)
				if strings.HasPrefix(string(response.Header.ContentType()), "text/event-stream") {
					t.Fatal("retrieval failure already started SSE")
				}
				if strings.Contains(string(response.Body()), "private") || strings.Contains(string(response.Body()), "secret") {
					t.Fatal("retrieval error leaked reference data")
				}
			})
		}
	}
}
