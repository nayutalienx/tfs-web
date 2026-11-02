package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestBacklogTagColumn(t *testing.T) {
	if got := workItemTags(" ; Интерфейс ; ; API ; "); !reflect.DeepEqual(got, []string{"Интерфейс", "API"}) {
		t.Fatalf("tags=%v", got)
	}
	p := page{Groups: []taskGroup{{Name: "Новый", Items: []workItem{
		{ID: 42, Fields: map[string]any{"System.Title": "Пример", "System.Tags": `Интерфейс; <script>alert(1)</script>`}},
		{ID: 43, Fields: map[string]any{"System.Title": "Без тегов"}},
	}}}}
	var output bytes.Buffer
	if err := tmpl.ExecuteTemplate(&output, "task-board", p); err != nil {
		t.Fatal(err)
	}
	body := output.String()
	if strings.Count(body, `class="task-tags"`) != 2 || !strings.Contains(body, `class="task-tag">Интерфейс</span>`) || !strings.Contains(body, `<span class="muted">—</span>`) {
		t.Fatal("tag column missing or incorrectly rendered")
	}
	if strings.Contains(body, "<script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("tag content must be escaped")
	}
}
