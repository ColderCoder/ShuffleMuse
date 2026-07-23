package api

import (
	"encoding/csv"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestTagExportIncludesOnlineAndMissingRecords(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	online := env.idx.Files[0].Filepath
	if err := env.tagStore.AddTag(online, "rock"); err != nil {
		t.Fatal(err)
	}
	if err := env.tagStore.AddTag(online, "favorite"); err != nil {
		t.Fatal(err)
	}
	if err := env.tagStore.AddTag("=danger.flac", "lost"); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(env.server.URL + "/api/tags/export")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	mediaType, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if err != nil || mediaType != "attachment" || params["filename"] != "shufflemuse-tags.csv" {
		t.Fatalf("Content-Disposition = %q", resp.Header.Get("Content-Disposition"))
	}
	if got := resp.Header.Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), "\xEF\xBB\xBF") {
		t.Fatal("CSV export is missing the UTF-8 BOM")
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(body), "\xEF\xBB\xBF"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %v, want header plus 2 records", rows)
	}
	if !slices.Equal(rows[0], []string{"filepath", "name", "dir", "tags", "status"}) {
		t.Fatalf("header = %v", rows[0])
	}
	if !slices.Equal(rows[1], []string{"'=danger.flac", "'=danger", ".", "lost", "missing"}) {
		t.Fatalf("missing row = %v", rows[1])
	}
	if rows[2][0] != online || rows[2][3] != "favorite;rock" || rows[2][4] != "online" {
		t.Fatalf("online row = %v", rows[2])
	}
}

func TestTagExportRequiresAuthentication(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	server := httptest.NewServer(env.api.RoutesWithAuth())
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/tags/export")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", resp.StatusCode)
	}

	cookie, err := env.api.Auth.Login("testpass", false)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/tags/export", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated status = %d, want 200", resp.StatusCode)
	}
}
