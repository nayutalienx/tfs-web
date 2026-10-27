package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestWikiCreate(t *testing.T) {
	for _, parent := range []string{"/", "/Руководство"} {
		t.Run(parent, func(t *testing.T) {
			puts := 0
			target := strings.TrimSuffix(parent, "/") + "/Начало"
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/wiki/wikis/w") {
					writeJSON(w, map[string]any{"id": "w", "name": "Документы"})
					return
				}
				if r.Method == "GET" {
					if r.URL.Query().Get("path") == parent && parent != "/" {
						writeJSON(w, map[string]any{"path": parent})
						return
					}
					w.WriteHeader(404)
					return
				}
				puts++
				if r.Method != "PUT" || r.URL.Query().Get("path") != target || r.Header.Get("If-Match") != "" || r.Header.Get("If-None-Match") != "*" {
					t.Errorf("bad creation request %s %s %v", r.Method, r.URL.String(), r.Header)
				}
				var payload map[string]string
				_ = json.NewDecoder(r.Body).Decode(&payload)
				if payload["content"] != "# Новый документ" || len(payload) != 1 {
					t.Errorf("bad payload %v", payload)
				}
				w.WriteHeader(201)
			})
			form := url.Values{"csrf": {"csrf-test"}, "parent": {parent}, "name": {"Начало"}, "content": {"# Новый документ"}}
			out := appRequest(app, "POST", "/wiki/w/new", form)
			if out.Code != 303 || puts != 1 || out.Header().Get("Location") != wikiURL("w", target) {
				t.Fatalf("bad creation %d %s", out.Code, out.Body.String())
			}
		})
	}
}
func TestWikiCreateFailuresKeepText(t *testing.T) {
	for _, mode := range []string{"existing", "missing-parent", "race", "denied"} {
		t.Run(mode, func(t *testing.T) {
			puts := 0
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/wiki/wikis/w") {
					writeJSON(w, map[string]any{"id": "w"})
					return
				}
				if r.Method == "PUT" {
					puts++
					if mode == "race" {
						w.WriteHeader(412)
					} else {
						w.WriteHeader(403)
					}
					return
				}
				if r.URL.Query().Get("path") == "/Parent" && mode != "missing-parent" || mode == "existing" {
					writeJSON(w, map[string]any{"path": r.URL.Query().Get("path")})
					return
				}
				w.WriteHeader(404)
			})
			out := appRequest(app, "POST", "/wiki/w/new", url.Values{"csrf": {"csrf-test"}, "parent": {"/Parent"}, "name": {"Child"}, "content": {"Сохранить <текст>"}})
			want := 409
			if mode == "missing-parent" {
				want = 400
			}
			if mode == "denied" {
				want = 502
			}
			if out.Code != want || !strings.Contains(out.Body.String(), "Сохранить &lt;текст&gt;") {
				t.Errorf("lost form or status: %d %s", out.Code, out.Body.String())
			}
			if (mode == "existing" || mode == "missing-parent") && puts != 0 {
				t.Error("unexpected mutation")
			}
		})
	}
}
func TestNewWikiPath(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", "a\\b", ".attachments", "x#y", "x?y", "x\ny"} {
		if _, err := newWikiPath("/", name); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	for _, parent := range []string{"relative", "/a/../b", "//other", "/a?b"} {
		if _, err := newWikiPath(parent, "Child"); err == nil {
			t.Errorf("accepted parent %q", parent)
		}
	}
}
func TestWikiCreateCSRFAndForm(t *testing.T) {
	calls := 0
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeJSON(w, map[string]any{"id": "w", "name": "Docs"})
	})
	out := appRequest(app, "POST", "/wiki/w/new", url.Values{"parent": {"/"}, "name": {"Child"}})
	if out.Code != 403 || calls != 0 {
		t.Error("CSRF bypass")
	}
	out = appRequest(app, "GET", "/wiki/w/new?parent=%2FParent", nil)
	if out.Code != 200 || !strings.Contains(out.Body.String(), `value="/Parent"`) || !strings.Contains(out.Body.String(), "Создать страницу") {
		t.Fatal("missing new page form")
	}
}
