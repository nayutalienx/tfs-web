package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestPRLinkedItemsAndOwnSearch(t *testing.T) {
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/pullrequests/7"):
			writeJSON(w, map[string]any{"pullRequestId": 7, "status": "active"})
		case strings.HasSuffix(r.URL.Path, "/wit/workitems") && r.Method == "GET":
			if r.URL.Query().Get("ids") == "11" {
				writeJSON(w, map[string]any{"value": []any{map[string]any{"id": 11, "fields": map[string]any{"System.Title": "Связанная <script>"}}}})
			} else {
				writeJSON(w, map[string]any{"value": []any{map[string]any{"id": 11}, map[string]any{"id": 12, "fields": map[string]any{"System.Title": "Моя задача"}}}})
			}
		case strings.HasSuffix(r.URL.Path, "/pullrequests/7/workitems"):
			writeJSON(w, map[string]any{"value": []any{map[string]string{"id": "11", "url": "https://untrusted.invalid"}}})
		case strings.HasSuffix(r.URL.Path, "/wiql"):
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			q := body["query"]
			if !strings.Contains(q, "[System.AssignedTo] = @Me") || !strings.Contains(q, "CONTAINS 'тест'") || strings.Contains(q, "CreatedDate") {
				t.Errorf("bad own search: %s", q)
			}
			writeJSON(w, map[string]any{"workItems": []any{map[string]int{"id": 11}, map[string]int{"id": 12}}})
		default:
			writeJSON(w, map[string]any{"value": []any{}})
		}
	})
	response := appRequest(app, "GET", "/repo/repo/pr/7?task="+url.QueryEscape("тест"), nil)
	body := response.Body.String()
	for _, want := range []string{"Связанная &lt;script&gt;", "value=\"12\"", "Моя задача", "name=\"task\""} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(body, "option value=\"11\"") {
		t.Fatal("already linked item in picker")
	}
}

func TestAttachPRWorkItem(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "attach", true: "duplicate"}[duplicate], func(t *testing.T) {
			patches := 0
			artifact := "vstfs:///Git/PullRequestId/project-guid%2Frepo-guid%2F7"
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/pullrequests/7"):
					writeJSON(w, map[string]any{"pullRequestId": 7, "repository": map[string]any{"id": "repo-guid", "project": map[string]string{"id": "project-guid"}}})
				case strings.HasSuffix(r.URL.Path, "/workitems/12") && r.Method == "GET":
					relations := []any{}
					if duplicate {
						relations = append(relations, map[string]string{"rel": "ArtifactLink", "url": artifact})
					}
					writeJSON(w, map[string]any{"id": 12, "rev": 4, "relations": relations})
				case strings.HasSuffix(r.URL.Path, "/workitems/12") && r.Method == "PATCH":
					patches++
					var changes []map[string]any
					json.NewDecoder(r.Body).Decode(&changes)
					if len(changes) != 2 || changes[0]["path"] != "/rev" || changes[0]["value"] != float64(4) {
						t.Fatalf("missing revision guard: %#v", changes)
					}
					value := changes[1]["value"].(map[string]any)
					if value["rel"] != "ArtifactLink" || value["url"] != artifact {
						t.Fatalf("bad relation: %#v", value)
					}
					writeJSON(w, map[string]int{"id": 12})
				default:
					t.Errorf("unexpected upstream %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 500)
				}
			})
			form := url.Values{"csrf": {"csrf-test"}, "workitem": {"12"}}
			response := appRequest(app, "POST", "/repo/repo/pr/7/workitems", form)
			if response.Code != 303 {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			expected := 1
			if duplicate {
				expected = 0
			}
			if patches != expected {
				t.Fatalf("patches %d", patches)
			}
			form.Set("csrf", "wrong")
			if got := appRequest(app, "POST", "/repo/repo/pr/7/workitems", form).Code; got != 403 {
				t.Fatalf("csrf status %d", got)
			}
			form.Set("csrf", "csrf-test")
			form.Set("workitem", "-1")
			if got := appRequest(app, "POST", "/repo/repo/pr/7/workitems", form).Code; got != 400 {
				t.Fatalf("invalid ID status %d", got)
			}
		})
	}
}
