package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestWorkItemStatesMetadata(t *testing.T) {
	for _, scenario := range []string{"preview", "definition", "transitions", "empty", "forbidden"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || !strings.Contains(r.URL.Path, "/workitemtypes/Задача примера") {
					t.Error("unexpected metadata request", r.Method, r.URL)
				}
				if strings.HasSuffix(r.URL.Path, "/states") {
					if r.URL.Query().Get("api-version") != "6.0-preview.1" {
						t.Error("states endpoint requires preview version", r.URL)
					}
					switch scenario {
					case "preview":
						writeJSON(w, list[itemState]{Value: []itemState{{Name: "Новое"}, {Name: "В работе"}, {Name: "Готово"}}})
					case "forbidden":
						w.WriteHeader(403)
					default:
						w.WriteHeader(404)
					}
					return
				}
				if r.URL.Query().Get("api-version") != "6.0" {
					t.Error("type definition should use stable API")
				}
				switch scenario {
				case "definition":
					w.Write([]byte(`{"states":[{"name":"Новое"},{"name":"В работе"},{"name":"Готово"}]}`))
				case "transitions":
					w.Write([]byte(`{"transitions":{"Новое":[{"to":"В работе"}],"В работе":[{"to":"Готово"},{"to":"В работе"}],"":[{"to":"Новое"}]}}`))
				default:
					w.Write([]byte(`{}`))
				}
			}))
			defer upstream.Close()
			c := newClient(config{BaseURL: upstream.URL, Project: "DemoProject", PAT: "synthetic"})
			states, err := c.workItemStates(context.Background(), "Задача примера")
			if scenario == "empty" || scenario == "forbidden" {
				if err == nil || (scenario == "forbidden" && calls != 1) {
					t.Fatal("metadata error hidden or permissions bypass attempted", calls, err)
				}
				return
			}
			want := []itemState{{Name: "Новое"}, {Name: "В работе"}, {Name: "Готово"}}
			if scenario == "transitions" {
				want = []itemState{{Name: "В работе"}, {Name: "Готово"}, {Name: "Новое"}}
			}
			if err != nil || !reflect.DeepEqual(states, want) {
				t.Fatal(states, err)
			}
		})
	}
}

func TestWorkItemStateSelector(t *testing.T) {
	for _, scenario := range []string{"view", "save", "conflict", "patch-error", "unavailable", "missing-current"} {
		t.Run(scenario, func(t *testing.T) {
			patches := 0
			current := "Новое"
			if scenario == "missing-current" {
				current = "Архивное"
			}
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "PATCH":
					patches++
					var changes patch
					json.NewDecoder(r.Body).Decode(&changes)
					if len(changes) != 2 || changes[0]["path"] != "/rev" || changes[0]["op"] != "test" || changes[1]["path"] != "/fields/System.State" || changes[1]["value"] != "В работе" {
						t.Error("incorrect state-only patch", changes)
					}
					if scenario == "patch-error" {
						w.WriteHeader(412)
						return
					}
					writeJSON(w, workItem{ID: 42, Rev: 6})
				case strings.HasSuffix(r.URL.Path, "/workitems/42"):
					writeJSON(w, workItem{ID: 42, Rev: 5, Fields: map[string]any{"System.Title": "Пример", "System.WorkItemType": "Задача примера", "System.State": current}})
				case strings.HasSuffix(r.URL.Path, "/states"):
					if scenario == "unavailable" {
						w.WriteHeader(503)
						return
					}
					writeJSON(w, list[itemState]{Value: []itemState{{Name: "Новое"}, {Name: "В работе"}, {Name: "Готово"}, {Name: "Новое"}}})
				default:
					writeJSON(w, map[string]any{"value": []any{}})
				}
			})
			method, path, status, selected := "GET", "/workitem/42", 200, current
			var form url.Values
			if scenario == "save" || scenario == "conflict" || scenario == "patch-error" {
				method, path, status, selected = "POST", "/workitem/42/update", 303, "В работе"
				form = url.Values{"csrf": {"csrf-test"}, "rev": {"5"}, "state": {selected}}
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
				t.Fatal(response.Code, status)
			}
			body := response.Body.String()
			if scenario == "unavailable" {
				if !strings.Contains(body, "Не удалось загрузить доступные состояния") || !strings.Contains(body, `input name="state" value="Новое"`) {
					t.Fatal("missing warning/manual fallback")
				}
			} else if status != 303 {
				if !strings.Contains(body, `select name="state"`) || !strings.Contains(body, `selected>`+selected+`</option>`) || !strings.Contains(body, `>Готово</option>`) || strings.Count(body, `value="Новое" data-state-class=`) != 1 {
					t.Fatal("state selector missing, duplicates or selection lost")
				}
			}
			wantPatches := 0
			if scenario == "save" || scenario == "patch-error" {
				wantPatches = 1
			}
			if patches != wantPatches {
				t.Fatal("unexpected mutations", patches)
			}
		})
	}
}
