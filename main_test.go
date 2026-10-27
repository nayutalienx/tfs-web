package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestBacklogAndAuthentication(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "" || pass != "secret" {
			t.Error("incorrect authentication")
		}
		if r.URL.Query().Get("api-version") != "6.0" {
			t.Error("incorrect API version")
		}
		switch r.URL.Path {
		case "/collection/Project/_apis/wit/wiql":
			fmt.Fprint(w, `{"workItems":[{"id":42}]}`)
		case "/collection/Project/_apis/wit/workitems":
			fmt.Fprint(w, `{"value":[{"id":42,"fields":{"System.Title":"Task"}}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	c := newClient(config{BaseURL: upstream.URL + "/collection", Project: "Project", PAT: "secret"})
	items, err := c.backlog(context.Background())
	if err != nil || len(items) != 1 || items[0].ID != 42 {
		t.Fatalf("items=%v err=%v", items, err)
	}
}

func TestMutationCSRFAndPatch(t *testing.T) {
	called := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `{"id":42,"rev":3,"fields":{"System.Title":"Старый заголовок"}}`)
			return
		}
		called++
		if r.Method != "PATCH" || r.Header.Get("Content-Type") != "application/json-patch+json" {
			t.Errorf("invalid patch request")
		}
		var p patch
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil || len(p) != 2 || p[0]["op"] != "test" || p[0]["path"] != "/rev" || p[0]["value"] != float64(3) {
			t.Errorf("missing revision guard: %v %v", p, err)
		}
		fmt.Fprint(w, `{"id":42}`)
	}))
	defer upstream.Close()
	s := server{c: newClient(config{BaseURL: upstream.URL, Project: "P", PAT: "secret"}), templates: tmpl, token: "test-token"}
	send := func(token string) *httptest.ResponseRecorder {
		f := url.Values{"csrf": {token}, "title": {"Новое название"}, "rev": {"3"}}
		req := httptest.NewRequest("POST", "/workitem/42/update", strings.NewReader(f.Encode()))
		req.Host = listenAddress
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		return rec
	}
	if got := send("wrong").Code; got != http.StatusForbidden || called != 0 {
		t.Fatalf("csrf status=%d calls=%d", got, called)
	}
	if got := send("test-token").Code; got != http.StatusSeeOther || called != 1 {
		t.Fatalf("valid status=%d calls=%d", got, called)
	}
}

func TestRawFile(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "plain file content") }))
	defer upstream.Close()
	c := newClient(config{BaseURL: upstream.URL, Project: "P", PAT: "secret"})
	var file fileContent
	if err := c.request(context.Background(), "GET", "git/repositories/r/items", nil, nil, &file); err != nil || file.Text != "plain file content" {
		t.Fatalf("file=%q err=%v", file.Text, err)
	}
}
func TestEscapesUntrustedContent(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":42,"fields":{"System.Title":"<script>alert(1)</script>"}}`)
	}))
	defer upstream.Close()
	s := server{c: newClient(config{BaseURL: upstream.URL, Project: "P", PAT: "secret"}), templates: tmpl}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/workitem/42", nil)
	req.Host = listenAddress
	s.routes().ServeHTTP(rec, req)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "<script>") || !strings.Contains(rec.Body.String(), "&lt;script&gt;") {
		t.Fatal("untrusted content was not escaped")
	}
}
func TestRejectsRedirect(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "https://example.com", 302) }))
	defer upstream.Close()
	c := newClient(config{BaseURL: upstream.URL, Project: "P", PAT: "secret"})
	var out any
	if c.request(context.Background(), "GET", "git/repositories", nil, nil, &out) == nil {
		t.Fatal("redirect must be rejected")
	}
}
