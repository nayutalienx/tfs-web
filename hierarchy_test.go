package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestChildTreeIncludesCompletedAndNestedTasks(t *testing.T) {
	rel := func(id string) itemRelation {
		return itemRelation{Rel: "System.LinkTypes.Hierarchy-Forward", URL: "https://tfs.example/_apis/wit/workItems/" + id}
	}
	root := workItem{ID: 1, Relations: []itemRelation{rel("2"), rel("3")}}
	u := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("$expand") != "relations" {
			t.Error("relations not requested")
		}
		if strings.Contains(r.URL.RawQuery, "State") {
			t.Error("state filter must not be used")
		}
		switch r.URL.Query().Get("ids") {
		case "2,3":
			writeJSON(w, list[workItem]{Value: []workItem{{ID: 2, Fields: map[string]any{"System.State": "Выполнено"}, Relations: []itemRelation{rel("4")}}, {ID: 3, Fields: map[string]any{"System.State": "Удалено"}}}})
		case "4":
			writeJSON(w, list[workItem]{Value: []workItem{{ID: 4, Fields: map[string]any{"System.State": "Разработка"}, Relations: []itemRelation{rel("1")}}}})
		default:
			t.Error(r.URL)
			writeJSON(w, list[workItem]{})
		}
	}))
	defer u.Close()
	c := newClient(config{BaseURL: u.URL, Project: "P", PAT: "test"})
	tree, err := c.childTree(context.Background(), root)
	if err != nil || len(tree) != 2 || len(tree[0].Children) != 1 || tree[0].Children[0].Item.ID != 4 || fieldText(tree[0].Item.Fields, "System.State") != "Выполнено" || fieldText(tree[1].Item.Fields, "System.State") != "Удалено" {
		t.Fatalf("tree=%+v err=%v", tree, err)
	}
}

func TestUnifiedDiffHasBothLineNumbers(t *testing.T) {
	lines, err := unifiedLines("one\nold\nlast\n", "one\nnew\nlast\n", "/x", "/x")
	if err != nil {
		t.Fatal(err)
	}
	foundAdd, foundDelete := false, false
	for _, line := range lines {
		if line.Text == "-old" {
			foundDelete = line.Old == "2" && line.New == "" && line.Kind == "delete"
		}
		if line.Text == "+new" {
			foundAdd = line.New == "2" && line.Old == "" && line.Kind == "add"
		}
	}
	if !foundAdd || !foundDelete {
		t.Fatal(lines)
	}
}

func TestParentDisplayedInWorkItem(t *testing.T) {
	for _, mode := range []string{"present", "absent", "unavailable", "stale-edit"} {
		t.Run(mode, func(t *testing.T) {
			parentCalls := 0
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/DemoProject/_apis/wit/workitems/42":
					if r.Method != "GET" {
						t.Fatal("unexpected mutation")
					}
					if r.URL.Query().Get("$expand") != "relations" {
						t.Error("relations not requested")
					}
					item := workItem{ID: 42, Rev: 5, Fields: map[string]any{"System.Title": "Дочерняя задача"}}
					if mode != "absent" {
						item.Relations = []itemRelation{
							{Rel: "System.LinkTypes.Related", URL: "https://other.invalid/_apis/wit/workItems/999"},
							{Rel: "System.LinkTypes.Hierarchy-Reverse", URL: "https://other.invalid/_apis/wit/workItems/7"},
						}
					}
					writeJSON(w, item)
				case "/DemoProject/_apis/wit/workitems/7":
					parentCalls++
					if mode == "unavailable" {
						http.NotFound(w, r)
						return
					}
					writeJSON(w, workItem{ID: 7, Fields: map[string]any{"System.Title": "Родитель <script>", "System.State": "Выполнено", "System.WorkItemType": "Элемент невыполненной работы по продукту"}})
				default:
					writeJSON(w, list[itemState]{})
				}
			})
			var w *httptest.ResponseRecorder
			if mode == "stale-edit" {
				w = appRequest(app, "POST", "/workitem/42/update", url.Values{"csrf": {"csrf-test"}, "rev": {"4"}, "title": {"Мои изменения"}})
				if w.Code != 409 {
					t.Fatal(w.Code)
				}
			} else {
				w = appRequest(app, "GET", "/workitem/42", nil)
				if w.Code != 200 {
					t.Fatal(w.Code)
				}
			}
			body := w.Body.String()
			if !strings.Contains(body, "Родительский элемент:") {
				t.Fatal("parent block missing")
			}
			if mode == "absent" {
				if parentCalls != 0 || !strings.Contains(body, "Не задан") {
					t.Fatal("unexpected parent")
				}
			} else {
				if parentCalls != 1 || !strings.Contains(body, `href="/workitem/7"`) {
					t.Fatal("parent link missing")
				}
				if mode == "unavailable" {
					if !strings.Contains(body, "Не удалось загрузить сведения о родителе") {
						t.Fatal("missing warning")
					}
				} else if !strings.Contains(body, "Родитель &lt;script&gt;") || !strings.Contains(body, "Выполнено") || strings.Contains(body, "Родитель <script>") {
					t.Fatal("incorrect parent rendering")
				}
			}
		})
	}
}
