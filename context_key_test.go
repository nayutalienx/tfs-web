package main

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPersistentContextSurvivesRestartAndRenews(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	key, err := loadContextKey()
	if err != nil {
		t.Fatal(err)
	}
	s := newContextServer()
	s.contextKey = key
	form := url.Values{"csrf": {s.token}, "url": {"https://tfs.invalid/Collection"}, "project": {"DemoProject"}, "pat": {"test-pat"}}
	out := contextRequest(s.routes(), "POST", "/context", form, nil)
	if out.Code != http.StatusSeeOther {
		t.Fatal(out.Code)
	}
	cookie := out.Result().Cookies()[0]
	if cookie.MaxAge != int(contextCookieLifetime.Seconds()) || cookie.Expires.Before(time.Now().Add(399*24*time.Hour)) {
		t.Fatal("cookie is not persistent")
	}
	restarted := newContextServer()
	restarted.contextKey, err = loadContextKey()
	if err != nil || !bytes.Equal(restarted.contextKey, key) {
		t.Fatal("key changed on restart", err)
	}
	out = contextRequest(restarted.routes(), "GET", "/context", nil, cookie)
	if out.Code != 200 || !bytes.Contains(out.Body.Bytes(), []byte("DemoProject")) {
		t.Fatal("context lost after restart")
	}
	renewed := out.Result().Cookies()
	if len(renewed) != 1 || renewed[0].Value != cookie.Value || renewed[0].MaxAge != cookie.MaxAge {
		t.Fatal("cookie not renewed")
	}
	path := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "tfs-web", "context.key")
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe key file", err)
	}
	info, err = os.Stat(filepath.Dir(path))
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("unsafe key directory", err)
	}
	out = contextRequest(restarted.routes(), "GET", "/not-found", nil, cookie)
	if out.Code != 404 || len(out.Result().Cookies()) != 1 {
		t.Fatal("normal authenticated requests must renew cookie")
	}
}

func TestContextKeyRejectsUnsafeFilesWithoutOverwrite(t *testing.T) {
	for _, mode := range []string{"permissions", "corrupt", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "context.key")
			original := bytes.Repeat([]byte{5}, 32)
			if mode == "corrupt" {
				original = []byte("invalid")
			}
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			input := path
			if mode == "permissions" {
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "symlink" {
				input = path + ".link"
				if err := os.Symlink(path, input); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := contextKeyFile(input); err == nil {
				t.Fatal("unsafe key accepted")
			}
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, original) {
				t.Fatal("existing key overwritten")
			}
		})
	}
}
