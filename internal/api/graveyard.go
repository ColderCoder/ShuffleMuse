package api

import (
	"errors"
	"net/http"

	"github.com/ColderCoder/ShuffleMuse/internal/index"
	"github.com/ColderCoder/ShuffleMuse/internal/tags"
)

func (a *API) handleGraveyard(w http.ResponseWriter, r *http.Request) {
	page, limit, ok := queryPage(w, r, 50)
	if !ok {
		return
	}
	idx, generation := a.currentSnapshot(r)
	items, total, err := a.Tags.GetGraveyard(onlinePaths(idx), page, limit)
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

func (a *API) handleDeleteGraveyard(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "MISSING_PATH", "path is required")
		return
	}
	if !queryWithinBytes(w, path, "path", 4096) {
		return
	}
	err := a.withSnapshot(func(idx *index.Index, _ uint64) error {
		return a.Tags.DeleteOrphan(path, func(candidate string) bool {
			if idx == nil {
				return false
			}
			entry, ok := idx.ByID[index.GenerateID(candidate)]
			return ok && entry.Filepath == candidate
		})
	})
	if errors.Is(err, tags.ErrFileOnline) {
		writeError(w, http.StatusConflict, "FILE_ONLINE", "tagged file is online")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TAG_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
