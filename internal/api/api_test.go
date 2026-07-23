package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/auth"
	"github.com/ColderCoder/ShuffleMuse/internal/config"
	"github.com/ColderCoder/ShuffleMuse/internal/index"
	"github.com/ColderCoder/ShuffleMuse/internal/mediaexec"
	"github.com/ColderCoder/ShuffleMuse/internal/stream"
	"github.com/ColderCoder/ShuffleMuse/internal/tags"
)

// testEnv holds all the dependencies for a test environment.
type testEnv struct {
	api      *API
	server   *httptest.Server
	tmpDir   string
	tagStore *tags.Store
	idx      *index.Index
	cleanup  func()
}

func setupTestEnv(t testing.TB) *testEnv {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "shufflemuse-api-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}

	// Create a few fake audio files
	musicDir := filepath.Join(tmpDir, "music")
	artistDir := filepath.Join(musicDir, "artist1")
	if err := os.MkdirAll(artistDir, 0o755); err != nil {
		t.Fatalf("create music dir: %v", err)
	}

	files := []string{
		filepath.Join(artistDir, "track1.flac"),
		filepath.Join(artistDir, "track2.flac"),
		filepath.Join(artistDir, "track3.opus"),
	}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("fake audio data"), 0o644); err != nil {
			t.Fatalf("create file %s: %v", f, err)
		}
	}

	// Scan the music dir
	idx, err := index.Scan(musicDir)
	if err != nil {
		t.Fatalf("scan music dir: %v", err)
	}

	// Open BoltDB for tags
	boltPath := filepath.Join(tmpDir, "tags.db")
	tagStore, err := tags.Open(boltPath)
	if err != nil {
		t.Fatalf("open tag store: %v", err)
	}

	cfg := &config.Config{
		Port:         8080,
		MusicDir:     musicDir,
		OpusBitrate:  128,
		AuthPassword: "testpass",
		BoltDBPath:   boltPath,
	}

	router := &stream.Router{Bitrate: 128}
	a := auth.New("testpass")

	api := NewAPI(cfg, idx, tagStore, router, a, nil)

	handler := api.Routes()
	server := httptest.NewServer(handler)

	return &testEnv{
		api:      api,
		server:   server,
		tmpDir:   tmpDir,
		tagStore: tagStore,
		idx:      idx,
		cleanup: func() {
			server.Close()
			tagStore.Close()
			os.RemoveAll(tmpDir)
		},
	}
}

func (e *testEnv) teardown() {
	e.cleanup()
}

func getJSON(t *testing.T, url string, headers map[string]string) (*http.Response, map[string]interface{}) {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("parse json: %v, body: %s", err, string(body))
	}
	return resp, result
}

func postJSON(t *testing.T, url string, body string, headers map[string]string) (*http.Response, map[string]interface{}) {
	t.Helper()
	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	respBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var result map[string]interface{}
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &result); err != nil {
			t.Fatalf("parse json: %v, body: %s", err, string(respBody))
		}
	}
	return resp, result
}

func doDelete(t *testing.T, url string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	resp.Body.Close()
	return resp
}

// --- Tests ---

func TestStatusEndpoint(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	resp, body := getJSON(t, env.server.URL+"/api/status", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d, body: %v", resp.StatusCode, body)
	}

	if body["authRequired"] != true {
		t.Errorf("expected authRequired=true, got %v", body["authRequired"])
	}
	if body["authenticated"] != false {
		t.Errorf("expected authenticated=false without cookie, got %v", body["authenticated"])
	}
	for _, internal := range []string{"fileCount", "scanStatus", "uptime", "lastScan", "libraryGeneration", "scanError"} {
		if _, exists := body[internal]; exists {
			t.Errorf("unauthenticated status leaked %s", internal)
		}
	}

	cookie, err := env.api.Auth.Login("testpass", false)
	if err != nil {
		t.Fatal(err)
	}
	resp, body = getJSON(t, env.server.URL+"/api/status", map[string]string{"Cookie": cookie.String()})
	if resp.StatusCode != http.StatusOK || body["authenticated"] != true {
		t.Fatalf("authenticated status = %d/%v", resp.StatusCode, body)
	}
	for _, field := range []string{"fileCount", "scanStatus", "uptime", "lastScan"} {
		if _, ok := body[field]; !ok {
			t.Errorf("authenticated status missing %s", field)
		}
	}

	// fileCount should match the number of scanned files
	fc := int(body["fileCount"].(float64))
	if fc != len(env.idx.Files) {
		t.Errorf("expected fileCount=%d, got %d", len(env.idx.Files), fc)
	}
}

func TestFilesEndpoint(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	resp, body := getJSON(t, env.server.URL+"/api/files", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d, body: %v", resp.StatusCode, body)
	}

	if _, ok := body["items"]; !ok {
		t.Error("expected items in response")
	}
	if _, ok := body["total"]; !ok {
		t.Error("expected total in response")
	}
	if _, ok := body["page"]; !ok {
		t.Error("expected page in response")
	}

	total := int(body["total"].(float64))
	if total != len(env.idx.Files) {
		t.Errorf("expected total=%d, got %d", len(env.idx.Files), total)
	}
}

func TestFilesPagination(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	resp, body := getJSON(t, env.server.URL+"/api/files?page=1&limit=2", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d, body: %v", resp.StatusCode, body)
	}

	items := body["items"].([]interface{})
	if len(items) > 2 {
		t.Errorf("expected at most 2 items, got %d", len(items))
	}
}

func TestCollectionEndpointsHandleMaximumPageNumber(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	if err := env.tagStore.AddTag(env.idx.Files[0].Filepath, "favorite"); err != nil {
		t.Fatal(err)
	}
	maxPage := int(^uint(0) >> 1)
	for _, path := range []string{
		"/api/files",
		"/api/tags/favorite/files",
	} {
		resp, body := getJSON(t, fmt.Sprintf("%s%s?page=%d&limit=2", env.server.URL, path, maxPage), nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, body: %v", path, resp.StatusCode, body)
		}
		items, ok := body["items"].([]interface{})
		if !ok || len(items) != 0 {
			t.Fatalf("GET %s expected an empty page, got %v", path, body["items"])
		}
	}
}

func TestFileTags(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	fileID := env.idx.Files[0].ID

	resp, body := getJSON(t, fmt.Sprintf("%s/api/files/%s/tags", env.server.URL, fileID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d, body: %v", resp.StatusCode, body)
	}

	tagsArr, ok := body["tags"].([]interface{})
	if !ok {
		t.Fatalf("expected tags array, got: %v", body)
	}
	// Initially should be empty
	if len(tagsArr) != 0 {
		t.Errorf("expected empty tags, got %v", tagsArr)
	}
}

func TestAddTag(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	fileID := env.idx.Files[0].ID

	resp, body := postJSON(t, fmt.Sprintf("%s/api/files/%s/tags", env.server.URL, fileID), `{"tag":"favorite"}`, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("expected 201, got %d, body: %v", resp.StatusCode, body)
	}

	// Verify tag was added
	_, tagsBody := getJSON(t, fmt.Sprintf("%s/api/files/%s/tags", env.server.URL, fileID), nil)
	tagsArr := tagsBody["tags"].([]interface{})
	if len(tagsArr) != 1 {
		t.Errorf("expected 1 tag, got %d", len(tagsArr))
	}
	if tagsArr[0] != "favorite" {
		t.Errorf("expected tag 'favorite', got %v", tagsArr[0])
	}
}

func TestAddDuplicateTag(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	fileID := env.idx.Files[0].ID

	// Add tag first time
	resp, _ := postJSON(t, fmt.Sprintf("%s/api/files/%s/tags", env.server.URL, fileID), `{"tag":"favorite"}`, nil)
	if resp.StatusCode != 201 {
		t.Fatalf("expected 201 on first add, got %d", resp.StatusCode)
	}

	// Add same tag again
	resp, body := postJSON(t, fmt.Sprintf("%s/api/files/%s/tags", env.server.URL, fileID), `{"tag":"favorite"}`, nil)
	if resp.StatusCode != 409 {
		t.Fatalf("expected 409 on duplicate, got %d, body: %v", resp.StatusCode, body)
	}
}

func TestRemoveTag(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	fileID := env.idx.Files[0].ID

	// Add a tag first
	postJSON(t, fmt.Sprintf("%s/api/files/%s/tags", env.server.URL, fileID), `{"tag":"favorite"}`, nil)

	// Remove it
	resp := doDelete(t, fmt.Sprintf("%s/api/files/%s/tags/favorite", env.server.URL, fileID), nil)
	if resp.StatusCode != 204 {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// Verify tag was removed
	_, tagsBody := getJSON(t, fmt.Sprintf("%s/api/files/%s/tags", env.server.URL, fileID), nil)
	tagsArr := tagsBody["tags"].([]interface{})
	if len(tagsArr) != 0 {
		t.Errorf("expected 0 tags after removal, got %d", len(tagsArr))
	}
}

func TestGetAllTags(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	// Add some tags first
	fileID1 := env.idx.Files[0].ID
	fileID2 := env.idx.Files[1].ID
	postJSON(t, fmt.Sprintf("%s/api/files/%s/tags", env.server.URL, fileID1), `{"tag":"rock"}`, nil)
	postJSON(t, fmt.Sprintf("%s/api/files/%s/tags", env.server.URL, fileID1), `{"tag":"pop"}`, nil)
	postJSON(t, fmt.Sprintf("%s/api/files/%s/tags", env.server.URL, fileID2), `{"tag":"rock"}`, nil)

	resp, body := getJSON(t, env.server.URL+"/api/tags", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d, body: %v", resp.StatusCode, body)
	}

	tagsArr, ok := body["tags"].([]interface{})
	if !ok {
		t.Fatalf("expected tags array, got: %v", body)
	}
	if len(tagsArr) < 2 {
		t.Errorf("expected at least 2 tags, got %d", len(tagsArr))
	}
	for _, item := range tagsArr {
		tagInfo, ok := item.(map[string]interface{})
		if !ok {
			t.Fatalf("expected tag object, got %T", item)
		}
		if _, ok := tagInfo["name"]; !ok {
			t.Errorf("expected lowercase name field in tag response, got %v", tagInfo)
		}
		if _, ok := tagInfo["count"]; !ok {
			t.Errorf("expected lowercase count field in tag response, got %v", tagInfo)
		}
		if _, ok := tagInfo["Name"]; ok {
			t.Errorf("unexpected exported Name field in tag response: %v", tagInfo)
		}
	}
}

func TestSearchEndpoint(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	resp, body := getJSON(t, env.server.URL+"/api/search?q=track1", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d, body: %v", resp.StatusCode, body)
	}

	items, ok := body["items"].([]interface{})
	if !ok {
		t.Fatalf("expected items array, got: %v", body)
	}
	if len(items) == 0 {
		t.Error("expected at least one search result for 'track1'")
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	resp, _ := getJSON(t, env.server.URL+"/api/search", nil)
	if resp.StatusCode != 400 {
		t.Fatalf("expected 400 for empty query, got %d", resp.StatusCode)
	}
}

func TestStreamByID(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	fileID := env.idx.Files[0].ID

	// The .flac file will try to transcode via ffmpeg which may not be available in test,
	// so we just check that the handler is wired up correctly by verifying it doesn't 404.
	resp, err := http.Get(fmt.Sprintf("%s/api/stream/%s", env.server.URL, fileID))
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	// We expect either 200 (if ffmpeg available) or 500 (ffmpeg not found)
	// but NOT 404 (route not registered)
	if resp.StatusCode == 404 {
		t.Error("stream endpoint returned 404 — route not registered")
	}
}

func TestFileNotFound(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	resp, _ := getJSON(t, env.server.URL+"/api/files/nonexistent/tags", nil)
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 for nonexistent file, got %d", resp.StatusCode)
	}
}

func TestAddTagInvalidBody(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	fileID := env.idx.Files[0].ID

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/api/files/%s/tags", env.server.URL, fileID), strings.NewReader(`invalid`))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}
}

func TestStrictJSONAndRequestSize(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	endpoint := fmt.Sprintf("%s/api/files/%s/tags", env.server.URL, env.idx.Files[0].ID)
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{"unknown field", `{"tag":"rock","extra":true}`, "INVALID_JSON", http.StatusBadRequest},
		{"trailing JSON", `{"tag":"rock"}{"tag":"pop"}`, "INVALID_JSON", http.StatusBadRequest},
		{"too large", `{"tag":"` + strings.Repeat("x", maxJSONBodyBytes) + `"}`, "REQUEST_TOO_LARGE", http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := postJSON(t, endpoint, tc.body, nil)
			if resp.StatusCode != tc.status || body["code"] != tc.code {
				t.Fatalf("status/body = %d/%v", resp.StatusCode, body)
			}
		})
	}
}

func TestQueryLengthAndPaginationValidation(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	for _, target := range []string{
		"/api/files?page=0",
		"/api/files?limit=invalid",
		"/api/files?limit=1001",
		"/api/browse?dir=.&page=-1",
	} {
		resp, body := getJSON(t, env.server.URL+target, nil)
		if resp.StatusCode != http.StatusBadRequest || body["code"] != "INVALID_PAGINATION" {
			t.Fatalf("GET %s = %d/%v", target, resp.StatusCode, body)
		}
	}
	resp, body := getJSON(t, env.server.URL+"/api/search?q="+strings.Repeat("a", 201), nil)
	if resp.StatusCode != http.StatusBadRequest || body["code"] != "QUERY_TOO_LONG" {
		t.Fatalf("long search = %d/%v", resp.StatusCode, body)
	}
}

func TestHostCSRFSecurityHeaders(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	resp, _ := getJSON(t, env.server.URL+"/api/status", nil)
	for _, header := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy", "Permissions-Policy"} {
		if resp.Header.Get(header) == "" {
			t.Errorf("missing security header %s", header)
		}
	}

	req, _ := http.NewRequest(http.MethodGet, env.server.URL+"/api/status", nil)
	req.Host = "attacker.example"
	invalidHost, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	invalidHost.Body.Close()
	if invalidHost.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid Host status = %d", invalidHost.StatusCode)
	}

	for _, headers := range []map[string]string{
		{"Origin": "https://attacker.example"},
		{"Sec-Fetch-Site": "cross-site"},
	} {
		response, body := postJSON(t, env.server.URL+"/api/rescan", `{}`, headers)
		if response.StatusCode != http.StatusForbidden || body["code"] != "CSRF_BLOCKED" {
			t.Fatalf("CSRF response = %d/%v", response.StatusCode, body)
		}
	}
}

type busyMediaProbe struct{}

func (busyMediaProbe) Probe(context.Context, string) (stream.Metadata, error) {
	return stream.Metadata{}, mediaexec.ErrQueueFull
}

type timeoutMediaProbe struct{}

func (timeoutMediaProbe) Probe(context.Context, string) (stream.Metadata, error) {
	return stream.Metadata{}, mediaexec.ErrTaskTimeout
}

func TestMetadataBusyUsesStableError(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	env.api.Metadata = busyMediaProbe{}
	resp, body := getJSON(t, env.server.URL+"/api/files/"+env.idx.Files[0].ID+"/metadata", nil)
	if resp.StatusCode != http.StatusServiceUnavailable || body["code"] != "MEDIA_BUSY" {
		t.Fatalf("busy metadata = %d/%v", resp.StatusCode, body)
	}
}

func TestMetadataTimeoutUsesStableError(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	env.api.Metadata = timeoutMediaProbe{}
	resp, body := getJSON(t, env.server.URL+"/api/files/"+env.idx.Files[0].ID+"/metadata", nil)
	if resp.StatusCode != http.StatusGatewayTimeout || body["code"] != "MEDIA_TIMEOUT" {
		t.Fatalf("timeout metadata = %d/%v", resp.StatusCode, body)
	}
}

func TestTagsByTagFiles(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	fileID := env.idx.Files[0].ID
	postJSON(t, fmt.Sprintf("%s/api/files/%s/tags", env.server.URL, fileID), `{"tag":"rock"}`, nil)

	resp, body := getJSON(t, env.server.URL+"/api/tags/rock/files", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d, body: %v", resp.StatusCode, body)
	}

	if _, ok := body["items"]; !ok {
		t.Error("expected items in response")
	}
	if _, ok := body["total"]; !ok {
		t.Error("expected total in response")
	}
}

func TestAPIStartTime(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	// Verify that startTime is set
	if env.api.startTime.IsZero() {
		t.Error("expected startTime to be set on API")
	}

	// Uptime should be positive
	since := time.Since(env.api.startTime)
	if since <= 0 {
		t.Error("expected positive uptime")
	}
}

func TestHandleStreamResolvesRelativePath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "shufflemuse-stream-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	musicDir := filepath.Join(tmpDir, "music")
	artistDir := filepath.Join(musicDir, "Artist")
	if err := os.MkdirAll(artistDir, 0o755); err != nil {
		t.Fatalf("create music dir: %v", err)
	}

	trackPath := filepath.Join(artistDir, "track.flac")
	if err := os.WriteFile(trackPath, []byte("fake audio data"), 0o644); err != nil {
		t.Fatalf("create file: %v", err)
	}

	idx, err := index.Scan(musicDir)
	if err != nil {
		t.Fatalf("scan music dir: %v", err)
	}

	if len(idx.Files) == 0 {
		t.Fatal("expected at least one file in index")
	}
	entry := &idx.Files[0]
	if filepath.IsAbs(entry.Filepath) {
		t.Errorf("expected index to store relative path, got: %s", entry.Filepath)
	}
	expectedRelative := filepath.Join("Artist", "track.flac")
	if entry.Filepath != expectedRelative {
		t.Errorf("expected relative path %s, got: %s", expectedRelative, entry.Filepath)
	}

	var capturedPath string
	mockStream := &pathCapturingRouter{captureFunc: func(path string) { capturedPath = path }}

	cfg := &config.Config{
		Port:         8080,
		MusicDir:     musicDir,
		OpusBitrate:  128,
		AuthPassword: "",
	}

	a := auth.New("")
	api := NewAPI(cfg, idx, nil, mockStream, a, nil)

	req := httptest.NewRequest("GET", "/api/stream/"+entry.ID, nil)
	req.SetPathValue("id", entry.ID)
	rr := httptest.NewRecorder()

	api.handleStream(rr, req)

	if capturedPath == "" {
		t.Fatal("ServeStream was not called with any path")
	}
	if !filepath.IsAbs(capturedPath) {
		t.Errorf("expected absolute path in ServeStream, got relative: %s", capturedPath)
	}
	expectedAbs := filepath.Join(musicDir, expectedRelative)
	if capturedPath != expectedAbs {
		t.Errorf("expected absolute path %s, got: %s", expectedAbs, capturedPath)
	}
}

func TestHandleStreamErrorBeforeAndAfterResponseCommit(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	entry := env.idx.Files[0]
	req := httptest.NewRequest(http.MethodGet, "/api/stream/"+entry.ID, nil)
	req.SetPathValue("id", entry.ID)

	env.api.Stream = &failingStreamRouter{}
	before := httptest.NewRecorder()
	env.api.handleStream(before, req)
	if before.Code != http.StatusInternalServerError || !strings.Contains(before.Body.String(), "STREAM_ERROR") {
		t.Fatalf("expected JSON error before commit, got %d %q", before.Code, before.Body.String())
	}

	env.api.Stream = &failingStreamRouter{writeFirst: true}
	after := httptest.NewRecorder()
	env.api.handleStream(after, req)
	if after.Code != http.StatusOK || after.Body.String() != "partial-audio" {
		t.Fatalf("expected committed audio only, got %d %q", after.Code, after.Body.String())
	}

	env.api.Stream = &failingStreamRouter{err: mediaexec.ErrTaskTimeout}
	timedOut := httptest.NewRecorder()
	env.api.handleStream(timedOut, req)
	if timedOut.Code != http.StatusGatewayTimeout || !strings.Contains(timedOut.Body.String(), "MEDIA_TIMEOUT") {
		t.Fatalf("expected timeout JSON before commit, got %d %q", timedOut.Code, timedOut.Body.String())
	}
}

func TestJSONResponsesUseRelativePaths(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	resp, body := getJSON(t, env.server.URL+"/api/files", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	items := body["items"].([]interface{})
	if len(items) > 0 {
		firstItem := items[0].(map[string]interface{})
		itemPath := firstItem["filepath"].(string)
		if itemPath != "" && filepath.IsAbs(itemPath) {
			t.Errorf("/api/files returned absolute path: %s", itemPath)
		}
	}

	resp, body = getJSON(t, env.server.URL+"/api/search?q=track", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	searchItems := body["items"].([]interface{})
	if len(searchItems) > 0 {
		firstItem := searchItems[0].(map[string]interface{})
		itemPath := firstItem["filepath"].(string)
		if itemPath != "" && filepath.IsAbs(itemPath) {
			t.Errorf("/api/search returned absolute path: %s", itemPath)
		}
	}
}

type pathCapturingRouter struct {
	captureFunc func(string)
}

type failingStreamRouter struct {
	writeFirst bool
	err        error
}

func (m *failingStreamRouter) ServeStream(w http.ResponseWriter, _ *http.Request, _ string) error {
	if m.writeFirst {
		_, _ = w.Write([]byte("partial-audio"))
	}
	if m.err != nil {
		return m.err
	}
	return errors.New("stream failed")
}

func (m *pathCapturingRouter) ServeStream(w http.ResponseWriter, req *http.Request, filepath string) error {
	if m.captureFunc != nil {
		m.captureFunc(filepath)
	}
	w.WriteHeader(http.StatusOK)
	return nil
}
