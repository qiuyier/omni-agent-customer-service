package feedback

import (
	"context"
	"time"
)

type Item struct {
	ID        string    `json:"id"`
	Question  string    `json:"question"`
	Count     int       `json:"count"`
	Revision  int64     `json:"revision"`
	Status    string    `json:"status"`
	FAQID     string    `json:"faq_id"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	ExpiresAt time.Time `json:"expires_at"`
}
type Change struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Status   string `json:"status"`
	FAQID    string `json:"faq_id"`
}
type Repository interface {
	Sink
	ListFeedback(context.Context) ([]Item, error)
	UpdateFeedback(context.Context, Change) error
	PurgeFeedback(context.Context) error
}

func StartJanitor(parent context.Context, repo Repository) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() { defer close(done); RunJanitor(ctx, repo) }()
	return func() { cancel(); <-done }
}

// RunJanitor blocks until cancellation. Run in an owned process lifecycle,
// including when capture is disabled, to remove expired stored questions.
// Reads always hide expired records; physical deletion is best effort hourly.
func RunJanitor(ctx context.Context, repo Repository) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = repo.PurgeFeedback(bounded)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
