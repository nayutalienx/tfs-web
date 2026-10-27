package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestStateColors(t *testing.T) {
	for state, want := range map[string]string{
		"Приостановлено": "status-review", "Список задач": "status-todo",
		"Новый": "status-new", "New": "status-new", "Создано": "status-new",
		"Утверждено": "status-ready", "Approved": "status-ready",
		"Разработка": "status-active", " Выполняется ": "status-active", "ACTIVE": "status-active",
		"Тестирование": "status-review", "Проверка": "status-review",
		"Resolved": "status-resolved", "Решено": "status-resolved",
		"Выполнено": "status-done", "Закрыто": "status-done", "Done": "status-done",
		"Удалено": "status-removed", "Removed": "status-removed",
		"Blocked": "status-blocked", "Заблокировано": "status-blocked",
		"Мой статус": "status-unknown", "": "status-unknown", `<script>`: "status-unknown",
	} {
		if got := stateClass(state); got != want {
			t.Errorf("stateClass(%q) = %q, want %q", state, got, want)
		}
	}
	if prStatusClass("completed") != "status-review" || prStatusClass("active") != "status-active" || prStatusClass("abandoned") != "status-removed" {
		t.Fatal("PR colors must distinguish active, merged and abandoned PRs")
	}
}

func TestStateColorsInTemplates(t *testing.T) {
	item := func(id int, state string) workItem {
		return workItem{ID: id, Fields: map[string]any{"System.State": state, "System.Title": "Задача", "System.WorkItemType": "Задача"}}
	}
	parent, current, child := item(1, "Разработка"), item(2, "Выполнено"), item(3, "Решено")
	p := page{Item: current, Parent: &parent, Children: []itemNode{{Item: child}}, Form: map[string]string{}, LinkedItems: []workItem{child}}
	for _, tc := range []struct {
		name string
		data any
		want []string
	}{
		{"workitem", p, []string{`badge status-active">Разработка`, `badge status-done">Выполнено`, `badge status-resolved">Решено`}},
		{"task-board", page{Groups: groupTasks([]workItem{current, parent}, "", "")}, []string{`status-label status-active">Разработка`, `status-label status-done">Выполнено`, `badge status-active">Разработка`, `badge status-done">Выполнено`}},
		{"pr-card", pullRequest{Status: "completed"}, []string{`badge status-review">Слит`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := tmpl.ExecuteTemplate(&b, tc.name, tc.data); err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(b.String(), want) {
					t.Errorf("missing %q", want)
				}
			}
		})
	}
}
