package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

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
