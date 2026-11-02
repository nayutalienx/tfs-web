package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestWorkItemSprintSelector(t *testing.T) {
	for _, scenario := range []string{"view", "save", "conflict", "patch-error", "missing-current", "tree-error"} {
		t.Run(scenario, func(t *testing.T) {
			current := `DemoProject\Sprint 1`
			next := `DemoProject\Sprint 2`
			if scenario == "missing-current" {
				current = `DemoProject\Archived sprint`
			}
			if scenario == "conflict" {
				next = `DemoProject\Unsaved sprint`
			}
			patches := 0
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "PATCH":
					patches++
					var changes patch
					if err := json.NewDecoder(r.Body).Decode(&changes); err != nil {
						t.Fatal(err)
					}
					if len(changes) != 2 || changes[0]["op"] != "test" || changes[0]["path"] != "/rev" || changes[0]["value"] != float64(5) || changes[1]["path"] != "/fields/System.IterationPath" || changes[1]["value"] != next {
						t.Errorf("incorrect sprint-only patch: %v", changes)
					}
					if scenario == "patch-error" {
						w.WriteHeader(412)
						return
					}
					writeJSON(w, workItem{ID: 42, Rev: 6})
				case strings.HasSuffix(r.URL.Path, "/wit/workitems/42"):
					writeJSON(w, workItem{ID: 42, Rev: 5, Fields: map[string]any{"System.Title": "Synthetic item", "System.IterationPath": current, "System.State": "New"}})
				case strings.HasSuffix(r.URL.Path, "/classificationnodes/iterations"):
					if scenario == "tree-error" {
						w.WriteHeader(503)
						return
					}
					w.Write([]byte(`{"name":"DemoProject","children":[{"name":"Sprint 1","attributes":{"startDate":"2024-01-01T00:00:00Z","finishDate":"2024-01-14T00:00:00Z"}},{"name":"Sprint 2","attributes":{"startDate":"2024-01-15T00:00:00Z","finishDate":"2024-01-28T00:00:00Z"}}]}`))
				default:
					writeJSON(w, map[string]any{"value": []any{}})
				}
			})
			method, path := "GET", "/workitem/42"
			var form url.Values
			selected, status := current, 200
			if scenario == "save" || scenario == "conflict" || scenario == "patch-error" {
				method, path = "POST", "/workitem/42/update"
				form = url.Values{"csrf": {"csrf-test"}, "rev": {"5"}, "iteration": {next}}
				selected = next
				status = 303
				if scenario == "conflict" {
					form.Set("rev", "4")
					status = 409
				}
				if scenario == "patch-error" {
					status = 409
				}
			}
			response := appRequest(app, method, path, form)
			if response.Code != status {
				t.Fatalf("status=%d want=%d: %s", response.Code, status, response.Body.String())
			}
			if scenario == "save" {
				if patches != 1 || response.Header().Get("Location") != "/workitem/42" {
					t.Fatalf("patches=%d location=%s", patches, response.Header().Get("Location"))
				}
				return
			}
			body := response.Body.String()
			if scenario == "tree-error" {
				if !strings.Contains(body, `<input name="iteration" value="`+current+`" required>`) || !strings.Contains(body, "Не удалось загрузить список спринтов") {
					t.Fatal("manual input/current path missing when tree unavailable")
				}
			} else {
				if !strings.Contains(body, `<select name="iteration"`) || !strings.Contains(body, `value="`+selected+`" selected`) || !strings.Contains(body, "2024-01-15 — 2024-01-28") {
					t.Fatal("sprint selector, selected path or date labels missing")
				}
			}
			if scenario != "patch-error" && patches != 0 {
				t.Fatalf("unexpected mutation: %d", patches)
			}
		})
	}
}
