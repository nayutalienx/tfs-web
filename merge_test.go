package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestMergeGuardsAndPayload(t *testing.T) {
	for _, tc := range []struct {
		name, status, merge, commit, confirm, csrf string
		draft                                      bool
		code                                       int
	}{
		{"success", "active", "succeeded", "abc", "yes", "csrf-test", false, 303},
		{"changed source", "active", "succeeded", "old", "yes", "csrf-test", false, 409},
		{"completed", "completed", "succeeded", "abc", "yes", "csrf-test", false, 400},
		{"draft", "active", "succeeded", "abc", "yes", "csrf-test", true, 400},
		{"conflicts", "active", "conflicts", "abc", "yes", "csrf-test", false, 400},
		{"pending", "active", "queued", "abc", "yes", "csrf-test", false, 400},
		{"no confirmation", "active", "succeeded", "abc", "", "csrf-test", false, 400},
		{"csrf", "active", "succeeded", "abc", "yes", "wrong", false, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutations := 0
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					writeJSON(w, pullRequest{ID: 42, Status: tc.status, MergeStatus: tc.merge, IsDraft: tc.draft, LastMergeSourceCommit: commitRef{ID: "abc"}})
					return
				}
				mutations++
				if r.Method != "PATCH" || r.URL.Path != "/DemoProject/_apis/git/repositories/r/pullrequests/42" {
					t.Errorf("unexpected mutation: %s %s", r.Method, r.URL)
				}
				var body struct {
					Status                string
					LastMergeSourceCommit commitRef
					CompletionOptions     map[string]any
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Status != "completed" || body.LastMergeSourceCommit.ID != "abc" || body.CompletionOptions["bypassPolicy"] != false || body.CompletionOptions["deleteSourceBranch"] != false || body.CompletionOptions["transitionWorkItems"] != false || body.CompletionOptions["mergeStrategy"] != "noFastForward" {
					t.Errorf("unsafe payload: %+v", body)
				}
				writeJSON(w, pullRequest{Status: "completed"})
			})
			w := appRequest(app, "POST", "/repo/r/pr/42/merge", url.Values{"csrf": {tc.csrf}, "sourceCommit": {tc.commit}, "confirm": {tc.confirm}})
			if w.Code != tc.code {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
			if (mutations == 1) != (tc.code == 303) {
				t.Fatalf("mutations %d", mutations)
			}
		})
	}
}

func TestCompletedPRLists(t *testing.T) {
	for _, path := range []string{"/prs", "/repo/r/prs"} {
		t.Run(path, func(t *testing.T) {
			calls := 0
			app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				q := r.URL.Query()
				if q.Get("searchCriteria.status") != "completed" || q.Get("$skip") != "100" || q.Get("searchCriteria.creatorId") != "" {
					t.Errorf("query %s", r.URL)
				}
				items := make([]pullRequest, 100)
				for i := range items {
					items[i] = pullRequest{ID: 100 + i, Title: "Слитый PR", Status: "completed", Repository: repository{ID: "r"}}
				}
				writeJSON(w, list[pullRequest]{Value: items})
			})
			w := appRequest(app, "GET", path+"?status=completed&skip=100", nil)
			body := w.Body.String()
			if w.Code != 200 || calls != 1 || !strings.Contains(body, "Слитый PR") || !strings.Contains(body, "/pr/100/changes") || !strings.Contains(body, "status=completed&amp;skip=200") || strings.Contains(body, "Мои активные PR") {
				t.Fatalf("status=%d calls=%d completed list/pagination invalid", w.Code, calls)
			}
		})
	}
}

func TestMergePolicyRejectionNotRetried(t *testing.T) {
	calls := 0
	app := testApp(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			writeJSON(w, pullRequest{Status: "active", MergeStatus: "succeeded", LastMergeSourceCommit: commitRef{ID: "abc"}})
			return
		}
		calls++
		http.Error(w, "policy denied", 403)
	})
	w := appRequest(app, "POST", "/repo/r/pr/42/merge", url.Values{"csrf": {"csrf-test"}, "sourceCommit": {"abc"}, "confirm": {"yes"}})
	if w.Code != 502 || calls != 1 {
		t.Fatalf("status %d mutations %d", w.Code, calls)
	}
}
