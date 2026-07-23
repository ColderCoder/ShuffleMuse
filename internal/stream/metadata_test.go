package stream

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/mediaexec"
)

func TestMetadataFromProbeFallsBackToAverageBitrate(t *testing.T) {
	var probe ffprobeOutput
	probe.Streams = append(probe.Streams, struct {
		CodecName string `json:"codec_name"`
		BitRate   string `json:"bit_rate"`
		Duration  string `json:"duration"`
	}{CodecName: "flac", Duration: "10"})

	metadata, err := metadataFromProbe(probe, "track.flac", 1_250_000)
	if err != nil {
		t.Fatalf("metadataFromProbe returned error: %v", err)
	}
	if metadata.Codec != "FLAC" || metadata.BitrateKbps != 1000 || !metadata.BitrateApproximate {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
}

func TestMetadataProbeLRUEviction(t *testing.T) {
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "a.flac"), filepath.Join(dir, "b.flac"), filepath.Join(dir, "c.flac")}
	for _, path := range paths {
		if err := os.WriteFile(path, []byte(path), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	calls := make(map[string]int)
	probe := NewMetadataProbe(nil, MetadataConfig{Capacity: 2})
	probe.probe = func(_ context.Context, key metadataKey) (Metadata, error) {
		calls[key.path]++
		return Metadata{Codec: "FLAC", DurationSeconds: 1}, nil
	}
	for _, path := range []string{paths[0], paths[1], paths[0], paths[2], paths[1]} {
		if _, err := probe.Probe(context.Background(), path); err != nil {
			t.Fatal(err)
		}
	}
	if calls[paths[0]] != 1 || calls[paths[1]] != 2 || calls[paths[2]] != 1 {
		t.Fatalf("unexpected probe calls after LRU eviction: %v", calls)
	}
}

func TestMetadataProbeReadsFlac(t *testing.T) {
	path := createTestFlac(t)
	metadata, err := NewMetadataProbe(nil).Probe(context.Background(), path)
	if err != nil {
		t.Fatalf("Probe returned error: %v", err)
	}
	if metadata.Codec != "FLAC" {
		t.Errorf("Codec = %q, want FLAC", metadata.Codec)
	}
	if metadata.DurationSeconds <= 0 || metadata.BitrateKbps <= 0 {
		t.Errorf("incomplete metadata: %+v", metadata)
	}
}

func TestMetadataProbeLimiterWaitHonorsCancellation(t *testing.T) {
	path := t.TempDir() + "/track.flac"
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	limiter := mediaexec.NewLimiter(1)
	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer limiter.Release()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := NewMetadataProbe(limiter).Probe(ctx, path)
		done <- err
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Probe error = %v, want context canceled", err)
	}
}

func TestMetadataProbeSingleflightAndIndependentWaiters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "track.flac")
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := NewMetadataProbe(nil)
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	calls := 0
	probe.probe = func(ctx context.Context, _ metadataKey) (Metadata, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		once.Do(func() { close(started) })
		select {
		case <-release:
			return Metadata{Codec: "FLAC", DurationSeconds: 1}, nil
		case <-ctx.Done():
			return Metadata{}, ctx.Err()
		}
	}

	const waiters = 100
	results := make(chan error, waiters)
	contexts := make([]context.CancelFunc, waiters)
	for i := 0; i < waiters; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		contexts[i] = cancel
		go func() {
			_, err := probe.Probe(ctx, path)
			results <- err
		}()
	}
	<-started
	for i := 0; i < waiters/2; i++ {
		contexts[i]()
	}
	close(release)
	canceled, successful := 0, 0
	for range waiters {
		if err := <-results; errors.Is(err, context.Canceled) {
			canceled++
		} else if err == nil {
			successful++
		} else {
			t.Fatalf("unexpected waiter error: %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 || canceled == 0 || successful == 0 {
		t.Fatalf("calls/canceled/successful = %d/%d/%d", calls, canceled, successful)
	}
}

func TestMetadataProbeNegativeCacheAndIdentityInvalidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.flac")
	if err := os.WriteFile(path, []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	probe := NewMetadataProbe(nil, MetadataConfig{Capacity: 4, NegativeTTL: time.Minute})
	probe.now = func() time.Time { return now }
	calls := 0
	probe.probe = func(context.Context, metadataKey) (Metadata, error) {
		calls++
		return Metadata{}, deterministicMetadataError{errors.New("invalid audio")}
	}
	for range 2 {
		if _, err := probe.Probe(context.Background(), path); err == nil {
			t.Fatal("expected deterministic failure")
		}
	}
	if calls != 1 {
		t.Fatalf("negative cache calls = %d", calls)
	}
	if err := os.WriteFile(path, []byte("changed-size"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Probe(context.Background(), path); err == nil {
		t.Fatal("expected failure after identity change")
	}
	if calls != 2 {
		t.Fatalf("identity change did not invalidate negative cache: %d", calls)
	}
	now = now.Add(2 * time.Minute)
	if _, err := probe.Probe(context.Background(), path); err == nil {
		t.Fatal("expected failure after TTL")
	}
	if calls != 3 {
		t.Fatalf("negative TTL did not expire: %d", calls)
	}
}

func TestMetadataProbeDoesNotCacheBusyTimeoutOrAbandonedFlight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "track.flac")
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, probeErr := range map[string]error{
		"busy":    mediaexec.ErrQueueFull,
		"timeout": mediaexec.ErrTaskTimeout,
	} {
		t.Run(name, func(t *testing.T) {
			probe := NewMetadataProbe(nil)
			calls := 0
			probe.probe = func(context.Context, metadataKey) (Metadata, error) {
				calls++
				return Metadata{}, probeErr
			}
			for range 2 {
				if _, err := probe.Probe(context.Background(), path); !errors.Is(err, probeErr) {
					t.Fatalf("probe error = %v, want %v", err, probeErr)
				}
			}
			if calls != 2 {
				t.Fatalf("transient failure was cached: calls=%d", calls)
			}
		})
	}

	probe := NewMetadataProbe(nil)
	started := make(chan struct{})
	canceled := make(chan struct{})
	probe.probe = func(ctx context.Context, _ metadataKey) (Metadata, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return Metadata{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := probe.Probe(ctx, path)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter cancellation = %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("last waiter did not cancel the underlying metadata flight")
	}
}
