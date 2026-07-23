package stream

import (
	"net/http"
	"os"
)

func ServeFile(w http.ResponseWriter, r *http.Request, filepath string) error {
	f, err := os.Open(filepath)
	if err != nil {
		return err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", MimeType(filepath))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Accept-Ranges", "bytes")

	http.ServeContent(w, r, filepath, stat.ModTime(), f)

	return nil
}
