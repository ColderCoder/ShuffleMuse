package mediaexec

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func managerForTest(total, reserved int) *Manager {
	return NewManager(ManagerConfig{
		MaxSessions: total, AuxReserved: reserved,
		TranscodeWaiters: 8, AuxWaiters: 16, WaitTimeout: time.Second,
	})
}

func TestManagerStrictLanesAndHardTotal(t *testing.T) {
	m := managerForTest(2, 1)
	transcode, err := m.Start(context.Background(), Transcode)
	if err != nil {
		t.Fatal(err)
	}
	aux, err := m.Start(context.Background(), AuxHigh)
	if err != nil {
		t.Fatalf("long transcode blocked auxiliary lane: %v", err)
	}
	stats := m.Stats()
	if stats.ActiveTotal != 2 || stats.ActiveTranscode != 1 || stats.ActiveAux != 1 {
		t.Fatalf("active lane counts = %+v", stats)
	}

	secondResult := make(chan *Task, 1)
	go func() {
		task, _ := m.Start(context.Background(), Transcode)
		secondResult <- task
	}()
	time.Sleep(10 * time.Millisecond)
	if stats := m.Stats(); stats.QueuedTranscode != 1 || stats.ActiveTotal != 2 {
		t.Fatalf("second transcode did not queue: %+v", stats)
	}
	aux.Done()
	select {
	case <-secondResult:
		t.Fatal("transcode borrowed the released auxiliary lane")
	case <-time.After(20 * time.Millisecond):
	}
	transcode.Done()
	second := <-secondResult
	second.Done()
}

func TestManagerSharedCompatibilityForSingleSession(t *testing.T) {
	m := managerForTest(1, 0)
	first, err := m.Start(context.Background(), Transcode)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan *Task, 1)
	go func() {
		task, _ := m.Start(context.Background(), AuxHigh)
		result <- task
	}()
	time.Sleep(10 * time.Millisecond)
	if m.Stats().ActiveTotal != 1 || m.Stats().QueuedAux != 1 {
		t.Fatalf("shared compatibility stats = %+v", m.Stats())
	}
	first.Done()
	second := <-result
	second.Done()
}

func TestManagerAuxPriorityFairness(t *testing.T) {
	m := managerForTest(2, 1)
	blocker, err := m.Start(context.Background(), AuxHigh)
	if err != nil {
		t.Fatal(err)
	}
	type completion struct {
		kind Kind
		task *Task
	}
	ready := make(chan completion, 8)
	var started sync.WaitGroup
	for _, kind := range []Kind{AuxRender, AuxHigh, AuxHigh, AuxHigh, AuxHigh, AuxHigh} {
		started.Add(1)
		go func(kind Kind) {
			started.Done()
			task, _ := m.Start(context.Background(), kind)
			ready <- completion{kind: kind, task: task}
		}(kind)
		time.Sleep(time.Millisecond)
	}
	started.Wait()
	blocker.Done()
	order := make([]Kind, 0, 6)
	for range 6 {
		next := <-ready
		order = append(order, next.kind)
		next.task.Done()
	}
	if order[0] != AuxHigh || order[1] != AuxHigh || order[2] != AuxHigh || order[3] != AuxRender {
		t.Fatalf("aux fairness order = %v, want three more highs then render after initial high streak", order)
	}
}

func TestManagerQueueFullTimeoutCancellationAndShutdown(t *testing.T) {
	m := NewManager(ManagerConfig{
		MaxSessions: 2, AuxReserved: 1,
		TranscodeWaiters: 1, AuxWaiters: 1, WaitTimeout: 25 * time.Millisecond,
	})
	active, _ := m.Start(context.Background(), Transcode)
	timedOut := make(chan error, 1)
	go func() {
		_, err := m.Start(context.Background(), Transcode)
		timedOut <- err
	}()
	time.Sleep(5 * time.Millisecond)
	if _, err := m.Start(context.Background(), Transcode); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("full queue error = %v", err)
	}
	if err := <-timedOut; !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("wait timeout error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() {
		_, err := m.Start(ctx, Transcode)
		canceled <- err
	}()
	time.Sleep(5 * time.Millisecond)
	cancel()
	if err := <-canceled; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	if m.Stats().QueuedTranscode != 0 {
		t.Fatalf("canceled waiter leaked: %+v", m.Stats())
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	done := make(chan bool, 1)
	go func() { done <- m.Shutdown(shutdownCtx) }()
	select {
	case <-active.Context().Done():
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel active task")
	}
	active.Done()
	if !<-done {
		t.Fatal("manager did not drain after active task completed")
	}
	if _, err := m.Start(context.Background(), AuxHigh); !errors.Is(err, ErrShutdown) {
		t.Fatalf("start after shutdown = %v", err)
	}
}
