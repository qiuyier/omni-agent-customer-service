package conversation

import "context"

// DeskQueueItem is a conversation and its agent-specific attention summary.
type DeskQueueItem struct {
	Conversation *Conversation
	Attention    Attention
}

// DeskQueueReader optionally projects the authorized queue and its summaries
// in one consistent snapshot. It returns at most 100 conversations, ordered by
// updated time then ID, including waiting conversations and the agent's own
// active conversations in the supplied channel. Latest is a bounded preview
// (161 runes); Seen is not populated. Actions must still reauthorize ownership.
type DeskQueueReader interface {
	ReadDeskQueue(context.Context, string, string) ([]DeskQueueItem, error)
}
