package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ColderCoder/ShuffleMuse/internal/cover"
	"github.com/ColderCoder/ShuffleMuse/internal/mediaexec"
	"github.com/ColderCoder/ShuffleMuse/internal/stream"
)

func TestBrowseListsMeaningfulFilesAndDirectories(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	artistDir := filepath.Join(env.tmpDir, "music", "artist1")
	mustWriteBrowseFile(t, filepath.Join(artistDir, "cover.jpg"), []byte("image"))
	mustWriteBrowseFile(t, filepath.Join(artistDir, "album.cue"), []byte("FILE track1.flac WAVE"))
	mustWriteBrowseFile(t, filepath.Join(artistDir, "archive.bin"), []byte{0, 1, 2})
	mustWriteBrowseFile(t, filepath.Join(artistDir, ".hidden.txt"), []byte("hidden"))
	mustWriteBrowseFile(t, filepath.Join(artistDir, "cover.jpg:Zone.Identifier"), []byte("metadata"))
	if err := os.Mkdir(filepath.Join(artistDir, "Scans"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(env.tmpDir, "outside.jpg")
	mustWriteBrowseFile(t, outside, []byte("outside"))
	if err := os.Symlink(outside, filepath.Join(artistDir, "escape.jpg")); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(env.server.URL + "/api/browse?dir=artist1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var result struct {
		Directories []browseDirectory `json:"directories"`
		Files       []browseFile      `json:"files"`
		Total       int               `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Directories) != 1 || result.Directories[0].Path != "artist1/Scans" {
		t.Fatalf("unexpected directories: %+v", result.Directories)
	}
	names := make(map[string]browseFile)
	for _, file := range result.Files {
		names[file.Name] = file
	}
	for _, expected := range []string{"track1.flac", "track2.flac", "track3.opus", "cover.jpg", "album.cue", "archive.bin"} {
		if _, ok := names[expected]; !ok {
			t.Errorf("missing %s in browse response", expected)
		}
	}
	for _, hidden := range []string{".hidden.txt", "cover.jpg:Zone.Identifier", "escape.jpg"} {
		if _, ok := names[hidden]; ok {
			t.Errorf("unexpected hidden or unsafe file %s", hidden)
		}
	}
	if !names["track1.flac"].Playable || names["track1.flac"].AudioID == "" {
		t.Errorf("indexed audio is not playable: %+v", names["track1.flac"])
	}
	if !names["cover.jpg"].Previewable || names["cover.jpg"].Kind != "image" {
		t.Errorf("image is not previewable: %+v", names["cover.jpg"])
	}
	if names["archive.bin"].Previewable {
		t.Errorf("binary file should be download-only: %+v", names["archive.bin"])
	}
}

func TestBrowseHandlesMaximumPageNumber(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	maxPage := int(^uint(0) >> 1)
	resp, err := http.Get(fmt.Sprintf("%s/api/browse?dir=artist1&page=%d&limit=2", env.server.URL, maxPage))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result struct {
		Files []browseFile `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || len(result.Files) != 0 {
		t.Fatalf("status/files = %d/%v", resp.StatusCode, result.Files)
	}
}

func TestBrowsePaginatesDirectoriesAndFilesTogether(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	root := filepath.Join(env.tmpDir, "music")
	for i := 0; i < 60; i++ {
		if err := os.Mkdir(filepath.Join(root, fmt.Sprintf("dir-%02d", i)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	seen := make(map[string]bool)
	for page := 1; page <= 2; page++ {
		resp, err := http.Get(fmt.Sprintf("%s/api/browse?dir=.&page=%d&limit=50", env.server.URL, page))
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Directories []browseDirectory `json:"directories"`
			Files       []browseFile      `json:"files"`
			Total       int               `json:"total"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			t.Fatal(err)
		}
		resp.Body.Close()
		if len(result.Directories)+len(result.Files) > 50 || result.Total < 61 {
			t.Fatalf("page %d bounds/total = %d/%d", page, len(result.Directories)+len(result.Files), result.Total)
		}
		for _, directory := range result.Directories {
			if seen["d:"+directory.Path] {
				t.Fatalf("duplicate directory across pages: %s", directory.Path)
			}
			seen["d:"+directory.Path] = true
		}
		for _, file := range result.Files {
			if seen["f:"+file.Path] {
				t.Fatalf("duplicate file across pages: %s", file.Path)
			}
			seen["f:"+file.Path] = true
		}
	}
}

func TestBrowseFlatDirectoriesRetainOnlyRequestedPrefix(t *testing.T) {
	for _, count := range []int{5_000, maxBrowseRetainedItems + 1} {
		t.Run(fmt.Sprintf("%d", count), func(t *testing.T) {
			env := setupTestEnv(t)
			defer env.teardown()
			flat := filepath.Join(env.tmpDir, "music", "flat")
			if err := os.Mkdir(flat, 0o755); err != nil {
				t.Fatal(err)
			}
			for i := count - 1; i >= 0; i-- {
				mustWriteBrowseFile(t, filepath.Join(flat, fmt.Sprintf("entry-%06d.txt", i)), nil)
			}
			resp, err := http.Get(fmt.Sprintf("%s/api/browse?dir=flat&page=17&limit=100", env.server.URL))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var result struct {
				Files []browseFile `json:"files"`
				Total int          `json:"total"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusOK || result.Total != count || len(result.Files) != 100 {
				t.Fatalf("flat result = %d/%d/%d", resp.StatusCode, result.Total, len(result.Files))
			}
			if result.Files[0].Name != "entry-001600.txt" || result.Files[99].Name != "entry-001699.txt" {
				t.Fatalf("wrong retained page boundaries: %q .. %q", result.Files[0].Name, result.Files[99].Name)
			}

			if count > maxBrowseRetainedItems {
				deepPage := maxBrowseRetainedItems/100 + 1
				deepResp, body := getJSON(t, fmt.Sprintf("%s/api/browse?dir=flat&page=%d&limit=100", env.server.URL, deepPage), nil)
				if deepResp.StatusCode != http.StatusBadRequest || body["code"] != "INVALID_PAGINATION" {
					t.Fatalf("deep page status/body = %d/%v", deepResp.StatusCode, body)
				}
			}
		})
	}
}

func TestBrowseSortKeyDirectoryPriorityAndSymlinkChanges(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	root := filepath.Join(env.tmpDir, "music")
	for _, name := range []string{"alpha", "Alpha", "ä-dir"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"alpha.txt", "Alpha.txt", "βeta.txt"} {
		mustWriteBrowseFile(t, filepath.Join(root, name), nil)
	}
	insideTarget := filepath.Join(root, "Alpha.txt")
	if err := os.Symlink(insideTarget, filepath.Join(root, "inside-link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing.txt"), filepath.Join(root, "broken.txt")); err != nil {
		t.Fatal(err)
	}

	read := func() (struct {
		Directories []browseDirectory `json:"directories"`
		Files       []browseFile      `json:"files"`
		Total       int               `json:"total"`
	}, int) {
		resp, err := http.Get(env.server.URL + "/api/browse?dir=.&limit=100")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var result struct {
			Directories []browseDirectory `json:"directories"`
			Files       []browseFile      `json:"files"`
			Total       int               `json:"total"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result, resp.StatusCode
	}
	result, status := read()
	if status != http.StatusOK || len(result.Directories) < 3 {
		t.Fatalf("browse = %d/%+v", status, result)
	}
	if result.Directories[0].Name != "Alpha" || result.Directories[1].Name != "alpha" {
		t.Fatalf("case tie-break order = %+v", result.Directories[:2])
	}
	fileNames := make(map[string]bool)
	for _, file := range result.Files {
		fileNames[file.Name] = true
	}
	if !fileNames["inside-link.txt"] || fileNames["broken.txt"] {
		t.Fatalf("symlink classification = %v", fileNames)
	}
	if err := os.Remove(filepath.Join(root, "inside-link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(env.tmpDir, "outside.txt"), filepath.Join(root, "inside-link.txt")); err != nil {
		t.Fatal(err)
	}
	mustWriteBrowseFile(t, filepath.Join(env.tmpDir, "outside.txt"), nil)
	changed, _ := read()
	for _, file := range changed.Files {
		if file.Name == "inside-link.txt" {
			t.Fatal("next request did not observe symlink changing to an outside target")
		}
	}
}

func TestBrowseRejectsPaginationBeforeResolvingRoot(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	env.api.Config.MusicDir = filepath.Join(env.tmpDir, "does-not-exist")
	resp, body := getJSON(t, env.server.URL+"/api/browse?dir=.&page=0", nil)
	if resp.StatusCode != http.StatusBadRequest || body["code"] != "INVALID_PAGINATION" {
		t.Fatalf("invalid pagination touched the missing root first: %d/%v", resp.StatusCode, body)
	}
}

func BenchmarkBrowseFlatDirectory(b *testing.B) {
	for _, count := range []int{100, 5_000, 50_000} {
		b.Run(fmt.Sprintf("%d", count), func(b *testing.B) {
			env := setupTestEnv(b)
			defer env.teardown()
			flat := filepath.Join(env.tmpDir, "music", "benchmark")
			_ = os.Mkdir(flat, 0o755)
			for i := 0; i < count; i++ {
				_ = os.WriteFile(filepath.Join(flat, fmt.Sprintf("entry-%06d.txt", i)), nil, 0o644)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req := httptest.NewRequest(http.MethodGet, "/api/browse?dir=benchmark&page=1&limit=100", nil)
				recorder := httptest.NewRecorder()
				env.api.handleBrowse(recorder, req)
				if recorder.Code != http.StatusOK {
					b.Fatalf("status = %d", recorder.Code)
				}
			}
		})
	}
}

func TestBrowsePreviewAndDownload(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	artistDir := filepath.Join(env.tmpDir, "music", "artist1")
	mustWriteBrowseFile(t, filepath.Join(artistDir, "notes.log"), []byte("verification log"))
	mustWriteBrowseFile(t, filepath.Join(artistDir, "archive.bin"), []byte{0, 1, 2, 3})

	previewURL := env.server.URL + "/api/browse/content?path=" + url.QueryEscape("artist1/notes.log")
	resp, err := http.Get(previewURL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "verification log" {
		t.Fatalf("preview status/body = %d/%q", resp.StatusCode, string(body))
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "inline") {
		t.Errorf("preview disposition = %q", resp.Header.Get("Content-Disposition"))
	}

	unsupported, err := http.Get(env.server.URL + "/api/browse/content?path=" + url.QueryEscape("artist1/archive.bin"))
	if err != nil {
		t.Fatal(err)
	}
	unsupported.Body.Close()
	if unsupported.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("unsupported preview status = %d", unsupported.StatusCode)
	}

	download, err := http.Get(env.server.URL + "/api/browse/download?path=" + url.QueryEscape("artist1/archive.bin"))
	if err != nil {
		t.Fatal(err)
	}
	downloadBody, _ := io.ReadAll(download.Body)
	download.Body.Close()
	if download.StatusCode != http.StatusOK || string(downloadBody) != string([]byte{0, 1, 2, 3}) {
		t.Fatalf("download status/body = %d/%v", download.StatusCode, downloadBody)
	}
	if !strings.HasPrefix(download.Header.Get("Content-Disposition"), "attachment") {
		t.Errorf("download disposition = %q", download.Header.Get("Content-Disposition"))
	}

	audioDownload, err := http.Get(env.server.URL + "/api/browse/download?path=" + url.QueryEscape("artist1/track1.flac"))
	if err != nil {
		t.Fatal(err)
	}
	audioDownload.Body.Close()
	if got := audioDownload.Header.Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("audio download Cache-Control = %q, want private, no-store", got)
	}
}

func TestOriginalStreamAndInvalidMode(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	id := env.idx.Files[0].ID

	req, _ := http.NewRequest(http.MethodGet, env.server.URL+"/api/stream/"+id+"?mode=original", nil)
	req.Header.Set("Range", "bytes=0-3")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Accept-Ranges") != "bytes" {
		t.Fatalf("original stream status/ranges = %d/%q", resp.StatusCode, resp.Header.Get("Accept-Ranges"))
	}

	invalid, err := http.Get(env.server.URL + "/api/stream/" + id + "?mode=invalid")
	if err != nil {
		t.Fatal(err)
	}
	invalid.Body.Close()
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid mode status = %d", invalid.StatusCode)
	}
}

type fixedMediaProbe struct {
	metadata stream.Metadata
}

func (p fixedMediaProbe) Probe(context.Context, string) (stream.Metadata, error) {
	return p.metadata, nil
}

func TestFileMetadataEndpoint(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	env.api.Metadata = fixedMediaProbe{metadata: stream.Metadata{
		Title:           "Metadata Title",
		Codec:           "FLAC",
		BitrateKbps:     987,
		DurationSeconds: 245.5,
	}}

	resp, err := http.Get(env.server.URL + "/api/files/" + env.idx.Files[0].ID + "/metadata")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var metadata stream.Metadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Title != "Metadata Title" || metadata.Codec != "FLAC" || metadata.BitrateKbps != 987 || metadata.DurationSeconds != 245.5 {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
}

func TestMetadataAndEmbeddedCoverShareOneProbe(t *testing.T) {
	probeDir := t.TempDir()
	countPath := filepath.Join(probeDir, "count")
	script := `#!/bin/sh
printf '1\n' >> "$SHUFFLEMUSE_TEST_FFPROBE_COUNT"
cat <<'JSON'
{"streams":[{"codec_type":"audio","codec_name":"flac","duration":"12","bit_rate":"1000000"},{"codec_type":"video","codec_name":"mjpeg","width":640,"height":640}],"format":{"duration":"12","bit_rate":"1000000","tags":{"TITLE":"Shared Title"}}}
JSON
`
	if err := os.WriteFile(filepath.Join(probeDir, "ffprobe"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHUFFLEMUSE_TEST_FFPROBE_COUNT", countPath)
	t.Setenv("PATH", probeDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	env := setupTestEnv(t)
	defer env.teardown()
	id := env.idx.Files[0].ID

	response, err := http.Get(env.server.URL + "/api/files/" + id + "/metadata")
	if err != nil {
		t.Fatal(err)
	}
	var metadata stream.Metadata
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || metadata.Title != "Shared Title" {
		t.Fatalf("metadata = %d/%+v", response.StatusCode, metadata)
	}

	response, err = http.Head(env.server.URL + "/api/files/" + id + "/cover")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("X-Cover-Source") != "embedded" {
		t.Fatalf("cover = %d source=%q", response.StatusCode, response.Header.Get("X-Cover-Source"))
	}

	count, err := os.ReadFile(countPath)
	if err != nil {
		t.Fatal(err)
	}
	if calls := bytes.Count(count, []byte{'\n'}); calls != 1 {
		t.Fatalf("ffprobe calls = %d, log=%q", calls, count)
	}
}

type fixedCoverProvider struct {
	descriptor cover.Descriptor
	data       []byte
	err        error
	renderErr  error
	rendered   int
}

func (p *fixedCoverProvider) Describe(context.Context, string) (cover.Descriptor, error) {
	return p.descriptor, p.err
}

func (p *fixedCoverProvider) DescribeDirectory(context.Context, string) (cover.Descriptor, error) {
	return p.descriptor, p.err
}

func (p *fixedCoverProvider) Render(context.Context, cover.Descriptor) ([]byte, error) {
	p.rendered++
	return p.data, p.renderErr
}

func (p *fixedCoverProvider) OpenFallback(descriptor cover.Descriptor) (*os.File, error) {
	return os.Open(descriptor.FilePath)
}

func TestFileCoverEndpoint(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	provider := &fixedCoverProvider{descriptor: cover.Descriptor{
		Kind:        cover.Embedded,
		ContentType: "image/png",
		Name:        "embedded-cover.png",
		Source:      "embedded",
		ModTime:     time.Unix(1_700_000_000, 0),
		ETag:        `"cover-v1"`,
	}, data: []byte("image-data")}
	env.api.Covers = provider

	resp, err := http.Get(env.server.URL + "/api/files/" + env.idx.Files[0].ID + "/cover")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("cover status/type = %d/%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if string(body) != "image-data" || resp.Header.Get("X-Cover-Source") != "embedded" {
		t.Fatalf("cover body/source = %q/%q", string(body), resp.Header.Get("X-Cover-Source"))
	}
	if resp.Header.Get("ETag") != `"cover-v1"` || provider.rendered != 1 {
		t.Fatalf("cover etag/render count = %q/%d", resp.Header.Get("ETag"), provider.rendered)
	}
	if resp.Header.Get("Cache-Control") != "private, max-age=3600" || resp.Header.Get("Content-Length") != "10" {
		t.Fatalf("cover cache/length = %q/%q", resp.Header.Get("Cache-Control"), resp.Header.Get("Content-Length"))
	}

	headRequest, _ := http.NewRequest(http.MethodHead, env.server.URL+"/api/files/"+env.idx.Files[0].ID+"/cover", nil)
	head, err := http.DefaultClient.Do(headRequest)
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	conditionalRequest, _ := http.NewRequest(http.MethodGet, env.server.URL+"/api/files/"+env.idx.Files[0].ID+"/cover", nil)
	conditionalRequest.Header.Set("If-None-Match", `"cover-v1"`)
	conditional, err := http.DefaultClient.Do(conditionalRequest)
	if err != nil {
		t.Fatal(err)
	}
	conditional.Body.Close()
	if head.StatusCode != http.StatusOK || conditional.StatusCode != http.StatusNotModified || provider.rendered != 1 {
		t.Fatalf("HEAD/conditional rendered cover: %d/%d renders=%d", head.StatusCode, conditional.StatusCode, provider.rendered)
	}

	env.api.Covers = &fixedCoverProvider{err: cover.ErrNotFound}
	missing, err := http.Get(env.server.URL + "/api/files/" + env.idx.Files[0].ID + "/cover")
	if err != nil {
		t.Fatal(err)
	}
	missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing cover status = %d", missing.StatusCode)
	}

	env.api.Covers = &fixedCoverProvider{err: errors.New("failed")}
	failed, err := http.Get(env.server.URL + "/api/files/" + env.idx.Files[0].ID + "/cover")
	if err != nil {
		t.Fatal(err)
	}
	failed.Body.Close()
	if failed.StatusCode != http.StatusInternalServerError {
		t.Fatalf("failed cover status = %d", failed.StatusCode)
	}
}

func TestDirectoryCoverEndpointHEADConditionalAndStrictPath(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	provider := &fixedCoverProvider{descriptor: cover.Descriptor{
		Kind:           cover.Fallback,
		ContentType:    "image/jpeg",
		Name:           "cover.jpg",
		Source:         "Cover.PNG",
		ModTime:        time.Unix(1_700_000_000, 0),
		ETag:           `"directory-v1"`,
		RequiresRender: true,
	}, data: []byte("converted-jpeg")}
	env.api.Covers = provider

	endpoint := env.server.URL + "/api/covers/directory?dir=artist1"
	response, err := http.Get(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "converted-jpeg" || response.Header.Get("X-Cover-Source") != "Cover.PNG" {
		t.Fatalf("directory cover = %d/%q/%q", response.StatusCode, body, response.Header.Get("X-Cover-Source"))
	}
	if response.Header.Get("Content-Disposition") != `inline; filename=cover.jpg` || response.Header.Get("Content-Length") != "14" {
		t.Fatalf("directory disposition/length = %q/%q", response.Header.Get("Content-Disposition"), response.Header.Get("Content-Length"))
	}

	headRequest, _ := http.NewRequest(http.MethodHead, endpoint, nil)
	head, err := http.DefaultClient.Do(headRequest)
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	if head.StatusCode != http.StatusOK || head.Header.Get("Content-Type") != "image/jpeg" || head.Header.Get("Content-Length") != "" || provider.rendered != 1 {
		t.Fatalf("directory HEAD = %d type=%q length=%q renders=%d", head.StatusCode, head.Header.Get("Content-Type"), head.Header.Get("Content-Length"), provider.rendered)
	}

	conditionalRequest, _ := http.NewRequest(http.MethodGet, endpoint, nil)
	conditionalRequest.Header.Set("If-None-Match", `"directory-v1"`)
	conditional, err := http.DefaultClient.Do(conditionalRequest)
	if err != nil {
		t.Fatal(err)
	}
	conditional.Body.Close()
	if conditional.StatusCode != http.StatusNotModified || provider.rendered != 1 {
		t.Fatalf("directory conditional = %d renders=%d", conditional.StatusCode, provider.rendered)
	}

	symlink := filepath.Join(env.api.Config.MusicDir, "linked")
	if err := os.Symlink(filepath.Join(env.api.Config.MusicDir, "artist1"), symlink); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"dir=../artist1",
		"dir=/artist1",
		"dir=artist1/../artist1",
		"dir=linked",
		"dir=artist1&dir=artist1",
	} {
		resp, body := getJSON(t, env.server.URL+"/api/covers/directory?"+query, nil)
		if resp.StatusCode != http.StatusBadRequest || body["code"] != "INVALID_DIR" {
			t.Fatalf("unsafe query %q = %d/%v", query, resp.StatusCode, body)
		}
	}
	missing, missingBody := getJSON(t, env.server.URL+"/api/covers/directory?dir=missing", nil)
	if missing.StatusCode != http.StatusNotFound || missingBody["code"] != "COVER_NOT_FOUND" {
		t.Fatalf("missing directory = %d/%v", missing.StatusCode, missingBody)
	}
}

func TestSmallFolderPNGIsPreservedAndWinsBeforeEmbeddedProbe(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	imagePath := filepath.Join(env.api.Config.MusicDir, "artist1", "Folder.PNG")
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 200, G: 40, B: 20, A: 80})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, encoded.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, endpoint := range []string{
		"/api/covers/directory?dir=artist1",
		"/api/files/" + env.idx.Files[0].ID + "/cover",
	} {
		response, err := http.Get(env.server.URL + endpoint)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "image/png" {
			t.Fatalf("GET %s = %d type=%q", endpoint, response.StatusCode, response.Header.Get("Content-Type"))
		}
		if !bytes.Equal(body, encoded.Bytes()) || response.Header.Get("Content-Length") != fmt.Sprint(encoded.Len()) {
			t.Fatalf("GET %s changed small PNG: length=%q body=%d", endpoint, response.Header.Get("Content-Length"), len(body))
		}
		if response.Header.Get("X-Cover-Source") != "Folder.PNG" {
			t.Fatalf("GET %s source = %q", endpoint, response.Header.Get("X-Cover-Source"))
		}
	}
}

func TestFileCoverBusyUsesStableError(t *testing.T) {
	for name, busyErr := range map[string]error{
		"queue full":   mediaexec.ErrQueueFull,
		"wait timeout": fmt.Errorf("extract embedded cover: %w", mediaexec.ErrWaitTimeout),
	} {
		t.Run(name, func(t *testing.T) {
			env := setupTestEnv(t)
			defer env.teardown()
			env.api.Covers = &fixedCoverProvider{err: busyErr}

			resp, body := getJSON(t, env.server.URL+"/api/files/"+env.idx.Files[0].ID+"/cover", nil)
			if resp.StatusCode != http.StatusServiceUnavailable || body["code"] != "MEDIA_BUSY" {
				t.Fatalf("busy cover = %d/%v", resp.StatusCode, body)
			}
		})
	}
}

func TestFileCoverTimeoutUsesStableError(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	env.api.Covers = &fixedCoverProvider{err: mediaexec.ErrTaskTimeout}
	resp, body := getJSON(t, env.server.URL+"/api/files/"+env.idx.Files[0].ID+"/cover", nil)
	if resp.StatusCode != http.StatusGatewayTimeout || body["code"] != "MEDIA_TIMEOUT" {
		t.Fatalf("timeout cover = %d/%v", resp.StatusCode, body)
	}
}

func TestBrowseAndMetadataRequireAuthentication(t *testing.T) {
	env := setupAuthTestEnv(t)
	defer env.teardown()
	for _, path := range []string{
		"/api/browse?dir=.",
		"/api/browse/content?path=artist1/track1.flac",
		"/api/browse/download?path=artist1/track1.flac",
		"/api/files/" + env.idx.Files[0].ID + "/metadata",
		"/api/files/" + env.idx.Files[0].ID + "/cover",
		"/api/covers/directory?dir=artist1",
		"/api/queues/forged/items?page=1",
	} {
		resp, err := http.Get(env.server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s status = %d, want 401", path, resp.StatusCode)
		}
	}
}

func mustWriteBrowseFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}
