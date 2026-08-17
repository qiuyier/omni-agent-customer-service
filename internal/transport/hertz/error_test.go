package hertz

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-kit/fault"
	"github.com/qiuyier/omni-agent-kit/transport/hertz/problem"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantCode   fault.Code
		wantStatus int
	}{
		{
			name:       "invalid application input",
			err:        customerservice.ErrInvalidInput,
			wantCode:   fault.CodeInvalidArgument,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid domain argument",
			err:        conversation.ErrInvalidArgument,
			wantCode:   fault.CodeInvalidArgument,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "conversation mismatch",
			err:        customerservice.ErrConversationMismatch,
			wantCode:   fault.CodeConflict,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "idempotency conflict",
			err:        customerservice.ErrIdempotencyConflict,
			wantCode:   fault.CodeConflict,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "conversation not found",
			err:        conversation.ErrConversationNotFound,
			wantCode:   fault.CodeNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "conversation closed",
			err:        conversation.ErrConversationClosed,
			wantCode:   fault.CodeFailedPrecondition,
			wantStatus: http.StatusPreconditionFailed,
		},
		{
			name:       "deadline",
			err:        context.DeadlineExceeded,
			wantCode:   fault.CodeDeadlineExceeded,
			wantStatus: http.StatusGatewayTimeout,
		},
		{
			name:       "unknown internal error",
			err:        errors.New("api_key=secret"),
			wantCode:   fault.CodeInternal,
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classified := classifyError(fmt.Errorf("wrapped: %w", test.err))
			if got := fault.CodeOf(classified); got != test.wantCode {
				t.Fatalf("code = %q, want %q", got, test.wantCode)
			}
			if got := problem.StatusCode(classified); got != test.wantStatus {
				t.Fatalf("status = %d, want %d", got, test.wantStatus)
			}
			if !errors.Is(classified, test.err) {
				t.Fatalf("classified error does not retain cause %v", test.err)
			}
		})
	}
}

func TestClassifyErrorPreservesFault(t *testing.T) {
	original := fault.New(fault.CodeUnavailable, "model unavailable")
	if got := classifyError(original); got != original {
		t.Fatalf("classifyError() = %v, want original fault", got)
	}
}
