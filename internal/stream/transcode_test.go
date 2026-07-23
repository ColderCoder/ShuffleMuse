package stream

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/mediaexec"
)

func createTestFlac(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/test_audio.flac"
	cmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", "sine=frequency=440:duration=1",
		"-c", "flac",
		"-y",
		path,
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create test flac: %v", err)
	}
	return path
}

func createTestFlacWithCover(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	coverPath := dir + "/cover.jpg"
	audioPath := dir + "/covered.flac"

	coverCmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", "color=c=red:s=32x32:d=1",
		"-frames:v", "1",
		"-y",
		coverPath,
	)
	if err := coverCmd.Run(); err != nil {
		t.Fatalf("failed to create cover image: %v", err)
	}

	audioCmd := exec.Command("ffmpeg",
		"-f", "lavfi",
		"-i", "sine=frequency=440:duration=1",
		"-i", coverPath,
		"-map", "0:a",
		"-map", "1:v",
		"-c:a", "flac",
		"-c:v", "mjpeg",
		"-disposition:v", "attached_pic",
		"-y",
		audioPath,
	)
	if err := audioCmd.Run(); err != nil {
		t.Fatalf("failed to create covered flac: %v", err)
	}

	return audioPath
}

func TestTranscodeOpusOutput(t *testing.T) {
	flacPath := createTestFlac(t)

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()

	err := Transcode(w, req, flacPath, 160, nil)
	if err != nil {
		t.Fatalf("Transcode returned error: %v", err)
	}

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "audio/ogg; codecs=opus" {
		t.Errorf("Content-Type = %q, want %q", ct, "audio/ogg; codecs=opus")
	}

	cc := resp.Header.Get("Cache-Control")
	if cc != "private, no-store" {
		t.Errorf("Cache-Control = %q, want %q", cc, "private, no-store")
	}

	ar := resp.Header.Get("Accept-Ranges")
	if ar != "" {
		t.Errorf("Accept-Ranges = %q, want empty (no seeking in transcoded streams)", ar)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	if len(body) == 0 {
		t.Fatal("response body is empty, expected OGG Opus data")
	}

	if !bytes.HasPrefix(body, []byte("OggS")) {
		t.Errorf("response body does not start with OggS magic bytes, got: %x", body[:min(len(body), 4)])
	}
}

func TestTranscodeAtStartsFromRequestedOffset(t *testing.T) {
	flacPath := createTestFlac(t)
	req := httptest.NewRequest(http.MethodGet, "/stream?mode=opus&start=0.5", nil)
	w := httptest.NewRecorder()

	if err := TranscodeAt(w, req, flacPath, 160, 0.5, nil); err != nil {
		t.Fatalf("TranscodeAt returned error: %v", err)
	}
	outputPath := t.TempDir() + "/offset.ogg"
	if err := os.WriteFile(outputPath, w.Body.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(
		"ffprobe", "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", outputPath,
	).Output()
	if err != nil {
		t.Fatalf("ffprobe offset output: %v", err)
	}
	duration, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil {
		t.Fatalf("parse offset duration %q: %v", output, err)
	}
	if duration <= 0 || duration >= 0.8 {
		t.Fatalf("offset output duration = %.3f, want approximately 0.5 seconds", duration)
	}
}

func TestTranscodeStripsCoverArtVideo(t *testing.T) {
	flacPath := createTestFlacWithCover(t)

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()

	if err := Transcode(w, req, flacPath, 160, nil); err != nil {
		t.Fatalf("Transcode returned error: %v", err)
	}

	body, err := io.ReadAll(w.Result().Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	outPath := t.TempDir() + "/out.ogg"
	if err := os.WriteFile(outPath, body, 0o644); err != nil {
		t.Fatalf("write transcode output: %v", err)
	}

	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-select_streams", "v",
		"-show_entries", "stream=index",
		"-of", "csv=p=0",
		outPath,
	)
	videoStreams, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe video streams: %v", err)
	}
	if len(bytes.TrimSpace(videoStreams)) != 0 {
		t.Fatalf("expected no video streams in transcoded output, got %q", string(videoStreams))
	}
}

func TestTranscodeClientDisconnect(t *testing.T) {
	flacPath := createTestFlac(t)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/stream", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan error, 1)
	go func() {
		done <- Transcode(w, req, flacPath, 160, nil)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Transcode hung after client disconnect — ffmpeg process likely not killed")
	}
}

func TestTranscodeFirstByteTimeoutTerminatesAndReleasesLane(t *testing.T) {
	bin := t.TempDir()
	fake := filepath.Join(bin, "ffmpeg")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	manager := mediaexec.NewManager(mediaexec.ManagerConfig{
		MaxSessions: 2, AuxReserved: 1, TranscodeWaiters: 1, AuxWaiters: 1, WaitTimeout: time.Second,
	})
	err := TranscodeWithConfig(
		httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/stream", nil),
		"unused.flac", 160, manager, TranscodeConfig{StartupTimeout: 30 * time.Millisecond, WriteIdle: time.Second},
	)
	if !errors.Is(err, mediaexec.ErrTaskTimeout) {
		t.Fatalf("first-byte timeout = %v", err)
	}
	if stats := manager.Stats(); stats.ActiveTotal != 0 {
		t.Fatalf("timed-out process leaked lane: %+v", stats)
	}
}

type failingResponseWriter struct {
	header http.Header
}

func (w *failingResponseWriter) Header() http.Header { return w.header }
func (w *failingResponseWriter) WriteHeader(int)     {}
func (w *failingResponseWriter) Write([]byte) (int, error) {
	return 0, errors.New("client disconnected")
}

func TestTranscodeWriteErrorTerminatesBeforeRelease(t *testing.T) {
	flacPath := createTestFlac(t)
	manager := mediaexec.NewManager(mediaexec.ManagerConfig{
		MaxSessions: 2, AuxReserved: 1, TranscodeWaiters: 1, AuxWaiters: 1, WaitTimeout: time.Second,
	})
	w := &failingResponseWriter{header: make(http.Header)}
	err := TranscodeWithConfig(w, httptest.NewRequest(http.MethodGet, "/stream", nil), flacPath, 160, manager, TranscodeConfig{})
	if err == nil || !strings.Contains(err.Error(), "client disconnected") {
		t.Fatalf("write error = %v", err)
	}
	if stats := manager.Stats(); stats.ActiveTotal != 0 {
		t.Fatalf("write failure leaked lane: %+v", stats)
	}
}

func TestTranscodeLimiterWaitHonorsCancellation(t *testing.T) {
	limiter := mediaexec.NewLimiter(1)
	if err := limiter.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer limiter.Release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/stream", nil).WithContext(ctx)
	err := Transcode(httptest.NewRecorder(), req, "/unused.flac", 160, limiter)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Transcode error = %v, want context canceled", err)
	}
}

func TestTranscodeNonexistentFile(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()

	err := Transcode(w, req, "/nonexistent/path/to/song.flac", 160, nil)
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}

func TestTranscodeBitrateParam(t *testing.T) {
	flacPath := createTestFlac(t)

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()

	err := Transcode(w, req, flacPath, 96, nil)
	if err != nil {
		t.Fatalf("Transcode with bitrate 96 returned error: %v", err)
	}

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response body: %v", err)
	}

	if len(body) == 0 {
		t.Fatal("response body is empty")
	}
	if !bytes.HasPrefix(body, []byte("OggS")) {
		t.Errorf("response body does not start with OggS magic bytes")
	}
}

func TestTranscodeSetsCorrectHeaders(t *testing.T) {
	flacPath := createTestFlac(t)

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()

	err := Transcode(w, req, flacPath, 160, nil)
	if err != nil {
		t.Fatalf("Transcode returned error: %v", err)
	}

	resp := w.Result()

	if resp.Header.Get("Content-Type") != "audio/ogg; codecs=opus" {
		t.Errorf("Content-Type = %q, want %q", resp.Header.Get("Content-Type"), "audio/ogg; codecs=opus")
	}
	if resp.Header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("Cache-Control = %q, want %q", resp.Header.Get("Cache-Control"), "private, no-store")
	}
	if resp.Header.Get("Accept-Ranges") != "" {
		t.Errorf("Accept-Ranges should be empty for transcoded streams, got %q", resp.Header.Get("Accept-Ranges"))
	}
}

func init() {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		panic("ffmpeg not found in PATH — required for transcode tests")
	}
}
