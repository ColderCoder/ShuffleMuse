package mediaexec

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLimiterBoundsSessionsAndHonorsCancellation(t *testing.T) {
	limiter := NewLimiter(1)
	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := limiter.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquire error = %v, want deadline exceeded", err)
	}

	limiter.Release()
	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatalf("slot was not reusable after release: %v", err)
	}
	limiter.Release()
}

func TestLimiterDoesNotAcquireForCanceledContext(t *testing.T) {
	limiter := NewLimiter(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := limiter.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire error = %v, want context canceled", err)
	}
	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatalf("canceled acquire consumed a slot: %v", err)
	}
	limiter.Release()
}

func TestLimiterBoundsQueueAndTimesOut(t *testing.T) {
	limiter := NewLimiter(1, QueueConfig{MaxWaiters: 1, WaitTimeout: 30 * time.Millisecond})
	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- limiter.Acquire(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for len(limiter.waiters) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := limiter.Acquire(context.Background()); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("overflow acquire = %v, want ErrQueueFull", err)
	}
	if err := <-done; !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("queued acquire = %v, want ErrWaitTimeout", err)
	}
	if len(limiter.waiters) != 0 {
		t.Fatal("timed out request did not release waiting slot")
	}
	limiter.Release()
}

func TestQueuedCancellationReleasesWaitingSlot(t *testing.T) {
	limiter := NewLimiter(1, QueueConfig{MaxWaiters: 1, WaitTimeout: time.Second})
	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- limiter.Acquire(ctx) }()
	deadline := time.Now().Add(time.Second)
	for len(limiter.waiters) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued acquire = %v, want canceled", err)
	}
	if len(limiter.waiters) != 0 {
		t.Fatal("canceled waiter leaked its queue slot")
	}
	limiter.Release()
}
