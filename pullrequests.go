package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type identity struct {
	ID         string `json:"id"`
	Name       string `json:"displayName"`
	UniqueName string `json:"uniqueName"`
}
type reviewer struct {
	identity
	Vote     int  `json:"vote"`
	Required bool `json:"isRequired"`
}

func voteLabel(v int) string {
	switch v {
	case 10:
		return "Одобрено"
	case 5:
		return "Одобрено с замечаниями"
	case -5:
		return "Ожидание автора"
	case -10:
		return "Отклонено"
	default:
		return "Нет голоса"
	}
}
func (c *client) identity(ctx context.Context) (identity, error) {
	c.identityMu.Lock()
	me := c.user
	c.identityMu.Unlock()
	if me.ID != "" {
		return me, nil
	}
	var types list[itemType]
	if err := c.request(ctx, "GET", "wit/workitemtypes", nil, nil, &types); err != nil {
		return identity{}, err
	}
	c.identityMu.Lock()
	me = c.user
	c.identityMu.Unlock()
	if me.ID == "" {
		return me, fmt.Errorf("TFS не вернул идентификатор владельца PAT (X-Vss-Userdata)")
	}
	return me, nil
}
func skipValue(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("skip"))
	if n < 0 || n > 1000000 {
		return 0
	}
	return n
}
func (s *server) loadPRLists(ctx context.Context, repo string, skip int, p *page) error {
	status := p.PRStatus
	if status == "" {
		status = "active"
	}
	p.PRStatus = status
	base := "git/pullrequests"
	if repo != "" {
		base = "git/repositories/" + url.PathEscape(repo) + "/pullrequests"
	}
	var others list[pullRequest]
	if err := s.c.request(ctx, "GET", base, url.Values{"searchCriteria.status": {status}, "$top": {"100"}, "$skip": {strconv.Itoa(skip)}}, nil, &others); err != nil {
		return err
	}
	p.HasNext, p.Skip = len(others.Value) == 100, skip
	if status == "completed" {
		p.PRs = others.Value
		return nil
	}
	me, err := s.c.identity(ctx)
	if err != nil {
		p.PRs = others.Value
		p.Notice += "Не удалось закрепить ваши PR: " + err.Error()
		return nil
	}
	p.Me = me
	// A separate creator query is essential: my PRs may not be in the first page.
	for offset := 0; ; offset += 100 {
		var mine list[pullRequest]
		q := url.Values{"searchCriteria.status": {"active"}, "searchCriteria.creatorId": {me.ID}, "$top": {"100"}, "$skip": {strconv.Itoa(offset)}}
		if err := s.c.request(ctx, "GET", base, q, nil, &mine); err != nil {
			return fmt.Errorf("не удалось загрузить закреплённые PR: %w", err)
		}
		for _, pr := range mine.Value {
			if strings.EqualFold(pr.CreatedBy.ID, me.ID) {
				p.Mine = append(p.Mine, pr)
			}
		}
		if len(mine.Value) < 100 {
			break
		}
		if offset >= 9900 {
			return fmt.Errorf("слишком много личных PR; сузьте выборку до репозитория")
		}
	}
	for _, pr := range others.Value {
		if !strings.EqualFold(pr.CreatedBy.ID, me.ID) {
			p.PRs = append(p.PRs, pr)
		}
	}
	return nil
}
func (s *server) pullRequestsPage(w http.ResponseWriter, r *http.Request) {
	repo := r.PathValue("repo")
	p := page{Title: "Активные pull requests", Repo: repo}
	p.PRStatus = r.URL.Query().Get("status")
	if p.PRStatus != "" && p.PRStatus != "active" && p.PRStatus != "completed" {
		formError(w, "Недопустимый статус PR")
		return
	}
	err := s.loadPRLists(r.Context(), repo, skipValue(r), &p)
	base := "/prs"
	if repo != "" {
		base = "/repo/" + url.PathEscape(repo) + "/prs"
	}
	if p.Skip > 0 {
		p.Prev = base + "?status=" + p.PRStatus + "&skip=" + strconv.Itoa(max(0, p.Skip-100))
	}
	if p.HasNext {
		p.Next = base + "?status=" + p.PRStatus + "&skip=" + strconv.Itoa(p.Skip+100)
	}
	s.render(w, p, err)
}
func (s *server) repositoryPage(w http.ResponseWriter, r *http.Request) {
	repo := r.PathValue("repo")
	p := page{Title: "Репозиторий", Repo: repo, Active: "repos"}
	base := "git/repositories/" + url.PathEscape(repo)
	err := s.c.request(r.Context(), "GET", base, nil, nil, &p.Repository)
	if err == nil {
		var refs list[struct {
			Name string `json:"name"`
		}]
		if re := s.c.request(r.Context(), "GET", base+"/refs", url.Values{"filter": {"heads/"}, "$top": {"100"}}, nil, &refs); re != nil {
			p.Notice = "Ветки недоступны: " + re.Error() + ". "
		} else {
			p.Branches = refs.Value
		}
		err = s.loadPRLists(r.Context(), repo, 0, &p)
	}
	s.render(w, p, err)
}
func (s *server) reviewRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /repo/{repo}/pr/{id}/merge", s.mergePR)
	mux.HandleFunc("GET /repo/{repo}/prs", s.pullRequestsPage)
	mux.HandleFunc("POST /repo/{repo}/pr/{id}/vote", func(w http.ResponseWriter, r *http.Request) {
		repo, id, base, ok := prRoute(w, r)
		if !ok {
			return
		}
		vote, err := strconv.Atoi(r.FormValue("vote"))
		if err != nil || (vote != 10 && vote != 5 && vote != 0 && vote != -5 && vote != -10) {
			formError(w, "Недопустимый голос")
			return
		}
		me, err := s.c.identity(r.Context())
		if err != nil {
			s.fail(w, err)
			return
		}
		var pr pullRequest
		if err = s.c.request(r.Context(), "GET", base, nil, nil, &pr); err != nil {
			s.fail(w, err)
			return
		}
		if pr.Status != "active" {
			formError(w, "Голосовать можно только в активном PR")
			return
		}
		var result reviewer
		err = s.c.request(r.Context(), "PUT", base+"/reviewers/"+url.PathEscape(me.ID), nil, map[string]any{"vote": vote}, &result)
		if err != nil {
			s.fail(w, err)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/repo/%s/pr/%d", url.PathEscape(repo), id), 303)
	})
	mux.HandleFunc("POST /repo/{repo}/pr/{id}/thread/{thread}/reply", func(w http.ResponseWriter, r *http.Request) {
		repo, id, base, ok := prRoute(w, r)
		if !ok {
			return
		}
		tid, err := strconv.Atoi(r.PathValue("thread"))
		text := strings.TrimSpace(r.FormValue("comment"))
		if err != nil || tid < 1 || text == "" {
			formError(w, "Укажите обсуждение и текст ответа")
			return
		}
		var result map[string]any
		err = s.c.request(r.Context(), "POST", fmt.Sprintf("%s/threads/%d/comments", base, tid), nil, map[string]any{"content": text, "parentCommentId": 0, "commentType": 1}, &result)
		if err != nil {
			s.fail(w, err)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/repo/%s/pr/%d#thread-%d", url.PathEscape(repo), id, tid), 303)
	})
}
func prRoute(w http.ResponseWriter, r *http.Request) (string, int, string, bool) {
	repo := r.PathValue("repo")
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 1 {
		formError(w, "Некорректный ID PR")
		return "", 0, "", false
	}
	return repo, id, fmt.Sprintf("git/repositories/%s/pullrequests/%d", url.PathEscape(repo), id), true
}
