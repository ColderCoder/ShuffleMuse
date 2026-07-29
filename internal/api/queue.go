package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ColderCoder/ShuffleMuse/internal/index"
	"github.com/ColderCoder/ShuffleMuse/internal/playqueue"
)

type createQueueBody struct {
	Tag            string `json:"tag,omitempty"`
	PinFileID      string `json:"pinFileId,omitempty"`
	ReplaceQueueID string `json:"replaceQueueId,omitempty"`
}

func (a *API) handleCreateQueue(w http.ResponseWriter, r *http.Request) {
	var body createQueueBody
	if !decodeStrictJSON(w, r, &body) {
		return
	}
	if len(body.Tag) > 50 {
		writeError(w, http.StatusBadRequest, "INVALID_TAG", "tag is too long")
		return
	}
	idx, generation := a.currentSnapshot(r)
	result, err := a.Queues.Create(r.Context(), idx, generation, playqueue.CreateRequest{
		Tag: body.Tag, PinFileID: body.PinFileID, ReplaceQueueID: body.ReplaceQueueID,
	})
	if err != nil {
		writeQueueError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (a *API) handleQueueItems(w http.ResponseWriter, r *http.Request) {
	page, ok := positiveQueryValue(w, r, "page", 1)
	if !ok {
		return
	}
	idx, generation := a.currentSnapshot(r)
	result, err := a.Queues.Page(r.PathValue("id"), page, idx, generation)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) handleQueueSelect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FileID string `json:"fileId"`
	}
	if !decodeStrictJSON(w, r, &body) {
		return
	}
	if body.FileID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_JSON", "fileId is required")
		return
	}
	idx, generation := a.currentSnapshot(r)
	result, err := a.Queues.Select(r.Context(), r.PathValue("id"), body.FileID, idx, generation)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) handleQueuePrependDirectory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir string `json:"dir"`
	}
	if !decodeStrictJSON(w, r, &body) {
		return
	}
	if body.Dir == "" || len(body.Dir) > 4096 {
		writeError(w, http.StatusBadRequest, "INVALID_DIRECTORY", "dir is required and must not exceed 4096 bytes")
		return
	}
	dir, err := cleanLibraryPath(body.Dir, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_DIRECTORY", err.Error())
		return
	}
	idx, generation := a.currentSnapshot(r)
	entries, found, err := a.directAudioEntries(r.Context(), dir, idx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		writeError(w, http.StatusInternalServerError, "BROWSE_ERROR", "failed to read directory")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "directory not found")
		return
	}
	result, err := a.Queues.Prepend(r.Context(), r.PathValue("id"), entries, idx, generation)
	if err != nil {
		writeQueueError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) directAudioEntries(
	ctx context.Context,
	dir string,
	idx *index.Index,
) ([]index.FileEntry, bool, error) {
	resolver, err := index.NewRootResolver(a.Config.MusicDir)
	if err != nil {
		return nil, false, err
	}
	absDir, stat, err := resolver.Stat(dir)
	if err != nil || !stat.IsDir() {
		return nil, false, nil
	}
	directory, err := os.Open(absDir)
	if err != nil {
		return nil, true, err
	}
	defer directory.Close()

	entries := make([]index.FileEntry, 0)
	for {
		if err := ctx.Err(); err != nil {
			return nil, true, err
		}
		batch, readErr := directory.ReadDir(256)
		for _, entry := range batch {
			if isSystemBrowseEntry(entry.Name()) {
				continue
			}
			_, regular := classifyBrowseEntry(resolver, entry, dir)
			if !regular {
				continue
			}
			relPath := browseRelativePath(dir, entry.Name())
			_, info, statErr := resolver.Stat(relPath)
			if statErr != nil || !info.Mode().IsRegular() {
				continue
			}
			audio := idx.ByID[index.GenerateID(relPath)]
			if audio == nil || filepath.Clean(audio.Filepath) != filepath.Clean(relPath) {
				continue
			}
			entries = append(entries, *audio)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, true, readErr
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		left := filepath.Base(entries[i].Filepath)
		right := filepath.Base(entries[j].Filepath)
		leftSort := strings.ToLower(left)
		rightSort := strings.ToLower(right)
		if leftSort != rightSort {
			return leftSort < rightSort
		}
		if left != right {
			return left < right
		}
		return entries[i].ID < entries[j].ID
	})
	return entries, true, nil
}

func (a *API) handleDeleteQueue(w http.ResponseWriter, r *http.Request) {
	a.Queues.Delete(r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func writeQueueError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, playqueue.ErrNotFound):
		writeError(w, http.StatusNotFound, "QUEUE_NOT_FOUND", "queue not found")
	case errors.Is(err, playqueue.ErrFileNotFound):
		writeError(w, http.StatusNotFound, "FILE_NOT_FOUND", "file not found")
	case errors.Is(err, playqueue.ErrNoAudioFiles):
		writeError(w, http.StatusUnprocessableEntity, "NO_AUDIO_FILES", "directory contains no audio files")
	case errors.Is(err, playqueue.ErrBusy):
		writeError(w, http.StatusServiceUnavailable, "QUEUE_BUSY", "queue builder is busy")
	case errors.Is(err, playqueue.ErrCapacity):
		writeError(w, http.StatusServiceUnavailable, "QUEUE_CAPACITY", "queue cache capacity exceeded")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// The client is gone. Avoid committing a misleading response.
		return
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}
