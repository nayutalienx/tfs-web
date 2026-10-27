package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testApp(t *testing.T, upstream http.HandlerFunc) http.Handler {
	t.Helper()
	u := httptest.NewServer(upstream)
	t.Cleanup(u.Close)
	return (&server{c: newClient(config{BaseURL: u.URL, Project: "DemoProject", PAT: "test-secret"}), templates: tmpl, token: "csrf-test"}).routes()
}
func appRequest(app http.Handler, method, path string, form url.Values) *httptest.ResponseRecorder {
	body := ""
	if form != nil {
		body = form.Encode()
	}
	r := httptest.NewRequest(method, "http://"+listenAddress+path, strings.NewReader(body))
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestPinsAuthorPRsOutsideGeneralPage(t *testing.T) {
	for _, path := range []string{"/prs", "/repo/r/prs"} {
		t.Run(path, func(t *testing.T) {
			mineCalls, allCalls := 0, 0
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Vss-Userdata", "me:DOMAIN\\author")
				if r.URL.Query().Get("searchCriteria.status") != "active" {
					t.Errorf("must query active: %s", r.URL)
				}
				if r.URL.Query().Get("searchCriteria.creatorId") == "me" {
					mineCalls++
					writeJSON(w, list[pullRequest]{Value: []pullRequest{{ID: 7, Title: "Личный старый PR", CreatedBy: identity{ID: "me"}, Repository: repository{ID: "r"}}, {ID: 8, Title: "Личный новый PR", CreatedBy: identity{ID: "me"}, Repository: repository{ID: "r"}}}})
				} else {
					allCalls++
					writeJSON(w, list[pullRequest]{Value: []pullRequest{{ID: 9, Title: "Чужой PR", CreatedBy: identity{ID: "other"}, Repository: repository{ID: "r"}}, {ID: 8, Title: "Личный новый PR", CreatedBy: identity{ID: "me"}, Repository: repository{ID: "r"}}}})
				}
			})
			w := appRequest(app, "GET", path, nil)
			body := w.Body.String()
			if w.Code != 200 || mineCalls != 1 || allCalls != 1 {
				t.Fatalf("status=%d mine=%d all=%d", w.Code, mineCalls, allCalls)
			}
			if strings.Index(body, "Личный старый PR") > strings.Index(body, "Чужой PR") || !strings.Contains(body, "Личный старый PR") {
				t.Fatal("mine not pinned")
			}
			if strings.Count(body, "Личный новый PR") != 1 {
				t.Fatal("own PR duplicated")
			}
		})
	}
}
func TestPersonalPRPagination(t *testing.T) {
	pages := 0
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Vss-Userdata", "me:user")
		if r.URL.Query().Get("searchCriteria.creatorId") == "" {
			writeJSON(w, list[pullRequest]{})
			return
		}
		pages++
		if r.URL.Query().Get("$skip") == "0" {
			prs := make([]pullRequest, 100)
			for i := range prs {
				prs[i] = pullRequest{ID: i + 1, Title: fmt.Sprintf("Мой PR %d", i), CreatedBy: identity{ID: "me"}, Repository: repository{ID: "r"}}
			}
			writeJSON(w, list[pullRequest]{Value: prs})
		} else {
			writeJSON(w, list[pullRequest]{Value: []pullRequest{{ID: 101, Title: "За пределами страницы", CreatedBy: identity{ID: "me"}, Repository: repository{ID: "r"}}}})
		}
	})
	w := appRequest(app, "GET", "/prs", nil)
	if w.Code != 200 || pages != 2 || !strings.Contains(w.Body.String(), "За пределами страницы") {
		t.Fatalf("pagination status=%d pages=%d", w.Code, pages)
	}
}
func TestVotesOnlyForPATOwner(t *testing.T) {
	for _, vote := range []string{"10", "5", "0", "-5", "-10", "999"} {
		t.Run(vote, func(t *testing.T) {
			puts := 0
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Vss-Userdata", "pat-owner:user")
				switch {
				case strings.HasSuffix(r.URL.Path, "workitemtypes"):
					writeJSON(w, list[itemType]{})
				case r.Method == "GET":
					writeJSON(w, pullRequest{ID: 42, Status: "active"})
				case r.Method == "PUT":
					puts++
					if !strings.HasSuffix(r.URL.Path, "/reviewers/pat-owner") {
						t.Errorf("untrusted reviewer: %s", r.URL.Path)
					}
					var body map[string]int
					_ = json.NewDecoder(r.Body).Decode(&body)
					if fmt.Sprint(body["vote"]) != vote {
						t.Error("wrong vote")
					}
					writeJSON(w, reviewer{})
				default:
					t.Errorf("unexpected mutation %s", r.Method)
				}
			})
			form := url.Values{"csrf": {"csrf-test"}, "vote": {vote}, "reviewerId": {"someone-else"}}
			if w := appRequest(app, "POST", "/repo/r/pr/42/vote", url.Values{"vote": {vote}}); w.Code != 403 {
				t.Fatal("CSRF bypass")
			}
			w := appRequest(app, "POST", "/repo/r/pr/42/vote", form)
			if vote == "999" {
				if w.Code != 400 || puts != 0 {
					t.Fatal("invalid vote accepted")
				}
			} else if w.Code != 303 || puts != 1 {
				t.Fatalf("status=%d puts=%d", w.Code, puts)
			}
		})
	}
}
func TestCompletedPRCannotBeApproved(t *testing.T) {
	mutations := 0
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Vss-Userdata", "me:user")
		if r.Method != "GET" {
			mutations++
		}
		if strings.HasSuffix(r.URL.Path, "workitemtypes") {
			writeJSON(w, list[itemType]{})
		} else {
			writeJSON(w, pullRequest{Status: "completed"})
		}
	})
	w := appRequest(app, "POST", "/repo/r/pr/42/vote", url.Values{"csrf": {"csrf-test"}, "vote": {"10"}})
	if w.Code != 400 || mutations != 0 {
		t.Fatal("voted on completed PR")
	}
}
func TestWorkitemPreservesUnchangedHTMLAndClearsFields(t *testing.T) {
	var changes patch
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			writeJSON(w, workItem{ID: 42, Rev: 5, Fields: map[string]any{"System.Title": "Задача", "System.Description": "<p><b>Описание</b></p>", "System.Tags": "old", "Microsoft.VSTS.Scheduling.RemainingWork": float64(2)}})
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&changes); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, workItem{ID: 42, Rev: 6})
	})
	w := appRequest(app, "POST", "/workitem/42/update", url.Values{"csrf": {"csrf-test"}, "rev": {"5"}, "description": {"Описание"}, "tags": {""}, "remaining": {"3.5"}})
	if w.Code != 303 || len(changes) != 3 {
		t.Fatalf("status=%d changes=%v", w.Code, changes)
	}
	if changes[0]["op"] != "test" || changes[1]["path"] != "/fields/System.Tags" || changes[1]["value"] != "" || changes[2]["value"] != float64(3.5) {
		t.Fatalf("unexpected patch: %v", changes)
	}
}
func TestWorkitemRejectsStaleRevisionRetainsForm(t *testing.T) {
	patches := 0
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			patches++
		}
		if strings.HasSuffix(r.URL.Path, "/states") {
			writeJSON(w, list[itemState]{})
			return
		}
		writeJSON(w, workItem{ID: 42, Rev: 6, Fields: map[string]any{"System.Title": "Обновлённый заголовок"}})
	})
	w := appRequest(app, "POST", "/workitem/42/update", url.Values{"csrf": {"csrf-test"}, "rev": {"5"}, "title": {"Мой несохранённый заголовок"}})
	if w.Code != 409 || patches != 0 || !strings.Contains(w.Body.String(), "Мой несохранённый заголовок") || !strings.Contains(w.Body.String(), `name="rev" value="5"`) {
		t.Fatalf("stale edit lost: status=%d patches=%d", w.Code, patches)
	}
}

func TestBrowserNewlinesDoNotRewriteRichText(t *testing.T) {
	patches := 0
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			patches++
		}
		writeJSON(w, workItem{ID: 42, Rev: 5, Fields: map[string]any{"System.Description": "<p><b>Первая строка</b><br>Вторая строка</p>"}})
	})
	w := appRequest(app, "POST", "/workitem/42/update", url.Values{"csrf": {"csrf-test"}, "rev": {"5"}, "description": {"Первая строка\r\nВторая строка"}})
	if w.Code != 303 || patches != 0 {
		t.Fatalf("unchanged rich text rewritten: status=%d patches=%d", w.Code, patches)
	}
}
func TestRichHTMLSanitization(t *testing.T) {
	input := `<p onclick="evil()">Текст <b>жирный</b></p><script>alert(1)</script><style>body{display:none}</style><img src="https://tracker/x" onerror="evil()"><svg onload="evil()"></svg><a href="jav&#x61;script:alert(1)">плохо</a><a href="https://example.org/x?q=1&b=2" style="display:none">хорошо</a><iframe srcdoc="evil"></iframe>`
	got := string(safeRich(input))
	for _, bad := range []string{"onclick", "onerror", "evil", "<script", "<style", "<img", "<svg", "javascript:", "iframe"} {
		if strings.Contains(got, bad) {
			t.Fatalf("unsafe token %q: %s", bad, got)
		}
	}
	if !strings.Contains(got, "<b>жирный</b>") || !strings.Contains(got, `href="https://example.org/x?q=1&amp;b=2"`) {
		t.Fatal("formatting or safe URL lost", got)
	}
	if got := plainText("<div>Один<br>Два</div><p><b>Три</b></p>"); got != "Один\nДва\nТри" {
		t.Fatalf("plain=%q", got)
	}
}
func TestTaskGroupingAndFilters(t *testing.T) {
	items := []workItem{{ID: 1, Fields: map[string]any{"System.State": "Выполнено", "System.WorkItemType": "Задача"}}, {ID: 2, Fields: map[string]any{"System.State": "Разработка", "System.WorkItemType": "Ошибка"}}, {ID: 3, Fields: map[string]any{"System.State": "Новый", "System.WorkItemType": "Задача"}}}
	groups := groupTasks(items, "", "")
	if len(groups) != 3 || groups[0].Name != "Новый" || groups[1].Name != "Разработка" {
		t.Fatal(groups)
	}
	groups = groupTasks(items, "Разработка", "Ошибка")
	if len(groups) != 1 || groups[0].Items[0].ID != 2 {
		t.Fatal(groups)
	}
	if len(groupTasks(items, "Разработка", "Задача")) != 0 {
		t.Fatal("type filter ignored")
	}
}
func TestPRDiffUsesPinnedIterationAndCommonBase(t *testing.T) {
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/iterations"):
			writeJSON(w, list[prIteration]{Value: []prIteration{{ID: 2, Source: commitRef{ID: "src"}, Common: commitRef{ID: "base"}, Target: commitRef{ID: "wrong-target"}}, {ID: 3, Source: commitRef{ID: "new-src"}}}})
		case strings.HasSuffix(r.URL.Path, "/items"):
			q := r.URL.Query()
			if q.Get("versionDescriptor.versionType") != "commit" {
				t.Error("not a commit snapshot")
			}
			switch q.Get("versionDescriptor.version") {
			case "base":
				if q.Get("path") != "/old.go" {
					t.Error("rename base path lost")
				}
				fmt.Fprint(w, "package main\n// до\n")
			case "src":
				if q.Get("path") != "/new.go" {
					t.Error("source path lost")
				}
				fmt.Fprint(w, "package main\n// после <script>\n")
			default:
				t.Error("used current iteration or target instead of pinned common base")
				http.Error(w, "bad", 500)
			}
		default:
			t.Error(r.URL.Path)
			http.NotFound(w, r)
		}
	})
	w := appRequest(app, "GET", "/repo/r/pr/42/file?path=/new.go&oldPath=/old.go&iteration=2", nil)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "diff-line add") || !strings.Contains(body, "diff-line delete") || strings.Contains(body, "после <script>") {
		t.Fatalf("bad diff status=%d", w.Code)
	}
}
func TestPRDiffAddedAndDeletedFiles(t *testing.T) {
	for _, missing := range []string{"base", "src"} {
		t.Run(missing, func(t *testing.T) {
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/iterations") {
					writeJSON(w, list[prIteration]{Value: []prIteration{{ID: 1, Source: commitRef{ID: "src"}, Common: commitRef{ID: "base"}}}})
					return
				}
				if r.URL.Query().Get("versionDescriptor.version") == missing {
					http.NotFound(w, r)
					return
				}
				fmt.Fprint(w, "content\n")
			})
			w := appRequest(app, "GET", "/repo/r/pr/42/file?path=/file.txt", nil)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "diff-line") {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
}
func TestCommentAndReplyRoutes(t *testing.T) {
	for _, suffix := range []string{"comment", "thread/7/reply"} {
		t.Run(suffix, func(t *testing.T) {
			calls := 0
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || !strings.Contains(r.URL.Path, "/pullrequests/42/threads") {
					t.Error("wrong comment API", r.URL)
				}
				var data map[string]any
				_ = json.NewDecoder(r.Body).Decode(&data)
				if suffix == "comment" {
					comments := data["comments"].([]any)
					if comments[0].(map[string]any)["content"] != "Комментарий" {
						t.Error(data)
					}
				} else if data["content"] != "Комментарий" || !strings.HasSuffix(r.URL.Path, "/threads/7/comments") {
					t.Error(data, r.URL)
				}
				writeJSON(w, map[string]any{"id": 8})
			})
			w := appRequest(app, "POST", "/repo/r/pr/42/"+suffix, url.Values{"csrf": {"csrf-test"}, "comment": {"Комментарий"}})
			if w.Code != 303 || calls != 1 {
				t.Fatalf("status=%d calls=%d", w.Code, calls)
			}
		})
	}
}
func TestFileBrowserFoldersFirstWithoutSelf(t *testing.T) {
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, list[gitItem]{Value: []gitItem{{Path: "/src", IsFolder: true}, {Path: "/src/z.go"}, {Path: "/src/nested", IsFolder: true}, {Path: "/src/a.go"}}})
	})
	w := appRequest(app, "GET", "/repo/r/files?path=/src&branch=refs/heads/feature", nil)
	body := w.Body.String()
	if w.Code != 200 || strings.Index(body, ">nested /</a>") > strings.Index(body, ">a.go</a>") || strings.Contains(body, ">src /</a>") {
		t.Fatal("bad directory rendering")
	}
	if !strings.Contains(body, "На уровень выше") || !strings.Contains(body, "feature") {
		t.Fatal("navigation context lost")
	}
}

func TestJSONSourceIsNotAnAPIEnvelope(t *testing.T) {
	const source = `{"content":"это код файла","other":42}`
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("$format") != "text" {
			t.Error("raw content not requested")
		}
		fmt.Fprint(w, source)
	})
	w := appRequest(app, "GET", "/repo/r/file?path=/data.json", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "other") || !strings.Contains(w.Body.String(), "content") {
		t.Fatal("source JSON incorrectly unwrapped")
	}
}
