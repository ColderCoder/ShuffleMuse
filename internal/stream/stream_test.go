package stream

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/ColderCoder/ShuffleMuse/internal/mediaexec"
)

// createTestFile creates a temp file with the given extension and content.
func createTestFile(t *testing.T, ext string, content []byte) string {
	t.Helper()
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "test*"+ext)
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	return f.Name()
}

func TestMimetypeDetection(t *testing.T) {
	cases := []struct {
		ext      string
		wantMime string
	}{
		{".opus", "audio/opus"},
		{".flac", "audio/flac"},
		{".mp3", "audio/mpeg"},
		{".ogg", "audio/ogg"},
		{".wav", "audio/wav"},
		{".aac", "audio/aac"},
		{".m4a", "audio/mp4"},
		{".wma", "audio/x-ms-wma"},
	}
	for _, tc := range cases {
		t.Run(tc.ext, func(t *testing.T) {
			got := MimeType("song" + tc.ext)
			if got != tc.wantMime {
				t.Errorf("MimeType(%q) = %q, want %q", "song"+tc.ext, got, tc.wantMime)
			}
		})
	}
}

func TestMimetypeUnknown(t *testing.T) {
	got := MimeType("song.xyz")
	if got != "application/octet-stream" {
		t.Errorf("MimeType unknown ext = %q, want %q", got, "application/octet-stream")
	}
}

func TestNativeOpusDetection(t *testing.T) {
	cases := []struct {
		path   string
		expect bool
	}{
		{"song.opus", true},
		{"track.Opus", true},
		{"song.OPUS", true},
		{"song.flac", false},
		{"song.mp3", false},
		{"song.wav", false},
		{"song.ogg", false},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := IsNativeOpus(tc.path)
			if got != tc.expect {
				t.Errorf("IsNativeOpus(%q) = %v, want %v", tc.path, got, tc.expect)
			}
		})
	}
}

func TestServeFullFile(t *testing.T) {
	content := []byte("fake audio data for full file test")
	path := createTestFile(t, ".opus", content)
	defer os.Remove(path)

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()

	err := ServeFile(w, req, path)
	if err != nil {
		t.Fatalf("ServeFile returned error: %v", err)
	}

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "audio/opus" {
		t.Errorf("Content-Type = %q, want %q", ct, "audio/opus")
	}

	cl := resp.Header.Get("Content-Length")
	if cl != strconv.Itoa(len(content)) {
		t.Errorf("Content-Length = %q, want %q", cl, strconv.Itoa(len(content)))
	}

	ar := resp.Header.Get("Accept-Ranges")
	if ar != "bytes" {
		t.Errorf("Accept-Ranges = %q, want %q", ar, "bytes")
	}

	cc := resp.Header.Get("Cache-Control")
	if cc != "private, no-store" {
		t.Errorf("Cache-Control = %q, want %q", cc, "private, no-store")
	}
}

func TestServeRangeRequest(t *testing.T) {
	content := make([]byte, 8192)
	for i := range content {
		content[i] = byte(i % 256)
	}
	path := createTestFile(t, ".flac", content)
	defer os.Remove(path)

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	req.Header.Set("Range", "bytes=0-1023")
	w := httptest.NewRecorder()

	err := ServeFile(w, req, path)
	if err != nil {
		t.Fatalf("ServeFile returned error: %v", err)
	}

	resp := w.Result()
	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusPartialContent)
	}

	cr := resp.Header.Get("Content-Range")
	expectedCR := "bytes 0-1023/8192"
	if cr != expectedCR {
		t.Errorf("Content-Range = %q, want %q", cr, expectedCR)
	}

	if resp.ContentLength != 1024 {
		t.Errorf("Content-Length = %d, want %d", resp.ContentLength, 1024)
	}
}

func TestServeRangeEndBeyondFile(t *testing.T) {
	content := make([]byte, 500)
	path := createTestFile(t, ".mp3", content)
	defer os.Remove(path)

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	req.Header.Set("Range", "bytes=0-999")
	w := httptest.NewRecorder()

	err := ServeFile(w, req, path)
	if err != nil {
		t.Fatalf("ServeFile returned error: %v", err)
	}

	resp := w.Result()
	if resp.StatusCode != http.StatusPartialContent {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusPartialContent)
	}

	cr := resp.Header.Get("Content-Range")
	expectedCR := "bytes 0-499/500"
	if cr != expectedCR {
		t.Errorf("Content-Range = %q, want %q", cr, expectedCR)
	}
}

func TestServeInvalidRange(t *testing.T) {
	content := []byte("fake audio data for invalid range test")
	path := createTestFile(t, ".wav", content)
	defer os.Remove(path)

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	req.Header.Set("Range", "bytes=invalid")
	w := httptest.NewRecorder()

	err := ServeFile(w, req, path)
	if err != nil {
		t.Fatalf("ServeFile returned error: %v", err)
	}

	resp := w.Result()
	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusRequestedRangeNotSatisfiable)
	}
}

func TestServeNonexistentFile(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()

	err := ServeFile(w, req, "/nonexistent/path/to/song.opus")
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}

func TestRouterServeStreamNativeOpus(t *testing.T) {
	content := []byte("fake opus audio data")
	path := createTestFile(t, ".opus", content)
	defer os.Remove(path)

	r := &Router{}

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()

	err := r.ServeStream(w, req, path)
	if err != nil {
		t.Fatalf("ServeStream returned error: %v", err)
	}

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "audio/opus" {
		t.Errorf("Content-Type = %q, want %q", ct, "audio/opus")
	}
}

func TestRouterServeStreamOriginalSupportsRange(t *testing.T) {
	flacPath := createTestFlac(t)
	r := &Router{Bitrate: 160}
	req := httptest.NewRequest(http.MethodGet, "/stream?mode=original", nil)
	req.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()

	if err := r.ServeStream(w, req, flacPath); err != nil {
		t.Fatalf("ServeStream returned error: %v", err)
	}
	resp := w.Result()
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusPartialContent)
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("Accept-Ranges = %q, want bytes", got)
	}
}

func TestRouterHeadForTranscodeDoesNotStartFFmpeg(t *testing.T) {
	router := &Router{Bitrate: 160, Limiter: mediaexec.NewLimiter(1)}
	request := httptest.NewRequest(http.MethodHead, "/api/stream/id?mode=opus", nil)
	recorder := httptest.NewRecorder()
	if err := router.ServeStream(recorder, request, "/path/does/not/exist.flac"); err != nil {
		t.Fatalf("HEAD returned error: %v", err)
	}
	if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Fatalf("HEAD status/body = %d/%q", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("HEAD cache control = %q", recorder.Header().Get("Cache-Control"))
	}
}

func TestRouterRejectsInvalidStreamOptions(t *testing.T) {
	path := createTestFile(t, ".opus", []byte("audio"))
	defer os.Remove(path)
	r := &Router{Bitrate: 160}

	for _, target := range []string{
		"/stream?mode=unknown",
		"/stream?mode=opus&start=-1",
		"/stream?mode=original&start=2",
	} {
		err := r.ServeStream(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil), path)
		if !errors.Is(err, ErrInvalidStreamOptions) {
			t.Errorf("ServeStream(%q) error = %v, want ErrInvalidStreamOptions", target, err)
		}
	}
}

func TestRouterServeStreamTranscode(t *testing.T) {
	flacPath := createTestFlac(t)

	r := &Router{Bitrate: 160}

	req := httptest.NewRequest(http.MethodGet, "/stream", nil)
	w := httptest.NewRecorder()

	err := r.ServeStream(w, req, flacPath)
	if err != nil {
		t.Fatalf("ServeStream returned error: %v", err)
	}

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	ct := resp.Header.Get("Content-Type")
	if ct != "audio/ogg; codecs=opus" {
		t.Errorf("Content-Type = %q, want %q", ct, "audio/ogg; codecs=opus")
	}
}
