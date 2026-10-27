package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
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
func (s *server) itemStates(ctx context.Context, item workItem) []itemState {
	var states list[itemState]
	err := s.c.request(ctx, "GET", "wit/workitemtypes/"+url.PathEscape(fieldText(item.Fields, "System.WorkItemType"))+"/states", nil, nil, &states)
	current := fieldText(item.Fields, "System.State")
	if err != nil {
		return []itemState{{current}}
	}
	for _, state := range states.Value {
		if state.Name == current {
			return states.Value
		}
	}
	return append([]itemState{{current}}, states.Value...)
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
		p := page{Title: "Рабочий элемент", Item: item, Form: form, States: s.itemStates(r.Context(), item), StatusCode: status}
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
