package directorysync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	cip "github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/hoophq/hoop/gateway/models"
	"google.golang.org/api/option"
)

func TestAuth0Provider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/oauth/token":
			_ = r.ParseForm()
			if r.FormValue("grant_type") != "client_credentials" || !strings.HasSuffix(r.FormValue("audience"), "/api/v2/") {
				t.Errorf("token request: %v", r.Form)
			}
			fmt.Fprint(w, `{"access_token":"tok","token_type":"Bearer","expires_in":3600}`)
		case r.Header.Get("Authorization") != "Bearer tok":
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/api/v2/roles":
			fmt.Fprint(w, `{"roles":[{"id":"rol_1","name":"dba"},{"id":"rol_2","name":"sre"}],"total":2}`)
		case r.URL.Path == "/api/v2/roles/rol_1/users":
			fmt.Fprint(w, `{"users":[{"user_id":"auth0|1","email":"ana@example.com","name":"Ana"},{"user_id":"auth0|2","email":"bob@example.com","name":"Bob"}],"total":2}`)
		case r.URL.Path == "/api/v2/users" && r.URL.Query().Get("q") == "blocked:true":
			fmt.Fprint(w, `{"users":[{"user_id":"auth0|2"}],"total":1}`)
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p := newAuth0Provider(Auth0Settings{Domain: srv.URL, ClientID: "id", ClientSecret: "secret"})
	groups, err := p.ListGroups(context.Background())
	if err != nil || !slices.Equal(groups, []Group{{ID: "rol_1", Name: "dba"}, {ID: "rol_2", Name: "sre"}}) {
		t.Fatalf("groups = %+v, err %v", groups, err)
	}
	members, err := p.ListMembers(context.Background(), "rol_1")
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	want := []User{
		{ExternalID: "auth0|1", Email: "ana@example.com", Name: "Ana", Active: true},
		{ExternalID: "auth0|2", Email: "bob@example.com", Name: "Bob", Active: false},
	}
	if !slices.Equal(members, want) {
		t.Fatalf("members = %+v, want %+v", members, want)
	}
}

func TestGoogleProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/admin/directory/v1/groups":
			if r.URL.Query().Get("customer") != "my_customer" {
				t.Errorf("customer = %q", r.URL.Query().Get("customer"))
			}
			fmt.Fprint(w, `{"groups":[{"id":"g1","email":"dba@example.com","name":"DBA"}]}`)
		case "/admin/directory/v1/groups/g1/members":
			if r.URL.Query().Get("includeDerivedMembership") != "true" {
				t.Errorf("nested members not requested")
			}
			fmt.Fprint(w, `{"members":[
				{"id":"1","email":"ana@example.com","type":"USER","status":"ACTIVE"},
				{"id":"2","email":"bob@example.com","type":"USER","status":"SUSPENDED"},
				{"id":"3","email":"nested@example.com","type":"GROUP","status":"ACTIVE"}]}`)
		default:
			t.Errorf("unexpected request %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	p, err := newGoogleProvider(context.Background(), GoogleSettings{},
		option.WithEndpoint(srv.URL+"/"), option.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	groups, err := p.ListGroups(context.Background())
	if err != nil || !slices.Equal(groups, []Group{{ID: "g1", Name: "dba@example.com"}}) {
		t.Fatalf("groups = %+v, err %v", groups, err)
	}
	members, err := p.ListMembers(context.Background(), "g1")
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	want := []User{
		{ExternalID: "1", Email: "ana@example.com", Name: "ana@example.com", Active: true},
		{ExternalID: "2", Email: "bob@example.com", Name: "bob@example.com", Active: false},
	}
	if !slices.Equal(members, want) {
		t.Fatalf("members = %+v, want %+v", members, want)
	}
}

func TestCognitoProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["UserPoolId"] != "us-east-1_pool" {
			t.Errorf("user pool = %v", body["UserPoolId"])
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		switch r.Header.Get("X-Amz-Target") {
		case "AWSCognitoIdentityProviderService.ListGroups":
			fmt.Fprint(w, `{"Groups":[{"GroupName":"dba"}]}`)
		case "AWSCognitoIdentityProviderService.ListUsersInGroup":
			fmt.Fprint(w, `{"Users":[
				{"Username":"ana","Enabled":true,"Attributes":[{"Name":"sub","Value":"s-1"},{"Name":"email","Value":"ana@example.com"},{"Name":"name","Value":"Ana"}]},
				{"Username":"bob","Enabled":false,"Attributes":[{"Name":"email","Value":"bob@example.com"}]}]}`)
		default:
			t.Errorf("unexpected target %q", r.Header.Get("X-Amz-Target"))
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	p, err := newCognitoProvider(context.Background(),
		CognitoSettings{Region: "us-east-1", UserPoolID: "us-east-1_pool", AccessKeyID: "AKID", SecretAccessKey: "secret"},
		func(o *cip.Options) { o.BaseEndpoint = aws.String(srv.URL) })
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	groups, err := p.ListGroups(context.Background())
	if err != nil || !slices.Equal(groups, []Group{{ID: "dba", Name: "dba"}}) {
		t.Fatalf("groups = %+v, err %v", groups, err)
	}
	members, err := p.ListMembers(context.Background(), "dba")
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	want := []User{
		{ExternalID: "s-1", Email: "ana@example.com", Name: "Ana", Active: true},
		{ExternalID: "bob", Email: "bob@example.com", Name: "bob@example.com", Active: false},
	}
	if !slices.Equal(members, want) {
		t.Fatalf("members = %+v, want %+v", members, want)
	}
}

func TestSettings(t *testing.T) {
	if err := ValidateSettings(models.ProvisioningSourceAuth0, json.RawMessage(`{"domain":"x.auth0.com","client_id":"a"}`)); err == nil {
		t.Errorf("auth0 without a secret must be refused")
	}
	if err := ValidateSettings(models.ProvisioningSourceAuth0, json.RawMessage(`{"domain":"x","client_id":"a","client_secret":"b","typo":1}`)); err == nil {
		t.Errorf("an unknown field must be refused")
	}
	if err := ValidateSettings(models.ProvisioningSourceCognito, json.RawMessage(`{"region":"us-east-1","user_pool_id":"p","access_key_id":"a"}`)); err == nil {
		t.Errorf("cognito with half a key pair must be refused")
	}
	if err := ValidateSettings("okta", json.RawMessage(`{}`)); err == nil {
		t.Errorf("an unknown provider must be refused")
	}

	stored := &models.DirectorySyncConfig{
		Provider: models.ProvisioningSourceAuth0,
		Settings: json.RawMessage(`{"domain":"x","client_id":"a","client_secret":"s3cret"}`),
	}
	redacted := RedactSettings(models.ProvisioningSourceAuth0, stored.Settings)
	if strings.Contains(string(redacted), "s3cret") {
		t.Fatalf("redacted settings leak the secret: %s", redacted)
	}
	merged, err := MergeSettings(models.ProvisioningSourceAuth0, redacted, stored)
	if err != nil || !strings.Contains(string(merged), "s3cret") {
		t.Fatalf("merge kept %s, err %v; want the stored secret back", merged, err)
	}
	if _, err := MergeSettings(models.ProvisioningSourceGoogle, json.RawMessage(`{"service_account_json":"********","admin_email":"a"}`), stored); err == nil {
		t.Fatalf("a placeholder with no stored secret of that provider must be refused")
	}
}
