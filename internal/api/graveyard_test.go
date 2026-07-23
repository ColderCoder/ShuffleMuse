package api

import (
	"net/http"
	"net/url"
	"testing"
)

func TestTagsAndGraveyardUseCurrentSnapshot(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	online := env.idx.Files[0].Filepath
	missing := "missing/ghost.flac"
	if err := env.tagStore.AddTag(online, "favorite"); err != nil {
		t.Fatal(err)
	}
	if err := env.tagStore.AddTag(missing, "favorite"); err != nil {
		t.Fatal(err)
	}
	if err := env.tagStore.AddTag(missing, "lost"); err != nil {
		t.Fatal(err)
	}

	resp, body := getJSON(t, env.server.URL+"/api/tags", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tags status = %d", resp.StatusCode)
	}
	tagList := body["tags"].([]interface{})
	if len(tagList) != 1 {
		t.Fatalf("online tag cloud = %v, want only favorite", tagList)
	}
	favorite := tagList[0].(map[string]interface{})
	if favorite["name"] != "favorite" || favorite["count"] != float64(1) {
		t.Fatalf("favorite tag = %v, want online count 1", favorite)
	}

	resp, body = getJSON(t, env.server.URL+"/api/graveyard?page=1&limit=10", nil)
	if resp.StatusCode != http.StatusOK || body["total"] != float64(1) || body["generation"] != float64(1) {
		t.Fatalf("graveyard response = %d %v", resp.StatusCode, body)
	}
	items := body["items"].([]interface{})
	entry := items[0].(map[string]interface{})
	if entry["filepath"] != missing || entry["name"] != "ghost" {
		t.Fatalf("graveyard entry = %v", entry)
	}

	onlineDelete := doDelete(t, env.server.URL+"/api/graveyard?path="+url.QueryEscape(online), nil)
	if onlineDelete.StatusCode != http.StatusConflict {
		t.Fatalf("online graveyard delete = %d, want 409", onlineDelete.StatusCode)
	}

	missingDelete := doDelete(t, env.server.URL+"/api/graveyard?path="+url.QueryEscape(missing), nil)
	if missingDelete.StatusCode != http.StatusNoContent {
		t.Fatalf("orphan graveyard delete = %d, want 204", missingDelete.StatusCode)
	}
	files, err := env.tagStore.GetFilesByTag("favorite")
	if err != nil || len(files) != 1 || files[0] != online {
		t.Fatalf("favorite reverse links after delete = %v, err=%v", files, err)
	}
	files, err = env.tagStore.GetFilesByTag("lost")
	if err != nil || len(files) != 0 {
		t.Fatalf("lost reverse links after delete = %v, err=%v", files, err)
	}
}
