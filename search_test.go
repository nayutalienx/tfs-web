package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestGlobalBacklogSearch(t *testing.T) {
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/DemoProject/_apis/wit/wiql":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			query := body["query"]
			for _, field := range []string{"System.Title", "System.Description", "Microsoft.VSTS.Common.AcceptanceCriteria", "Microsoft.VSTS.TCM.ReproSteps"} {
				if !strings.Contains(query, "["+field+"] CONTAINS 'сбой O''Brien'") {
					t.Error(query)
				}
			}
			if !strings.Contains(query, "AND (") || strings.Count(query, " OR ") != 3 {
				t.Error("missing OR grouping", query)
			}
			for _, restriction := range []string{"@Me", "CreatedDate", "System.State", "AssignedTo"} {
				if strings.Contains(query, restriction) {
					t.Error("not global", query)
				}
			}
			if r.URL.Query().Get("$top") != "100" {
				t.Error("missing result bound")
			}
			writeJSON(w, map[string]any{"workItems": []map[string]int{{"id": 42}}})
		case "/DemoProject/_apis/wit/workitems":
			writeJSON(w, list[workItem]{Value: []workItem{{ID: 42, Fields: map[string]any{"System.Title": "Завершённая задача", "System.State": "Выполнено"}}}})
		default:
			t.Error(r.URL)
			http.NotFound(w, r)
		}
	})
	w := appRequest(app, "GET", "/search?"+url.Values{"q": {"  сбой O'Brien  "}, "scope": {"mine"}}.Encode(), nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Завершённая задача") || !strings.Contains(w.Body.String(), "Поиск по бэклогу") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestGlobalSearchEmptyAndTooLong(t *testing.T) {
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected upstream request", r.URL) })
	for _, q := range []string{"", "  "} {
		w := appRequest(app, "GET", "/search?"+url.Values{"q": {q}}.Encode(), nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Введите текст для поиска") {
			t.Fatal(w.Code)
		}
	}
	w := appRequest(app, "GET", "/search?"+url.Values{"q": {strings.Repeat("я", 201)}}.Encode(), nil)
	if w.Code == 200 || !strings.Contains(w.Body.String(), "Слишком длинный запрос") {
		t.Fatal(w.Code)
	}
}
