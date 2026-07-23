package stream

import (
	"path/filepath"
	"strings"
)

var mimeTypes = map[string]string{
	".opus": "audio/opus",
	".flac": "audio/flac",
	".mp3":  "audio/mpeg",
	".ogg":  "audio/ogg",
	".wav":  "audio/wav",
	".aac":  "audio/aac",
	".m4a":  "audio/mp4",
	".wma":  "audio/x-ms-wma",
}

func MimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if mt, ok := mimeTypes[ext]; ok {
		return mt
	}
	return "application/octet-stream"
}

func IsNativeOpus(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".opus"
}
