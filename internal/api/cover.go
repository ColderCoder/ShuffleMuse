package api

import (
	"bytes"
	"context"
	"errors"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/cover"
	"github.com/ColderCoder/ShuffleMuse/internal/index"
	"github.com/ColderCoder/ShuffleMuse/internal/mediaexec"
)

func (a *API) handleFileCover(w http.ResponseWriter, r *http.Request) {
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
	descriptor, err := a.Covers.Describe(r.Context(), absPath)
	if !writeCoverResult(w, err) {
		return
	}
	a.serveCover(w, r, descriptor)
}

func (a *API) handleDirectoryCover(w http.ResponseWriter, r *http.Request) {
	values, exists := r.URL.Query()["dir"]
	if !exists || len(values) != 1 {
		writeError(w, http.StatusBadRequest, "INVALID_DIR", "dir must be supplied exactly once")
		return
	}
	if !queryWithinBytes(w, values[0], "dir", 4096) {
		return
	}
	directory, err := resolveCoverDirectory(a.Config.MusicDir, values[0])
	if err != nil {
		if errors.Is(err, errInvalidCoverDirectory) {
			writeError(w, http.StatusBadRequest, "INVALID_DIR", "dir must be a safe relative directory")
			return
		}
		writeError(w, http.StatusNotFound, "COVER_NOT_FOUND", "cover art is unavailable")
		return
	}
	descriptor, err := a.Covers.DescribeDirectory(r.Context(), directory)
	if !writeCoverResult(w, err) {
		return
	}
	a.serveCover(w, r, descriptor)
}

func (a *API) serveCover(w http.ResponseWriter, r *http.Request, descriptor cover.Descriptor) {
	if coverNotModified(r, descriptor) {
		setCoverHeaders(w, descriptor, "")
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method == http.MethodHead {
		setCoverHeaders(w, descriptor, descriptor.ContentLength())
		w.WriteHeader(http.StatusOK)
		return
	}

	if descriptor.Kind == cover.Fallback && !descriptor.RequiresRender {
		file, err := a.Covers.OpenFallback(descriptor)
		if !writeCoverResult(w, err) {
			return
		}
		defer file.Close()
		setCoverHeaders(w, descriptor, descriptor.ContentLength())
		http.ServeContent(w, r, descriptor.Name, descriptor.ModTime, file)
		return
	}
	data, err := a.Covers.Render(r.Context(), descriptor)
	if !writeCoverResult(w, err) {
		return
	}
	setCoverHeaders(w, descriptor, strconv.Itoa(len(data)))
	http.ServeContent(w, r, descriptor.Name, descriptor.ModTime, bytes.NewReader(data))
}

func setCoverHeaders(w http.ResponseWriter, descriptor cover.Descriptor, contentLength string) {
	w.Header().Set("Content-Type", descriptor.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": descriptor.Name}))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Cover-Source", descriptor.Source)
	w.Header().Set("ETag", descriptor.ETag)
	if !descriptor.ModTime.IsZero() {
		w.Header().Set("Last-Modified", descriptor.ModTime.UTC().Format(http.TimeFormat))
	}
	if contentLength == "" {
		w.Header().Del("Content-Length")
	} else {
		w.Header().Set("Content-Length", contentLength)
	}
}

var errInvalidCoverDirectory = errors.New("invalid cover directory")

// resolveCoverDirectory accepts only a clean slash-separated relative path.
// Every component below the canonical music root must be a real directory;
// directory symlinks are rejected even when their target remains in the root.
func resolveCoverDirectory(musicRoot, relative string) (string, error) {
	if relative == "" || strings.ContainsRune(relative, '\x00') || strings.Contains(relative, "\\") || path.IsAbs(relative) {
		return "", errInvalidCoverDirectory
	}
	clean := path.Clean(relative)
	if clean != relative || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errInvalidCoverDirectory
	}
	root, err := filepath.Abs(musicRoot)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	current := root
	if clean == "." {
		return current, nil
	}
	for _, component := range strings.Split(clean, "/") {
		if component == "" || component == "." || component == ".." {
			return "", errInvalidCoverDirectory
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", errInvalidCoverDirectory
		}
	}
	return current, nil
}

func writeCoverResult(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	w.Header().Del("Content-Length")
	if mediaexec.IsBusy(err) {
		writeError(w, http.StatusServiceUnavailable, "MEDIA_BUSY", "media processing is busy")
		return false
	}
	if mediaexec.IsTimeout(err) {
		writeError(w, http.StatusGatewayTimeout, "MEDIA_TIMEOUT", "media processing timed out")
		return false
	}
	if errors.Is(err, cover.ErrNotFound) || errors.Is(err, cover.ErrStale) {
		writeError(w, http.StatusNotFound, "COVER_NOT_FOUND", "cover art is unavailable")
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	writeError(w, http.StatusInternalServerError, "COVER_ERROR", "failed to load cover art")
	return false
}

func coverNotModified(r *http.Request, descriptor cover.Descriptor) bool {
	if values := r.Header.Values("If-None-Match"); len(values) > 0 {
		for _, value := range values {
			for _, candidate := range strings.Split(value, ",") {
				candidate = strings.TrimSpace(candidate)
				if candidate == "*" || strings.TrimPrefix(candidate, "W/") == descriptor.ETag {
					return true
				}
			}
		}
		return false
	}
	if modifiedSince := r.Header.Get("If-Modified-Since"); modifiedSince != "" {
		if parsed, err := http.ParseTime(modifiedSince); err == nil {
			return !descriptor.ModTime.After(parsed.Add(time.Second))
		}
	}
	return false
}
