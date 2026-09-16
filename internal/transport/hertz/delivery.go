package hertz

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/qiuyier/omni-agent-customer-service/internal/customerservice"
	"github.com/qiuyier/omni-agent-customer-service/internal/transport/hertz/model"
	"github.com/qiuyier/omni-agent-kit/fault"
)

type deliveryChecker interface {
	CheckDeliveryEligibility(context.Context, customerservice.DeliveryCheck) (customerservice.DeliveryEligibility, error)
}

func (h *Handler) CheckDeliveryEligibility(ctx context.Context, c *app.RequestContext) {
	// Decode JSON only, then overwrite path IDs: bodies cannot redirect the check.
	var request model.DeliveryEligibilityRequest
	if err := json.Unmarshal(c.Request.Body(), &request); err != nil {
		writeProblem(c, fault.Wrap(fault.CodeInvalidArgument, "invalid request", err))
		return
	}
	request.ConversationID = c.Param("conversation_id")
	request.TriggerMessageID = c.Param("trigger_message_id")
	if err := c.Validate(&request); err != nil {
		writeProblem(c, fault.Wrap(fault.CodeInvalidArgument, "invalid request", err))
		return
	}
	checker, ok := h.application.(deliveryChecker)
	if !ok {
		writeProblem(c, invalidDependency("delivery checker is not configured"))
		return
	}
	result, err := checker.CheckDeliveryEligibility(ctx, customerservice.DeliveryCheck{ConversationID: request.ConversationID, TriggerMessageID: request.TriggerMessageID, CustomerID: request.CustomerID, Channel: request.Channel, ReplyMessageID: request.ReplyMessageID, Content: request.Content})
	if err != nil {
		writeProblem(c, classifyError(err))
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, &model.DeliveryEligibilityResponse{Eligible: result.Eligible, Reason: result.Reason})
}
