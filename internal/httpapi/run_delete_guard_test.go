package httpapi_test

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/xdanny/hyperplane-workflows/internal/store"
)

func TestDeleteSDKRunWaitsForOwnerToStop(t *testing.T) {
	_, server := newTestServer(t, nil)
	st := openStore(t)
	id := fmt.Sprintf("delete-guard-%d", time.Now().UnixNano())
	owner, err := st.OwnSDKRun(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := st.CreateRun(t.Context(), store.Run{RunID: id, DAG: "node-workflow", State: "running", OwnerPID: os.Getpid(), Options: store.JSON(fmt.Sprintf(`{"runtime":"codex-sdk","ownership":"postgres-session-v1","ownerSession":%d}`, owner.SessionID()))}); err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, want int) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s %s status %d want %d", method, path, response.StatusCode, want)
		}
	}
	request(http.MethodDelete, "/api/runs/"+id, 409)
	request(http.MethodPost, "/api/runs/"+id+"/cancel", 200)
	request(http.MethodDelete, "/api/runs/"+id, 409)
	owner.Close()
	request(http.MethodDelete, "/api/runs/"+id, 200)
	request(http.MethodDelete, "/api/runs/"+id, 404)
}
