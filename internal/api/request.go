package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

const maxJSONBodyBytes = 8 << 10

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, destination interface{}) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "request body is too large")
		} else {
			writeError(w, http.StatusBadRequest, "INVALID_JSON", "invalid request body")
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "request body is too large")
		} else {
			writeError(w, http.StatusBadRequest, "INVALID_JSON", "request body must contain exactly one JSON object")
		}
		return false
	}
	return true
}

func queryPage(w http.ResponseWriter, r *http.Request, defaultLimit int) (int, int, bool) {
	page, ok := positiveQueryValue(w, r, "page", 1)
	if !ok {
		return 0, 0, false
	}
	limit, ok := positiveQueryValue(w, r, "limit", defaultLimit)
	if !ok {
		return 0, 0, false
	}
	if limit > maxPageSize {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", "limit exceeds maximum page size")
		return 0, 0, false
	}
	return page, limit, true
}

func positiveQueryValue(w http.ResponseWriter, r *http.Request, key string, fallback int) (int, bool) {
	raw, exists := r.URL.Query()[key]
	if !exists {
		return fallback, true
	}
	if len(raw) != 1 || raw[0] == "" {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", key+" must be a positive integer")
		return 0, false
	}
	value, err := strconv.Atoi(raw[0])
	if err != nil || value < 1 {
		writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", key+" must be a positive integer")
		return 0, false
	}
	return value, true
}

func queryWithinBytes(w http.ResponseWriter, value, name string, max int) bool {
	if len(value) <= max {
		return true
	}
	writeError(w, http.StatusBadRequest, "QUERY_TOO_LONG", name+" exceeds the maximum length")
	return false
}
