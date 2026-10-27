package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func (s *server) liveBranches(w http.ResponseWriter, r *http.Request) {
	var branches list[struct {
		Name string `json:"name"`
	}]
	err := s.c.request(r.Context(), "GET", "git/repositories/"+url.PathEscape(r.PathValue("repo"))+"/refs", url.Values{"filter": {"heads/"}, "$top": {"1000"}}, nil, &branches)
	if err != nil {
		http.Error(w, "Не удалось загрузить ветки", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(branches)
}

func typeClass(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "ошибка", "bug":
		return "type-bug"
	case "функция", "фича", "feature":
		return "type-feature"
	case "элемент невыполненной работы по продукту", "product backlog item", "pbi":
		return "type-pbi"
	case "задача", "task":
		return "type-task"
	default:
		return "type-other"
	}
}

// Only a small display DTO goes to the browser; never return upstream URLs or config.
type searchResult struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Type   string `json:"type"`
	Class  string `json:"class"`
	State  string `json:"state"`
	Person string `json:"person"`
	URL    string `json:"url"`
}

func (s *server) liveSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(query)) > 200 {
		formError(w, "Запрос длиннее 200 символов")
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope == "mine" {
		scope = "mine-all"
	} else if scope != "all" {
		formError(w, "Неизвестная область поиска")
		return
	}
	items, err := s.c.searchBacklog(r.Context(), query, scope)
	if err != nil {
		http.Error(w, "Не удалось выполнить поиск в TFS", http.StatusBadGateway)
		return
	}
	results := make([]searchResult, 0, len(items))
	for _, item := range items {
		kind := fieldText(item.Fields, "System.WorkItemType")
		results = append(results, searchResult{ID: item.ID, Title: fieldText(item.Fields, "System.Title"), Type: shortType(kind), Class: typeClass(kind), State: fieldText(item.Fields, "System.State"), Person: personName(item.Fields["System.AssignedTo"]), URL: "/workitem/" + strconv.Itoa(item.ID)})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(struct {
		Items   []searchResult `json:"items"`
		Limited bool           `json:"limited"`
	}{results, len(items) == 100})
}
