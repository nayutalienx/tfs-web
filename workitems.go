package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

type apiError struct{ Status int }

func (e *apiError) Error() string {
	if e.Status == 409 || e.Status == 412 {
		return "Элемент изменён другим пользователем. Обновите страницу и сравните значения перед сохранением."
	}
	return fmt.Sprintf("TFS вернул HTTP %d. Если выполнялось изменение, перед повтором проверьте данные на сервере.", e.Status)
}

type itemState struct {
	Name string `json:"name"`
}

func parseWork(value string) (float64, error) {
	n, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
		return 0, fmt.Errorf("неверная оценка")
	}
	return n, nil
}

func normalizeNewlines(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}

type editableField struct {
	Input, Field string
	Rich, Number bool
}

var editableFields = []editableField{
	{"title", "System.Title", false, false}, {"state", "System.State", false, false},
	{"iteration", "System.IterationPath", false, false}, {"area", "System.AreaPath", false, false},
	{"assigned", "System.AssignedTo", false, false}, {"tags", "System.Tags", false, false},
	{"activity", "Microsoft.VSTS.Common.Activity", false, false},
	{"priority", "Microsoft.VSTS.Common.Priority", false, true},
	{"remaining", "Microsoft.VSTS.Scheduling.RemainingWork", false, true},
	{"original", "Microsoft.VSTS.Scheduling.OriginalEstimate", false, true},
	{"completed", "Microsoft.VSTS.Scheduling.CompletedWork", false, true},
	{"description", "System.Description", true, false},
	{"criteria", "Microsoft.VSTS.Common.AcceptanceCriteria", true, false},
	{"repro", "Microsoft.VSTS.TCM.ReproSteps", true, false},
}

func itemForm(item workItem) map[string]string {
	form := map[string]string{"rev": strconv.Itoa(item.Rev)}
	for _, f := range editableFields {
		value := fieldText(item.Fields, f.Field)
		if f.Rich {
			value = plainText(value)
		}
		if f.Input == "assigned" {
			if v, ok := item.Fields[f.Field].(map[string]any); ok {
				value, _ = v["uniqueName"].(string)
			}
		}
		form[f.Input] = value
	}
	return form
}
func (c *client) workItemStates(ctx context.Context, kind string) ([]itemState, error) {
	if kind == "" {
		return nil, errors.New("не указан тип элемента")
	}
	path := "wit/workitemtypes/" + url.PathEscape(kind)
	var states list[itemState]
	err := c.request(ctx, "GET", path+"/states", url.Values{"api-version": {"6.0-preview.1"}}, nil, &states)
	if err == nil && len(states.Value) > 0 {
		return states.Value, nil
	}
	// Older servers may expose workflow metadata only on the type definition.
	if err != nil {
		var api *apiError
		if !errors.As(err, &api) || (api.Status != 400 && api.Status != 404 && api.Status != 405) {
			return nil, err
		}
	}
	var definition struct {
		States      []itemState `json:"states"`
		Transitions map[string][]struct {
			To string `json:"to"`
		} `json:"transitions"`
	}
	if err := c.request(ctx, "GET", path, nil, nil, &definition); err != nil {
		return nil, err
	}
	if len(definition.States) > 0 {
		return definition.States, nil
	}
	names := map[string]bool{}
	for from, transitions := range definition.Transitions {
		if from != "" {
			names[from] = true
		}
		for _, transition := range transitions {
			if transition.To != "" {
				names[transition.To] = true
			}
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		states.Value = append(states.Value, itemState{Name: name})
	}
	if len(states.Value) == 0 {
		return nil, errors.New("TFS не вернул состояния типа элемента")
	}
	return states.Value, nil
}

func (s *server) itemStates(ctx context.Context, p *page) {
	states, err := s.c.workItemStates(ctx, fieldText(p.Item.Fields, "System.WorkItemType"))
	if err != nil {
		p.Notice += " Не удалось загрузить доступные состояния. Состояние можно указать вручную: " + err.Error()
		return
	}
	seen := map[string]bool{}
	for _, state := range append(states, itemState{Name: fieldText(p.Item.Fields, "System.State")}, itemState{Name: p.Form["state"]}) {
		if state.Name != "" && !seen[state.Name] {
			p.States = append(p.States, state)
			seen[state.Name] = true
		}
	}
}
func (s *server) itemSprints(ctx context.Context, p *page) {
	options, err := s.c.projectSprints(ctx)
	if err != nil {
		p.Notice += " Не удалось загрузить список спринтов. Путь итерации можно указать вручную: " + err.Error()
		return
	}
	// Preserve both the stored iteration and an unsaved selection, even if
	// the classification tree no longer contains either path.
	for _, path := range []string{fieldText(p.Item.Fields, "System.IterationPath"), p.Form["iteration"]} {
		if path == "" {
			continue
		}
		found := false
		for _, option := range options {
			if option.Path == path {
				found = true
				break
			}
		}
		if !found {
			options = append(options, sprintOption{Path: path, Label: path})
		}
	}
	p.Sprints = options
}

func (s *server) updateWorkItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id < 1 {
		formError(w, "Некорректный ID")
		return
	}
	var item workItem
	if err = s.c.request(r.Context(), "GET", fmt.Sprintf("wit/workitems/%d", id), url.Values{"$expand": {"relations"}}, nil, &item); err != nil {
		s.fail(w, err)
		return
	}
	form := itemForm(item)
	for _, f := range editableFields {
		if r.PostForm.Has(f.Input) {
			form[f.Input] = r.PostForm.Get(f.Input)
		}
	}
	form["rev"], form["comment"] = r.FormValue("rev"), r.FormValue("comment")
	failure := func(status int, message string) {
		p := page{Title: "Рабочий элемент", Item: item, Form: form, StatusCode: status}
		s.itemStates(r.Context(), &p)
		s.itemSprints(r.Context(), &p)
		s.itemHierarchy(r.Context(), &p)
		s.render(w, p, errors.New(message))
	}
	rev, err := strconv.Atoi(r.FormValue("rev"))
	if err != nil || rev < 1 {
		failure(400, "Не указана ревизия. Откройте карточку элемента заново.")
		return
	}
	if rev != item.Rev {
		failure(409, "Элемент уже изменён. Ваши введённые значения сохранены в форме ниже. Откройте свежую карточку в новой вкладке и сравните данные; не отправляйте эту устаревшую форму повторно.")
		return
	}
	p := patch{{"op": "test", "path": "/rev", "value": rev}}
	current := itemForm(item)
	for _, f := range editableFields {
		if !r.PostForm.Has(f.Input) {
			continue
		}
		value := strings.TrimSpace(form[f.Input])
		previous := strings.TrimSpace(current[f.Input])
		if f.Rich {
			// HTML forms submit CRLF even when the textarea was rendered with LF.
			// This must not rewrite otherwise unchanged rich-text fields.
			value, previous = normalizeNewlines(value), normalizeNewlines(previous)
		}
		if value == previous {
			continue
		}
		if value == "" && (f.Input == "title" || f.Input == "iteration" || f.Input == "state" || f.Input == "area") {
			failure(400, "Заголовок, состояние, область и итерация не могут быть пустыми.")
			return
		}
		kind := fieldText(item.Fields, "System.WorkItemType")
		if value == "" && (f.Input == "criteria" && kind == "Элемент невыполненной работы по продукту" || f.Input == "repro" && kind == "Ошибка") {
			failure(400, "Обязательное поле для этого типа элемента нельзя очистить.")
			return
		}
		var typed any = value
		if f.Number {
			if value == "" {
				if item.Fields[f.Field] != nil {
					p = append(p, map[string]any{"op": "remove", "path": "/fields/" + f.Field})
				}
				continue
			}
			n, e := strconv.ParseFloat(value, 64)
			if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || f.Input == "priority" && (n < 1 || n > 4 || n != math.Trunc(n)) {
				failure(400, "Оценки должны быть неотрицательными числами, приоритет — целым числом от 1 до 4.")
				return
			}
			typed = n
		}
		if f.Rich {
			typed = rich(value)
		}
		p = append(p, map[string]any{"op": "add", "path": "/fields/" + f.Field, "value": typed})
	}
	if comment := strings.TrimSpace(r.FormValue("comment")); comment != "" {
		addField(&p, "System.History", rich(comment))
	}
	if len(p) == 1 {
		http.Redirect(w, r, fmt.Sprintf("/workitem/%d", id), 303)
		return
	}
	var updated workItem
	if err = s.c.request(r.Context(), "PATCH", fmt.Sprintf("wit/workitems/%d", id), nil, p, &updated); err != nil {
		status := 502
		var ae *apiError
		if errors.As(err, &ae) && (ae.Status == 409 || ae.Status == 412) {
			status = 409
		}
		failure(status, err.Error())
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/workitem/%d", id), 303)
}
