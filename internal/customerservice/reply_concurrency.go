package customerservice

import (
	"fmt"
	"sync"
)

// replyCapacityLimiter is a process-local, non-blocking semaphore shared by
// non-streaming and streaming reply generation. A nil limiter means unlimited
// capacity so Service remains usable by callers that do not opt into a limit.
type replyCapacityLimiter struct {
	mu     sync.Mutex
	active int
	limit  int
}

type replyCapacityLease struct {
	limiter  *replyCapacityLimiter
	once     sync.Once
	released ReplyCapacitySnapshot
}

// WithMaxConcurrentReplies limits active model generations for this Service.
// Capacity exhaustion fails immediately instead of building an unbounded wait
// queue whose requests may already be stale when they finally reach the model.
func WithMaxConcurrentReplies(maxConcurrent int) Option {
	return func(service *Service) error {
		if maxConcurrent <= 0 {
			return fmt.Errorf(
				"%w: maximum concurrent replies must be positive",
				ErrInvalidDependency,
			)
		}
		service.replyCapacity = &replyCapacityLimiter{
			limit: maxConcurrent,
		}
		return nil
	}
}

func (s *Service) acquireReplyCapacity() (
	*replyCapacityLease,
	ReplyCapacitySnapshot,
	error,
) {
	if s.replyCapacity == nil {
		return &replyCapacityLease{}, ReplyCapacitySnapshot{}, nil
	}
	limiter := s.replyCapacity

	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if limiter.active >= limiter.limit {
		capacity := ReplyCapacitySnapshot{
			Active: limiter.active,
			Limit:  limiter.limit,
		}
		return nil, capacity, fmt.Errorf(
			"%w: all %d model-generation slots are in use",
			ErrReplyCapacityExceeded,
			limiter.limit,
		)
	}
	limiter.active++
	capacity := ReplyCapacitySnapshot{
		Active: limiter.active,
		Limit:  limiter.limit,
	}
	return &replyCapacityLease{limiter: limiter}, capacity, nil
}

func (l *replyCapacityLease) release() ReplyCapacitySnapshot {
	if l == nil || l.limiter == nil {
		return ReplyCapacitySnapshot{}
	}
	l.once.Do(func() {
		l.limiter.mu.Lock()
		l.limiter.active--
		l.released = ReplyCapacitySnapshot{
			Active: l.limiter.active,
			Limit:  l.limiter.limit,
		}
		l.limiter.mu.Unlock()
	})
	return l.released
}
