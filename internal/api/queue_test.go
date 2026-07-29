package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ColderCoder/ShuffleMuse/internal/index"
	"github.com/ColderCoder/ShuffleMuse/internal/playqueue"
)

func TestQueueAPICreatePageSelectReplaceDelete(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	createdResponse, created := postJSON(t, env.server.URL+"/api/queues", `{}`, nil)
	if createdResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d/%v", createdResponse.StatusCode, created)
	}
	queue := created["queue"].(map[string]interface{})
	queueID := queue["id"].(string)
	if tag, present := queue["tag"]; !present || tag != "" {
		t.Fatalf("untagged queue must expose an empty tag field: %v", queue)
	}
	if int(queue["pageSize"].(float64)) != playqueue.PageSize || int(queue["total"].(float64)) != len(env.idx.Files) {
		t.Fatalf("queue description = %v", queue)
	}
	items := created["items"].([]interface{})
	if len(items) != len(env.idx.Files) || items[0].(map[string]interface{})["queueIndex"].(float64) != 0 {
		t.Fatalf("first page = %v", items)
	}

	pageResponse, page := getJSON(t, env.server.URL+"/api/queues/"+queueID+"/items?page=1", nil)
	if pageResponse.StatusCode != http.StatusOK || page["queue"].(map[string]interface{})["id"] != queueID {
		t.Fatalf("page = %d/%v", pageResponse.StatusCode, page)
	}
	selectedID := items[len(items)-1].(map[string]interface{})["id"].(string)
	selectResponse, selected := postJSON(t, env.server.URL+"/api/queues/"+queueID+"/select", `{"fileId":"`+selectedID+`"}`, nil)
	if selectResponse.StatusCode != http.StatusOK || int(selected["queueIndex"].(float64)) != len(items)-1 {
		t.Fatalf("select existing = %d/%v", selectResponse.StatusCode, selected)
	}

	deleteResponse := doDelete(t, env.server.URL+"/api/queues/"+queueID, nil)
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", deleteResponse.StatusCode)
	}
	deleteResponse = doDelete(t, env.server.URL+"/api/queues/"+queueID, nil)
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("idempotent delete = %d", deleteResponse.StatusCode)
	}
	missingResponse, missing := getJSON(t, env.server.URL+"/api/queues/"+queueID+"/items?page=1", nil)
	if missingResponse.StatusCode != http.StatusNotFound || missing["code"] != "QUEUE_NOT_FOUND" {
		t.Fatalf("deleted page = %d/%v", missingResponse.StatusCode, missing)
	}
}

func TestQueueAPIPinTagReplaceAndStableErrors(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	pinned := env.idx.Files[0]
	if err := env.tagStore.AddTag(env.idx.Files[1].Filepath, "focus"); err != nil {
		t.Fatal(err)
	}

	response, result := postJSON(t, env.server.URL+"/api/queues", `{"tag":"focus","pinFileId":"`+pinned.ID+`"}`, nil)
	if response.StatusCode != http.StatusCreated || result["pinApplied"] != false {
		t.Fatalf("outside-tag pin create = %d/%v", response.StatusCode, result)
	}
	items := result["items"].([]interface{})
	if len(items) != 1 || items[0].(map[string]interface{})["id"] != env.idx.Files[1].ID {
		t.Fatalf("strict tag items = %v", items)
	}
	oldID := result["queue"].(map[string]interface{})["id"].(string)
	replaceResponse, replacement := postJSON(t, env.server.URL+"/api/queues", `{"tag":"focus","pinFileId":"`+env.idx.Files[1].ID+`","replaceQueueId":"`+oldID+`"}`, nil)
	if replaceResponse.StatusCode != http.StatusCreated ||
		replacement["pinApplied"] != true ||
		replacement["queue"].(map[string]interface{})["id"] == oldID {
		t.Fatalf("replace = %d/%v", replaceResponse.StatusCode, replacement)
	}
	replacementItems := replacement["items"].([]interface{})
	if len(replacementItems) != 1 || replacementItems[0].(map[string]interface{})["id"] != env.idx.Files[1].ID {
		t.Fatalf("matching tag pin items = %v", replacementItems)
	}
	oldResponse, old := getJSON(t, env.server.URL+"/api/queues/"+oldID+"/items?page=1", nil)
	if oldResponse.StatusCode != http.StatusNotFound || old["code"] != "QUEUE_NOT_FOUND" {
		t.Fatalf("old queue after replace = %d/%v", oldResponse.StatusCode, old)
	}

	badPage, badPageBody := getJSON(t, env.server.URL+"/api/queues/forged/items?page=0", nil)
	if badPage.StatusCode != http.StatusBadRequest || badPageBody["code"] != "INVALID_PAGINATION" {
		t.Fatalf("bad page = %d/%v", badPage.StatusCode, badPageBody)
	}
	forged, forgedBody := getJSON(t, env.server.URL+"/api/queues/forged/items?page=1", nil)
	if forged.StatusCode != http.StatusNotFound || forgedBody["code"] != "QUEUE_NOT_FOUND" {
		t.Fatalf("forged = %d/%v", forged.StatusCode, forgedBody)
	}
	missingFile, missingFileBody := postJSON(t, env.server.URL+"/api/queues", `{"pinFileId":"missing"}`, nil)
	if missingFile.StatusCode != http.StatusNotFound || missingFileBody["code"] != "FILE_NOT_FOUND" {
		t.Fatalf("missing file = %d/%v", missingFile.StatusCode, missingFileBody)
	}
}

func TestQueueAPIPrependsDirectDirectoryAudioInFilenameOrder(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	nestedDir := filepath.Join(env.tmpDir, "music", "artist1", "Disc 2")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "nested.flac"), []byte("fake audio data"), 0o644); err != nil {
		t.Fatal(err)
	}
	next, err := index.Scan(filepath.Join(env.tmpDir, "music"))
	if err != nil {
		t.Fatal(err)
	}
	env.api.UpdateIndex(next)
	env.idx = next

	directTagged := next.ByID[index.GenerateID(filepath.Join("artist1", "track2.flac"))]
	if directTagged == nil {
		t.Fatal("direct tagged track not indexed")
	}
	if err := env.tagStore.AddTag(directTagged.Filepath, "focus"); err != nil {
		t.Fatal(err)
	}
	createdResponse, created := postJSON(t, env.server.URL+"/api/queues", `{"tag":"focus"}`, nil)
	if createdResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d/%v", createdResponse.StatusCode, created)
	}
	oldID := created["queue"].(map[string]interface{})["id"].(string)

	response, result := postJSON(
		t,
		env.server.URL+"/api/queues/"+oldID+"/prepend-directory",
		`{"dir":"artist1"}`,
		nil,
	)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("prepend = %d/%v", response.StatusCode, result)
	}
	queue := result["queue"].(map[string]interface{})
	if queue["id"] == oldID || queue["tag"] != "focus" || int(queue["total"].(float64)) != 3 {
		t.Fatalf("replacement queue = %v", queue)
	}
	if int(result["directoryTrackCount"].(float64)) != 3 {
		t.Fatalf("directory track count = %v", result["directoryTrackCount"])
	}
	items := result["items"].([]interface{})
	if len(items) != 3 {
		t.Fatalf("items = %v", items)
	}
	wantNames := []string{"track1", "track2", "track3"}
	seen := make(map[string]bool)
	for index, raw := range items {
		item := raw.(map[string]interface{})
		if item["name"] != wantNames[index] || int(item["queueIndex"].(float64)) != index {
			t.Fatalf("item %d = %v, want %q", index, item, wantNames[index])
		}
		id := item["id"].(string)
		if seen[id] {
			t.Fatalf("duplicate directory item %q", id)
		}
		seen[id] = true
	}
	oldResponse, old := getJSON(t, env.server.URL+"/api/queues/"+oldID+"/items?page=1", nil)
	if oldResponse.StatusCode != http.StatusNotFound || old["code"] != "QUEUE_NOT_FOUND" {
		t.Fatalf("old queue after prepend = %d/%v", oldResponse.StatusCode, old)
	}
}

func TestQueueAPIPrependDirectoryValidationAndEmptyRoot(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()
	createdResponse, created := postJSON(t, env.server.URL+"/api/queues", `{}`, nil)
	if createdResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d/%v", createdResponse.StatusCode, created)
	}
	queueID := created["queue"].(map[string]interface{})["id"].(string)

	empty, emptyBody := postJSON(
		t,
		env.server.URL+"/api/queues/"+queueID+"/prepend-directory",
		`{"dir":"."}`,
		nil,
	)
	if empty.StatusCode != http.StatusUnprocessableEntity || emptyBody["code"] != "NO_AUDIO_FILES" {
		t.Fatalf("empty root = %d/%v", empty.StatusCode, emptyBody)
	}
	invalid, invalidBody := postJSON(
		t,
		env.server.URL+"/api/queues/"+queueID+"/prepend-directory",
		`{"dir":"../outside"}`,
		nil,
	)
	if invalid.StatusCode != http.StatusBadRequest || invalidBody["code"] != "INVALID_DIRECTORY" {
		t.Fatalf("invalid dir = %d/%v", invalid.StatusCode, invalidBody)
	}
	missing, missingBody := postJSON(
		t,
		env.server.URL+"/api/queues/"+queueID+"/prepend-directory",
		`{"dir":"missing"}`,
		nil,
	)
	if missing.StatusCode != http.StatusNotFound || missingBody["code"] != "NOT_FOUND" {
		t.Fatalf("missing dir = %d/%v", missing.StatusCode, missingBody)
	}
}

func TestQueueAPIStrictJSONLimit(t *testing.T) {
	env := setupTestEnv(t)
	defer env.teardown()

	unknown, unknownBody := postJSON(t, env.server.URL+"/api/queues", `{"unknown":true}`, nil)
	if unknown.StatusCode != http.StatusBadRequest || unknownBody["code"] != "INVALID_JSON" {
		t.Fatalf("unknown field = %d/%v", unknown.StatusCode, unknownBody)
	}
	request, err := http.NewRequest(http.MethodPost, env.server.URL+"/api/queues", strings.NewReader(`{"tag":"`+strings.Repeat("x", 9000)+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusRequestEntityTooLarge || payload["code"] != "REQUEST_TOO_LARGE" {
		t.Fatalf("large body = %d/%v", response.StatusCode, payload)
	}
}
