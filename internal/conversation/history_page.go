package conversation

import (
	"context"
	"errors"
)

var ErrHistoryCursor = errors.New("conversation: invalid history cursor")

// MessagePager is an optional transaction capability. The caller must authorize the conversation.
type MessagePager interface {
	PageMessages(id, before, after, from string) (HistoryPage, error)
}
type HistoryPage struct {
	Messages           []Message
	HasOlder, HasNewer bool
	Truncated          map[string]bool
}

// DeskAttentionReader reads receipts only for the supplied page, plus global summary metadata.
// Latest may contain only the first 161 Unicode characters, enough for the 160-character preview.
type DeskAttentionReader interface {
	ReadDeskAttention(context.Context, string, string, string, []string) (Attention, error)
}
