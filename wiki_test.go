package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestWikiList(t *testing.T) {
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/DemoProject/_apis/wiki/wikis" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		writeJSON(w, map[string]any{"value": []any{map[string]any{"id": "docs", "name": "Документы <script>", "type": "projectWiki"}}})
	})
	out := appRequest(app, "GET", "/wiki", nil)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "/wiki/docs") || !strings.Contains(out.Body.String(), "Документы &lt;script&gt;") {
		t.Fatal("wiki list missing or unsafe")
	}
}
func TestWikiImageProxy(t *testing.T) {
	png := append([]byte{137, 80, 78, 71, 13, 10, 26, 10}, make([]byte, 100)...)
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/wiki/wikis/w") {
			writeJSON(w, map[string]any{"id": "w", "repositoryId": "repo", "mappedPath": "/docs", "versions": []any{map[string]any{"version": "wikiMaster"}}})
			return
		}
		q := r.URL.Query()
		if r.URL.Path != "/DemoProject/_apis/git/repositories/repo/items" || q.Get("path") != "/docs/.attachments/a.png" || q.Get("versionDescriptor.version") != "wikiMaster" {
			t.Errorf("bad image route %s", r.URL.String())
		}
		_, pat, _ := r.BasicAuth()
		if pat != "test-secret" {
			t.Error("missing PAT")
		}
		_, _ = w.Write(png)
	})
	out := appRequest(app, "GET", "/wiki/w/image?path=%2F.attachments%2Fa.png", nil)
	if out.Code != 200 || out.Header().Get("Content-Type") != "image/png" || !bytes.Equal(out.Body.Bytes(), png) {
		t.Fatalf("bad image: %d", out.Code)
	}
}

func TestWikiTreeAndMarkdown(t *testing.T) {
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/wiki/wikis/w/pages"):
			if r.URL.Query().Get("recursionLevel") == "full" {
				writeJSON(w, map[string]any{"path": "/", "subPages": []any{map[string]any{"path": "/Руководство", "subPages": []any{map[string]any{"path": "/Руководство/Начало"}}}}})
				return
			}
			w.Header().Set("ETag", "\"version-1\"")
			writeJSON(w, map[string]any{"path": "/Руководство/Начало", "content": "# Начало\n\n**Текст** [Сосед](Сосед)\n\n<script>alert(1)</script>"})
		case strings.HasSuffix(r.URL.Path, "/wiki/wikis/w"):
			writeJSON(w, map[string]any{"id": "w", "name": "Документы"})
		default:
			t.Errorf("unexpected %s", r.URL.String())
		}
	})
	for _, suffix := range []string{"", "&edit=1"} {
		out := appRequest(app, "GET", "/wiki/w?path="+url.QueryEscape("/Руководство/Начало")+suffix, nil)
		if out.Code != 200 {
			t.Fatalf("%d %s", out.Code, out.Body.String())
		}
		text := out.Body.String()
		for _, part := range []string{"Дерево документов", "Руководство", "aria-current=\"page\""} {
			if !strings.Contains(text, part) {
				t.Errorf("missing %s", part)
			}
		}
		if suffix == "" && !strings.Contains(text, "<strong>Текст</strong>") {
			t.Error("Markdown not rendered")
		}
		if strings.Contains(text, "<script>alert(1)</script>") {
			t.Error("unsafe raw HTML")
		}
		if suffix != "" && !strings.Contains(text, "version-1") {
			t.Error("missing ETag")
		}
	}
}
func TestWikiSaveConcurrency(t *testing.T) {
	for _, status := range []int{200, 412, 403} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "PUT" || r.Header.Get("If-Match") != "\"v1\"" || r.URL.Query().Get("path") != "/Документ" {
					t.Errorf("bad update %s %v", r.Method, r.Header)
				}
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["content"] != "Новый текст" || len(body) != 1 {
					t.Errorf("bad body %v", body)
				}
				w.WriteHeader(status)
			})
			form := url.Values{"csrf": {"csrf-test"}, "path": {"/Документ"}, "etag": {"\"v1\""}, "content": {"Новый текст"}}
			out := appRequest(app, "POST", "/wiki/w", form)
			want := 303
			if status == 412 {
				want = 409
			}
			if status == 403 {
				want = 502
			}
			if out.Code != want || calls != 1 {
				t.Errorf("status %d calls %d", out.Code, calls)
			}
			if status != 200 && !strings.Contains(out.Body.String(), "Новый текст") {
				t.Error("lost submitted text")
			}
			form.Del("csrf")
			out = appRequest(app, "POST", "/wiki/w", form)
			if out.Code != 403 || calls != 1 {
				t.Error("CSRF bypass")
			}
		})
	}
}
func TestWikiMarkdownSafety(t *testing.T) {
	text := string(wikiMarkdown("[bad](javascript:alert%281%29)\n\n[local](Next.md)\n\n![img](/.attachments/test.png)\n\n![remote](https://evil.invalid/image)\n\n<div onclick=evil()>bad</div>", "wiki-id", "/Folder/Page"))
	for _, bad := range []string{"javascript:", "onclick", `src="https://evil`} {
		if strings.Contains(text, bad) {
			t.Errorf("unsafe: %s", text)
		}
	}
	for _, good := range []string{"/wiki/wiki-id?path=%2FFolder%2FNext", "/wiki/wiki-id/image?path=%2F.attachments%2Ftest.png"} {
		if !strings.Contains(text, good) {
			t.Errorf("missing %s in %s", good, text)
		}
	}
}
func TestWikiImageRejectsTraversal(t *testing.T) {
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) { t.Error("must not fetch") })
	for _, p := range []string{"/secret", "/.attachments/../secret", "https://evil/image"} {
		out := appRequest(app, "GET", "/wiki/w/image?path="+url.QueryEscape(p), nil)
		if out.Code != 400 {
			t.Errorf("%s: %d", p, out.Code)
		}
	}
}
