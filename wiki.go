package main

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	xhtml "golang.org/x/net/html"
)

type wikiInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	RepositoryID string `json:"repositoryId"`
	MappedPath   string `json:"mappedPath"`
	Versions     []struct {
		Version string `json:"version"`
	} `json:"versions"`
}
type wikiNode struct {
	ID       int        `json:"id"`
	Path     string     `json:"path"`
	Order    int        `json:"order"`
	Children []wikiNode `json:"subPages"`
	URL      string
	Name     string
	Selected bool
	Open     bool
}
type wikiDocument struct {
	ID      int    `json:"id"`
	Path    string `json:"path"`
	Content string `json:"content"`
	ETag    string `json:"-"`
}
type wikiUpdate struct {
	Content string `json:"content"`
	ETag    string `json:"-"`
}
type wikiCreate struct {
	Content string `json:"content"`
}

func wikiURL(id, p string) string {
	return "/wiki/" + url.PathEscape(id) + "?" + url.Values{"path": {p}}.Encode()
}
func wikiAPI(id string) string { return "wiki/wikis/" + url.PathEscape(id) }
func wikiTree(nodes []wikiNode, id, selected string, depth int, count *int) ([]wikiNode, error) {
	if depth > 50 {
		return nil, errors.New("Дерево Wiki глубже 50 уровней")
	}
	for i := range nodes {
		*count++
		if *count > 10000 {
			return nil, errors.New("Дерево Wiki больше 10000 страниц")
		}
		n := &nodes[i]
		n.Name = path.Base(n.Path)
		n.URL = wikiURL(id, n.Path)
		n.Selected = n.Path == selected
		var err error
		n.Children, err = wikiTree(n.Children, id, selected, depth+1, count)
		n.Open = n.Selected || strings.HasPrefix(selected, strings.TrimSuffix(n.Path, "/")+"/")
		if err != nil {
			return nil, err
		}
	}
	return nodes, nil
}

// Markdown HTML is disabled by goldmark. Rewrite document links to this site,
// permit only HTTP(S) external links and proxy only wiki-owned raster images.
func wikiMarkdown(content, id, current string) template.HTML {
	var buf bytes.Buffer
	md := goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithParserOptions(parser.WithAutoHeadingID()))
	if err := md.Convert([]byte(content), &buf); err != nil {
		return template.HTML(template.HTMLEscapeString(content))
	}
	doc, err := xhtml.Parse(strings.NewReader(buf.String()))
	if err != nil {
		return ""
	}
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode {
			if n.Data == "a" || n.Data == "img" {
				key := "href"
				if n.Data == "img" {
					key = "src"
				}
				attrs := []xhtml.Attribute{}
				for _, a := range n.Attr {
					if a.Key == "alt" {
						attrs = append(attrs, a)
					}
					if a.Key != key {
						continue
					}
					u, e := url.Parse(a.Val)
					if e != nil || u.User != nil {
						continue
					}
					if u.IsAbs() || u.Host != "" {
						if n.Data == "a" && (u.Scheme == "https" || u.Scheme == "http") {
							attrs = append(attrs, a, xhtml.Attribute{Key: "rel", Val: "noreferrer noopener"})
						}
						continue
					}
					if n.Data == "img" {
						p := path.Clean("/" + strings.TrimPrefix(u.Path, "/"))
						if strings.HasPrefix(p, "/.attachments/") {
							attrs = append(attrs, xhtml.Attribute{Key: "src", Val: "/wiki/" + url.PathEscape(id) + "/image?" + url.Values{"path": {p}}.Encode()}, xhtml.Attribute{Key: "class", Val: "screenshot"}, xhtml.Attribute{Key: "loading", Val: "lazy"})
						}
					} else if u.Path == "" {
						attrs = append(attrs, xhtml.Attribute{Key: "href", Val: "#" + u.Fragment})
					} else {
						p := u.Path
						if !strings.HasPrefix(p, "/") {
							p = path.Join(path.Dir(current), p)
						}
						p = strings.TrimSuffix(p, ".md")
						local := wikiURL(id, path.Clean("/"+strings.TrimPrefix(p, "/")))
						if u.Fragment != "" {
							local += "#" + url.PathEscape(u.Fragment)
						}
						attrs = append(attrs, xhtml.Attribute{Key: "href", Val: local})
					}
				}
				n.Attr = attrs
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	var out bytes.Buffer
	var body *xhtml.Node
	var find func(*xhtml.Node)
	find = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && n.Data == "body" {
			body = n
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			find(c)
		}
	}
	find(doc)
	if body != nil {
		for c := body.FirstChild; c != nil; c = c.NextSibling {
			_ = xhtml.Render(&out, c)
		}
	}
	return template.HTML(out.String()) // goldmark disables raw HTML; URLs rewritten above.
}
func (s *server) wikiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /wiki", func(w http.ResponseWriter, r *http.Request) {
		p := page{Title: "Wiki", Active: "wiki"}
		var result list[wikiInfo]
		err := s.c.request(r.Context(), "GET", "wiki/wikis", nil, nil, &result)
		p.Wikis = result.Value
		s.render(w, p, err)
	})
	mux.HandleFunc("GET /wiki/{wiki}", s.wikiPage)
	mux.HandleFunc("POST /wiki/{wiki}", s.wikiSave)
	mux.HandleFunc("GET /wiki/{wiki}/new", s.wikiNew)
	mux.HandleFunc("POST /wiki/{wiki}/new", s.wikiNew)
	mux.HandleFunc("GET /wiki/{wiki}/image", s.wikiImage)
}
func (s *server) wikiPage(w http.ResponseWriter, r *http.Request) {
	p := page{Title: "Wiki", Active: "wiki", WikiEdit: r.URL.Query().Get("edit") == "1"}
	id := r.PathValue("wiki")
	base := wikiAPI(id)
	selected := r.URL.Query().Get("path")
	if selected == "" {
		selected = "/"
	}
	err := s.c.request(r.Context(), "GET", base, nil, nil, &p.Wiki)
	var root wikiNode
	if err == nil {
		err = s.c.request(r.Context(), "GET", base+"/pages", url.Values{"path": {"/"}, "recursionLevel": {"full"}}, nil, &root)
	}
	if err == nil {
		count := 0
		p.WikiTree, err = wikiTree(root.Children, id, selected, 0, &count)
	}
	if err == nil && selected != "/" {
		err = s.c.request(r.Context(), "GET", base+"/pages", url.Values{"path": {selected}, "includeContent": {"true"}}, nil, &p.WikiDoc)
		if err == nil {
			p.WikiHTML = wikiMarkdown(p.WikiDoc.Content, id, p.WikiDoc.Path)
			p.ParentPath = path.Dir(p.WikiDoc.Path)
		}
	}
	s.render(w, p, err)
}
func (s *server) wikiSave(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("wiki")
	p := r.FormValue("path")
	etag := r.FormValue("etag")
	content := r.FormValue("content")
	if p == "" || p == "/" || etag == "" || strings.ContainsAny(etag, "\r\n") {
		formError(w, "Для сохранения откройте существующую страницу в редакторе")
		return
	}
	update := wikiUpdate{Content: content, ETag: etag}
	err := s.c.request(r.Context(), "PUT", wikiAPI(id)+"/pages", url.Values{"path": {p}}, update, nil)
	if err != nil {
		code := http.StatusBadGateway
		var api *apiError
		if errors.As(err, &api) && (api.Status == 409 || api.Status == 412) {
			code = http.StatusConflict
			err = errors.New("Страница изменилась в TFS. Ваш текст сохранён ниже: скопируйте его перед обновлением страницы")
		}
		view := page{Title: "Wiki", Active: "wiki", Wiki: wikiInfo{ID: id}, WikiDoc: wikiDocument{Path: p, Content: content, ETag: etag}, WikiEdit: true, StatusCode: code}
		s.render(w, view, err)
		return
	}
	http.Redirect(w, r, wikiURL(id, p), http.StatusSeeOther)
}
func (s *server) wikiImage(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if !strings.HasPrefix(p, "/.attachments/") || path.Clean(p) != p {
		formError(w, "Недопустимый путь вложения Wiki")
		return
	}
	var info wikiInfo
	err := s.c.request(r.Context(), "GET", wikiAPI(r.PathValue("wiki")), nil, nil, &info)
	if err != nil {
		s.fail(w, err)
		return
	}
	if info.RepositoryID == "" || len(info.Versions) == 0 {
		s.fail(w, fmt.Errorf("Wiki не содержит метаданных репозитория"))
		return
	}
	q := url.Values{"path": {path.Join("/", info.MappedPath, p)}, "versionDescriptor.version": {strings.TrimPrefix(info.Versions[0].Version, "refs/heads/")}, "versionDescriptor.versionType": {"branch"}, "$format": {"octetStream"}}
	var image imageContent
	err = s.c.request(r.Context(), "GET", "git/repositories/"+url.PathEscape(info.RepositoryID)+"/items", q, nil, &image)
	if err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", image.Type)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	_, _ = w.Write(image.Data)
}
