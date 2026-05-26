package loginflow

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalAuth_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	var gotEmail, gotPassword string
	mux.HandleFunc("/api/localauth/login", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// Body parsing is trivial; just look for the strings.
		if strings.Contains(string(body), `"email":"alice@example.com"`) {
			gotEmail = "alice@example.com"
		}
		if strings.Contains(string(body), `"password":"hunter2"`) {
			gotPassword = "hunter2"
		}
		w.Header().Set("Token", "jwt-from-gateway")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	token, err := LocalAuth(context.Background(), srv.Client(), srv.URL, "alice@example.com", "hunter2")
	if err != nil {
		t.Fatalf("LocalAuth: %v", err)
	}
	if token != "jwt-from-gateway" {
		t.Errorf("token = %q, want jwt-from-gateway", token)
	}
	if gotEmail != "alice@example.com" {
		t.Errorf("gateway did not see email")
	}
	if gotPassword != "hunter2" {
		t.Errorf("gateway did not see password")
	}
}

func TestLocalAuth_InvalidCredentials(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/localauth/login", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"message":"nope"}`)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			_, err := LocalAuth(context.Background(), srv.Client(), srv.URL, "a", "b")
			if !errors.Is(err, ErrInvalidLocalCredentials) {
				t.Errorf("err = %v, want ErrInvalidLocalCredentials", err)
			}
		})
	}
}

func TestLocalAuth_OtherErrorPropagatesMessage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/localauth/login", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"message":"email is required"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := LocalAuth(context.Background(), srv.Client(), srv.URL, "a", "b")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "email is required") {
		t.Errorf("err = %v, want substring 'email is required'", err)
	}
}

func TestLocalAuth_EmptyTokenHeader(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/localauth/login", func(w http.ResponseWriter, r *http.Request) {
		// 2xx but no Token header — buggy gateway.
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := LocalAuth(context.Background(), srv.Client(), srv.URL, "a", "b")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Token header was empty") {
		t.Errorf("err = %v, want substring 'Token header was empty'", err)
	}
}

func TestLocalAuth_RejectsEmptyArgs(t *testing.T) {
	cases := []struct {
		name, url, email, password string
	}{
		{"no url", "", "a@b", "p"},
		{"no email", "http://x", "", "p"},
		{"no password", "http://x", "a@b", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LocalAuth(context.Background(), nil, tc.url, tc.email, tc.password)
			if err == nil {
				t.Error("expected error")
			}
		})
	}
}
