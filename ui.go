package main

import (
	"fmt"
	"html"
	"html/template"
	"net/url"
	"sort"
	"strings"

	xhtml "golang.org/x/net/html"
)

type taskGroup struct {
	Name  string
	Items []workItem
}
type codeLine struct {
	Number int
	Text   string
}
type diffLine struct {
	Kind, Text string
	Old, New   string
}

func shortType(s string) string {
	if s == "Элемент невыполненной работы по продукту" {
		return "Элемент бэклога"
	}
	return s
}

func activeSection(title string) string {
	switch title {
	case "Бэклог", "Мои задачи", "Рабочий элемент", "Новый элемент", "Поиск по бэклогу":
		return "backlog"
	case "Активные pull requests", "Pull request", "Изменения PR", "Код PR", "Новый pull request":
		return "prs"
	default:
		return "repos"
	}
}
func fieldText(fields map[string]any, key string) string {
	if v := fields[key]; v != nil {
		return fmt.Sprint(v)
	}
	return ""
}
func personName(v any) string {
	if m, ok := v.(map[string]any); ok {
		if name, ok := m["displayName"].(string); ok {
			return name
		}
	}
	if v == nil {
		return "Не назначено"
	}
	return fmt.Sprint(v)
}
func groupTasks(items []workItem, state, kind string) []taskGroup {
	groups := map[string][]workItem{}
	for _, item := range items {
		s, k := fieldText(item.Fields, "System.State"), fieldText(item.Fields, "System.WorkItemType")
		if state != "" && s != state || kind != "" && k != kind {
			continue
		}
		groups[s] = append(groups[s], item)
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	rank := func(s string) int {
		switch s {
		case "Новый", "Новое", "Создано":
			return 0
		case "Утверждено", "К выполнению":
			return 1
		case "Разработка", "Выполняется", "Активный":
			return 2
		case "Тестирование", "Проверка":
			return 3
		case "Выполнено", "Закрыто":
			return 5
		case "Удалено":
			return 6
		}
		return 4
	}
	sort.Slice(names, func(i, j int) bool {
		if rank(names[i]) != rank(names[j]) {
			return rank(names[i]) < rank(names[j])
		}
		return names[i] < names[j]
	})
	result := make([]taskGroup, 0, len(names))
	for _, name := range names {
		result = append(result, taskGroup{name, groups[name]})
	}
	return result
}
func numberedCode(content string) []codeLine {
	if content == "" {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	out := make([]codeLine, len(lines))
	for i, line := range lines {
		out[i] = codeLine{i + 1, line}
	}
	return out
}

// Rich TFS fields are untrusted. Rebuild only a small formatting allowlist;
// scripts, styles, embedded content and event handlers never survive.
// Images are allowed only through the local, collection-scoped attachment proxy.
func safeRich(input string, imageBase ...string) template.HTML {
	doc, err := xhtml.Parse(strings.NewReader(input))
	if err != nil {
		return template.HTML(html.EscapeString(input))
	}
	allowed := map[string]bool{"p": true, "div": true, "span": true, "br": true, "strong": true, "b": true, "em": true, "i": true, "u": true, "s": true, "ul": true, "ol": true, "li": true, "h1": true, "h2": true, "h3": true, "h4": true, "pre": true, "code": true, "blockquote": true, "table": true, "thead": true, "tbody": true, "tr": true, "th": true, "td": true, "hr": true, "a": true}
	var out strings.Builder
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.TextNode {
			out.WriteString(html.EscapeString(n.Data))
			return
		}
		if n.Type == xhtml.ElementNode {
			if n.Data == "img" && len(imageBase) > 0 {
				for _, attr := range n.Attr {
					if attr.Key == "src" {
						if id := attachmentID(imageBase[0], attr.Val); id != "" {
							src := "/attachments/" + id
							out.WriteString(`<a href="` + src + `" target="_blank" rel="noopener noreferrer"><img class="screenshot" src="` + src + `" alt="Скриншот — открыть полный размер" loading="lazy"></a>`)
						}
					}
				}
				return
			}
			switch n.Data {
			case "script", "style", "iframe", "object", "embed", "svg", "math", "template", "head":
				return
			}
		}
		tag := n.Type == xhtml.ElementNode && allowed[n.Data]
		if tag {
			out.WriteString("<" + n.Data)
			if n.Data == "a" {
				for _, a := range n.Attr {
					if a.Key != "href" {
						continue
					}
					u, err := url.Parse(strings.TrimSpace(a.Val))
					if err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil {
						out.WriteString(` href="` + html.EscapeString(u.String()) + `" rel="noreferrer noopener"`)
					}
				}
			}
			out.WriteString(">")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if tag && n.Data != "br" && n.Data != "hr" {
			out.WriteString("</" + n.Data + ">")
		}
	}
	walk(doc)
	return template.HTML(out.String()) // Only the reconstructed allowlist is trusted.
}
func plainText(input string) string {
	doc, err := xhtml.Parse(strings.NewReader(input))
	if err != nil {
		return input
	}
	var out strings.Builder
	newline := func() {
		if out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
			out.WriteByte('\n')
		}
	}
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.TextNode {
			out.WriteString(n.Data)
			return
		}
		if n.Type == xhtml.ElementNode {
			switch n.Data {
			case "script", "style", "head", "template":
				return
			case "br":
				out.WriteByte('\n')
				return
			case "p", "div", "li", "h1", "h2", "h3", "h4", "tr", "pre", "blockquote":
				newline()
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == xhtml.ElementNode {
			switch n.Data {
			case "p", "div", "li", "h1", "h2", "h3", "h4", "tr", "pre", "blockquote":
				newline()
			case "td", "th":
				out.WriteString("\t")
			}
		}
	}
	walk(doc)
	return strings.TrimSpace(out.String())
}
