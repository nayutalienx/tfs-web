package main

import (
	"fmt"
	"net/http"
	"net/url"
)

// Completion is explicit, tied to the displayed source commit, and never bypasses policies.
func (s *server) mergePR(w http.ResponseWriter, r *http.Request) {
	repo, id, base, ok := prRoute(w, r)
	if !ok {
		return
	}
	source := r.FormValue("sourceCommit")
	if r.FormValue("confirm") != "yes" || source == "" {
		formError(w, "Подтвердите слияние и обновите страницу PR")
		return
	}
	var pr pullRequest
	if err := s.c.request(r.Context(), "GET", base, nil, nil, &pr); err != nil {
		s.fail(w, err)
		return
	}
	if pr.Status != "active" || pr.IsDraft || pr.MergeStatus != "succeeded" {
		formError(w, "Слияние недоступно: PR должен быть активным, не черновиком и без конфликтов; проверка слияния должна завершиться")
		return
	}
	if pr.LastMergeSourceCommit.ID != source {
		http.Error(w, "Исходная ветка изменилась. Обновите PR и проверьте новый код перед слиянием", http.StatusConflict)
		return
	}
	body := map[string]any{
		"status":                "completed",
		"lastMergeSourceCommit": commitRef{ID: source},
		"completionOptions":     map[string]any{"mergeStrategy": "noFastForward", "deleteSourceBranch": false, "bypassPolicy": false, "transitionWorkItems": false},
	}
	var result pullRequest
	if err := s.c.request(r.Context(), "PATCH", base, nil, body, &result); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/repo/%s/pr/%d", url.PathEscape(repo), id), http.StatusSeeOther)
}
