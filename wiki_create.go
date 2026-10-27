package main

import (
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode"
)

func validWikiParent(p string) bool {
	return strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.ContainsAny(p, "\\?#") && !strings.ContainsFunc(p, unicode.IsControl)
}
func newWikiPath(parent, name string) (string, error) {
	if !validWikiParent(parent) {
		return "", errors.New("Недопустимый путь родительской страницы")
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || len([]rune(name)) > 200 || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "/\\:#?*\"<>|") || strings.ContainsFunc(name, unicode.IsControl) {
		return "", errors.New("Введите название страницы до 200 символов, без / \\ : # ? * и служебных символов")
	}
	return path.Join(parent, name), nil
}
func (s *server) wikiNew(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("wiki")
	parent := r.FormValue("parent")
	if parent == "" {
		parent = "/"
	}
	form := map[string]string{"parent": parent, "name": r.FormValue("name"), "content": r.FormValue("content")}
	p := page{Title: "Wiki", Active: "wiki", Wiki: wikiInfo{ID: id}, Form: form, Mode: "wiki-new"}
	fail := func(status int, err error) { p.StatusCode = status; s.render(w, p, err) }
	if !validWikiParent(parent) {
		fail(400, errors.New("Недопустимый путь родителя"))
		return
	}
	err := s.c.request(r.Context(), "GET", wikiAPI(id), nil, nil, &p.Wiki)
	if err != nil {
		fail(502, err)
		return
	}
	if r.Method == "GET" {
		s.render(w, p, nil)
		return
	}
	target, err := newWikiPath(parent, form["name"])
	if err != nil {
		fail(400, err)
		return
	}
	// A subpage must have an existing parent; do not silently invent ancestors.
	if parent != "/" {
		var doc wikiDocument
		err = s.c.request(r.Context(), "GET", wikiAPI(id)+"/pages", url.Values{"path": {parent}}, nil, &doc)
		if err != nil {
			fail(400, errors.New("Не удалось найти родительскую страницу. Проверьте путь и доступ к Wiki"))
			return
		}
	}
	var existing wikiDocument
	err = s.c.request(r.Context(), "GET", wikiAPI(id)+"/pages", url.Values{"path": {target}}, nil, &existing)
	if err == nil {
		fail(409, errors.New("Страница с таким путём уже существует. Откройте её для редактирования или измените название"))
		return
	}
	var api *apiError
	if !errors.As(err, &api) || api.Status != 404 {
		fail(502, err)
		return
	}
	// Wiki API creation omits If-Match (required to update existing pages).
	// Also send If-None-Match to protect against a concurrent creator.
	err = s.c.request(r.Context(), "PUT", wikiAPI(id)+"/pages", url.Values{"path": {target}}, wikiCreate{Content: form["content"]}, nil)
	if err != nil {
		code := 502
		if errors.As(err, &api) && (api.Status == 409 || api.Status == 412) {
			code = 409
			err = errors.New("Страница уже создана другим пользователем. Ваш текст остался в форме")
		}
		if code == 502 {
			err = errors.New("TFS не подтвердил создание страницы. Текст остался в форме. Перед повтором проверьте дерево Wiki: страница могла уже создаться")
		}
		fail(code, err)
		return
	}
	http.Redirect(w, r, wikiURL(id, target), http.StatusSeeOther)
}
