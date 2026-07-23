package api

import (
	"context"
	"errors"
	"net/http"

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
