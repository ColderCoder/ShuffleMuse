package mediaexec

import (
	"context"
	"errors"
	"time"
)

var (
	ErrQueueFull   = errors.New("media process queue is full")
	ErrWaitTimeout = errors.New("timed out waiting for a media process")
)

type Limiter struct {
	slots       chan struct{}
	waiters     chan struct{}
	waitTimeout time.Duration
}

type QueueConfig struct {
	MaxWaiters  int
	WaitTimeout time.Duration
}

func NewLimiter(maxSessions int, options ...QueueConfig) *Limiter {
	if maxSessions < 1 {
		panic("media process limit must be positive")
	}
	maxWaiters := 8
	waitTimeout := 15 * time.Second
	if len(options) > 0 {
		maxWaiters = options[0].MaxWaiters
		waitTimeout = options[0].WaitTimeout
	}
	if maxWaiters < 0 || waitTimeout <= 0 {
		panic("invalid media process queue configuration")
	}
	return &Limiter{
		slots:       make(chan struct{}, maxSessions),
		waiters:     make(chan struct{}, maxWaiters),
		waitTimeout: waitTimeout,
	}
}

func (l *Limiter) Acquire(ctx context.Context) error {
	if l == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case l.slots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-l.slots
			return err
		}
		return nil
	default:
	}

	select {
	case l.waiters <- struct{}{}:
		defer func() { <-l.waiters }()
	default:
		return ErrQueueFull
	}

	timer := time.NewTimer(l.waitTimeout)
	defer timer.Stop()
	select {
	case l.slots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-l.slots
			return err
		}
		return nil
	case <-timer.C:
		return ErrWaitTimeout
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Limiter) Release() {
	if l != nil {
		<-l.slots
	}
}

// Start lets the legacy shared limiter satisfy Executor. New deployments use
// Manager; this adapter keeps older integrations and focused unit tests valid.
func (l *Limiter) Start(ctx context.Context, _ Kind) (*Task, error) {
	if l == nil {
		return noopTask(ctx), nil
	}
	if err := l.Acquire(ctx); err != nil {
		return nil, err
	}
	taskCtx, cancel := context.WithCancel(ctx)
	return &Task{ctx: taskCtx, cancel: cancel, done: l.Release}, nil
}

func IsBusy(err error) bool {
	return errors.Is(err, ErrQueueFull) || errors.Is(err, ErrWaitTimeout)
}
