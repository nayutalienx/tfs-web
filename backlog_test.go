package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestSprintTreeLoadsTruncatedBranches(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("$depth") != "2" {
			t.Error(r.URL)
		}
		switch r.URL.Path {
		case "/DemoProject/_apis/wit/classificationnodes/iterations":
			fmt.Fprint(w, `{"name":"DemoProject","children":[{"name":"Release","hasChildren":true}]}`)
		case "/DemoProject/_apis/wit/classificationnodes/iterations/Release":
			fmt.Fprint(w, `{"name":"Release","hasChildren":true,"children":[{"name":"Past sprint","attributes":{"startDate":"2020-01-01T00:00:00Z"}}]}`)
		default:
			t.Error(r.URL)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	c := newClient(config{BaseURL: upstream.URL, Project: "DemoProject", PAT: "synthetic"})
	options, err := c.projectSprints(context.Background())
	if err != nil || calls != 2 || len(options) != 3 || options[2].Path != "DemoProject\\Release\\Past sprint" {
		t.Fatal(options, calls, err)
	}
}

func TestMineBacklogIncludesOldAndClosedItems(t *testing.T) {
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		q := body["query"]
		if !strings.Contains(q, "[System.AssignedTo] = @Me") || strings.Contains(q, "CreatedDate") || strings.Contains(q, "System.State") {
			t.Error(q)
		}
		writeJSON(w, map[string]any{"workItems": []any{}})
	})
	if response := appRequest(app, "GET", "/backlog?scope=mine", nil); response.Code != 200 {
		t.Fatal(response.Code)
	}
}

func TestBacklogStopsAtResultLimit(t *testing.T) {
	queries, batches := 0, 0
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/wiql"):
			queries++
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			q := body["query"]
			for _, forbidden := range []string{"@Me", "CreatedDate", "System.State", "System.WorkItemType"} {
				if strings.Contains(q, forbidden) {
					t.Error(q)
				}
			}
			if r.URL.Query().Get("$top") != "100" || !strings.Contains(q, "ORDER BY [System.ChangedDate] DESC") {
				t.Error(q, r.URL)
			}
			first, last := 1, 100
			if queries > 1 {
				t.Fatal("unexpected automatic pagination")
			}
			refs := []map[string]int{}
			for id := first; id <= last; id++ {
				refs = append(refs, map[string]int{"id": id})
			}
			writeJSON(w, map[string]any{"workItems": refs})
		case strings.HasSuffix(r.URL.Path, "/workitems"):
			batches++
			ids := strings.Split(r.URL.Query().Get("ids"), ",")
			if len(ids) > 100 {
				t.Fatal("batch too large")
			}
			items := []workItem{}
			for _, raw := range ids {
				id, _ := strconv.Atoi(raw)
				items = append(items, workItem{ID: id, Fields: map[string]any{"System.Title": fmt.Sprintf("Элемент %d", id), "System.State": "Закрыто"}})
			}
			writeJSON(w, list[workItem]{Value: items})
		default:
			t.Error(r.URL)
			http.NotFound(w, r)
		}
	})
	for _, path := range []string{"/backlog", "/backlog?scope=all", "/search?q=example"} {
		queries, batches = 0, 0
		w := appRequest(app, "GET", path, nil)
		if w.Code != 200 || queries != 1 || batches != 1 || !strings.Contains(w.Body.String(), "Элемент 100") {
			t.Fatal(path, w.Code, queries, batches)
		}
		if path != "/search?q=example" && !strings.Contains(w.Body.String(), "Достигнут лимит") {
			t.Error("missing limit notice", path)
		}
	}
}

func TestSprintSelectionAndNavigation(t *testing.T) {
	path := "DemoProject\\Release\\Sprint O'Brien"
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/classificationnodes/iterations"):
			fmt.Fprint(w, `{"name":"DemoProject","hasChildren":true,"children":[{"name":"Release","hasChildren":true,"children":[{"name":"Earlier","attributes":{"startDate":"2024-01-01T00:00:00Z"}},{"name":"Sprint O'Brien","attributes":{"startDate":"2024-02-01T00:00:00Z","finishDate":"2024-02-14T00:00:00Z"}},{"name":"Later","attributes":{"startDate":"2024-03-01T00:00:00Z"}}]}]}`)
		case strings.HasSuffix(r.URL.Path, "/wiql"):
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			q := body["query"]
			if r.URL.Query().Get("$top") != "100" {
				t.Error("sprint results must be bounded", r.URL)
			}
			if !strings.Contains(q, "[System.IterationPath] = 'DemoProject\\Release\\Sprint O''Brien'") || strings.Contains(q, "@Me") || strings.Contains(q, "UNDER") {
				t.Error(q)
			}
			writeJSON(w, map[string]any{"workItems": []map[string]int{{"id": 42}}})
		case strings.HasSuffix(r.URL.Path, "/workitems"):
			writeJSON(w, list[workItem]{Value: []workItem{{ID: 42, Fields: map[string]any{"System.Title": "Завершённый PBI", "System.State": "Закрыто", "System.WorkItemType": "Product Backlog Item"}}}})
		default:
			t.Error(r.URL)
			http.NotFound(w, r)
		}
	})
	w := appRequest(app, "GET", "/backlog?"+url.Values{"iteration": {path}}.Encode(), nil)
	for _, want := range []string{"Завершённый PBI", "Предыдущая итерация", "Следующая итерация", "2024-02-01 — 2024-02-14", "Earlier", "Later"} {
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Error(w.Code, want)
		}
	}
}

func TestBacklogDoesNotSilentlyReturnPartialResults(t *testing.T) {
	for _, failBatch := range []bool{false, true} {
		t.Run(strconv.FormatBool(failBatch), func(t *testing.T) {
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/wiql") {
					writeJSON(w, map[string]any{"workItems": []map[string]int{{"id": 42}}})
				} else if failBatch {
					http.Error(w, "unavailable", 503)
				} else {
					writeJSON(w, list[workItem]{})
				}
			})
			response := appRequest(app, "GET", "/backlog", nil)
			if response.Code != http.StatusBadGateway {
				t.Fatal(response.Code)
			}
		})
	}
}
