package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/auth"
	"github.com/ColderCoder/ShuffleMuse/internal/config"
	"github.com/ColderCoder/ShuffleMuse/internal/index"
	"github.com/ColderCoder/ShuffleMuse/internal/stream"
	"github.com/ColderCoder/ShuffleMuse/internal/tags"
)

type authTestEnv struct {
	api      *API
	server   *httptest.Server
	tmpDir   string
	tagStore *tags.Store
	idx      *index.Index
	password string
	cleanup  func()
}

func setupAuthTestEnv(t *testing.T) *authTestEnv {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "shufflemuse-auth-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}

	// Create music dir with fake audio files
	musicDir := filepath.Join(tmpDir, "music")
	artistDir := filepath.Join(musicDir, "artist1")
	if err := os.MkdirAll(artistDir, 0o755); err != nil {
		t.Fatalf("create music dir: %v", err)
	}

	files := []string{
		filepath.Join(artistDir, "track1.flac"),
		filepath.Join(artistDir, "track2.flac"),
	}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("fake audio data"), 0o644); err != nil {
			t.Fatalf("create file %s: %v", f, err)
		}
	}

	// Scan music dir
	idx, err := index.Scan(musicDir)
	if err != nil {
		t.Fatalf("scan music dir: %v", err)
	}

	// Open BoltDB
	boltPath := filepath.Join(tmpDir, "tags.db")
	tagStore, err := tags.Open(boltPath)
	if err != nil {
		t.Fatalf("open tag store: %v", err)
	}

	password := "testpass123"

	cfg := &config.Config{
		Port:         8080,
		MusicDir:     musicDir,
		OpusBitrate:  128,
		AuthPassword: password,
		BoltDBPath:   boltPath,
	}

	router := &stream.Router{Bitrate: 128}
	a := auth.New(password)

	api := NewAPI(cfg, idx, tagStore, router, a, nil)

	handler := api.RoutesWithAuth()
	server := httptest.NewServer(handler)

	return &authTestEnv{
		api:      api,
		server:   server,
		tmpDir:   tmpDir,
		tagStore: tagStore,
		idx:      idx,
		password: password,
		cleanup: func() {
			server.Close()
			tagStore.Close()
			os.RemoveAll(tmpDir)
		},
	}
}

func (e *authTestEnv) teardown() {
	e.cleanup()
}

func (e *authTestEnv) login(t *testing.T, password string, remember bool) *http.Response {
	t.Helper()
	body := strings.NewReader(`{"password":"` + password + `","remember":` + boolStr(remember) + `}`)
	req, err := http.NewRequest("POST", e.server.URL+"/api/auth/login", body)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func getBody(resp *http.Response) string {
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

// Tests

func TestUnauthenticatedRequest(t *testing.T) {
	env := setupAuthTestEnv(t)
	defer env.teardown()

	resp, err := http.Get(env.server.URL + "/api/files")
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}

	var errBody map[string]string
	json.NewDecoder(resp.Body).Decode(&errBody)
	if errBody["error"] != "Authentication required" {
		t.Errorf("expected 'Authentication required', got %q", errBody["error"])
	}
	if errBody["code"] != "UNAUTHORIZED" {
		t.Errorf("expected 'UNAUTHORIZED', got %q", errBody["code"])
	}
}

func TestWhitelistedSubnetBypassesLogin(t *testing.T) {
	env := setupAuthTestEnv(t)
	defer env.teardown()
	env.api.Auth = auth.New(env.password, netip.MustParsePrefix("127.0.0.0/8"))

	resp, err := http.Get(env.server.URL + "/api/files")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("whitelisted files status = %d, want 200", resp.StatusCode)
	}

	statusResp, err := http.Get(env.server.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer statusResp.Body.Close()
	var status map[string]interface{}
	if err := json.NewDecoder(statusResp.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status["authRequired"] != false || status["authenticated"] != true {
		t.Fatalf("unexpected whitelisted auth status: %v", status)
	}
}

func TestLoginSuccess(t *testing.T) {
	env := setupAuthTestEnv(t)
	defer env.teardown()

	resp := env.login(t, env.password, false)
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d, body: %s", resp.StatusCode, getBody(resp))
	}

	cookies := resp.Cookies()
	if len(cookies) == 0 {
		t.Error("expected Set-Cookie header")
	}
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "shufflemuse-session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Error("expected shufflemuse-session cookie")
	}
}

func TestLoginFailure(t *testing.T) {
	env := setupAuthTestEnv(t)
	defer env.teardown()

	resp := env.login(t, "wrongpassword", false)
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}

	var errBody map[string]string
	json.NewDecoder(resp.Body).Decode(&errBody)
	if errBody["error"] != "Authentication required" {
		t.Errorf("expected 'Authentication required', got %q", errBody["error"])
	}
}

func TestLoginIPBanRetryAfterExpiryAndSuccessReset(t *testing.T) {
	env := setupAuthTestEnv(t)
	defer env.teardown()
	env.api.LoginGuard = auth.NewLoginGuard(3, 50*time.Millisecond)

	for attempt := 1; attempt <= 3; attempt++ {
		resp := env.login(t, "wrong", false)
		want := http.StatusUnauthorized
		if attempt == 3 {
			want = http.StatusTooManyRequests
		}
		if resp.StatusCode != want {
			t.Fatalf("attempt %d status = %d", attempt, resp.StatusCode)
		}
		if attempt == 3 {
			if resp.Header.Get("Retry-After") == "" {
				t.Fatal("blocked response missing Retry-After")
			}
			var body map[string]string
			_ = json.NewDecoder(resp.Body).Decode(&body)
			if body["code"] != "LOGIN_IP_BLOCKED" {
				t.Fatalf("blocked code = %q", body["code"])
			}
		}
		resp.Body.Close()
	}

	blocked := env.login(t, env.password, false)
	blocked.Body.Close()
	if blocked.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("correct password during ban status = %d", blocked.StatusCode)
	}
	time.Sleep(60 * time.Millisecond)
	success := env.login(t, env.password, false)
	success.Body.Close()
	if success.StatusCode != http.StatusOK {
		t.Fatalf("login after ban expiry status = %d", success.StatusCode)
	}

	for i := 0; i < 2; i++ {
		failure := env.login(t, "wrong", false)
		failure.Body.Close()
	}
	reset := env.login(t, env.password, false)
	reset.Body.Close()
	failure := env.login(t, "wrong", false)
	failure.Body.Close()
	if failure.StatusCode != http.StatusUnauthorized {
		t.Fatalf("successful login did not reset counter: %d", failure.StatusCode)
	}
}

func TestMalformedAndOversizedLoginBodiesDoNotCountAsFailures(t *testing.T) {
	env := setupAuthTestEnv(t)
	defer env.teardown()
	env.api.LoginGuard = auth.NewLoginGuard(2, time.Hour)

	for _, body := range []string{`not-json`, `{"password":"x","unknown":true}`, `{"password":"` + strings.Repeat("x", maxJSONBodyBytes) + `"}`} {
		response, _ := postJSON(t, env.server.URL+"/api/auth/login", body, nil)
		if response.StatusCode != http.StatusBadRequest && response.StatusCode != http.StatusRequestEntityTooLarge {
			t.Fatalf("malformed login status = %d", response.StatusCode)
		}
	}
	first := env.login(t, "wrong", false)
	first.Body.Close()
	if first.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid bodies counted as failures: status = %d", first.StatusCode)
	}
}

func TestAuthenticatedAccess(t *testing.T) {
	env := setupAuthTestEnv(t)
	defer env.teardown()

	// First login
	loginResp := env.login(t, env.password, false)
	loginResp.Body.Close()

	req, err := http.NewRequest("GET", env.server.URL+"/api/files", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	for _, c := range loginResp.Cookies() {
		req.AddCookie(c)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d, body: %s", resp.StatusCode, getBody(resp))
	}
}

func TestLogoutClearsCookie(t *testing.T) {
	env := setupAuthTestEnv(t)
	defer env.teardown()

	// First login
	loginResp := env.login(t, env.password, false)
	loginResp.Body.Close()

	var sessionCookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == "shufflemuse-session" {
			sessionCookie = c
			break
		}
	}

	req, err := http.NewRequest("POST", env.server.URL+"/api/auth/logout", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.AddCookie(sessionCookie)

	logoutResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer logoutResp.Body.Close()

	if logoutResp.StatusCode != 204 {
		t.Errorf("expected 204 on logout, got %d", logoutResp.StatusCode)
	}

	var clearedCookie *http.Cookie
	for _, c := range logoutResp.Cookies() {
		if c.Name == "shufflemuse-session" {
			clearedCookie = c
			break
		}
	}
	if clearedCookie == nil {
		t.Error("expected cookie to be cleared on logout")
	} else if clearedCookie.MaxAge != -1 {
		t.Errorf("expected MaxAge=-1 on cleared cookie, got %d", clearedCookie.MaxAge)
	}

	req2, err := http.NewRequest("GET", env.server.URL+"/api/files", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req2.AddCookie(sessionCookie)

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != 401 {
		t.Errorf("expected 401 after logout, got %d", resp2.StatusCode)
	}
}

func TestPublicEndpointsAccessible(t *testing.T) {
	env := setupAuthTestEnv(t)
	defer env.teardown()

	resp, err := http.Get(env.server.URL + "/api/status")
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200 for /api/status, got %d", resp.StatusCode)
	}
}

func TestStreamEndpointRequiresAuth(t *testing.T) {
	env := setupAuthTestEnv(t)
	defer env.teardown()

	if len(env.idx.Files) == 0 {
		t.Fatal("no files in index")
	}
	fileID := env.idx.Files[0].ID

	resp, err := http.Get(env.server.URL + "/api/stream/" + fileID)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("stream endpoint should require auth, got %d", resp.StatusCode)
	}
}

func TestNoPasswordMode(t *testing.T) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "shufflemuse-nopass-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create music dir with fake audio files
	musicDir := filepath.Join(tmpDir, "music")
	artistDir := filepath.Join(musicDir, "artist1")
	if err := os.MkdirAll(artistDir, 0o755); err != nil {
		t.Fatalf("create music dir: %v", err)
	}

	files := []string{
		filepath.Join(artistDir, "track1.flac"),
	}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("fake audio data"), 0o644); err != nil {
			t.Fatalf("create file %s: %v", f, err)
		}
	}

	// Scan music dir
	idx, err := index.Scan(musicDir)
	if err != nil {
		t.Fatalf("scan music dir: %v", err)
	}

	// Open BoltDB
	boltPath := filepath.Join(tmpDir, "tags.db")
	tagStore, err := tags.Open(boltPath)
	if err != nil {
		t.Fatalf("open tag store: %v", err)
	}
	defer tagStore.Close()

	cfg := &config.Config{
		Port:         8080,
		MusicDir:     musicDir,
		OpusBitrate:  128,
		AuthPassword: "",
		BoltDBPath:   boltPath,
	}

	router := &stream.Router{Bitrate: 128}
	a := auth.New("") // Auth with empty password

	api := NewAPI(cfg, idx, tagStore, router, a, nil)
	handler := api.RoutesWithAuth()
	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/files")
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("expected 200 for /api/files in no-password mode, got %d", resp.StatusCode)
	}

	resp2, err := http.Get(server.URL + "/api/status")
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Errorf("expected 200 for /api/status in no-password mode, got %d", resp2.StatusCode)
	}
}

func TestLoginEndpointNotProtected(t *testing.T) {
	env := setupAuthTestEnv(t)
	defer env.teardown()

	resp, err := http.Post(env.server.URL+"/api/auth/login", "application/json", strings.NewReader(`{"password":"test"}`))
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 401 {
		t.Errorf("expected 401 for wrong password, got %d", resp.StatusCode)
	}
}

func TestStatusEndpointNotProtected(t *testing.T) {
	env := setupAuthTestEnv(t)
	defer env.teardown()

	req, err := http.NewRequest("GET", env.server.URL+"/api/status", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	var sessionCookie *http.Cookie
	sessionCookie = &http.Cookie{Name: "shufflemuse-session", Value: "some-value"}
	req.AddCookie(sessionCookie)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200 for /api/status even with cookie, got %d", resp.StatusCode)
	}
}
