// Package filetree is the pure, I/O-free resolution core for the Viewer chrome.
// It consumes the set of project-root-relative Bundle Manifest keys and a request
// path and decides what the path addresses. project_root never enters this module
// — that purity is the structural privacy guarantee (PRD: Viewer Chrome, ADR-0024).
package filetree

import (
	"sort"
	"strings"
)

// Kind is the category of node a request path resolves to.
type Kind int

const (
	// NotFound means the path matches neither a manifest key nor a directory prefix.
	NotFound Kind = iota
	// File means the path is an exact manifest key.
	File
	// Dir means the path is a directory prefix shared by one or more keys.
	Dir
)

// Result is the outcome of Classify. For File, Key is the exact manifest key; for
// Dir, Key is the normalized directory prefix (no leading/trailing slash).
type Result struct {
	Kind Kind
	Key  string
}

// Classify resolves reqPath against the manifest keys. An exact key match wins
// over a directory prefix (so a file and a same-stem folder can coexist). A path
// that is the prefix of some key (or the empty root) is a directory; anything
// else is NotFound. Leading and trailing slashes are ignored.
func Classify(keys []string, reqPath string) Result {
	p := strings.Trim(reqPath, "/")
	for _, k := range keys {
		if k == p {
			return Result{Kind: File, Key: k}
		}
	}
	if p == "" {
		return Result{Kind: Dir, Key: ""}
	}
	prefix := p + "/"
	for _, k := range keys {
		if strings.HasPrefix(k, prefix) {
			return Result{Kind: Dir, Key: p}
		}
	}
	return Result{Kind: NotFound}
}

// TreeNode is a node in the synthetic file tree built from manifest keys. A
// directory exists only because it is a path prefix of one or more keys (no
// folders are stored). Both files and directories are addressable at
// /{slug}/{Path}: for a file Path is its exact manifest key, for a directory the
// shared prefix. The synthetic root has empty Name and Path.
type TreeNode struct {
	Name     string
	Path     string
	IsDir    bool
	Open     bool // directory is an ancestor of the current node (auto-expand)
	Current  bool // this node is the addressed node (highlight)
	Children []*TreeNode
}

// BuildTree assembles the nested folder/file tree from the manifest keys. The
// current node's ancestor directories are flagged Open and the current node
// itself Current, so the sidebar auto-expands to and highlights it. Children are
// ordered folders-first, then files, alphabetically within each group.
func BuildTree(keys []string, currentKey string) *TreeNode {
	root := &TreeNode{IsDir: true}
	for _, k := range keys {
		insert(root, strings.Trim(k, "/"))
	}
	sortChildren(root)
	markCurrent(root, strings.Trim(currentKey, "/"))
	return root
}

// insert adds key's path to the tree, creating intermediate directory nodes.
func insert(root *TreeNode, key string) {
	if key == "" {
		return
	}
	segs := strings.Split(key, "/")
	cur := root
	for i, seg := range segs {
		isDir := i < len(segs)-1
		child := findChild(cur, seg, isDir)
		if child == nil {
			child = &TreeNode{Name: seg, Path: strings.Join(segs[:i+1], "/"), IsDir: isDir}
			cur.Children = append(cur.Children, child)
		}
		cur = child
	}
}

// findChild returns cur's child matching name and node kind. A file and a
// same-stem directory coexist as distinct siblings, so kind is part of the match.
func findChild(cur *TreeNode, name string, isDir bool) *TreeNode {
	for _, c := range cur.Children {
		if c.Name == name && c.IsDir == isDir {
			return c
		}
	}
	return nil
}

// sortChildren orders every node's children folders-first then alphabetical.
func sortChildren(n *TreeNode) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return a.Name < b.Name
	})
	for _, c := range n.Children {
		sortChildren(c)
	}
}

// markCurrent walks currentKey's segments, opening each ancestor directory and
// marking the addressed leaf Current. A miss (key not in the tree) is a no-op.
func markCurrent(root *TreeNode, key string) {
	if key == "" {
		return
	}
	segs := strings.Split(key, "/")
	cur := root
	for i, seg := range segs {
		isDir := i < len(segs)-1
		child := findChild(cur, seg, isDir)
		if child == nil {
			return
		}
		if isDir {
			child.Open = true
		} else {
			child.Current = true
		}
		cur = child
	}
}

// Crumb is one segment of a Breadcrumb. Path addresses the segment at
// /{slug}/{Path} (empty Path is the home crumb → bundle root). IsCurrent marks
// the final segment (the addressed node), which renders unlinked.
type Crumb struct {
	Name      string
	Path      string
	IsCurrent bool
}

// Breadcrumb returns the clickable path from the bundle root to key. The first
// crumb is always the home crumb (empty Name and Path); each subsequent crumb
// carries the cumulative prefix. For the root (empty key) only the home crumb is
// returned, marked current.
func Breadcrumb(key string) []Crumb {
	key = strings.Trim(key, "/")
	crumbs := []Crumb{{IsCurrent: key == ""}}
	if key == "" {
		return crumbs
	}
	segs := strings.Split(key, "/")
	for i, seg := range segs {
		crumbs = append(crumbs, Crumb{
			Name:      seg,
			Path:      strings.Join(segs[:i+1], "/"),
			IsCurrent: i == len(segs)-1,
		})
	}
	return crumbs
}
