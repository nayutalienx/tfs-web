package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestLiveSearch(t *testing.T) {
	for _, scope := range []string{"mine", "all"} {
		t.Run(scope, func(t *testing.T) {
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/wiql") {
					var body map[string]string
					json.NewDecoder(r.Body).Decode(&body)
					query := body["query"]
					if strings.Contains(query, "@Me") != (scope == "mine") || !strings.Contains(query, "[System.Id] = 42") || strings.Contains(query, "CreatedDate") {
						t.Errorf("bad scope/query: %s", query)
					}
					writeJSON(w, map[string]any{"workItems": []map[string]int{{"id": 42}}})
				} else {
					writeJSON(w, map[string]any{"value": []workItem{{ID: 42, Fields: map[string]any{"System.Title": "<script>unsafe</script>", "System.WorkItemType": "Ошибка", "System.State": "Выполнено"}}}})
				}
			})
			response := appRequest(app, "GET", "/api/search?q=%2342&scope="+scope, nil)
			var data struct {
				Items []searchResult `json:"items"`
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &data) != nil || len(data.Items) != 1 {
				t.Fatalf("%d %s", response.Code, response.Body.String())
			}
			if data.Items[0].Class != "type-bug" || data.Items[0].URL != "/workitem/42" || strings.Contains(response.Body.String(), "test-secret") || strings.Contains(response.Body.String(), "<script>") {
				t.Fatal(response.Body.String())
			}
		})
	}
}

func TestLiveSearchValidation(t *testing.T) {
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) { t.Error("invalid request reached TFS") })
	for _, target := range []string{"/api/search?scope=unknown", "/api/search?scope=all&q=" + strings.Repeat("x", 201)} {
		if response := appRequest(app, "GET", target, nil); response.Code != 400 {
			t.Fatal(response.Code)
		}
	}
}

func TestLiveBranches(t *testing.T) {
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/DemoProject/_apis/git/repositories/repo/refs" || r.URL.Query().Get("filter") != "heads/" || r.URL.Query().Get("$top") != "1000" {
			t.Error(r.URL)
		}
		writeJSON(w, map[string]any{"value": []map[string]string{{"name": "refs/heads/develop"}}})
	})
	if response := appRequest(app, "GET", "/api/branches/repo", nil); response.Code != 200 || !strings.Contains(response.Body.String(), "develop") {
		t.Fatal(response.Body.String())
	}
}

func TestWorkItemTypeColors(t *testing.T) {
	for kind, want := range map[string]string{"Ошибка": "type-bug", "Bug": "type-bug", "Функция": "type-feature", "Feature": "type-feature", "Элемент невыполненной работы по продукту": "type-pbi", "Задача": "type-task", "Task": "type-task", "Unknown": "type-other"} {
		if got := typeClass(kind); got != want {
			t.Errorf("%s: %s != %s", kind, got, want)
		}
	}
}

func TestSearchScriptIsLocal(t *testing.T) {
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected TFS call") })
	response := appRequest(app, "GET", "/static/search.js", nil)
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "connect-src 'self'") {
		t.Fatal("Live search blocked by CSP")
	}
	if response.Code != 200 || response.Body.String() != string(searchScript) || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/javascript") {
		t.Fatal(response.Code)
	}
}
