package mediaexec

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrShutdown    = errors.New("media manager is shutting down")
	ErrTaskTimeout = errors.New("media task timed out")
)

type Kind uint8

const (
	Transcode Kind = iota
	AuxHigh
	AuxRender
)

type Executor interface {
	Start(ctx context.Context, kind Kind) (*Task, error)
}

type ManagerConfig struct {
	MaxSessions      int
	AuxReserved      int
	TranscodeWaiters int
	AuxWaiters       int
	WaitTimeout      time.Duration
}

type Task struct {
	ctx      context.Context
	cancel   context.CancelFunc
	done     func()
	doneOnce sync.Once
}

func (t *Task) Context() context.Context {
	if t == nil || t.ctx == nil {
		return context.Background()
	}
	return t.ctx
}

// Cancel terminates the task context but deliberately leaves the lane held.
// Callers should terminate and Wait for their process before calling Done.
func (t *Task) Cancel() {
	if t != nil && t.cancel != nil {
		t.cancel()
	}
}

func (t *Task) Done() {
	if t == nil {
		return
	}
	t.doneOnce.Do(func() {
		if t.cancel != nil {
			t.cancel()
		}
		if t.done != nil {
			t.done()
		}
	})
}

type taskResult struct {
	task *Task
	err  error
}

type waiter struct {
	kind   Kind
	ctx    context.Context
	seq    uint64
	queued bool
	result chan taskResult
}

type Manager struct {
	mu sync.Mutex

	maxSessions      int
	auxReserved      int
	transcodeWaiters int
	auxWaiters       int
	waitTimeout      time.Duration

	activeTranscode int
	activeAux       int
	activeShared    int
	highStreak      int
	nextSequence    uint64
	transcodeQueue  []*waiter
	highQueue       []*waiter
	renderQueue     []*waiter
	active          map[*Task]context.CancelFunc
	closing         bool
	wg              sync.WaitGroup
}

func NewManager(config ManagerConfig) *Manager {
	if config.MaxSessions < 1 {
		panic("media process limit must be positive")
	}
	if config.AuxReserved < 0 || config.AuxReserved >= config.MaxSessions {
		panic("auxiliary reservation must be non-negative and below total sessions")
	}
	if config.TranscodeWaiters < 0 || config.AuxWaiters < 0 || config.WaitTimeout <= 0 {
		panic("invalid media process queue configuration")
	}
	return &Manager{
		maxSessions: config.MaxSessions, auxReserved: config.AuxReserved,
		transcodeWaiters: config.TranscodeWaiters, auxWaiters: config.AuxWaiters,
		waitTimeout: config.WaitTimeout, active: make(map[*Task]context.CancelFunc),
	}
}

func (m *Manager) Start(ctx context.Context, kind Kind) (*Task, error) {
	if m == nil {
		return noopTask(ctx), nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return nil, ErrShutdown
	}
	if m.canStartImmediatelyLocked(kind) {
		task := m.startLocked(ctx, kind)
		m.mu.Unlock()
		return task, nil
	}
	if !m.canQueueLocked(kind) {
		m.mu.Unlock()
		return nil, ErrQueueFull
	}
	m.nextSequence++
	w := &waiter{kind: kind, ctx: ctx, seq: m.nextSequence, queued: true, result: make(chan taskResult, 1)}
	m.enqueueLocked(w)
	m.mu.Unlock()

	timer := time.NewTimer(m.waitTimeout)
	defer timer.Stop()
	select {
	case result := <-w.result:
		return result.task, result.err
	case <-ctx.Done():
		m.cancelWaiter(w)
		return nil, ctx.Err()
	case <-timer.C:
		m.cancelWaiter(w)
		return nil, ErrWaitTimeout
	}
}

func (m *Manager) cancelWaiter(w *waiter) {
	m.mu.Lock()
	if w.queued {
		m.removeWaiterLocked(w)
		w.queued = false
		m.scheduleLocked()
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	// A grant raced cancellation or timeout. Release it without exposing the
	// task to a caller that has already abandoned the wait.
	result := <-w.result
	if result.task != nil {
		result.task.Done()
	}
}

func (m *Manager) canStartImmediatelyLocked(kind Kind) bool {
	if m.auxReserved == 0 {
		return m.activeShared < m.maxSessions && len(m.transcodeQueue) == 0 && len(m.highQueue) == 0 && len(m.renderQueue) == 0
	}
	if kind == Transcode {
		return m.activeTranscode < m.maxSessions-m.auxReserved && len(m.transcodeQueue) == 0
	}
	return m.activeAux < m.auxReserved && len(m.highQueue) == 0 && len(m.renderQueue) == 0
}

func (m *Manager) canQueueLocked(kind Kind) bool {
	if kind == Transcode {
		return len(m.transcodeQueue) < m.transcodeWaiters
	}
	return len(m.highQueue)+len(m.renderQueue) < m.auxWaiters
}

func (m *Manager) enqueueLocked(w *waiter) {
	switch w.kind {
	case Transcode:
		m.transcodeQueue = append(m.transcodeQueue, w)
	case AuxHigh:
		m.highQueue = append(m.highQueue, w)
	case AuxRender:
		m.renderQueue = append(m.renderQueue, w)
	}
}

func (m *Manager) removeWaiterLocked(target *waiter) {
	queue := &m.transcodeQueue
	switch target.kind {
	case AuxHigh:
		queue = &m.highQueue
	case AuxRender:
		queue = &m.renderQueue
	}
	for i, candidate := range *queue {
		if candidate == target {
			copy((*queue)[i:], (*queue)[i+1:])
			*queue = (*queue)[:len(*queue)-1]
			return
		}
	}
}

func (m *Manager) startLocked(parent context.Context, kind Kind) *Task {
	ctx, cancel := context.WithCancel(parent)
	task := &Task{ctx: ctx, cancel: cancel}
	task.done = func() { m.finish(task, kind) }
	if m.auxReserved == 0 {
		m.activeShared++
	} else if kind == Transcode {
		m.activeTranscode++
	} else {
		m.activeAux++
	}
	if kind == AuxHigh {
		m.highStreak++
	} else if kind == AuxRender {
		m.highStreak = 0
	}
	m.active[task] = cancel
	m.wg.Add(1)
	return task
}

func (m *Manager) finish(task *Task, kind Kind) {
	m.mu.Lock()
	if _, exists := m.active[task]; !exists {
		m.mu.Unlock()
		return
	}
	delete(m.active, task)
	if m.auxReserved == 0 {
		m.activeShared--
	} else if kind == Transcode {
		m.activeTranscode--
	} else {
		m.activeAux--
	}
	m.scheduleLocked()
	m.mu.Unlock()
	m.wg.Done()
}

func (m *Manager) scheduleLocked() {
	if m.closing {
		return
	}
	if m.auxReserved == 0 {
		for m.activeShared < m.maxSessions {
			w := m.nextSharedLocked()
			if w == nil {
				return
			}
			m.grantLocked(w)
		}
		return
	}
	for m.activeTranscode < m.maxSessions-m.auxReserved && len(m.transcodeQueue) > 0 {
		m.grantLocked(m.popFrontLocked(&m.transcodeQueue))
	}
	for m.activeAux < m.auxReserved {
		w := m.nextAuxLocked()
		if w == nil {
			break
		}
		m.grantLocked(w)
	}
}

func (m *Manager) nextSharedLocked() *waiter {
	aux := m.peekAuxLocked()
	var transcode *waiter
	if len(m.transcodeQueue) > 0 {
		transcode = m.transcodeQueue[0]
	}
	if aux == nil {
		return m.popFrontLocked(&m.transcodeQueue)
	}
	if transcode == nil || aux.seq < transcode.seq {
		return m.nextAuxLocked()
	}
	return m.popFrontLocked(&m.transcodeQueue)
}

func (m *Manager) peekAuxLocked() *waiter {
	if len(m.highQueue) > 0 && (m.highStreak < 4 || len(m.renderQueue) == 0) {
		return m.highQueue[0]
	}
	if len(m.renderQueue) > 0 {
		return m.renderQueue[0]
	}
	if len(m.highQueue) > 0 {
		return m.highQueue[0]
	}
	return nil
}

func (m *Manager) nextAuxLocked() *waiter {
	next := m.peekAuxLocked()
	if next == nil {
		return nil
	}
	if next.kind == AuxHigh {
		return m.popFrontLocked(&m.highQueue)
	}
	return m.popFrontLocked(&m.renderQueue)
}

func (m *Manager) popFrontLocked(queue *[]*waiter) *waiter {
	if len(*queue) == 0 {
		return nil
	}
	w := (*queue)[0]
	copy((*queue)[0:], (*queue)[1:])
	*queue = (*queue)[:len(*queue)-1]
	return w
}

func (m *Manager) grantLocked(w *waiter) {
	if w == nil {
		return
	}
	w.queued = false
	if err := w.ctx.Err(); err != nil {
		w.result <- taskResult{err: err}
		return
	}
	w.result <- taskResult{task: m.startLocked(w.ctx, w.kind)}
}

func (m *Manager) Shutdown(ctx context.Context) bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	if !m.closing {
		m.closing = true
		for _, queue := range [][]*waiter{m.transcodeQueue, m.highQueue, m.renderQueue} {
			for _, w := range queue {
				w.queued = false
				w.result <- taskResult{err: ErrShutdown}
			}
		}
		m.transcodeQueue = nil
		m.highQueue = nil
		m.renderQueue = nil
		for _, cancel := range m.active {
			cancel()
		}
	}
	m.mu.Unlock()
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

type Stats struct {
	ActiveTotal     int
	ActiveTranscode int
	ActiveAux       int
	QueuedTranscode int
	QueuedAux       int
}

func (m *Manager) Stats() Stats {
	if m == nil {
		return Stats{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	activeTotal := m.activeShared + m.activeTranscode + m.activeAux
	return Stats{
		ActiveTotal: activeTotal, ActiveTranscode: m.activeTranscode,
		ActiveAux: m.activeAux, QueuedTranscode: len(m.transcodeQueue),
		QueuedAux: len(m.highQueue) + len(m.renderQueue),
	}
}

func noopTask(ctx context.Context) *Task {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Task{ctx: ctx}
}

func Start(executor Executor, ctx context.Context, kind Kind) (*Task, error) {
	if executor == nil {
		return noopTask(ctx), nil
	}
	return executor.Start(ctx, kind)
}

func IsTimeout(err error) bool {
	return errors.Is(err, ErrTaskTimeout)
}
