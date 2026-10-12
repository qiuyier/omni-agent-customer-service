package webchat

import (
	"errors"
	"github.com/qiuyier/omni-agent-customer-service/internal/conversation"
	"unicode/utf8"
)

var errHistoryCursor = errors.New("invalid history cursor")

// Storage append order is authoritative; timestamps and lexical message IDs are not cursors.
func deskHistory(all []conversation.Message, before, after, from string, out *deskState) error {
	public := []conversation.Message{}
	for _, m := range all {
		if m.Sender() == conversation.SenderCustomer || m.Sender() == conversation.SenderBot || m.Sender() == conversation.SenderHumanAgent {
			public = append(public, m)
		}
	}
	lo, hi := 0, len(public)
	forward := after != "" || from != ""
	cursor := before
	if after != "" {
		cursor = after
	}
	if from != "" {
		cursor = from
	}
	if cursor != "" {
		index := -1
		for i, m := range public {
			if m.ID() == cursor {
				index = i
				break
			}
		}
		if index < 0 {
			return errHistoryCursor
		}
		if before != "" {
			hi = index
		} else {
			lo = index
			if after != "" {
				lo++
			}
		}
	}
	start, end := lo, hi
	budget := 128 << 10
	out.Messages = []Message{}
	if forward {
		end = lo
		for end < hi && end-lo < 100 {
			size := len(public[end].Content())
			if size > budget && end > lo {
				break
			}
			budget -= size
			end++
			if budget <= 0 {
				break
			}
		}
	} else {
		start = hi
		for start > lo && hi-start < 100 {
			size := len(public[start-1].Content())
			if size > budget && start < hi {
				break
			}
			budget -= size
			start--
			if budget <= 0 {
				break
			}
		}
	}
	for _, m := range public[start:end] {
		content := m.Content()
		truncated := len(content) > 128<<10
		if truncated {
			content = content[:128<<10]
			for !utf8.ValidString(content) {
				content = content[:len(content)-1]
			}
		}
		out.Messages = append(out.Messages, Message{ID: m.ID(), Role: m.Sender(), Content: content, CreatedAt: m.CreatedAt(), ContentTruncated: truncated})
	}
	out.HasOlder = start > 0
	out.HasNewer = end < len(public)
	return nil
}
