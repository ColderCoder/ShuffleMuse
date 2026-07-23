package api

import (
	"bytes"
	"encoding/csv"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/ColderCoder/ShuffleMuse/internal/index"
)

func (a *API) handleExportTags(w http.ResponseWriter, r *http.Request) {
	records, err := a.Tags.GetTaggedFiles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TAG_ERROR", err.Error())
		return
	}

	idx, _ := a.currentSnapshot(r)
	var output bytes.Buffer
	output.WriteString("\xEF\xBB\xBF")
	writer := csv.NewWriter(&output)
	if err := writer.Write([]string{"filepath", "name", "dir", "tags", "status"}); err != nil {
		writeError(w, http.StatusInternalServerError, "CSV_ERROR", "failed to create tag export")
		return
	}

	for _, record := range records {
		name := strings.TrimSuffix(filepath.Base(record.Filepath), filepath.Ext(record.Filepath))
		dir := filepath.Dir(record.Filepath)
		status := "missing"
		if entry, ok := idx.ByID[index.GenerateID(record.Filepath)]; ok && entry.Filepath == record.Filepath {
			name = entry.Name
			dir = entry.Dir
			status = "online"
		}
		row := []string{
			safeCSVCell(record.Filepath),
			safeCSVCell(name),
			safeCSVCell(dir),
			safeCSVCell(strings.Join(record.Tags, ";")),
			status,
		}
		if err := writer.Write(row); err != nil {
			writeError(w, http.StatusInternalServerError, "CSV_ERROR", "failed to create tag export")
			return
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		writeError(w, http.StatusInternalServerError, "CSV_ERROR", "failed to create tag export")
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "shufflemuse-tags.csv"}))
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(output.Bytes())
}

// Spreadsheet applications may interpret leading formula characters even in
// quoted CSV fields. Prefix them with an apostrophe to keep filenames and paths
// inert when the export is opened directly.
func safeCSVCell(value string) string {
	if value == "" {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + value
	default:
		return value
	}
}
