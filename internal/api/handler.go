package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/auth"
	"github.com/ColderCoder/ShuffleMuse/internal/config"
	"github.com/ColderCoder/ShuffleMuse/internal/cover"
	"github.com/ColderCoder/ShuffleMuse/internal/index"
	"github.com/ColderCoder/ShuffleMuse/internal/mediaexec"
	"github.com/ColderCoder/ShuffleMuse/internal/playqueue"
	"github.com/ColderCoder/ShuffleMuse/internal/stream"
	"github.com/ColderCoder/ShuffleMuse/internal/tags"
)

type StreamRouter interface {
	ServeStream(w http.ResponseWriter, req *http.Request, filepath string) error
}

type MediaProbe interface {
	Probe(ctx context.Context, filepath string) (stream.Metadata, error)
}

type CoverProvider interface {
	Describe(ctx context.Context, filepath string) (cover.Descriptor, error)
	DescribeDirectory(ctx context.Context, directory string) (cover.Descriptor, error)
	Render(ctx context.Context, descriptor cover.Descriptor) ([]byte, error)
	OpenFallback(descriptor cover.Descriptor) (*os.File, error)
}

type LibraryProvider interface {
	View() (*index.Index, index.ScanStatus)
	Snapshot() (*index.Index, uint64)
	WithSnapshot(func(*index.Index, uint64) error) error
	Rescan() error
}

type librarySnapshot struct {
	Index      *index.Index
	Generation uint64
}

type librarySnapshotKey struct{}

type API struct {
	Index      *index.Index
	Tags       *tags.Store
	Stream     StreamRouter
	Metadata   MediaProbe
	Covers     CoverProvider
	Config     *config.Config
	Auth       *auth.Auth
	ClientIPs  *auth.ClientIPResolver
	LoginGuard *auth.LoginGuard
	Queues     *playqueue.Manager
	startTime  time.Time
	indexMu    sync.RWMutex
	generation uint64
	library    LibraryProvider
}

func NewAPI(cfg *config.Config, idx *index.Index, tagStore *tags.Store, router StreamRouter, a *auth.Auth, media mediaexec.Executor) *API {
	trusted, _ := cfg.TrustedProxyPrefixes()
	maxFailures := cfg.LoginMaxFailures
	if maxFailures <= 0 {
		maxFailures = 3
	}
	banDuration := cfg.LoginBanDuration
	if banDuration <= 0 {
		banDuration = time.Hour
	}
	metadataCapacity := cfg.MetadataCacheEntries
	if metadataCapacity <= 0 {
		metadataCapacity = 4096
	}
	realIPHeader := cfg.RealIPHeader
	if realIPHeader == "" {
		realIPHeader = "remote"
	}
	apiHandler := &API{
		Index:  idx,
		Tags:   tagStore,
		Stream: router,
		Metadata: stream.NewMetadataProbe(media, stream.MetadataConfig{
			Capacity: metadataCapacity, NegativeTTL: cfg.MediaNegativeCache, TaskTimeout: cfg.MediaTaskTimeout,
		}),
		Covers: cover.NewLoader(media, cover.Config{
			Entries: cfg.CoverCacheEntries, Bytes: cfg.CoverCacheBytes,
			NegativeTTL: cfg.MediaNegativeCache, TaskTimeout: cfg.MediaTaskTimeout,
		}),
		Config:     cfg,
		Auth:       a,
		ClientIPs:  auth.NewClientIPResolver(realIPHeader, trusted),
		LoginGuard: auth.NewLoginGuard(maxFailures, banDuration),
		startTime:  time.Now(),
	}
	var queueTags playqueue.TagSource
	if tagStore != nil {
		queueTags = tagStore
	}
	apiHandler.Queues = playqueue.NewManager(playqueue.Config{
		MaxQueues: cfg.QueueCacheMaxQueues,
		MaxBytes:  cfg.QueueCacheBytes,
		Idle:      cfg.QueueIdle,
	}, queueTags)
	if idx != nil {
		apiHandler.generation = 1
	}
	return apiHandler
}

func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/status", a.handleStatus)
	mux.HandleFunc("GET /api/ready", a.handleReady)
	mux.HandleFunc("POST /api/rescan", a.handleRescan)
	mux.HandleFunc("GET /api/files", a.withLibraryReady(a.handleFiles))
	mux.HandleFunc("POST /api/queues", a.withLibraryReady(a.handleCreateQueue))
	mux.HandleFunc("GET /api/queues/{id}/items", a.withLibraryReady(a.handleQueueItems))
	mux.HandleFunc("POST /api/queues/{id}/select", a.withLibraryReady(a.handleQueueSelect))
	mux.HandleFunc("DELETE /api/queues/{id}", a.handleDeleteQueue)
	mux.HandleFunc("GET /api/files/{id}/metadata", a.withLibraryReady(a.handleFileMetadata))
	mux.HandleFunc("GET /api/files/{id}/cover", a.withLibraryReady(a.handleFileCover))
	mux.HandleFunc("GET /api/covers/directory", a.withLibraryReady(a.handleDirectoryCover))
	mux.HandleFunc("GET /api/files/{id}/tags", a.withLibraryReady(a.handleFileTags))
	mux.HandleFunc("GET /api/browse", a.withLibraryReady(a.handleBrowse))
	mux.HandleFunc("GET /api/browse/content", a.withLibraryReady(a.handleBrowseContent))
	mux.HandleFunc("GET /api/browse/download", a.withLibraryReady(a.handleBrowseDownload))
	mux.HandleFunc("POST /api/files/{id}/tags", a.withLibraryReady(a.handleAddTag))
	mux.HandleFunc("DELETE /api/files/{id}/tags/{tag}", a.withLibraryReady(a.handleRemoveTag))
	mux.HandleFunc("GET /api/tags", a.withLibraryReady(a.handleGetAllTags))
	mux.HandleFunc("GET /api/tags/export", a.withLibraryReady(a.handleExportTags))
	mux.HandleFunc("GET /api/tags/{tag}/files", a.withLibraryReady(a.handleTagFiles))
	mux.HandleFunc("GET /api/graveyard", a.withLibraryReady(a.handleGraveyard))
	mux.HandleFunc("DELETE /api/graveyard", a.withLibraryReady(a.handleDeleteGraveyard))
	mux.HandleFunc("GET /api/search", a.withLibraryReady(a.handleSearch))
	mux.HandleFunc("GET /api/stream/{id}", a.withLibraryReady(a.handleStream))

	return loggingMiddleware(SecurityMiddleware(a.Config, mux))
}

func (a *API) RoutesWithAuth() http.Handler {
	mux := http.NewServeMux()

	if a.Config.AuthPassword == "" {
		return a.Routes()
	}

	mux.HandleFunc("POST /api/auth/login", a.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", a.handleLogout)
	mux.HandleFunc("GET /api/status", a.handleStatus)
	mux.HandleFunc("GET /api/ready", a.handleReady)
	mux.HandleFunc("POST /api/rescan", a.withAuth(a.handleRescan))

	mux.HandleFunc("GET /api/files", a.withAuth(a.withLibraryReady(a.handleFiles)))
	mux.HandleFunc("POST /api/queues", a.withAuth(a.withLibraryReady(a.handleCreateQueue)))
	mux.HandleFunc("GET /api/queues/{id}/items", a.withAuth(a.withLibraryReady(a.handleQueueItems)))
	mux.HandleFunc("POST /api/queues/{id}/select", a.withAuth(a.withLibraryReady(a.handleQueueSelect)))
	mux.HandleFunc("DELETE /api/queues/{id}", a.withAuth(a.handleDeleteQueue))
	mux.HandleFunc("GET /api/files/{id}/metadata", a.withAuth(a.withLibraryReady(a.handleFileMetadata)))
	mux.HandleFunc("GET /api/files/{id}/cover", a.withAuth(a.withLibraryReady(a.handleFileCover)))
	mux.HandleFunc("GET /api/covers/directory", a.withAuth(a.withLibraryReady(a.handleDirectoryCover)))
	mux.HandleFunc("GET /api/files/{id}/tags", a.withAuth(a.withLibraryReady(a.handleFileTags)))
	mux.HandleFunc("GET /api/browse", a.withAuth(a.withLibraryReady(a.handleBrowse)))
	mux.HandleFunc("GET /api/browse/content", a.withAuth(a.withLibraryReady(a.handleBrowseContent)))
	mux.HandleFunc("GET /api/browse/download", a.withAuth(a.withLibraryReady(a.handleBrowseDownload)))
	mux.HandleFunc("POST /api/files/{id}/tags", a.withAuth(a.withLibraryReady(a.handleAddTag)))
	mux.HandleFunc("DELETE /api/files/{id}/tags/{tag}", a.withAuth(a.withLibraryReady(a.handleRemoveTag)))
	mux.HandleFunc("GET /api/tags", a.withAuth(a.withLibraryReady(a.handleGetAllTags)))
	mux.HandleFunc("GET /api/tags/export", a.withAuth(a.withLibraryReady(a.handleExportTags)))
	mux.HandleFunc("GET /api/tags/{tag}/files", a.withAuth(a.withLibraryReady(a.handleTagFiles)))
	mux.HandleFunc("GET /api/graveyard", a.withAuth(a.withLibraryReady(a.handleGraveyard)))
	mux.HandleFunc("DELETE /api/graveyard", a.withAuth(a.withLibraryReady(a.handleDeleteGraveyard)))
	mux.HandleFunc("GET /api/search", a.withAuth(a.withLibraryReady(a.handleSearch)))
	mux.HandleFunc("GET /api/stream/{id}", a.withAuth(a.withLibraryReady(a.handleStream)))

	return loggingMiddleware(SecurityMiddleware(a.Config, mux))
}

func (a *API) withAuth(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.Auth.Allows(r) {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
			return
		}
		handler(w, r)
	}
}

func (a *API) withLibraryReady(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idx, status := a.libraryView()
		if !status.LibraryReady || idx == nil {
			writeError(w, http.StatusServiceUnavailable, "LIBRARY_SCANNING", "music library is not ready")
			return
		}
		snapshot := librarySnapshot{Index: idx, Generation: status.Generation}
		handler(w, r.WithContext(context.WithValue(r.Context(), librarySnapshotKey{}, snapshot)))
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code string, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

type commitTrackingWriter struct {
	http.ResponseWriter
	committed bool
}

func (w *commitTrackingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *commitTrackingWriter) Flush() {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *commitTrackingWriter) WriteHeader(status int) {
	if w.committed {
		return
	}
	w.committed = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *commitTrackingWriter) Write(p []byte) (int, error) {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	idx, scanStatus := a.libraryView()
	uptime := time.Since(a.startTime).Round(time.Second).String()
	authRequired := a.Config.AuthPassword != ""
	if authRequired && a.Auth.IsWhitelisted(r) {
		authRequired = false
	}
	authenticated := !authRequired
	if authRequired && a.Auth.Validate(r) == nil {
		authenticated = true
	}
	if authRequired && !authenticated {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"authRequired":  true,
			"authenticated": false,
		})
		return
	}

	fileCount := 0
	if idx != nil {
		fileCount = len(idx.Files)
	}
	var lastScan interface{}
	if scanStatus.LastScan != nil {
		lastScan = scanStatus.LastScan.Format(time.RFC3339)
	}
	var scanError interface{}
	if scanStatus.ScanError != "" {
		scanError = scanStatus.ScanError
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"fileCount":         fileCount,
		"libraryReady":      scanStatus.LibraryReady,
		"libraryGeneration": scanStatus.Generation,
		"scanStatus":        scanStatus.State,
		"uptime":            uptime,
		"lastScan":          lastScan,
		"scanError":         scanError,
		"opusBitrate":       a.Config.OpusBitrate,
		"authRequired":      authRequired,
		"authenticated":     authenticated,
	})
}

func (a *API) handleReady(w http.ResponseWriter, _ *http.Request) {
	idx, status := a.libraryView()
	if !status.LibraryReady || idx == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"ready":      false,
			"generation": status.Generation,
			"code":       "LIBRARY_SCANNING",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ready":      true,
		"generation": status.Generation,
	})
}

func (a *API) handleRescan(w http.ResponseWriter, _ *http.Request) {
	if a.library == nil {
		writeError(w, http.StatusConflict, "RESCAN_UNAVAILABLE", "library rescan is unavailable")
		return
	}
	if err := a.library.Rescan(); err != nil {
		switch {
		case errors.Is(err, index.ErrLibraryInitializing):
			writeError(w, http.StatusConflict, "LIBRARY_INITIALIZING", err.Error())
		case errors.Is(err, index.ErrScanInProgress):
			writeError(w, http.StatusConflict, "SCAN_IN_PROGRESS", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "RESCAN_ERROR", err.Error())
		}
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (a *API) handleFiles(w http.ResponseWriter, r *http.Request) {
	idx, generation := a.currentSnapshot(r)
	page, limit, ok := queryPage(w, r, 50)
	if !ok {
		return
	}

	total := len(idx.Files)
	start, end := pageBounds(total, page, limit)
	items := make([]map[string]string, end-start)
	for i := start; i < end; i++ {
		e := &idx.Files[i]
		items[i-start] = map[string]string{
			"id":       e.ID,
			"filepath": e.Filepath,
			"name":     e.Name,
			"dir":      e.Dir,
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":      items,
		"total":      total,
		"page":       page,
		"generation": generation,
	})
}

func (a *API) handleFileMetadata(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	idx, _ := a.currentSnapshot(r)
	entry, ok := idx.ByID[id]
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "file not found")
		return
	}

	absPath, err := index.ResolveWithinRoot(a.Config.MusicDir, entry.Filepath)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "file is outside the music library")
		return
	}
	metadata, err := a.Metadata.Probe(r.Context(), absPath)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if mediaexec.IsBusy(err) {
			writeError(w, http.StatusServiceUnavailable, "MEDIA_BUSY", "media processing is busy")
			return
		}
		if mediaexec.IsTimeout(err) {
			writeError(w, http.StatusGatewayTimeout, "MEDIA_TIMEOUT", "media processing timed out")
			return
		}
		writeError(w, http.StatusUnprocessableEntity, "METADATA_ERROR", "audio metadata is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, metadata)
}

func (a *API) handleFileTags(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	idx, _ := a.currentSnapshot(r)
	entry, ok := idx.ByID[id]
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "file not found")
		return
	}

	fileTags, err := a.Tags.GetTags(entry.Filepath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TAG_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tags": fileTags,
	})
}

func (a *API) handleAddTag(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	idx, _ := a.currentSnapshot(r)
	entry, ok := idx.ByID[id]
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "file not found")
		return
	}

	var body struct {
		Tag string `json:"tag"`
	}
	if !decodeStrictJSON(w, r, &body) {
		return
	}

	if body.Tag == "" {
		writeError(w, http.StatusBadRequest, "MISSING_TAG", "tag is required")
		return
	}

	if err := a.Tags.AddTag(entry.Filepath, body.Tag); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			writeError(w, http.StatusConflict, "DUPLICATE_TAG", err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, "INVALID_TAG", err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"tag": body.Tag})
}

func (a *API) handleRemoveTag(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tag := r.PathValue("tag")

	idx, _ := a.currentSnapshot(r)
	entry, ok := idx.ByID[id]
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "file not found")
		return
	}

	fileTags, err := a.Tags.GetTags(entry.Filepath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TAG_ERROR", err.Error())
		return
	}

	found := false
	for _, t := range fileTags {
		if t == tag {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "tag not found on file")
		return
	}

	if err := a.Tags.RemoveTag(entry.Filepath, tag); err != nil {
		writeError(w, http.StatusInternalServerError, "TAG_ERROR", err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) handleGetAllTags(w http.ResponseWriter, r *http.Request) {
	idx, _ := a.currentSnapshot(r)
	allTags, err := a.Tags.GetOnlineTags(onlinePaths(idx))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TAG_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"tags": allTags,
	})
}

func (a *API) handleTagFiles(w http.ResponseWriter, r *http.Request) {
	tag := r.PathValue("tag")

	page, limit, ok := queryPage(w, r, 50)
	if !ok {
		return
	}

	idx, generation := a.currentSnapshot(r)
	start := saturatedPageStart(page, limit)
	total := 0
	items := make([]map[string]string, 0, limit)
	visited := 0
	err := a.Tags.ForEachFileByTag(tag, func(fp string) error {
		visited++
		if visited&1023 == 0 {
			if err := r.Context().Err(); err != nil {
				return err
			}
		}
		if entry, ok := idx.ByID[index.GenerateID(fp)]; ok && entry.Filepath == fp {
			if total >= start && len(items) < limit {
				items = append(items, map[string]string{
					"id":       entry.ID,
					"filepath": entry.Filepath,
					"name":     entry.Name,
					"dir":      entry.Dir,
				})
			}
			total++
		}
		return nil
	})
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TAG_ERROR", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":      items,
		"total":      total,
		"page":       page,
		"generation": generation,
	})
}

func (a *API) handleSearch(w http.ResponseWriter, r *http.Request) {
	idx, generation := a.currentSnapshot(r)
	query := r.URL.Query().Get("q")
	if query == "" {
		writeError(w, http.StatusBadRequest, "MISSING_QUERY", "search query is required")
		return
	}
	if !queryWithinBytes(w, query, "q", 200) {
		return
	}

	page, limit, ok := queryPage(w, r, 50)
	if !ok {
		return
	}
	lower := strings.ToLower(query)
	start, _ := pageBounds(len(idx.Files), page, limit)
	results := make([]map[string]string, 0, limit)
	total := 0
	for i := range idx.Files {
		if i&1023 == 0 {
			if err := r.Context().Err(); err != nil {
				return
			}
		}
		if strings.Contains(strings.ToLower(idx.Files[i].Name), lower) {
			if total >= start && len(results) < limit {
				results = append(results, map[string]string{
					"id":       idx.Files[i].ID,
					"filepath": idx.Files[i].Filepath,
					"name":     idx.Files[i].Name,
					"dir":      idx.Files[i].Dir,
				})
			}
			total++
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"items":      results,
		"total":      total,
		"page":       page,
		"query":      query,
		"generation": generation,
	})
}

func (a *API) handleStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	idx, _ := a.currentSnapshot(r)
	entry, ok := idx.ByID[id]
	if !ok {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "file not found")
		return
	}

	absPath, err := index.ResolveWithinRoot(a.Config.MusicDir, entry.Filepath)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "file is outside the music library")
		return
	}
	tracked := &commitTrackingWriter{ResponseWriter: w}
	if err := a.Stream.ServeStream(tracked, r, absPath); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		log.Printf("stream error for %s: %v", entry.Filepath, err)
		if !tracked.committed {
			if mediaexec.IsBusy(err) {
				writeError(w, http.StatusServiceUnavailable, "MEDIA_BUSY", "media processing is busy")
				return
			}
			if mediaexec.IsTimeout(err) {
				writeError(w, http.StatusGatewayTimeout, "MEDIA_TIMEOUT", "media processing timed out")
				return
			}
			if errors.Is(err, stream.ErrInvalidStreamOptions) {
				writeError(w, http.StatusBadRequest, "INVALID_STREAM_OPTIONS", err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "STREAM_ERROR", fmt.Sprintf("failed to stream file: %v", err))
		}
	}
}

func (a *API) handleLogin(w http.ResponseWriter, r *http.Request) {
	client := "unknown"
	if address, ok := a.ClientIPs.ClientIP(r); ok {
		client = address.String()
	}
	if retry, blocked := a.LoginGuard.Check(client); blocked {
		a.writeLoginBlocked(w, retry)
		return
	}
	var body struct {
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	if !decodeStrictJSON(w, r, &body) {
		return
	}

	cookie, err := a.Auth.Login(body.Password, body.Remember)
	if err != nil {
		if retry, blocked := a.LoginGuard.Failure(client); blocked {
			a.writeLoginBlocked(w, retry)
			return
		}
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	a.LoginGuard.Success(client)

	http.SetCookie(w, cookie)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged in"})
}

func (a *API) writeLoginBlocked(w http.ResponseWriter, retry time.Duration) {
	seconds := int64((retry + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", fmt.Sprintf("%d", seconds))
	writeError(w, http.StatusTooManyRequests, "LOGIN_IP_BLOCKED", "too many failed login attempts")
}

func (a *API) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie := a.Auth.Logout(r)
	http.SetCookie(w, cookie)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) UpdateIndex(idx *index.Index) {
	a.indexMu.Lock()
	if !sameIndexPaths(a.Index, idx) {
		a.generation++
	}
	a.Index = idx
	a.indexMu.Unlock()
}

func (a *API) SetLibraryProvider(provider LibraryProvider) {
	a.library = provider
}

func (a *API) libraryView() (*index.Index, index.ScanStatus) {
	if a.library != nil {
		return a.library.View()
	}
	a.indexMu.RLock()
	defer a.indexMu.RUnlock()
	if a.Index == nil {
		return nil, index.ScanStatus{State: index.ScanInitializing}
	}
	lastScan := a.startTime
	return a.Index, index.ScanStatus{
		State:        index.ScanIdle,
		LibraryReady: true,
		Generation:   a.generation,
		LastScan:     &lastScan,
	}
}

func (a *API) snapshot() (*index.Index, uint64) {
	if a.library != nil {
		return a.library.Snapshot()
	}
	a.indexMu.RLock()
	defer a.indexMu.RUnlock()
	return a.Index, a.generation
}

func (a *API) currentSnapshot(r *http.Request) (*index.Index, uint64) {
	if snapshot, ok := r.Context().Value(librarySnapshotKey{}).(librarySnapshot); ok {
		return snapshot.Index, snapshot.Generation
	}
	return a.snapshot()
}

func (a *API) withSnapshot(fn func(*index.Index, uint64) error) error {
	if a.library != nil {
		return a.library.WithSnapshot(fn)
	}
	a.indexMu.RLock()
	defer a.indexMu.RUnlock()
	return fn(a.Index, a.generation)
}

func onlinePaths(idx *index.Index) map[string]bool {
	paths := make(map[string]bool, len(idx.Files))
	for i := range idx.Files {
		paths[idx.Files[i].Filepath] = true
	}
	return paths
}

func sameIndexPaths(a, b *index.Index) bool {
	if a == nil || b == nil {
		return a == b
	}
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
