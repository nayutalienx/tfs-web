package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
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
		if strings.HasSuffix(r.URL.Path, "/classificationnodes/iterations") {
			fmt.Fprint(w, `{"name":"DemoProject"}`)
			return
		}
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
		case strings.HasSuffix(r.URL.Path, "/classificationnodes/iterations"):
			fmt.Fprint(w, `{"name":"DemoProject"}`)
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
				if strings.HasSuffix(r.URL.Path, "/classificationnodes/iterations") {
					fmt.Fprint(w, `{"name":"DemoProject"}`)
				} else if strings.HasSuffix(r.URL.Path, "/wiql") {
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

func TestBacklogIncreaseLimitPreservesFilters(t *testing.T) {
	queries, batches, requested := 0, 0, 0
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/classificationnodes/iterations"):
			fmt.Fprint(w, `{"name":"DemoProject","children":[{"name":"Sprint A"}]}`)
		case strings.HasSuffix(r.URL.Path, "/wiql"):
			queries++
			limit, _ := strconv.Atoi(r.URL.Query().Get("$top"))
			if limit != requested {
				t.Errorf("limit = %d, want %d", limit, requested)
			}
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			for _, clause := range []string{"[System.WorkItemType] = 'PBI'", "[System.State] = 'Approved'", "[System.IterationPath] = 'DemoProject\\Sprint A'", "[System.AssignedTo] = @Me", "CONTAINS 'example'"} {
				if !strings.Contains(body["query"], clause) {
					t.Error("missing filter", clause, body)
				}
			}
			refs := []map[string]int{}
			for id := 1; id <= min(limit, 250); id++ {
				refs = append(refs, map[string]int{"id": id})
			}
			writeJSON(w, map[string]any{"workItems": refs})
		case strings.HasSuffix(r.URL.Path, "/workitems"):
			batches++
			ids := strings.Split(r.URL.Query().Get("ids"), ",")
			if len(ids) > 100 {
				t.Fatal("batch exceeds 100")
			}
			items := []workItem{}
			for _, raw := range ids {
				id, _ := strconv.Atoi(raw)
				items = append(items, workItem{ID: id, Fields: map[string]any{"System.Title": fmt.Sprintf("Example %d", id), "System.WorkItemType": "PBI", "System.State": "Approved"}})
			}
			writeJSON(w, list[workItem]{Value: items})
		default:
			t.Error(r.URL)
		}
	})
	path := "/backlog?" + url.Values{"q": {"example"}, "scope": {"mine"}, "sprints": {"1"}, "iteration": {"DemoProject\\Sprint A"}, "type": {"PBI"}, "state": {"Approved"}}.Encode()
	for _, limit := range []int{100, 200, 300} {
		requested, queries, batches = limit, 0, 0
		response := appRequest(app, "GET", path, nil)
		body := html.UnescapeString(response.Body.String())
		if response.Code != 200 || queries != 1 || batches != (min(limit, 250)+99)/100 || !strings.Contains(body, fmt.Sprintf("Загружено: %d", min(limit, 250))) {
			t.Fatal(response.Code, queries, batches, body)
		}
		if limit < 300 {
			query, _ := url.Parse(path)
			values := query.Query()
			values.Set("limit", strconv.Itoa(limit+100))
			path = "/backlog?" + values.Encode()
			if !strings.Contains(body, `href="`+path+`">Ещё 100</a>`) {
				t.Fatal("missing next limit link with preserved filters", body)
			}
		} else if !strings.Contains(body, `<button disabled>Ещё 100</button>`) {
			t.Fatal("load more should be disabled after final partial batch")
		}
	}
}

func TestTypeAndStateFilterBeforeLimit(t *testing.T) {
	for _, route := range []string{"/backlog", "/api/search"} {
		t.Run(route, func(t *testing.T) {
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/classificationnodes/iterations") {
					fmt.Fprint(w, `{"name":"DemoProject"}`)
				} else if strings.HasSuffix(r.URL.Path, "/wiql") {
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					// Without upstream filtering, 100 newer tasks hide this older PBI.
					if !strings.Contains(body["query"], "[System.WorkItemType] = 'Product''s PBI'") || !strings.Contains(body["query"], "[System.State] = 'Approved'") {
						t.Error("filters must precede the TFS result limit", body)
						writeJSON(w, map[string]any{"workItems": []any{}})
						return
					}
					writeJSON(w, map[string]any{"workItems": []map[string]int{{"id": 42}}})
				} else {
					writeJSON(w, list[workItem]{Value: []workItem{{ID: 42, Fields: map[string]any{"System.Title": "Older matching PBI", "System.WorkItemType": "Product's PBI", "System.State": "Approved"}}}})
				}
			})
			response := appRequest(app, "GET", route+"?"+url.Values{"scope": {"all"}, "type": {"Product's PBI"}, "state": {"Approved"}}.Encode(), nil)
			if response.Code != 200 || !strings.Contains(response.Body.String(), "Older matching PBI") {
				t.Fatal(response.Code, response.Body.String())
			}
		})
	}
}

func TestNormalizeWorkItemLimit(t *testing.T) {
	for input, want := range map[int]int{-1: 100, 0: 100, 100: 100, 101: 200, 200: 200, 300: 300, 19999: 20000, 20001: 20000} {
		if got := normalizeWorkItemLimit(input); got != want {
			t.Errorf("normalize(%d) = %d, want %d", input, got, want)
		}
	}
}

func TestCurrentSprintDateBoundaries(t *testing.T) {
	options := []sprintOption{
		{Path: "DemoProject\\Release", Start: "2024-01-01T00:00:00Z", Finish: "2024-12-31T00:00:00Z"},
		{Path: "DemoProject\\Release\\Sprint A", Start: "2024-02-01T00:00:00Z", Finish: "2024-02-14T00:00:00Z"},
		{Path: "DemoProject\\Release\\Sprint B", Start: "2024-02-15T00:00:00Z", Finish: "2024-02-28T00:00:00Z"},
		{Path: "DemoProject\\Release\\Invalid", Start: "2024-02-01", Finish: "invalid"},
	}
	for _, tc := range []struct{ date, want string }{
		{"2024-02-01T00:00:00Z", options[1].Path},
		{"2024-02-14T23:59:59Z", options[1].Path},
		{"2024-02-15T00:00:00Z", options[2].Path},
		{"2025-01-01T00:00:00Z", ""},
	} {
		now, _ := time.Parse(time.RFC3339, tc.date)
		if got := currentSprint(options, now); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.date, got, tc.want)
		}
	}
}

func TestBacklogDefaultsToCurrentSprintAndFiltersAssignee(t *testing.T) {
	for _, route := range []string{"/backlog", "/api/search"} {
		for _, explicitAll := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/all=%t", route, explicitAll), func(t *testing.T) {
				app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
					switch {
					case strings.HasSuffix(r.URL.Path, "/classificationnodes/iterations"):
						today := time.Now().Format("2006-01-02")
						fmt.Fprintf(w, `{"name":"DemoProject","children":[{"name":"Current","attributes":{"startDate":%q,"finishDate":%q}}]}`, today, today)
					case strings.HasSuffix(r.URL.Path, "/wiql"):
						var body map[string]string
						json.NewDecoder(r.Body).Decode(&body)
						q := body["query"]
						if !strings.Contains(q, "[System.AssignedTo] = 'user.o''brien@example.invalid'") || strings.Contains(q, "@Me") {
							t.Error("explicit assignee must override mine scope and be escaped", q)
						}
						wantSprint := route == "/backlog" && !explicitAll
						if strings.Contains(q, "[System.IterationPath] = 'DemoProject\\Current'") != wantSprint {
							t.Error("incorrect default/explicit iteration", q)
						}
						writeJSON(w, map[string]any{"workItems": []map[string]int{{"id": 42}}})
					case strings.HasSuffix(r.URL.Path, "/workitems"):
						writeJSON(w, list[workItem]{Value: []workItem{{ID: 42, Fields: map[string]any{"System.Title": "Example item", "System.AssignedTo": map[string]any{"displayName": "Example Person", "uniqueName": "user.o'brien@example.invalid"}}}}})
					default:
						t.Error(r.URL)
					}
				})
				values := url.Values{"scope": {"mine"}, "assignee": {"user.o'brien@example.invalid"}}
				if explicitAll {
					values.Set("iteration", "")
				}
				w := appRequest(app, "GET", route+"?"+values.Encode(), nil)
				body := html.UnescapeString(w.Body.String())
				if w.Code != 200 {
					t.Fatal(w.Code, body)
				}
				if route == "/backlog" {
					for _, want := range []string{`name="iteration"`, `name="assignee"`, `value="user.o'brien@example.invalid"`, "Example Person"} {
						if !strings.Contains(body, want) {
							t.Error("missing filter or person suggestion", want)
						}
					}
					if !explicitAll && !strings.Contains(body, `value="DemoProject\Current" selected`) {
						t.Error("current sprint not selected", body)
					}
				}
			})
		}
	}
}
