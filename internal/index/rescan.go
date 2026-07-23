package index

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/config"
)

const (
	ScanInitializing = "initializing"
	ScanIdle         = "idle"
	ScanScanning     = "scanning"
	ScanError        = "error"
)

var (
	ErrLibraryInitializing = errors.New("library is initializing")
	ErrScanInProgress      = errors.New("library scan is already in progress")
)

type ScanStatus struct {
	State        string
	LibraryReady bool
	Generation   uint64
	LastScan     *time.Time
	ScanError    string
}

// Rescanner owns the published library snapshot and scan lifecycle. A scan is
// always built independently and becomes visible with one atomic pointer swap.
// Failed and cancelled scans never replace the last good snapshot.
type Rescanner struct {
	musicDir      string
	interval      time.Duration
	current       atomic.Pointer[Index]
	beforePublish func(*Index) error
	scan          func(context.Context, string) (*Index, error)

	trigger     chan struct{}
	done        chan struct{}
	cancel      context.CancelFunc
	started     atomic.Bool
	stopping    atomic.Bool
	stopOnce    sync.Once
	lifecycleMu sync.Mutex

	publishMu sync.RWMutex
	statusMu  sync.RWMutex
	status    ScanStatus
}

// NewRescanner accepts an optional ready snapshot for compatibility with
// callers that performed the initial scan synchronously. Passing nil enables
// the intended cold-start lifecycle: initializing/generation 0, followed by an
// immediate background scan when Start is called.
func NewRescanner(cfg *config.Config, initial *Index, beforePublish func(*Index) error) *Rescanner {
	r := &Rescanner{
		musicDir:      cfg.MusicDir,
		interval:      cfg.RescanInterval,
		beforePublish: beforePublish,
		scan:          ScanContext,
		trigger:       make(chan struct{}, 1),
		done:          make(chan struct{}),
		status: ScanStatus{
			State: ScanInitializing,
		},
	}
	if initial != nil {
		now := time.Now()
		r.current.Store(initial)
		r.status = ScanStatus{State: ScanIdle, LibraryReady: true, Generation: 1, LastScan: &now}
	}
	return r
}

func (r *Rescanner) Start(ctx context.Context) {
	r.lifecycleMu.Lock()
	defer r.lifecycleMu.Unlock()
	if r.started.Load() || r.stopping.Load() {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.started.Store(true)
	go r.run(ctx)
}

func (r *Rescanner) run(ctx context.Context) {
	defer close(r.done)
	if r.current.Load() == nil {
		r.performScan(ctx)
	}

	var ticker *time.Ticker
	var ticks <-chan time.Time
	if r.interval > 0 {
		ticker = time.NewTicker(r.interval)
		ticks = ticker.C
		defer ticker.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.trigger:
			r.performScan(ctx)
		case <-ticks:
			if r.current.Load() != nil && r.beginScan() {
				r.performScan(ctx)
			}
		}
	}
}

// Rescan requests one asynchronous full scan. Requests are never queued while
// another scan is running.
func (r *Rescanner) Rescan() error {
	r.statusMu.Lock()
	if r.status.State == ScanInitializing {
		r.statusMu.Unlock()
		return ErrLibraryInitializing
	}
	if r.status.State == ScanScanning {
		r.statusMu.Unlock()
		return ErrScanInProgress
	}
	if r.current.Load() == nil {
		r.status.State = ScanInitializing
	} else {
		r.status.State = ScanScanning
	}
	r.status.ScanError = ""
	r.statusMu.Unlock()

	select {
	case r.trigger <- struct{}{}:
		return nil
	default:
		return ErrScanInProgress
	}
}

func (r *Rescanner) beginScan() bool {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	if r.status.State == ScanInitializing || r.status.State == ScanScanning {
		return false
	}
	if r.current.Load() == nil {
		r.status.State = ScanInitializing
	} else {
		r.status.State = ScanScanning
	}
	r.status.ScanError = ""
	return true
}

func (r *Rescanner) performScan(ctx context.Context) {
	// The initial scan starts in initializing already. Manual and timer scans
	// transition before reaching here so status changes are observable at once.
	idx, err := r.scan(ctx, r.musicDir)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		r.recordScanError(err)
		return
	}
	if r.stopping.Load() || ctx.Err() != nil {
		return
	}
	old := r.current.Load()
	changed := old == nil || !samePaths(old, idx)
	now := time.Now()
	r.publishMu.Lock()
	if r.beforePublish != nil {
		if err := r.beforePublish(idx); err != nil {
			err = fmt.Errorf("prepare scan publication: %w", err)
			r.statusMu.Lock()
			r.status.State = ScanError
			r.status.ScanError = err.Error()
			r.status.LibraryReady = r.current.Load() != nil
			r.statusMu.Unlock()
			r.publishMu.Unlock()
			log.Printf("index: scan failed: %v", err)
			return
		}
	}
	r.statusMu.Lock()
	r.current.Store(idx)
	r.status.State = ScanIdle
	r.status.LibraryReady = true
	r.status.ScanError = ""
	r.status.LastScan = &now
	if changed {
		r.status.Generation++
	}
	r.statusMu.Unlock()
	r.publishMu.Unlock()
}

func (r *Rescanner) recordScanError(err error) {
	log.Printf("index: scan failed: %v", err)
	r.statusMu.Lock()
	r.status.State = ScanError
	r.status.ScanError = err.Error()
	r.status.LibraryReady = r.current.Load() != nil
	r.statusMu.Unlock()
}

func samePaths(a, b *Index) bool {
	if len(a.Files) != len(b.Files) {
		return false
	}
	paths := make(map[string]struct{}, len(a.Files))
	for i := range a.Files {
		paths[a.Files[i].Filepath] = struct{}{}
	}
	for i := range b.Files {
		if _, ok := paths[b.Files[i].Filepath]; !ok {
			return false
		}
	}
	return true
}

// WithSnapshot holds publication while fn checks and mutates state whose
// correctness depends on the current path set.
func (r *Rescanner) WithSnapshot(fn func(*Index, uint64) error) error {
	r.publishMu.RLock()
	defer r.publishMu.RUnlock()
	r.statusMu.RLock()
	idx, generation := r.current.Load(), r.status.Generation
	r.statusMu.RUnlock()
	return fn(idx, generation)
}

func (r *Rescanner) Stop() {
	r.stopOnce.Do(func() {
		r.lifecycleMu.Lock()
		if !r.started.Load() {
			r.stopping.Store(true)
			r.lifecycleMu.Unlock()
			return
		}
		r.stopping.Store(true)
		cancel := r.cancel
		done := r.done
		r.lifecycleMu.Unlock()
		cancel()
		<-done
	})
}

// BeginShutdown makes readiness fail before the HTTP server starts draining.
func (r *Rescanner) BeginShutdown() {
	r.stopping.Store(true)
	r.statusMu.Lock()
	r.status.LibraryReady = false
	r.statusMu.Unlock()
}

func (r *Rescanner) Current() *Index {
	return r.current.Load()
}

// Snapshot captures the index and its generation as one consistent pair.
// Request handlers should call it once and retain the returned index for the
// lifetime of the request.
func (r *Rescanner) Snapshot() (*Index, uint64) {
	r.statusMu.RLock()
	defer r.statusMu.RUnlock()
	return r.current.Load(), r.status.Generation
}

func (r *Rescanner) Status() ScanStatus {
	r.statusMu.RLock()
	defer r.statusMu.RUnlock()
	return r.status
}

// View returns the published snapshot and its complete lifecycle status from
// one critical section for status/readiness responses.
func (r *Rescanner) View() (*Index, ScanStatus) {
	r.statusMu.RLock()
	defer r.statusMu.RUnlock()
	status := r.status
	if r.stopping.Load() {
		status.LibraryReady = false
	}
	return r.current.Load(), status
}
