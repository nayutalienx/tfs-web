package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Resolve only IDs returned by TFS, never the URLs supplied in resource references.
func (c *client) linkedPRItems(ctx context.Context, base string) ([]workItem, error) {
	var refs list[struct {
		ID string `json:"id"`
	}]
	if err := c.request(ctx, "GET", base+"/workitems", nil, nil, &refs); err != nil {
		return nil, err
	}
	var result []workItem
	for start := 0; start < len(refs.Value); start += 200 {
		var ids []string
		for _, ref := range refs.Value[start:min(start+200, len(refs.Value))] {
			id, err := strconv.Atoi(ref.ID)
			if err != nil || id < 1 {
				return nil, fmt.Errorf("TFS вернул некорректный ID связанной задачи")
			}
			ids = append(ids, strconv.Itoa(id))
		}
		var items list[workItem]
		if err := c.request(ctx, "GET", "wit/workitems", url.Values{"ids": {strings.Join(ids, ",")}}, nil, &items); err != nil {
			return nil, err
		}
		result = append(result, items.Value...)
	}
	return result, nil
}

func (s *server) prWorkItems(r *http.Request, p *page) {
	base := fmt.Sprintf("git/repositories/%s/pullrequests/%d", url.PathEscape(p.Repo), p.PR.ID)
	items, err := s.c.linkedPRItems(r.Context(), base)
	if err != nil {
		p.LinkNotice = "Связанные задачи недоступны: " + err.Error()
	} else {
		p.LinkedItems = items
		p.LinksLoaded = true
	}
	p.LinkSearch = strings.TrimSpace(r.URL.Query().Get("task"))
	candidates, err := s.c.searchBacklog(r.Context(), p.LinkSearch, "mine-all")
	if err != nil {
		p.LinkNotice += " Поиск ваших задач недоступен: " + err.Error()
		return
	}
	p.CandidatesLoaded = true
	linked := map[int]bool{}
	for _, item := range items {
		linked[item.ID] = true
	}
	for _, item := range candidates {
		if !linked[item.ID] {
			p.LinkCandidates = append(p.LinkCandidates, item)
		}
	}
}

func (s *server) attachPRWorkItem(w http.ResponseWriter, r *http.Request) {
	repo, id, base, ok := prRoute(w, r)
	if !ok {
		return
	}
	itemID, err := strconv.Atoi(r.FormValue("workitem"))
	if err != nil || itemID < 1 {
		formError(w, "Выберите задачу")
		return
	}
	var pr pullRequest
	if err = s.c.request(r.Context(), "GET", base, nil, nil, &pr); err != nil {
		s.fail(w, err)
		return
	}
	if pr.Repository.Project.ID == "" || pr.Repository.ID == "" {
		if err = s.c.request(r.Context(), "GET", "git/repositories/"+url.PathEscape(repo), nil, nil, &pr.Repository); err != nil {
			s.fail(w, err)
			return
		}
	}
	if pr.Repository.Project.ID == "" || pr.Repository.ID == "" {
		s.fail(w, fmt.Errorf("TFS не вернул идентификаторы проекта и репозитория"))
		return
	}
	var item workItem
	itemPath := "wit/workitems/" + strconv.Itoa(itemID)
	if err = s.c.request(r.Context(), "GET", itemPath, url.Values{"$expand": {"relations"}}, nil, &item); err != nil {
		s.fail(w, err)
		return
	}
	artifact := "vstfs:///Git/PullRequestId/" + pr.Repository.Project.ID + "%2F" + pr.Repository.ID + "%2F" + strconv.Itoa(id)
	location := fmt.Sprintf("/repo/%s/pr/%d#workitems", url.PathEscape(repo), id)
	for _, relation := range item.Relations {
		if relation.Rel == "ArtifactLink" && strings.EqualFold(relation.URL, artifact) {
			http.Redirect(w, r, location, 303)
			return
		}
	}
	changes := patch{
		{"op": "test", "path": "/rev", "value": item.Rev},
		{"op": "add", "path": "/relations/-", "value": map[string]any{"rel": "ArtifactLink", "url": artifact, "attributes": map[string]string{"name": "Pull Request"}}},
	}
	if err = s.c.request(r.Context(), "PATCH", itemPath, nil, changes, nil); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, location, 303)
}
