package main

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestVersionOutput(t *testing.T) {
	originalVersion, originalRevision, originalBuildDate := version, revision, buildDate
	t.Cleanup(func() {
		version, revision, buildDate = originalVersion, originalRevision, originalBuildDate
	})
	version, revision, buildDate = "0.1.0", "abc1234", "2026-07-23T00:00:00Z"

	if got, want := versionLine(), "ShuffleMuse 0.1.0 (commit abc1234, built 2026-07-23T00:00:00Z)"; got != want {
		t.Fatalf("versionLine() = %q, want %q", got, want)
	}
	if !versionRequested([]string{"shufflemuse", "--version"}) {
		t.Fatal("--version was not recognized")
	}
	if !versionRequested([]string{"shufflemuse", "-version"}) {
		t.Fatal("-version was not recognized")
	}
	for _, args := range [][]string{{"shufflemuse"}, {"shufflemuse", "--version", "extra"}, {"shufflemuse", "--help"}} {
		if versionRequested(args) {
			t.Fatalf("versionRequested(%q) = true", args)
		}
	}
}

func TestSPAFileSystemFallbackAndMissingAssets(t *testing.T) {
	root := spaFS{fsys: fstest.MapFS{
		"index.html":          &fstest.MapFile{Data: []byte("app")},
		"assets/index-abc.js": &fstest.MapFile{Data: []byte("script")},
	}}

	for _, name := range []string{"tags", "browse/album"} {
		file, err := root.Open(name)
		if err != nil {
			t.Fatalf("Open(%q): %v", name, err)
		}
		data, _ := io.ReadAll(file)
		file.Close()
		if string(data) != "app" {
			t.Fatalf("Open(%q) = %q", name, data)
		}
	}
	if _, err := root.Open("assets/missing.js"); err == nil {
		t.Fatal("missing hashed asset fell back to index.html")
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing asset error = %v", err)
	}
	if _, err := root.Open("missing.ico"); err == nil {
		t.Fatal("missing file with extension fell back to index.html")
	}
}

func TestStaticCacheHeaders(t *testing.T) {
	handler := staticCacheHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/assets/index-abc.js", want: "public, max-age=31536000, immutable"},
		{path: "/", want: "no-cache"},
		{path: "/tags", want: "no-cache"},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
		if got := recorder.Header().Get("Cache-Control"); got != test.want {
			t.Errorf("%s Cache-Control = %q, want %q", test.path, got, test.want)
		}
	}
}
