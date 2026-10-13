package sidecardemo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDemoAPIServesUsersAndDeletes(t *testing.T) {
	srv := httptest.NewServer(NewServer().Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/users")
	if err != nil {
		t.Fatal(err)
	}
	var users []user
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(users) != 4 || !strings.Contains(users[0].Email, "@") {
		t.Fatalf("users = %+v, want the four seeded users with emails", users)
	}

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/users/1", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("DELETE status = %d, want 204 (the sidecar, not the API, is what refuses it)", resp.StatusCode)
	}

	resp, err = http.Post(srv.URL+"/users", "application/json", strings.NewReader(`{"name":"Ada","email":"ada@example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("POST status = %d, want 201", resp.StatusCode)
	}
}
