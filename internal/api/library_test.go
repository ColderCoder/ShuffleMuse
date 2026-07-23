package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/auth"
	"github.com/ColderCoder/ShuffleMuse/internal/config"
	"github.com/ColderCoder/ShuffleMuse/internal/index"
)

type fakeLibrary struct {
	mu             sync.Mutex
	status         index.ScanStatus
	idx            *index.Index
	generation     uint64
	nextIdx        *index.Index
	nextGeneration uint64
	viewCalls      int
	snapshotCalls  int
	rescanCalls    int
	rescanErr      error
}

func (f *fakeLibrary) Status() index.ScanStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func (f *fakeLibrary) View() (*index.Index, index.ScanStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.viewCalls++
	idx, status := f.idx, f.status
	if f.nextIdx != nil {
		f.idx = f.nextIdx
		f.generation = f.nextGeneration
		f.status.Generation = f.nextGeneration
		f.nextIdx = nil
	}
	return idx, status
}

func (f *fakeLibrary) Snapshot() (*index.Index, uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshotCalls++
	idx, generation := f.idx, f.generation
	if f.nextIdx != nil {
		f.idx = f.nextIdx
		f.generation = f.nextGeneration
		f.nextIdx = nil
	}
	return idx, generation
}

func (f *fakeLibrary) WithSnapshot(fn func(*index.Index, uint64) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fn(f.idx, f.generation)
}

func (f *fakeLibrary) Rescan() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rescanCalls++
	return f.rescanErr
}

func testIndex(paths ...string) *index.Index {
	idx := &index.Index{Files: make([]index.FileEntry, len(paths)), ByID: make(map[string]*index.FileEntry, len(paths))}
	for i, path := range paths {
		idx.Files[i] = index.FileEntry{ID: index.GenerateID(path), Filepath: path, Name: path, Dir: "."}
		idx.ByID[idx.Files[i].ID] = &idx.Files[i]
	}
	return idx
}

func newLibraryTestAPI(provider LibraryProvider, password string) *API {
	a := NewAPI(
		&config.Config{MusicDir: "/music", AuthPassword: password, AllowedHosts: []string{"example.com"}, OpusBitrate: 192},
		nil,
		nil,
		nil,
		auth.New(password),
		nil,
	)
	a.SetLibraryProvider(provider)
	return a
}

func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", rr.Body.String(), err)
	}
	return body
}

func TestInitializingLibraryKeepsControlEndpointsAvailable(t *testing.T) {
	provider := &fakeLibrary{
		status:    index.ScanStatus{State: index.ScanInitializing},
		rescanErr: index.ErrLibraryInitializing,
	}
	a := newLibraryTestAPI(provider, "")
	handler := a.Routes()

	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if status.Code != http.StatusOK {
		t.Fatalf("status code = %d", status.Code)
	}
	statusBody := decodeResponse(t, status)
	if statusBody["libraryReady"] != false || statusBody["libraryGeneration"] != float64(0) || statusBody["lastScan"] != nil || statusBody["opusBitrate"] != float64(192) {
		t.Fatalf("unexpected initializing status: %v", statusBody)
	}

	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/api/ready", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready code = %d, want 503", ready.Code)
	}

	for _, path := range []string{
		"/api/files",
		"/api/files/missing/metadata",
		"/api/files/missing/cover",
		"/api/covers/directory?dir=.",
		"/api/browse",
		"/api/browse/content?path=notes.txt",
		"/api/tags",
		"/api/search?q=track",
		"/api/stream/missing",
		"/api/graveyard",
	} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
		if rr.Code != http.StatusServiceUnavailable || decodeResponse(t, rr)["code"] != "LIBRARY_SCANNING" {
			t.Fatalf("unexpected readiness response for %s: %d %s", path, rr.Code, rr.Body.String())
		}
	}

	rescan := httptest.NewRecorder()
	handler.ServeHTTP(rescan, httptest.NewRequest(http.MethodPost, "/api/rescan", nil))
	if rescan.Code != http.StatusConflict || decodeResponse(t, rescan)["code"] != "LIBRARY_INITIALIZING" {
		t.Fatalf("unexpected rescan response: %d %s", rescan.Code, rescan.Body.String())
	}
}

func TestUnauthenticatedStatusDoesNotExposeRuntimeConfiguration(t *testing.T) {
	a := newLibraryTestAPI(&fakeLibrary{status: index.ScanStatus{State: index.ScanIdle}}, "secret")
	rr := httptest.NewRecorder()
	a.RoutesWithAuth().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	body := decodeResponse(t, rr)
	if _, exists := body["opusBitrate"]; exists {
		t.Fatalf("unauthenticated status exposed Opus bitrate: %v", body)
	}
}

func TestReadyRemainsHealthyDuringLaterRescan(t *testing.T) {
	now := time.Now()
	provider := &fakeLibrary{
		status: index.ScanStatus{
			State:        index.ScanScanning,
			LibraryReady: true,
			Generation:   7,
			LastScan:     &now,
		},
		idx:        testIndex("old.flac"),
		generation: 7,
	}
	a := newLibraryTestAPI(provider, "")
	handler := a.Routes()

	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/api/ready", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("ready code = %d, want 200", ready.Code)
	}

	files := httptest.NewRecorder()
	handler.ServeHTTP(files, httptest.NewRequest(http.MethodGet, "/api/files", nil))
	body := decodeResponse(t, files)
	if files.Code != http.StatusOK || body["generation"] != float64(7) || body["total"] != float64(1) {
		t.Fatalf("unexpected old-snapshot response: %d %v", files.Code, body)
	}
}

func TestLibraryRequestCapturesSnapshotOnce(t *testing.T) {
	provider := &fakeLibrary{
		status:         index.ScanStatus{State: index.ScanIdle, LibraryReady: true, Generation: 1},
		idx:            testIndex("old.flac"),
		generation:     1,
		nextIdx:        testIndex("new.flac"),
		nextGeneration: 2,
	}
	a := newLibraryTestAPI(provider, "")
	rr := httptest.NewRecorder()
	a.Routes().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/files", nil))
	body := decodeResponse(t, rr)
	items := body["items"].([]interface{})
	if len(items) != 1 || items[0].(map[string]interface{})["filepath"] != "old.flac" || body["generation"] != float64(1) {
		t.Fatalf("request did not retain initial snapshot: %v", body)
	}
	if provider.viewCalls != 1 || provider.snapshotCalls != 0 {
		t.Fatalf("View/Snapshot calls = %d/%d, want 1/0", provider.viewCalls, provider.snapshotCalls)
	}
}

func TestRescanRequiresAuthentication(t *testing.T) {
	provider := &fakeLibrary{
		status:     index.ScanStatus{State: index.ScanIdle, LibraryReady: true, Generation: 1},
		idx:        testIndex("track.flac"),
		generation: 1,
		rescanErr:  errors.New("must not be called without authentication"),
	}
	a := newLibraryTestAPI(provider, "secret")
	rr := httptest.NewRecorder()
	a.RoutesWithAuth().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/rescan", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("rescan code = %d, want 401", rr.Code)
	}
	graveyard := httptest.NewRecorder()
	a.RoutesWithAuth().ServeHTTP(graveyard, httptest.NewRequest(http.MethodGet, "/api/graveyard", nil))
	if graveyard.Code != http.StatusUnauthorized {
		t.Fatalf("graveyard code = %d, want 401", graveyard.Code)
	}
}

func TestManualRescanRequestIsAccepted(t *testing.T) {
	provider := &fakeLibrary{
		status:     index.ScanStatus{State: index.ScanIdle, LibraryReady: true, Generation: 1},
		idx:        testIndex("track.flac"),
		generation: 1,
	}
	a := newLibraryTestAPI(provider, "")
	rr := httptest.NewRecorder()
	a.Routes().ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/rescan", nil))

	if rr.Code != http.StatusAccepted || decodeResponse(t, rr)["status"] != "accepted" {
		t.Fatalf("manual rescan response = %d %s", rr.Code, rr.Body.String())
	}
	provider.mu.Lock()
	calls := provider.rescanCalls
	provider.mu.Unlock()
	if calls != 1 {
		t.Fatalf("Rescan calls = %d, want 1", calls)
	}
}

func TestFilesAndSearchPaginationAreBounded(t *testing.T) {
	paths := make([]string, 1005)
	for i := range paths {
		paths[i] = fmt.Sprintf("track-%04d.flac", i)
	}
	a := NewAPI(
		&config.Config{MusicDir: t.TempDir(), AllowedHosts: []string{"example.com"}},
		testIndex(paths...),
		nil,
		nil,
		auth.New(""),
		nil,
	)
	handler := a.Routes()

	files := httptest.NewRecorder()
	handler.ServeHTTP(files, httptest.NewRequest(http.MethodGet, "/api/files?page=1&limit=1000", nil))
	filesBody := decodeResponse(t, files)
	if got := len(filesBody["items"].([]interface{})); got != maxPageSize {
		t.Fatalf("files page length = %d, want %d", got, maxPageSize)
	}

	search := httptest.NewRecorder()
	handler.ServeHTTP(search, httptest.NewRequest(http.MethodGet, "/api/search?q=track&page=2&limit=2", nil))
	searchBody := decodeResponse(t, search)
	if searchBody["total"] != float64(len(paths)) || searchBody["page"] != float64(2) || len(searchBody["items"].([]interface{})) != 2 {
		t.Fatalf("unexpected search page: %v", searchBody)
	}
	first := searchBody["items"].([]interface{})[0].(map[string]interface{})
	if first["filepath"] != "track-0002.flac" {
		t.Fatalf("search page starts at %v, want track-0002.flac", first["filepath"])
	}
}
