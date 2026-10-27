package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type config struct {
	BaseURL string `json:"baseUrl"`
	Project string `json:"project"`
	PAT     string `json:"pat"`
}

type client struct {
	cfg        config
	http       *http.Client
	identityMu sync.Mutex
	user       identity
}

func newClient(c config) *client {
	return &client{cfg: c, http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *client) request(ctx context.Context, method, path string, q url.Values, body any, out any) error {
	if q == nil {
		q = url.Values{}
	}
	if q.Get("api-version") == "" {
		q.Set("api-version", "6.0")
	}
	if _, ok := out.(*fileContent); ok {
		// Force raw content: a source JSON file containing a "content" key
		// must never be mistaken for an API response envelope.
		q.Set("$format", "text")
	}
	var data io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return e
		}
		data = bytes.NewReader(b)
	}
	u := c.cfg.BaseURL + "/" + url.PathEscape(c.cfg.Project) + "/_apis/" + path + "?" + q.Encode()
	req, e := http.NewRequestWithContext(ctx, method, u, data)
	if e != nil {
		return e
	}
	req.SetBasicAuth("", c.cfg.PAT)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		if update, ok := body.(wikiUpdate); ok {
			req.Header.Set("If-Match", update.ETag)
		}
		if _, ok := body.(wikiCreate); ok {
			req.Header.Set("If-None-Match", "*")
		}
		if _, ok := body.(patch); ok {
			req.Header.Set("Content-Type", "application/json-patch+json")
		}
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return fmt.Errorf("TFS недоступен: %w", e)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if parts := strings.SplitN(resp.Header.Get("X-Vss-Userdata"), ":", 2); len(parts) == 2 && parts[0] != "" {
			c.identityMu.Lock()
			c.user = identity{ID: parts[0], Name: parts[1], UniqueName: parts[1]}
			c.identityMu.Unlock()
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{Status: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if doc, ok := out.(*wikiDocument); ok {
		doc.ETag = resp.Header.Get("ETag")
	}
	if target, ok := out.(*imageContent); ok {
		return target.read(resp.Body)
	}
	if target, ok := out.(*fileContent); ok {
		b, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
		if err != nil {
			return err
		}
		if len(b) > 2<<20 {
			return errors.New("Файл больше 2 МиБ")
		}
		if bytes.IndexByte(b, 0) >= 0 {
			return errors.New("Бинарный файл нельзя показать как текст")
		}
		target.Text = string(b)
		return nil
	}
	if e = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out); e != nil {
		return fmt.Errorf("неверный ответ TFS: %w", e)
	}
	return nil
}

type workItem struct {
	ID        int            `json:"id"`
	Rev       int            `json:"rev"`
	Fields    map[string]any `json:"fields"`
	Relations []itemRelation `json:"relations"`
}
type repository struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DefaultBranch string `json:"defaultBranch"`
	Project       struct {
		ID string `json:"id"`
	} `json:"project"`
}
type pullRequest struct {
	ID                    int        `json:"pullRequestId"`
	Title                 string     `json:"title"`
	Status                string     `json:"status"`
	Source                string     `json:"sourceRefName"`
	Target                string     `json:"targetRefName"`
	Description           string     `json:"description"`
	Repository            repository `json:"repository"`
	CreatedBy             identity   `json:"createdBy"`
	Reviewers             []reviewer `json:"reviewers"`
	IsDraft               bool       `json:"isDraft"`
	MergeStatus           string     `json:"mergeStatus"`
	LastMergeSourceCommit commitRef  `json:"lastMergeSourceCommit"`
}
type list[T any] struct {
	Value []T `json:"value"`
}

func (c *client) backlog(ctx context.Context) ([]workItem, error) {
	var result struct {
		WorkItems []struct {
			ID int `json:"id"`
		} `json:"workItems"`
	}
	query := map[string]string{"query": "SELECT [System.Id] FROM WorkItems WHERE [System.TeamProject] = @project AND [System.AssignedTo] = @Me AND [System.CreatedDate] >= @Today - 60 AND [System.State] NOT IN ('Выполнено', 'Удалено') ORDER BY [System.ChangedDate] DESC"}
	if err := c.request(ctx, "POST", "wit/wiql", url.Values{"$top": {"100"}}, query, &result); err != nil {
		return nil, err
	}
	if len(result.WorkItems) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(result.WorkItems))
	for _, w := range result.WorkItems {
		ids = append(ids, strconv.Itoa(w.ID))
	}
	var items list[workItem]
	err := c.request(ctx, "GET", "wit/workitems", url.Values{"ids": {strings.Join(ids, ",")}}, nil, &items)
	return items.Value, err
}

type page struct {
	ContextURL, ContextProject string
	Wikis                      []wikiInfo
	Wiki                       wikiInfo
	WikiTree                   []wikiNode
	WikiDoc                    wikiDocument
	WikiHTML                   template.HTML
	WikiEdit                   bool
	PRStatus                   string
	ImageBase                  string
	Title, Error, Repo         string
	Items                      []workItem
	Item                       workItem
	Repos                      []repository
	PRs                        []pullRequest
	PR                         pullRequest
	Branches                   []struct {
		Name string `json:"name"`
	}
	Content                             string
	Token                               string
	Types                               []itemType
	Threads                             []thread
	Files                               []gitItem
	Path, Branch, Search, Mode          string
	Comments                            []workComment
	Changes                             []gitChange
	Notice                              string
	Active                              string
	Groups                              []taskGroup
	StateFilter, TypeFilter             string
	States                              []itemState
	Form                                map[string]string
	StatusCode                          int
	Repository                          repository
	Mine                                []pullRequest
	Me                                  identity
	Skip                                int
	HasNext                             bool
	Prev, Next                          string
	ParentPath                          string
	Lines                               []codeLine
	Diff                                []diffLine
	OldPath, SourceCommit, TargetCommit string
	Iteration                           int
	BeforeLines                         []codeLine
	Children                            []itemNode
	Parent                              *workItem
	FileDiffs                           []fileDiff
	LinkedItems, LinkCandidates         []workItem
	LinkSearch, LinkNotice              string
	LinksLoaded, CandidatesLoaded       bool
}

//go:embed templates/*.html
var templateFiles embed.FS

//go:embed static/ui.js
var uiScript []byte

//go:embed static/search.js
var searchScript []byte

//go:embed static/context.js
var contextScript []byte
var tmpl = template.Must(template.New("layout.html").Funcs(template.FuncMap{
	"safeRich": safeRich, "plain": plainText, "branch": func(s string) string { return strings.TrimPrefix(s, "refs/heads/") },
	"field": fieldText, "person": personName, "vote": voteLabel, "base": filepath.Base,
	"shortType":  shortType,
	"typeClass":  typeClass,
	"stateClass": stateClass, "prStatusClass": prStatusClass,
}).ParseFS(templateFiles, "templates/*.html"))

type server struct {
	contextKey []byte
	c          *client
	templates  *template.Template
	token      string
}

func (s *server) render(w http.ResponseWriter, p page, err error) {
	if s.c != nil {
		p.ContextURL = s.c.cfg.BaseURL
		p.ContextProject = s.c.cfg.Project
	}
	p.ImageBase = s.c.cfg.BaseURL
	p.Token = s.token
	if p.Active == "" {
		p.Active = activeSection(p.Title)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err != nil {
		p.Error = err.Error()
		if p.StatusCode == 0 {
			p.StatusCode = http.StatusBadGateway
		}
	}
	if p.StatusCode != 0 {
		w.WriteHeader(p.StatusCode)
	}
	if e := s.templates.Execute(w, p); e != nil {
		log.Print(e)
	}
}

const listenAddress = "127.0.0.1:18763"

func (s *server) routes() http.Handler {
	if len(s.contextKey) != 0 {
		return s.browserContextRoutes()
	}
	mux := http.NewServeMux()
	s.addRoutes(mux)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/backlog", http.StatusSeeOther)
	})
	backlogPage := func(w http.ResponseWriter, r *http.Request) {
		p := page{Title: "Мои задачи", Search: strings.TrimSpace(r.URL.Query().Get("q")), Mode: r.URL.Query().Get("scope"), StateFilter: r.URL.Query().Get("state"), TypeFilter: r.URL.Query().Get("type")}
		global := r.URL.Path == "/search"
		if global {
			p.Title = "Поиск по бэклогу"
			p.Mode = "all"
		}
		var items []workItem
		var e error
		if !global || p.Search != "" {
			items, e = s.c.searchBacklog(r.Context(), p.Search, p.Mode)
		}
		p.Items = items
		p.Groups = groupTasks(items, p.StateFilter, p.TypeFilter)
		seenStates, seenTypes := map[string]bool{}, map[string]bool{}
		for _, item := range items {
			state, kind := fieldText(item.Fields, "System.State"), fieldText(item.Fields, "System.WorkItemType")
			if !seenStates[state] {
				p.States = append(p.States, itemState{Name: state})
				seenStates[state] = true
			}
			if !seenTypes[kind] {
				p.Types = append(p.Types, itemType{Name: kind})
				seenTypes[kind] = true
			}
		}
		s.render(w, p, e)
	}
	mux.HandleFunc("GET /backlog", backlogPage)
	mux.HandleFunc("GET /search", backlogPage)
	mux.HandleFunc("GET /workitem/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, e := strconv.Atoi(r.PathValue("id"))
		if e != nil || id < 1 {
			http.Error(w, "Неверный ID", 400)
			return
		}
		var item workItem
		e = s.c.request(r.Context(), "GET", fmt.Sprintf("wit/workitems/%d", id), url.Values{"$expand": {"relations"}}, nil, &item)
		p := page{Title: "Рабочий элемент", Item: item}
		if e == nil {
			p.Form = itemForm(item)
			s.itemHierarchy(r.Context(), &p)
			p.States = s.itemStates(r.Context(), item)
			var comments struct {
				Value    []workComment `json:"value"`
				Comments []workComment `json:"comments"`
			}
			ce := s.c.request(r.Context(), "GET", fmt.Sprintf("wit/workitems/%d/comments", id), url.Values{"api-version": {"5.0-preview.2"}, "$top": {"100"}, "fromRevision": {"1"}, "order": {"asc"}}, nil, &comments)
			if ce != nil {
				p.Notice += " Не удалось загрузить комментарии: " + ce.Error()
			} else {
				p.Comments = comments.Value
				if len(p.Comments) == 0 {
					p.Comments = comments.Comments
				}
			}
		}
		s.render(w, p, e)
	})
	mux.HandleFunc("GET /repos", func(w http.ResponseWriter, r *http.Request) {
		var v list[repository]
		e := s.c.request(r.Context(), "GET", "git/repositories", nil, nil, &v)
		s.render(w, page{Title: "Репозитории", Repos: v.Value}, e)
	})
	mux.HandleFunc("GET /repo/{repo}", func(w http.ResponseWriter, r *http.Request) {
		s.repositoryPage(w, r)
	})
	mux.HandleFunc("GET /prs", func(w http.ResponseWriter, r *http.Request) {
		s.pullRequestsPage(w, r)
	})
	mux.HandleFunc("GET /repo/{repo}/pr/{id}", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("repo")
		id, e := strconv.Atoi(r.PathValue("id"))
		if e != nil || id < 1 {
			http.Error(w, "Неверный ID", 400)
			return
		}
		var pr pullRequest
		e = s.c.request(r.Context(), "GET", fmt.Sprintf("git/repositories/%s/pullrequests/%d", url.PathEscape(repo), id), nil, nil, &pr)
		p := page{Title: "Pull request", PR: pr, Repo: repo, Repository: pr.Repository}
		if e == nil {
			s.prWorkItems(r, &p)
			var threads list[thread]
			if te := s.c.request(r.Context(), "GET", fmt.Sprintf("git/repositories/%s/pullrequests/%d/threads", url.PathEscape(repo), id), nil, nil, &threads); te != nil {
				p.Notice = "Обсуждение недоступно: " + te.Error()
			} else {
				p.Threads = threads.Value
			}
			if me, ie := s.c.identity(r.Context()); ie == nil {
				p.Me = me
			} else {
				p.Notice += " Не удалось определить пользователя: голосование недоступно."
			}
		}
		s.render(w, p, e)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != listenAddress && r.Host != "localhost:18763" {
			http.Error(w, "Недопустимый Host", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
			if err := r.ParseForm(); err != nil || s.token == "" || subtle.ConstantTimeCompare([]byte(r.FormValue("csrf")), []byte(s.token)) != 1 {
				http.Error(w, "Недопустимый запрос", http.StatusForbidden)
				return
			}
		}
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'; img-src 'self'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		mux.ServeHTTP(w, r)
	})
}
func main() {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		log.Fatal(e)
	}
	key, err := loadContextKey()
	if err != nil {
		log.Fatal(err)
	}
	s := server{c: newClient(config{}), templates: tmpl, token: hex.EncodeToString(b), contextKey: key}
	log.Print("TFS Web: http://" + listenAddress)
	httpServer := &http.Server{Addr: listenAddress, Handler: s.routes(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	log.Fatal(httpServer.ListenAndServe())
}
