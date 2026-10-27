package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pmezard/go-difflib/difflib"
)

type commitRef struct {
	ID string `json:"commitId"`
}
type prIteration struct {
	ID     int       `json:"id"`
	Source commitRef `json:"sourceRefCommit"`
	Target commitRef `json:"targetRefCommit"`
	Common commitRef `json:"commonRefCommit"`
}

type fileDiff struct {
	Path, OldPath, ChangeType, Error string
	Lines                            []diffLine
}

func unifiedLines(before, after, oldPath, path string) ([]diffLine, error) {
	if before == after {
		return nil, nil
	}
	if len(before)+len(after) > 128<<10 || strings.Count(before, "\n")+strings.Count(after, "\n") > 4000 {
		return nil, fmt.Errorf("Большой файл: откройте версии до и после отдельно")
	}
	text, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{A: difflib.SplitLines(before), B: difflib.SplitLines(after), FromFile: "a" + oldPath, ToFile: "b" + path, Context: 3})
	if err != nil {
		return nil, err
	}
	var lines []diffLine
	old, new := 0, 0
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		d := diffLine{Kind: "context", Text: line}
		switch {
		case strings.HasPrefix(line, "@@"):
			d.Kind = "hunk"
			// The range length is optional when it is one line.
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				old, _ = strconv.Atoi(strings.Split(strings.TrimPrefix(parts[1], "-"), ",")[0])
				new, _ = strconv.Atoi(strings.Split(strings.TrimPrefix(parts[2], "+"), ",")[0])
			}
		case len(lines) < 2 && (strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++")):
			d.Kind = "header"
		case strings.HasPrefix(line, "+"):
			d.Kind = "add"
			d.New = strconv.Itoa(new)
			new++
		case strings.HasPrefix(line, "-"):
			d.Kind = "delete"
			d.Old = strconv.Itoa(old)
			old++
		case strings.HasPrefix(line, " "):
			d.Old = strconv.Itoa(old)
			d.New = strconv.Itoa(new)
			old++
			new++
		}
		lines = append(lines, d)
	}
	return lines, nil
}

func (c *client) changeDiff(ctx context.Context, repo string, it prIteration, change gitChange) fileDiff {
	d := fileDiff{Path: change.Item.Path, OldPath: change.OriginalPath, ChangeType: change.ChangeType}
	if d.OldPath == "" {
		d.OldPath = d.Path
	}
	base := it.Common.ID
	if base == "" {
		base = it.Target.ID
	}
	if base == "" || it.Source.ID == "" {
		d.Error = "Не получены коммиты для сравнения"
		return d
	}
	before, bExists, err := c.fileAtCommit(ctx, repo, d.OldPath, base)
	if err != nil {
		d.Error = err.Error()
		return d
	}
	after, aExists, err := c.fileAtCommit(ctx, repo, d.Path, it.Source.ID)
	if err != nil {
		d.Error = err.Error()
		return d
	}
	if !bExists && !aExists {
		d.Error = "Файл не найден в обоих коммитах"
		return d
	}
	d.Lines, err = unifiedLines(before, after, d.OldPath, d.Path)
	if err != nil {
		d.Error = err.Error()
	}
	return d
}

func (c *client) iteration(ctx context.Context, base string, requested int) (prIteration, error) {
	var iterations list[prIteration]
	if err := c.request(ctx, "GET", base+"/iterations", nil, nil, &iterations); err != nil {
		return prIteration{}, err
	}
	var chosen prIteration
	for _, it := range iterations.Value {
		if requested > 0 && it.ID == requested {
			return it, nil
		}
		if requested == 0 && it.ID > chosen.ID {
			chosen = it
		}
	}
	if chosen.ID == 0 {
		return chosen, fmt.Errorf("Итерация PR не найдена")
	}
	return chosen, nil
}
func (s *server) codeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /repo/{repo}/pr/{id}/changes", func(w http.ResponseWriter, r *http.Request) {
		repo, id, base, ok := prRoute(w, r)
		if !ok {
			return
		}
		requested, _ := strconv.Atoi(r.URL.Query().Get("iteration"))
		it, err := s.c.iteration(r.Context(), base, requested)
		p := page{Title: "Изменения PR", Repo: repo, PR: pullRequest{ID: id}, Iteration: it.ID}
		if err == nil {
			var changes struct {
				Entries  []gitChange `json:"changeEntries"`
				NextSkip int         `json:"nextSkip"`
			}
			skip := skipValue(r)
			err = s.c.request(r.Context(), "GET", fmt.Sprintf("%s/iterations/%d/changes", base, it.ID), url.Values{"$top": {"100"}, "$skip": {strconv.Itoa(skip)}, "$compareTo": {"0"}}, nil, &changes)
			p.Changes = changes.Entries
			if err == nil {
				// Keep initial render bounded. Every other file remains available individually.
				ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
				p.FileDiffs = make([]fileDiff, min(8, len(changes.Entries)))
				var wg sync.WaitGroup
				limit := make(chan struct{}, 4)
				for i := range p.FileDiffs {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						limit <- struct{}{}
						defer func() { <-limit }()
						p.FileDiffs[i] = s.c.changeDiff(ctx, repo, it, changes.Entries[i])
					}(i)
				}
				wg.Wait()
				cancel()
				if len(changes.Entries) > 8 {
					p.Notice = "Встроенный diff первых 8 файлов. Остальные откройте по ссылкам в списке."
				}
			}
			local := fmt.Sprintf("/repo/%s/pr/%d/changes?iteration=%d&skip=", url.PathEscape(repo), id, it.ID)
			if skip > 0 {
				p.Prev = local + strconv.Itoa(max(0, skip-100))
			}
			if changes.NextSkip > skip {
				p.Next = local + strconv.Itoa(changes.NextSkip)
			} else if len(changes.Entries) == 100 {
				p.Next = local + strconv.Itoa(skip+100)
			}
		}
		s.render(w, p, err)
	})
	mux.HandleFunc("GET /repo/{repo}/pr/{id}/file", func(w http.ResponseWriter, r *http.Request) {
		repo, id, base, ok := prRoute(w, r)
		if !ok {
			return
		}
		path := r.URL.Query().Get("path")
		oldPath := r.URL.Query().Get("oldPath")
		if path == "" {
			formError(w, "Укажите путь к файлу")
			return
		}
		if oldPath == "" {
			oldPath = path
		}
		requested, _ := strconv.Atoi(r.URL.Query().Get("iteration"))
		it, err := s.c.iteration(r.Context(), base, requested)
		p := page{Title: "Код PR", Repo: repo, PR: pullRequest{ID: id}, Path: path, OldPath: oldPath, Iteration: it.ID, SourceCommit: it.Source.ID, TargetCommit: it.Common.ID}
		if p.TargetCommit == "" {
			p.TargetCommit = it.Target.ID
		}
		if err == nil && (p.SourceCommit == "" || p.TargetCommit == "") {
			err = fmt.Errorf("TFS не вернул снимки коммитов для сравнения")
		}
		if err == nil {
			before, beforeExists, e := s.c.fileAtCommit(r.Context(), repo, oldPath, p.TargetCommit)
			if e != nil {
				s.render(w, p, e)
				return
			}
			after, afterExists, e := s.c.fileAtCommit(r.Context(), repo, path, p.SourceCommit)
			if e != nil {
				s.render(w, p, e)
				return
			}
			if !beforeExists && !afterExists {
				s.render(w, p, fmt.Errorf("Файл не найден ни в исходном, ни в базовом коммите"))
				return
			}
			p.BeforeLines, p.Lines = numberedCode(before), numberedCode(after)
			if len(before)+len(after) > 128<<10 || len(p.BeforeLines)+len(p.Lines) > 4000 {
				p.Notice = "Файл слишком большой для вычисления diff: ниже доступны обе версии кода (до 2 МиБ каждая)."
			} else {
				p.Diff, err = unifiedLines(before, after, oldPath, path)
			}
		}
		s.render(w, p, err)
	})
}
func (c *client) fileAtCommit(ctx context.Context, repo, path, commit string) (string, bool, error) {
	var file fileContent
	err := c.request(ctx, "GET", "git/repositories/"+url.PathEscape(repo)+"/items", url.Values{"path": {path}, "includeContent": {"true"}, "versionDescriptor.version": {commit}, "versionDescriptor.versionType": {"commit"}}, nil, &file)
	var ae *apiError
	if errors.As(err, &ae) && ae.Status == 404 {
		return "", false, nil
	}
	return file.Text, err == nil, err
}
