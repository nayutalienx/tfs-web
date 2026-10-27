package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The browser owns an opaque encrypted context, never a JS-readable PAT.
const contextCookie = "tfs_context"

// Browsers cap persistent cookies at roughly 400 days. Renew on use;
// the encrypted context itself has no expiry.
const contextCookieLifetime = 400 * 24 * time.Hour

type browserContext struct {
	Config config
	CSRF   string
}

func (s *server) contextCipher() (cipher.AEAD, error) {
	b, err := aes.NewCipher(s.contextKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}
func (s *server) encodeContext(c browserContext) (string, error) {
	a, err := s.contextCipher()
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(a.Seal(nonce, nonce, data, []byte(contextCookie)))
	if len(encoded) > 3800 {
		return "", errors.New("Контекст слишком большой")
	}
	return encoded, nil
}
func (s *server) decodeContext(r *http.Request) (browserContext, bool) {
	var result browserContext
	cookie, err := r.Cookie(contextCookie)
	if err != nil || len(cookie.Value) > 3800 {
		return result, false
	}
	data, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return result, false
	}
	a, err := s.contextCipher()
	if err != nil || len(data) < a.NonceSize() {
		return result, false
	}
	plain, err := a.Open(nil, data[:a.NonceSize()], data[a.NonceSize():], []byte(contextCookie))
	if err != nil {
		return result, false
	}
	if json.Unmarshal(plain, &result) != nil || result.CSRF == "" || validateBrowserConfig(result.Config) != nil {
		return browserContext{}, false
	}
	return result, true
}
func validateBrowserConfig(c config) error {
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return errors.New("Укажите HTTPS URL коллекции TFS без логина, query и fragment")
	}
	if strings.TrimSpace(c.Project) == "" || len(c.Project) > 200 || strings.ContainsAny(c.Project, "/\\\r\n") || strings.TrimSpace(c.PAT) == "" || len(c.PAT) > 1000 || strings.ContainsAny(c.PAT, "\r\n") {
		return errors.New("Укажите проект и корректный PAT")
	}
	return nil
}
func contextHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'; img-src 'self'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("Cache-Control", "no-store")
	// Same-origin form POSTs must retain their Origin. With no-referrer,
	// browsers can serialize it as "null", failing our strict origin check.
	// Cross-origin destinations still receive no Referer.
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}
func (s *server) browserContextRoutes() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != listenAddress && r.Host != "localhost:18763" {
			http.Error(w, "Недопустимый Host", 403)
			return
		}
		contextHeaders(w)
		current, ok := s.decodeContext(r)
		if r.URL.Path == "/static/context.js" && r.Method == "GET" {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = w.Write(contextScript)
			return
		}
		if r.URL.Path == "/context" {
			if r.Method != "GET" && r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			p := page{Title: "Контекст подключения", Form: map[string]string{}}
			view := *s
			view.contextKey = nil
			view.c = newClient(config{})
			if ok {
				p.Form["url"] = current.Config.BaseURL
				p.Form["project"] = current.Config.Project
				view.c = newClient(current.Config)
			}
			if r.Method == "POST" {
				r.Body = http.MaxBytesReader(w, r.Body, 8192)
				origin := r.Header.Get("Origin")
				if r.ParseForm() != nil {
					http.Error(w, "Не удалось прочитать форму контекста", 400)
					return
				}
				if subtle.ConstantTimeCompare([]byte(r.FormValue("csrf")), []byte(s.token)) != 1 {
					http.Error(w, "Форма устарела. Откройте /context заново и повторите ввод", 403)
					return
				}
				if origin != "" && origin != "http://"+r.Host {
					http.Error(w, "Недопустимый источник формы. Откройте /context заново и повторите ввод", 403)
					return
				}
				if r.FormValue("action") == "clear" {
					http.SetCookie(w, &http.Cookie{Name: contextCookie, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
					http.Redirect(w, r, "/context", 303)
					return
				}
				cfg := config{BaseURL: strings.TrimRight(strings.TrimSpace(r.FormValue("url")), "/"), Project: strings.TrimSpace(r.FormValue("project")), PAT: strings.TrimSpace(r.FormValue("pat"))}
				// Blank PAT keeps the existing secret only for the same trusted TFS URL.
				if cfg.PAT == "" && ok && cfg.BaseURL == current.Config.BaseURL {
					cfg.PAT = current.Config.PAT
				}
				p.Form["url"] = cfg.BaseURL
				p.Form["project"] = cfg.Project
				err := validateBrowserConfig(cfg)
				if err == nil {
					b := make([]byte, 32)
					_, err = rand.Read(b)
					if err == nil {
						var value string
						value, err = s.encodeContext(browserContext{Config: cfg, CSRF: hex.EncodeToString(b)})
						if err == nil {
							setContextCookie(w, value)
							http.Redirect(w, r, "/backlog", 303)
							return
						}
					}
				}
				p.StatusCode = 400
				view.render(w, p, err)
				return
			}
			if ok {
				cookie, _ := r.Cookie(contextCookie)
				setContextCookie(w, cookie.Value)
			}
			view.render(w, p, nil)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/static/") {
			core := *s
			core.contextKey = nil
			core.c = newClient(config{})
			core.routes().ServeHTTP(w, r)
			return
		}
		if !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "Установите контекст подключения", 401)
				return
			}
			if r.Method != "GET" {
				http.Error(w, "Контекст подключения отсутствует", 401)
				return
			}
			http.Redirect(w, r, "/context", 303)
			return
		}
		cookie, _ := r.Cookie(contextCookie)
		setContextCookie(w, cookie.Value)
		scoped := server{c: newClient(current.Config), templates: s.templates, token: current.CSRF}
		scoped.routes().ServeHTTP(w, r)
	})
}

func setContextCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{Name: contextCookie, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(contextCookieLifetime.Seconds()), Expires: time.Now().Add(contextCookieLifetime)})
}
