package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func contextRequest(app http.Handler, method, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+listenAddress+path, strings.NewReader(form.Encode()))
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	out := httptest.NewRecorder()
	app.ServeHTTP(out, r)
	return out
}
func newContextServer() *server {
	return &server{c: newClient(config{}), templates: tmpl, token: "bootstrap-csrf", contextKey: bytes.Repeat([]byte{7}, 32)}
}
func TestBrowserContextSetupAndIsolation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pat, _ := r.BasicAuth()
		if pat != "alice-pat" && pat != "bob-pat" {
			t.Errorf("bad PAT %s", pat)
		}
		if !strings.HasPrefix(r.URL.Path, "/"+strings.TrimSuffix(pat, "-pat")+"/") {
			t.Errorf("wrong project %s", r.URL.Path)
		}
		writeJSON(w, list[repository]{Value: []repository{{ID: "r", Name: strings.TrimSuffix(pat, "-pat") + "-repo"}}})
	}))
	defer upstream.Close()
	s := newContextServer()
	app := s.routes()
	out := contextRequest(app, "GET", "/repos", nil, nil)
	if out.Code != 303 || out.Header().Get("Location") != "/context" {
		t.Fatal("missing-context gate failed")
	}
	out = contextRequest(app, "GET", "/context", nil, nil)
	if out.Code != 200 || !strings.Contains(out.Body.String(), "URL коллекции") {
		t.Fatal("setup missing")
	}
	cookies := map[string]*http.Cookie{}
	for _, person := range []string{"alice", "bob"} {
		out = contextRequest(app, "POST", "/context", url.Values{"csrf": {"bootstrap-csrf"}, "url": {upstream.URL}, "project": {person}, "pat": {person + "-pat"}}, nil)
		if out.Code != 303 {
			t.Fatalf("login %d %s", out.Code, out.Body.String())
		}
		cookie := out.Result().Cookies()[0]
		cookies[person] = cookie
		if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || strings.Contains(cookie.Value, person+"-pat") {
			t.Fatal("unsafe cookie")
		}
	}
	for person, cookie := range cookies {
		out = contextRequest(app, "GET", "/repos", nil, cookie)
		if out.Code != 200 || !strings.Contains(out.Body.String(), person+"-repo") {
			t.Fatalf("context isolation failed %s %d", person, out.Code)
		}
		out = contextRequest(app, "GET", "/context", nil, cookie)
		if strings.Contains(out.Body.String(), person+"-pat") {
			t.Fatal("secret echoed")
		}
	}
}
func TestBrowserContextTamperingAndChangedKey(t *testing.T) {
	s := newContextServer()
	valid := browserContext{Config: config{BaseURL: "https://tfs.invalid/Collection", Project: "DemoProject", PAT: "secret"}, CSRF: "csrf"}
	value, err := s.encodeContext(valid)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"tamper", "restart"} {
		t.Run(mode, func(t *testing.T) {
			v := value
			testServer := newContextServer()
			if mode == "tamper" {
				v = "x" + v[1:]
			}
			if mode == "restart" {
				testServer.contextKey = bytes.Repeat([]byte{3}, 32)
			}
			out := contextRequest(testServer.routes(), "GET", "/repos", nil, &http.Cookie{Name: contextCookie, Value: v})
			if out.Code != 303 {
				t.Fatal("invalid context accepted")
			}
		})
	}
}
func TestBrowserContextCSRFAndClear(t *testing.T) {
	s := newContextServer()
	app := s.routes()
	form := url.Values{"url": {"https://tfs.invalid/Collection"}, "project": {"DemoProject"}, "pat": {"secret"}}
	out := contextRequest(app, "POST", "/context", form, nil)
	if out.Code != 403 {
		t.Fatal("missing CSRF allowed")
	}
	form.Set("csrf", s.token)
	out = contextRequest(app, "POST", "/context", form, nil)
	cookie := out.Result().Cookies()[0]
	form.Set("action", "clear")
	out = contextRequest(app, "POST", "/context", form, cookie)
	if out.Code != 303 || out.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("clear failed")
	}
	r := httptest.NewRequest("POST", "http://"+listenAddress+"/context", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://evil.invalid")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("bad origin allowed")
	}
}
func TestContextChangeDoesNotSendPATToNewURL(t *testing.T) {
	s := newContextServer()
	value, _ := s.encodeContext(browserContext{Config: config{BaseURL: "https://one.invalid", Project: "DemoProject", PAT: "secret"}, CSRF: "csrf"})
	cookie := &http.Cookie{Name: contextCookie, Value: value}
	out := contextRequest(s.routes(), "POST", "/context", url.Values{"csrf": {s.token}, "url": {"https://two.invalid"}, "project": {"DemoProject"}}, cookie)
	if out.Code != 400 || strings.Contains(out.Body.String(), "secret") {
		t.Fatal("old secret reused or echoed")
	}
	out = contextRequest(s.routes(), "POST", "/context", url.Values{"csrf": {s.token}, "url": {"https://one.invalid"}, "project": {"Other"}}, cookie)
	if out.Code != 303 {
		t.Fatal("same-URL secret preservation failed")
	}
}

func TestContextRequiredForAPIAndMutations(t *testing.T) {
	app := newContextServer().routes()
	for _, tc := range []struct{ method, path string }{{"GET", "/api/search?q=test"}, {"POST", "/workitem/42/update"}} {
		out := contextRequest(app, tc.method, tc.path, nil, nil)
		if out.Code != 401 {
			t.Errorf("missing context: %s %d", tc.path, out.Code)
		}
	}
	out := contextRequest(app, "GET", "/static/context.js", nil, nil)
	if out.Code != 200 || !bytes.Equal(out.Body.Bytes(), contextScript) || !strings.Contains(out.Header().Get("Content-Security-Policy"), "script-src 'self'") {
		t.Error("context script unavailable or unsafe CSP")
	}
}

func TestContextSameOriginFormPolicy(t *testing.T) {
	for _, host := range []string{listenAddress, "localhost:18763"} {
		s := newContextServer()
		app := s.routes()
		get := httptest.NewRequest("GET", "http://"+host+"/context", nil)
		page := httptest.NewRecorder()
		app.ServeHTTP(page, get)
		if page.Header().Get("Referrer-Policy") != "same-origin" {
			t.Fatal("policy can turn form Origin into null")
		}
		for _, origin := range []string{"http://" + host, "null", "https://evil.invalid"} {
			form := url.Values{"csrf": {s.token}, "url": {"https://tfs.invalid/Collection"}, "project": {"DemoProject"}, "pat": {"test-pat"}}
			r := httptest.NewRequest("POST", "http://"+host+"/context", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Origin", origin)
			out := httptest.NewRecorder()
			app.ServeHTTP(out, r)
			want := 403
			if origin == "http://"+host {
				want = 303
			}
			if out.Code != want {
				t.Errorf("host %s origin %s: %d", host, origin, out.Code)
			}
		}
	}
}
