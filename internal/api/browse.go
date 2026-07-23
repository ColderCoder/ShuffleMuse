package api

import (
	"container/heap"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/index"
)

const (
	defaultBrowsePageSize  = 100
	maxBrowseRetainedItems = 50_000
	maxTextPreviewSize     = 2 << 20
)

type browseDirectory struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type browseFile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	Dir         string `json:"dir"`
	Kind        string `json:"kind"`
	MimeType    string `json:"mimeType"`
	Size        int64  `json:"size"`
	Modified    string `json:"modified"`
	Previewable bool   `json:"previewable"`
	Playable    bool   `json:"playable"`
	AudioID     string `json:"audioId,omitempty"`
	TrackName   string `json:"trackName,omitempty"`
}

type browseCandidate struct {
	name        string
	sortName    string
	isDirectory bool
}

// browseCandidateHeap is a max-heap: the least desirable retained candidate
// is at the root and can be replaced in O(log K).
type browseCandidateHeap []browseCandidate

func (h browseCandidateHeap) Len() int { return len(h) }
func (h browseCandidateHeap) Less(i, j int) bool {
	return browseCandidateBefore(h[j], h[i])
}
func (h browseCandidateHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *browseCandidateHeap) Push(value interface{}) {
	*h = append(*h, value.(browseCandidate))
}
func (h *browseCandidateHeap) Pop() interface{} {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

func browseCandidateBefore(a, b browseCandidate) bool {
	if a.isDirectory != b.isDirectory {
		return a.isDirectory
	}
	if a.sortName != b.sortName {
		return a.sortName < b.sortName
	}
	return a.name < b.name
}

func retainBrowseCandidate(retained *browseCandidateHeap, candidate browseCandidate, maximum int) {
	if maximum <= 0 {
		return
	}
	if retained.Len() < maximum {
		heap.Push(retained, candidate)
		return
	}
	if browseCandidateBefore(candidate, (*retained)[0]) {
		(*retained)[0] = candidate
		heap.Fix(retained, 0)
	}
}

func sortedBrowseCandidates(retained browseCandidateHeap) []browseCandidate {
	result := append([]browseCandidate(nil), retained...)
	sort.Slice(result, func(i, j int) bool { return browseCandidateBefore(result[i], result[j]) })
	return result
}

func (a *API) handleBrowse(w http.ResponseWriter, r *http.Request) {
	// Validate every attacker-controlled value before touching the filesystem.
	rawDir := r.URL.Query().Get("dir")
	if !queryWithinBytes(w, rawDir, "dir", 4096) {
		return
	}
	dir, err := cleanLibraryPath(rawDir, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_DIRECTORY", err.Error())
		return
	}
	page, limit, ok := queryPage(w, r, defaultBrowsePageSize)
	if !ok {
		return
	}
	requestedEnd := saturatedPageEnd(page, limit)
	retainedLimit := requestedEnd
	if retainedLimit > maxBrowseRetainedItems {
		// Count the directory without retaining attacker-controlled amounts of
		// metadata. Once the total is known, an out-of-range page can still
		// preserve the existing empty-page response contract.
		retainedLimit = 0
	}

	resolver, err := index.NewRootResolver(a.Config.MusicDir)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_DIRECTORY", "directory must be inside the music library")
		return
	}
	absDir, stat, err := resolver.Stat(dir)
	if err != nil || !stat.IsDir() {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "directory not found")
		return
	}

	directory, err := os.Open(absDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "BROWSE_ERROR", "failed to read directory")
		return
	}
	defer directory.Close()

	initialCapacity := min(retainedLimit, 4096)
	retainedCandidates := make(browseCandidateHeap, 0, initialCapacity)
	total := 0
	for {
		if r.Context().Err() != nil {
			return
		}
		entries, readErr := directory.ReadDir(256)
		for _, entry := range entries {
			if isSystemBrowseEntry(entry.Name()) {
				continue
			}
			isDirectory, isRegular := classifyBrowseEntry(resolver, entry, dir)
			candidate := browseCandidate{name: entry.Name(), sortName: strings.ToLower(entry.Name())}
			switch {
			case isDirectory:
				candidate.isDirectory = true
			case isRegular:
				// Files sort after directories.
			default:
				continue
			}
			total++
			retainBrowseCandidate(&retainedCandidates, candidate, retainedLimit)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			writeError(w, http.StatusInternalServerError, "BROWSE_ERROR", "failed to read directory")
			return
		}
	}

	start, end := pageBounds(total, page, limit)
	if start < total && requestedEnd > maxBrowseRetainedItems {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", "browse page exceeds the maximum sortable window")
		return
	}
	pageCandidates := candidateSlice(sortedBrowseCandidates(retainedCandidates), start, end)

	directories := make([]browseDirectory, 0, len(pageCandidates))
	files := make([]browseFile, 0, len(pageCandidates))
	idx, generation := a.currentSnapshot(r)
	for _, candidate := range pageCandidates {
		if r.Context().Err() != nil {
			return
		}
		relPath := browseRelativePath(dir, candidate.name)
		_, info, resolveErr := resolver.Stat(relPath)
		if resolveErr != nil {
			continue
		}
		if candidate.isDirectory {
			if info.IsDir() {
				directories = append(directories, browseDirectory{Name: candidate.name, Path: filepath.ToSlash(relPath)})
			}
		} else if info.Mode().IsRegular() {
			files = append(files, makeBrowseFile(idx, relPath, info))
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"directories": directories,
		"files":       files,
		"total":       total,
		"page":        page,
		"generation":  generation,
	})
}

func classifyBrowseEntry(resolver *index.RootResolver, entry os.DirEntry, dir string) (bool, bool) {
	entryType := entry.Type()
	if entryType&os.ModeSymlink != 0 {
		relPath := browseRelativePath(dir, entry.Name())
		_, info, err := resolver.Stat(relPath)
		if err != nil {
			return false, false
		}
		return info.IsDir(), info.Mode().IsRegular()
	}
	if entryType.IsDir() {
		return true, false
	}
	if entryType == 0 {
		// Type zero means either an ordinary file or an unknown d_type. One
		// Info call is required to distinguish them safely.
		info, err := entry.Info()
		if err != nil {
			return false, false
		}
		return info.IsDir(), info.Mode().IsRegular()
	}
	return false, false
}

func browseRelativePath(dir, name string) string {
	if dir == "." {
		return name
	}
	return filepath.Join(dir, name)
}

func candidateSlice(candidates []browseCandidate, start, end int) []browseCandidate {
	start = min(max(start, 0), len(candidates))
	end = min(max(end, start), len(candidates))
	return candidates[start:end]
}

func saturatedPageEnd(page, limit int) int {
	maximum := int(^uint(0) >> 1)
	if page <= 0 || limit <= 0 || page > maximum/limit {
		return maximum
	}
	return page * limit
}

func makeBrowseFile(idx *index.Index, relPath string, info os.FileInfo) browseFile {
	id := index.GenerateID(relPath)
	kind, mimeType := classifyBrowseFile(relPath)
	file := browseFile{
		ID:          id,
		Name:        filepath.Base(relPath),
		Path:        filepath.ToSlash(relPath),
		Dir:         filepath.ToSlash(filepath.Dir(relPath)),
		Kind:        kind,
		MimeType:    mimeType,
		Size:        info.Size(),
		Modified:    info.ModTime().UTC().Format(time.RFC3339),
		Previewable: kind == "image" || kind == "pdf" || (kind == "text" && info.Size() <= maxTextPreviewSize),
	}
	if audio, ok := idx.ByID[id]; ok {
		file.Playable = true
		file.AudioID = audio.ID
		file.TrackName = audio.Name
	}
	return file
}

func classifyBrowseFile(path string) (string, string) {
	ext := strings.ToLower(filepath.Ext(path))
	if index.AudioExtensions[ext] {
		return "audio", audioMimeType(ext)
	}
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".bmp":
		return "image", typeByExtension(ext, "image/jpeg")
	case ".txt", ".cue", ".log", ".m3u", ".m3u8", ".md", ".nfo", ".lrc", ".json":
		return "text", "text/plain; charset=utf-8"
	case ".pdf":
		return "pdf", "application/pdf"
	default:
		return "other", typeByExtension(ext, "application/octet-stream")
	}
}

func audioMimeType(ext string) string {
	switch ext {
	case ".flac":
		return "audio/flac"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".opus":
		return "audio/opus"
	case ".wav":
		return "audio/wav"
	case ".aac":
		return "audio/aac"
	case ".m4a":
		return "audio/mp4"
	case ".wma":
		return "audio/x-ms-wma"
	default:
		return "application/octet-stream"
	}
}

func typeByExtension(ext, fallback string) string {
	if detected := mime.TypeByExtension(ext); detected != "" {
		return detected
	}
	return fallback
}

func (a *API) handleBrowseContent(w http.ResponseWriter, r *http.Request) {
	a.serveBrowseFile(w, r, false)
}

func (a *API) handleBrowseDownload(w http.ResponseWriter, r *http.Request) {
	a.serveBrowseFile(w, r, true)
}

func (a *API) serveBrowseFile(w http.ResponseWriter, r *http.Request, download bool) {
	rawPath := r.URL.Query().Get("path")
	if !queryWithinBytes(w, rawPath, "path", 4096) {
		return
	}
	relPath, absPath, info, err := a.resolveBrowseFile(rawPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}
	kind, mimeType := classifyBrowseFile(relPath)
	if !download && kind != "image" && kind != "pdf" && !(kind == "text" && info.Size() <= maxTextPreviewSize) {
		writeError(w, http.StatusUnsupportedMediaType, "PREVIEW_UNAVAILABLE", "preview is unavailable for this file")
		return
	}

	file, err := os.Open(absPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "file not found")
		return
	}
	defer file.Close()

	disposition := "inline"
	if download {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": filepath.Base(relPath)}))
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if kind == "audio" {
		w.Header().Set("Cache-Control", "private, no-store")
	} else {
		w.Header().Set("Cache-Control", "private, max-age=3600")
	}
	http.ServeContent(w, r, filepath.Base(relPath), info.ModTime(), file)
}

func (a *API) resolveBrowseFile(rawPath string) (string, string, os.FileInfo, error) {
	relPath, err := cleanLibraryPath(rawPath, false)
	if err != nil {
		return "", "", nil, err
	}
	absPath, err := index.ResolveWithinRoot(a.Config.MusicDir, relPath)
	if err != nil {
		return "", "", nil, fmt.Errorf("file must be inside the music library")
	}
	info, err := os.Stat(absPath)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", nil, fmt.Errorf("file not found")
	}
	return relPath, absPath, info, nil
}

func cleanLibraryPath(rawPath string, allowRoot bool) (string, error) {
	if rawPath == "" && allowRoot {
		return ".", nil
	}
	path := filepath.Clean(filepath.FromSlash(rawPath))
	if filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must be inside the music library")
	}
	if path == "." {
		if allowRoot {
			return path, nil
		}
		return "", fmt.Errorf("file path is required")
	}
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if isSystemBrowseEntry(part) {
			return "", fmt.Errorf("file is not available for browsing")
		}
	}
	return path, nil
}

func isSystemBrowseEntry(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(name, ".") ||
		strings.HasSuffix(lower, ":zone.identifier") ||
		lower == "thumbs.db" ||
		lower == "desktop.ini"
}
