package main

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
)

type itemRelation struct {
	Rel string `json:"rel"`
	URL string `json:"url"`
}
type itemNode struct {
	Item     workItem
	Children []itemNode
}

func childIDs(item workItem) []int {
	return relationIDs(item, "System.LinkTypes.Hierarchy-Forward")
}

func relationIDs(item workItem, kind string) []int {
	var ids []int
	for _, rel := range item.Relations {
		if rel.Rel != kind {
			continue
		}
		u, err := url.Parse(rel.URL)
		if err != nil {
			continue
		}
		id, err := strconv.Atoi(path.Base(u.Path))
		if err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

func (c *client) parentItem(ctx context.Context, item workItem) (*workItem, error) {
	ids := relationIDs(item, "System.LinkTypes.Hierarchy-Reverse")
	if len(ids) == 0 {
		return nil, nil
	}
	// Only use the relation's ID, never send credentials to its URL.
	parent := workItem{ID: ids[0]}
	var loaded workItem
	if err := c.request(ctx, "GET", fmt.Sprintf("wit/workitems/%d", parent.ID), nil, nil, &loaded); err != nil {
		return &parent, err
	}
	parent.Fields = loaded.Fields
	return &parent, nil
}

func (s *server) itemHierarchy(ctx context.Context, p *page) {
	var err error
	p.Parent, err = s.c.parentItem(ctx, p.Item)
	if err != nil {
		p.Notice += "Не удалось загрузить сведения о родителе: " + err.Error() + ". "
	}
	p.Children, err = s.c.childTree(ctx, p.Item)
	if err != nil {
		p.Notice += "Дерево загружено не полностью: " + err.Error()
	}
}

// Follow hierarchy relations, not WIQL state filters: completed tasks belong here too.
func (c *client) childTree(ctx context.Context, root workItem) ([]itemNode, error) {
	items := map[int]workItem{root.ID: root}
	seen := map[int]bool{root.ID: true}
	queue := childIDs(root)
	var loadErr error
	for len(queue) > 0 {
		var ids []string
		next := queue
		queue = nil
		for _, id := range next {
			if seen[id] {
				continue
			}
			seen[id] = true
			if len(seen) > 2000 {
				loadErr = fmt.Errorf("предел 2000 элементов; откройте отдельную ветвь дерева")
				break
			}
			ids = append(ids, strconv.Itoa(id))
		}
		for start := 0; start < len(ids); start += 200 {
			var response list[workItem]
			end := min(start+200, len(ids))
			if err := c.request(ctx, "GET", "wit/workitems", url.Values{"ids": {strings.Join(ids[start:end], ",")}, "$expand": {"relations"}}, nil, &response); err != nil {
				loadErr = err
				break
			}
			for _, item := range response.Value {
				items[item.ID] = item
				queue = append(queue, childIDs(item)...)
			}
		}
		if loadErr != nil {
			break
		}
	}
	var build func(workItem, map[int]bool, int) []itemNode
	build = func(parent workItem, ancestors map[int]bool, depth int) []itemNode {
		if depth > 50 {
			loadErr = fmt.Errorf("слишком глубокая иерархия; откройте дочерний элемент")
			return nil
		}
		var nodes []itemNode
		for _, id := range childIDs(parent) {
			if ancestors[id] {
				continue
			}
			item, ok := items[id]
			if !ok {
				continue
			}
			ancestors[id] = true
			children := build(item, ancestors, depth+1)
			delete(ancestors, id)
			nodes = append(nodes, itemNode{item, children})
		}
		return nodes
	}
	return build(root, map[int]bool{root.ID: true}, 0), loadErr
}
