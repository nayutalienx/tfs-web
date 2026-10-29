package main

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	pathpkg "path"
	"sort"
	"strconv"
	"strings"
)

type patch []map[string]any
type fileContent struct{ Text string }
type workComment struct {
	Text string `json:"text"`
	By   struct {
		Name string `json:"displayName"`
	} `json:"revisedBy"`
	Date string `json:"revisedDate"`
}
type gitChange struct {
	ChangeType   string  `json:"changeType"`
	Item         gitItem `json:"item"`
	OriginalPath string  `json:"originalPath"`
}
type itemType struct {
	Name     string `json:"name"`
	Disabled bool   `json:"isDisabled"`
}
type gitItem struct {
	Path          string `json:"path"`
	IsFolder      bool   `json:"isFolder"`
	GitObjectType string `json:"gitObjectType"`
}
type thread struct {
	ID       int    `json:"id"`
	Status   string `json:"status"`
	Comments []struct {
		Content   string `json:"content"`
		IsDeleted bool   `json:"isDeleted"`
		Author    struct {
			Name string `json:"displayName"`
		} `json:"author"`
	} `json:"comments"`
}

func (c *client) searchBacklog(ctx context.Context, text, scope string) ([]workItem, error) {
	return c.searchWorkItems(ctx, text, scope, "", 0)
}

func (c *client) searchWorkItems(ctx context.Context, text, scope, iteration string, limit int) ([]workItem, error) {
	text = strings.TrimSpace(text)
	if len([]rune(text)) > 200 {
		return nil, fmt.Errorf("Слишком длинный запрос")
	}
	where := "[System.TeamProject] = @project"
	if scope != "all" {
		where += " AND [System.AssignedTo] = @Me"
	}
	if iteration != "" {
		where += " AND [System.IterationPath] = '" + strings.ReplaceAll(iteration, "'", "''") + "'"
	}
	if text != "" {
		var matches []string
		for _, field := range []string{"System.Title", "System.Description", "Microsoft.VSTS.Common.AcceptanceCriteria", "Microsoft.VSTS.TCM.ReproSteps"} {
			matches = append(matches, "["+field+"] CONTAINS '"+strings.ReplaceAll(text, "'", "''")+"'")
		}
		if id, err := strconv.Atoi(strings.TrimPrefix(text, "#")); err == nil && id > 0 {
			matches = append(matches, "[System.Id] = "+strconv.Itoa(id))
		}
		where += " AND (" + strings.Join(matches, " OR ") + ")"
	}
	return c.queryWorkItems(ctx, where, limit)
}
func rich(s string) string {
	return "<div>" + strings.ReplaceAll(html.EscapeString(s), "\n", "<br />") + "</div>"
}
func addField(p *patch, field, value string) {
	*p = append(*p, map[string]any{"op": "add", "path": "/fields/" + field, "value": value})
}
func formError(w http.ResponseWriter, msg string) { http.Error(w, msg, http.StatusBadRequest) }
func (s *server) fail(w http.ResponseWriter, err error) {
	s.render(w, page{Title: "Ошибка операции"}, err)
}
func (s *server) addRoutes(mux *http.ServeMux) {
	s.wikiRoutes(mux)
	mux.HandleFunc("GET /api/search", s.liveSearch)
	mux.HandleFunc("GET /api/branches/{repo}", s.liveBranches)
	mux.HandleFunc("GET /static/search.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(searchScript)
	})
	mux.HandleFunc("GET /static/ui.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(uiScript)
	})
	mux.HandleFunc("GET /attachments/{id}", s.screenshot)
	mux.HandleFunc("POST /repo/{repo}/pr/{id}/workitems", s.attachPRWorkItem)
	s.reviewRoutes(mux)
	s.codeRoutes(mux)
	mux.HandleFunc("GET /workitem/new", func(w http.ResponseWriter, r *http.Request) {
		var types list[itemType]
		err := s.c.request(r.Context(), "GET", "wit/workitemtypes", nil, nil, &types)
		s.render(w, page{Title: "Новый элемент", Types: types.Value}, err)
	})
	mux.HandleFunc("POST /workitem/new", func(w http.ResponseWriter, r *http.Request) {
		kind, title, iteration := strings.TrimSpace(r.FormValue("type")), strings.TrimSpace(r.FormValue("title")), strings.TrimSpace(r.FormValue("iteration"))
		desc, criteria, repro := r.FormValue("description"), r.FormValue("criteria"), r.FormValue("repro")
		if kind == "" || title == "" || iteration == "" || (kind == "Элемент невыполненной работы по продукту" && strings.TrimSpace(criteria) == "") || (kind == "Ошибка" && strings.TrimSpace(repro) == "") {
			formError(w, "Заполните тип, заголовок, итерацию и обязательные для типа поля")
			return
		}
		p := patch{}
		addField(&p, "System.Title", title)
		addField(&p, "System.IterationPath", iteration)
		if desc != "" {
			addField(&p, "System.Description", rich(desc))
		}
		if criteria != "" {
			addField(&p, "Microsoft.VSTS.Common.AcceptanceCriteria", rich(criteria))
		}
		if repro != "" {
			addField(&p, "Microsoft.VSTS.TCM.ReproSteps", rich(repro))
		}
		if kind == "Задача" {
			for _, f := range []struct{ input, field string }{{"original", "Microsoft.VSTS.Scheduling.OriginalEstimate"}, {"remaining", "Microsoft.VSTS.Scheduling.RemainingWork"}} {
				n, err := parseWork(r.FormValue(f.input))
				if err != nil {
					formError(w, "Для задачи укажите исходную оценку и оставшуюся работу в часах")
					return
				}
				p = append(p, map[string]any{"op": "add", "path": "/fields/" + f.field, "value": n})
			}
			addField(&p, "Microsoft.VSTS.Common.Activity", "Разработка")
		}
		parent := strings.TrimSpace(r.FormValue("parent"))
		if parent != "" {
			id, err := strconv.Atoi(parent)
			if err != nil || id < 1 {
				formError(w, "Некорректный ID родителя")
				return
			}
			p = append(p, map[string]any{"op": "add", "path": "/relations/-", "value": map[string]any{"rel": "System.LinkTypes.Hierarchy-Reverse", "url": fmt.Sprintf("%s/_apis/wit/workItems/%d", s.c.cfg.BaseURL, id)}})
		}
		var item workItem
		err := s.c.request(r.Context(), "POST", "wit/workitems/$"+url.PathEscape(kind), nil, p, &item)
		if err != nil {
			s.fail(w, fmt.Errorf("%w. Не повторяйте создание без проверки существующих элементов", err))
			return
		}
		if item.ID < 1 {
			s.fail(w, fmt.Errorf("Ответ не содержит ID. Проверьте, создан ли элемент, перед повтором"))
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/workitem/%d", item.ID), http.StatusSeeOther)
	})
	mux.HandleFunc("POST /workitem/{id}/update", s.updateWorkItem)
	mux.HandleFunc("GET /repo/{repo}/files", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("repo")
		path := r.URL.Query().Get("path")
		if path == "" {
			path = "/"
		}
		branch := r.URL.Query().Get("branch")
		q := url.Values{"scopePath": {path}, "recursionLevel": {"OneLevel"}, "includeContentMetadata": {"true"}}
		if branch != "" {
			q.Set("versionDescriptor.version", strings.TrimPrefix(branch, "refs/heads/"))
			q.Set("versionDescriptor.versionType", "branch")
		}
		var items list[gitItem]
		err := s.c.request(r.Context(), "GET", "git/repositories/"+url.PathEscape(repo)+"/items", q, nil, &items)
		files := []gitItem{}
		for _, item := range items.Value {
			if item.Path != path {
				files = append(files, item)
			}
		}
		sort.SliceStable(files, func(i, j int) bool {
			a, b := files[i].IsFolder || files[i].GitObjectType == "tree", files[j].IsFolder || files[j].GitObjectType == "tree"
			if a != b {
				return a
			}
			return files[i].Path < files[j].Path
		})
		s.render(w, page{Title: "Файлы репозитория", Repo: repo, Path: path, ParentPath: pathpkg.Dir(path), Branch: branch, Files: files}, err)
	})
	mux.HandleFunc("GET /repo/{repo}/file", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("repo")
		path := r.URL.Query().Get("path")
		branch := r.URL.Query().Get("branch")
		if path == "" {
			formError(w, "Укажите путь")
			return
		}
		q := url.Values{"path": {path}, "includeContent": {"true"}}
		if branch != "" {
			q.Set("versionDescriptor.version", strings.TrimPrefix(branch, "refs/heads/"))
			q.Set("versionDescriptor.versionType", "branch")
		}
		var item fileContent
		err := s.c.request(r.Context(), "GET", "git/repositories/"+url.PathEscape(repo)+"/items", q, nil, &item)
		s.render(w, page{Title: "Файл", Repo: repo, Path: path, ParentPath: pathpkg.Dir(path), Branch: branch, Content: item.Text, Lines: numberedCode(item.Text)}, err)
	})
	mux.HandleFunc("POST /repo/{repo}/pr/{id}/comment", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("repo")
		id, err := strconv.Atoi(r.PathValue("id"))
		text := strings.TrimSpace(r.FormValue("comment"))
		if err != nil || id < 1 || text == "" {
			formError(w, "Укажите текст комментария и ID")
			return
		}
		var result thread
		err = s.c.request(r.Context(), "POST", fmt.Sprintf("git/repositories/%s/pullrequests/%d/threads", url.PathEscape(repo), id), nil, map[string]any{"comments": []any{map[string]any{"parentCommentId": 0, "content": text, "commentType": 1}}, "status": "active"}, &result)
		if err != nil {
			s.fail(w, err)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/repo/%s/pr/%d", url.PathEscape(repo), id), http.StatusSeeOther)
	})
	mux.HandleFunc("GET /repo/{repo}/pr/new", func(w http.ResponseWriter, r *http.Request) {
		s.render(w, page{Title: "Новый pull request", Repo: r.PathValue("repo")}, nil)
	})
	mux.HandleFunc("POST /repo/{repo}/pr/new", func(w http.ResponseWriter, r *http.Request) {
		repo := r.PathValue("repo")
		source, target, title := strings.TrimSpace(r.FormValue("source")), strings.TrimSpace(r.FormValue("target")), strings.TrimSpace(r.FormValue("title"))
		if source == "" || target == "" || title == "" || source == target {
			formError(w, "Укажите разные ветки и заголовок")
			return
		}
		if !strings.HasPrefix(source, "refs/heads/") {
			source = "refs/heads/" + source
		}
		if !strings.HasPrefix(target, "refs/heads/") {
			target = "refs/heads/" + target
		}
		if source == target {
			formError(w, "Исходная и целевая ветки должны различаться")
			return
		}
		var refs list[struct {
			Name string `json:"name"`
		}]
		err := s.c.request(r.Context(), "GET", "git/repositories/"+url.PathEscape(repo)+"/refs", url.Values{"filter": {"heads/"}, "$top": {"1000"}}, nil, &refs)
		if err != nil {
			s.fail(w, err)
			return
		}
		foundSource, foundTarget := false, false
		for _, ref := range refs.Value {
			foundSource = foundSource || ref.Name == source
			foundTarget = foundTarget || ref.Name == target
		}
		if !foundSource || !foundTarget {
			formError(w, "Ветки не найдены на сервере среди первых 1000 веток")
			return
		}
		var pr pullRequest
		err = s.c.request(r.Context(), "POST", "git/repositories/"+url.PathEscape(repo)+"/pullrequests", nil, map[string]any{"sourceRefName": source, "targetRefName": target, "title": title, "description": r.FormValue("description")}, &pr)
		if err != nil {
			s.fail(w, fmt.Errorf("%w. Перед повтором проверьте список PR", err))
			return
		}
		if pr.ID < 1 {
			s.fail(w, fmt.Errorf("Ответ без ID: перед повтором проверьте список PR"))
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/repo/%s/pr/%d", url.PathEscape(repo), pr.ID), http.StatusSeeOther)
	})
}
