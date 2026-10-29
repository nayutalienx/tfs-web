package main

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

const workItemLimit = 100

// Bound every list, including sprint views, to one WIQL query and one details
// request. Search is applied upstream before the result limit.
func (c *client) queryWorkItems(ctx context.Context, where string, limit int) ([]workItem, error) {
	if limit <= 0 || limit > workItemLimit {
		limit = workItemLimit
	}
	var result struct {
		WorkItems []struct {
			ID int `json:"id"`
		} `json:"workItems"`
	}
	query := "SELECT [System.Id] FROM WorkItems WHERE " + where + " ORDER BY [System.ChangedDate] DESC"
	if err := c.request(ctx, "POST", "wit/wiql", url.Values{"$top": {strconv.Itoa(limit)}}, map[string]string{"query": query}, &result); err != nil {
		return nil, err
	}
	if len(result.WorkItems) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, min(len(result.WorkItems), limit))
	for _, ref := range result.WorkItems[:min(len(result.WorkItems), limit)] {
		ids = append(ids, strconv.Itoa(ref.ID))
	}
	var items list[workItem]
	if err := c.request(ctx, "GET", "wit/workitems", url.Values{"ids": {strings.Join(ids, ",")}}, nil, &items); err != nil {
		return nil, err
	}
	if len(items.Value) != len(ids) {
		return nil, fmt.Errorf("TFS вернул неполный набор элементов бэклога")
	}
	sort.SliceStable(items.Value, func(i, j int) bool {
		return fieldText(items.Value[i].Fields, "System.ChangedDate") > fieldText(items.Value[j].Fields, "System.ChangedDate")
	})
	return items.Value, nil
}

type sprintOption struct {
	Path, Label, Start string
}

type iterationNode struct {
	Name        string          `json:"name"`
	HasChildren bool            `json:"hasChildren"`
	Children    []iterationNode `json:"children"`
	Attributes  struct {
		Start  string `json:"startDate"`
		Finish string `json:"finishDate"`
	} `json:"attributes"`
}

// Use project classification nodes rather than only the team's current/future
// iterations, so past and unassigned-to-team sprints remain accessible too.
func (c *client) projectSprints(ctx context.Context) ([]sprintOption, error) {
	var options []sprintOption
	var walk func(string, string, *iterationNode) error
	walk = func(resource, parent string, node *iterationNode) error {
		if node == nil || node.HasChildren && len(node.Children) == 0 {
			var loaded iterationNode
			if err := c.request(ctx, "GET", resource, url.Values{"$depth": {"2"}}, nil, &loaded); err != nil {
				return err
			}
			node = &loaded
			if node.HasChildren && len(node.Children) == 0 {
				return fmt.Errorf("TFS вернул неполное дерево итераций")
			}
		}
		path := node.Name
		if parent != "" {
			path = parent + "\\" + node.Name
		}
		label := path
		if node.Attributes.Start != "" {
			label += " · " + strings.Split(node.Attributes.Start, "T")[0]
			if node.Attributes.Finish != "" {
				label += " — " + strings.Split(node.Attributes.Finish, "T")[0]
			}
		}
		options = append(options, sprintOption{Path: path, Label: label, Start: node.Attributes.Start})
		for i := range node.Children {
			child := &node.Children[i]
			if err := walk(resource+"/"+url.PathEscape(child.Name), path, child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk("wit/classificationnodes/iterations", "", nil); err != nil {
		return nil, err
	}
	sort.SliceStable(options, func(i, j int) bool {
		if options[i].Start != options[j].Start {
			return options[i].Start < options[j].Start
		}
		return options[i].Path < options[j].Path
	})
	return options, nil
}

func setSprintNavigation(p *page) {
	link := func(path string) string {
		return "/backlog?" + url.Values{"scope": {"all"}, "sprints": {"1"}, "iteration": {path}}.Encode()
	}
	for i, option := range p.Sprints {
		if option.Path != p.SprintPath {
			continue
		}
		if i > 0 {
			p.SprintPrev = link(p.Sprints[i-1].Path)
		}
		if i+1 < len(p.Sprints) {
			p.SprintNext = link(p.Sprints[i+1].Path)
		}
		break
	}
}
