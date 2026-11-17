package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestDecisionsCRUDWithoutBundledLabImport(t *testing.T) {
	server := setupTestServer(t)
	// The old import path matches the remaining read-by-ID route, so POST is
	// method-not-allowed. It cannot silently reload bundled personal history.
	status, body := postBody(t, server.URL+"/api/decisions/import", `{}`)
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("retired import status=%d body=%s", status, body)
	}
	const id = "sdk-decisions-crud"
	const project = "sdk-decisions-project"
	status, body = postBody(t, server.URL+"/api/decisions", `{"id":"`+id+`","title":"Keep owner decisions","project":"`+project+`","decision":"Use the reviewed SDK workflow","source":"owner"}`)
	if status != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", status, body)
	}
	body, status = getJSON(t, server.URL+"/api/decisions/"+id)
	var decision store.Decision
	if err := json.Unmarshal(body, &decision); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || decision.Project != project || decision.Source != "owner" {
		t.Fatalf("read status=%d decision=%+v", status, decision)
	}
	status, body = postBody(t, server.URL+"/api/decisions", `{"id":"`+id+`","title":"Keep owner decisions","project":"`+project+`","decision":"Use the updated SDK workflow","source":"owner"}`)
	if status != http.StatusCreated {
		t.Fatalf("update status=%d body=%s", status, body)
	}
	body, status = getJSON(t, server.URL+"/api/decisions?project="+url.QueryEscape(project))
	var listed []store.Decision
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatal(err)
	}
	if status != http.StatusOK || len(listed) != 1 || listed[0].Decision != "Use the updated SDK workflow" {
		t.Fatalf("list status=%d body=%s", status, body)
	}
	request, err := http.NewRequest(http.MethodDelete, server.URL+"/api/decisions/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("delete status=%d", response.StatusCode)
	}
	_, status = getJSON(t, server.URL+"/api/decisions/"+id)
	if status != http.StatusNotFound {
		t.Fatalf("deleted decision status=%d", status)
	}
}
