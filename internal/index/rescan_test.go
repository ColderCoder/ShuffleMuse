package index

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/config"
)

func writeAudio(t *testing.T, root, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitStatus(t *testing.T, r *Rescanner, want string) ScanStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status := r.Status()
		if status.State == want {
			return status
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; last status: %+v", want, r.Status())
	return ScanStatus{}
}

func TestInitialScanLifecycle(t *testing.T) {
	root := t.TempDir()
	writeAudio(t, root, "track.flac")
	r := NewRescanner(&config.Config{MusicDir: root}, nil, nil)
	status := r.Status()
	if status.State != ScanInitializing || status.LibraryReady || status.Generation != 0 || status.LastScan != nil {
		t.Fatalf("unexpected cold status: %+v", status)
	}
	if err := r.Rescan(); !errors.Is(err, ErrLibraryInitializing) {
		t.Fatalf("Rescan error = %v", err)
	}
	r.Start(context.Background())
	t.Cleanup(r.Stop)
	status = waitStatus(t, r, ScanIdle)
	if !status.LibraryReady || status.Generation != 1 || status.LastScan == nil || len(r.Current().Files) != 1 {
		t.Fatalf("unexpected ready state: status=%+v index=%+v", status, r.Current())
	}
}

func TestInitialFailureCanBeRetried(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	r := NewRescanner(&config.Config{MusicDir: root}, nil, nil)
	r.Start(context.Background())
	t.Cleanup(r.Stop)
	status := waitStatus(t, r, ScanError)
	if status.LibraryReady || status.Generation != 0 || status.ScanError == "" || r.Current() != nil {
		t.Fatalf("unexpected failed initial state: %+v", status)
	}
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAudio(t, root, "recovered.flac")
	if err := r.Rescan(); err != nil {
		t.Fatal(err)
	}
	status = waitStatus(t, r, ScanIdle)
	if !status.LibraryReady || status.Generation != 1 || len(r.Current().Files) != 1 {
		t.Fatalf("unexpected retry result: %+v", status)
	}
}

func TestTimerDoesNotRetryFailedInitialization(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	r := NewRescanner(&config.Config{MusicDir: root, RescanInterval: 10 * time.Millisecond}, nil, nil)
	r.Start(context.Background())
	t.Cleanup(r.Stop)
	waitStatus(t, r, ScanError)
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAudio(t, root, "track.flac")
	time.Sleep(40 * time.Millisecond)
	status := r.Status()
	if status.State != ScanError || status.LibraryReady || r.Current() != nil {
		t.Fatalf("timer retried failed initialization: %+v", status)
	}
}

func TestZeroIntervalDisablesPeriodicScanButAllowsManualRescan(t *testing.T) {
	root := t.TempDir()
	writeAudio(t, root, "track.flac")
	initial, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRescanner(&config.Config{MusicDir: root, RescanInterval: 0}, initial, nil)
	scanned := make(chan struct{}, 1)
	r.scan = func(ctx context.Context, musicDir string) (*Index, error) {
		scanned <- struct{}{}
		return ScanContext(ctx, musicDir)
	}
	r.Start(context.Background())
	t.Cleanup(r.Stop)

	select {
	case <-scanned:
		t.Fatal("zero interval started an automatic rescan")
	case <-time.After(40 * time.Millisecond):
	}

	if err := r.Rescan(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-scanned:
	case <-time.After(time.Second):
		t.Fatal("manual rescan did not run with a zero interval")
	}
	status := waitStatus(t, r, ScanIdle)
	if !status.LibraryReady || status.Generation != 1 {
		t.Fatalf("manual rescan changed readiness unexpectedly: %+v", status)
	}
}

func TestRescanPublishesAtomicallyAndOnlyBumpsChangedGeneration(t *testing.T) {
	root := t.TempDir()
	writeAudio(t, root, "old.flac")
	initial, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRescanner(&config.Config{MusicDir: root}, initial, nil)
	r.Start(context.Background())
	t.Cleanup(r.Stop)

	old := r.Current()
	previousLastScan := *r.Status().LastScan
	time.Sleep(time.Millisecond)
	if err := r.Rescan(); err != nil {
		t.Fatal(err)
	}
	status := waitStatus(t, r, ScanIdle)
	if r.Current() == old {
		t.Fatal("successful unchanged scan did not publish its complete snapshot")
	}
	if status.Generation != 1 {
		t.Fatalf("unchanged generation = %d, want 1", status.Generation)
	}
	if status.LastScan == nil || !status.LastScan.After(previousLastScan) {
		t.Fatalf("unchanged scan did not advance lastScan: before=%v after=%v", previousLastScan, status.LastScan)
	}

	writeAudio(t, root, "new.flac")
	old = r.Current()
	if err := r.Rescan(); err != nil {
		t.Fatal(err)
	}
	status = waitStatus(t, r, ScanIdle)
	if status.Generation != 2 || len(r.Current().Files) != 2 || len(old.Files) != 1 {
		t.Fatalf("atomic publish failed: status=%+v old=%d current=%d", status, len(old.Files), len(r.Current().Files))
	}
}

func TestSamePathsIgnoresOrder(t *testing.T) {
	a := &Index{Files: []FileEntry{{Filepath: "a.flac"}, {Filepath: "b.flac"}}}
	b := &Index{Files: []FileEntry{{Filepath: "b.flac"}, {Filepath: "a.flac"}}}
	if !samePaths(a, b) {
		t.Fatal("same path set with different order reported changed")
	}
}

func TestFailedRescanKeepsReadySnapshotAndGeneration(t *testing.T) {
	root := t.TempDir()
	writeAudio(t, root, "track.flac")
	initial, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRescanner(&config.Config{MusicDir: root}, initial, nil)
	r.Start(context.Background())
	t.Cleanup(r.Stop)
	if err := os.Symlink(filepath.Join(root, "missing.flac"), filepath.Join(root, "broken.flac")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := r.Rescan(); err != nil {
		t.Fatal(err)
	}
	status := waitStatus(t, r, ScanError)
	if !status.LibraryReady || status.Generation != 1 || r.Current() != initial {
		t.Fatalf("failed scan replaced ready state: %+v", status)
	}
}

func TestBeforePublishFailureKeepsReadySnapshotAndGeneration(t *testing.T) {
	root := t.TempDir()
	writeAudio(t, root, "track.flac")
	initial, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRescanner(&config.Config{MusicDir: root}, initial, func(*Index) error {
		return errors.New("tag migration failed")
	})
	r.Start(context.Background())
	t.Cleanup(r.Stop)
	writeAudio(t, root, "new.flac")
	if err := r.Rescan(); err != nil {
		t.Fatal(err)
	}
	status := waitStatus(t, r, ScanError)
	if !status.LibraryReady || status.Generation != 1 || r.Current() != initial || !strings.Contains(status.ScanError, "tag migration failed") {
		t.Fatalf("publication hook failure changed snapshot: %+v", status)
	}
}

func TestBeforePublishKeepsOldSnapshotReadable(t *testing.T) {
	root := t.TempDir()
	writeAudio(t, root, "track.flac")
	initial, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	r := NewRescanner(&config.Config{MusicDir: root}, initial, func(*Index) error {
		close(entered)
		<-release
		return nil
	})
	r.Start(context.Background())
	t.Cleanup(r.Stop)
	writeAudio(t, root, "new.flac")
	if err := r.Rescan(); err != nil {
		t.Fatal(err)
	}
	<-entered
	viewed := make(chan struct{})
	go func() {
		idx, status := r.View()
		if idx != initial || status.State != ScanScanning || status.Generation != 1 {
			t.Errorf("unexpected view during publication preparation: idx=%p status=%+v", idx, status)
		}
		close(viewed)
	}()
	select {
	case <-viewed:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("old snapshot view blocked during publication preparation")
	}
	close(release)
	status := waitStatus(t, r, ScanIdle)
	if status.Generation != 2 || len(r.Current().Files) != 2 {
		t.Fatalf("prepared snapshot was not published: %+v", status)
	}
}

func TestRescanAcceptsEmptyAccessibleDirectory(t *testing.T) {
	root := t.TempDir()
	writeAudio(t, root, "track.flac")
	initial, _ := Scan(root)
	r := NewRescanner(&config.Config{MusicDir: root}, initial, nil)
	r.Start(context.Background())
	t.Cleanup(r.Stop)
	if err := os.Remove(filepath.Join(root, "track.flac")); err != nil {
		t.Fatal(err)
	}
	if err := r.Rescan(); err != nil {
		t.Fatal(err)
	}
	status := waitStatus(t, r, ScanIdle)
	if status.Generation != 2 || len(r.Current().Files) != 0 {
		t.Fatalf("empty snapshot not published: %+v", status)
	}
}

func TestStopCancelsInFlightScan(t *testing.T) {
	r := NewRescanner(&config.Config{MusicDir: t.TempDir()}, nil, nil)
	entered := make(chan struct{})
	r.scan = func(ctx context.Context, _ string) (*Index, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	r.Start(context.Background())
	<-entered
	done := make(chan struct{})
	go func() { r.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel scan")
	}
}

func TestConcurrentStartStopDoesNotDeadlock(t *testing.T) {
	for i := 0; i < 50; i++ {
		r := NewRescanner(&config.Config{MusicDir: t.TempDir(), RescanInterval: time.Hour}, nil, nil)
		r.scan = func(ctx context.Context, _ string) (*Index, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		done := make(chan struct{})
		go func() { r.Start(context.Background()); close(done) }()
		stopped := make(chan struct{})
		go func() { r.Stop(); close(stopped) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Start deadlocked")
		}
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("Stop deadlocked")
		}
	}
}

func TestBeginShutdownFailsReadiness(t *testing.T) {
	r := NewRescanner(
		&config.Config{MusicDir: t.TempDir(), RescanInterval: time.Hour},
		&Index{Files: []FileEntry{{ID: "track", Filepath: "track.flac"}}, ByID: map[string]*FileEntry{}},
		nil,
	)
	if _, status := r.View(); !status.LibraryReady {
		t.Fatal("ready snapshot started unavailable")
	}
	r.BeginShutdown()
	if _, status := r.View(); status.LibraryReady {
		t.Fatal("readiness stayed healthy after shutdown began")
	}
}
