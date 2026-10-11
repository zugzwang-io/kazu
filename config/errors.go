package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Error is every problem found in one file. Load and Parse return it when
// the file can't be used.
type Error struct {
	File     string
	Problems []Problem
}

// Problem is one thing wrong with the file.
type Problem struct {
	// Line and Column are 1-based; zero when unknown.
	Line, Column int
	// Path is where in the file, as written in messages:
	// scenarios.db-blip.faults[0].after_requests. Empty for the whole file.
	Path    string
	Message string
}

func (p Problem) String() string {
	var b strings.Builder
	if p.Path != "" {
		b.WriteString(p.Path)
		b.WriteString(": ")
	}
	b.WriteString(p.Message)
	return b.String()
}

// Error prints one problem per line as file:line:col: path: message, in
// file order.
func (e *Error) Error() string {
	var b strings.Builder
	for i, p := range e.Problems {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.File)
		if p.Line > 0 {
			fmt.Fprintf(&b, ":%d:%d", p.Line, p.Column)
		}
		b.WriteString(": ")
		b.WriteString(p.String())
	}
	return b.String()
}

// problems collects problems against one flattened document.
type problems struct {
	root *yaml.Node
	list []Problem
	seen map[string]bool
}

// add records a problem at path, a list of map keys and list indexes from
// the document root. The position is that of the deepest node path reaches;
// key marks a map key rather than its value (an edge name, a scenario name).
func (ps *problems) add(path []string, msg string) { ps.addAt(path, false, msg) }

func (ps *problems) addKey(path []string, msg string) { ps.addAt(path, true, msg) }

func (ps *problems) addAt(path []string, key bool, msg string) {
	p := Problem{Path: formatPath(ps.root, path), Message: msg}
	if n := lookup(ps.root, path, key); n != nil {
		p.Line, p.Column = n.Line, n.Column
	}
	id := p.String()
	if ps.seen[id] {
		return
	}
	if ps.seen == nil {
		ps.seen = map[string]bool{}
	}
	ps.seen[id] = true
	ps.list = append(ps.list, p)
}

func (ps *problems) err(file string) error {
	if len(ps.list) == 0 {
		return nil
	}
	sort.SliceStable(ps.list, func(i, j int) bool {
		a, b := ps.list[i], ps.list[j]
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	return &Error{File: file, Problems: ps.list}
}

// lookup walks path from root and returns the deepest node it reaches, or
// for key, the key node of the last step.
func lookup(root *yaml.Node, path []string, key bool) *yaml.Node {
	n := root
	for i, tok := range path {
		if key && i == len(path)-1 && n.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(n.Content); j += 2 {
				if n.Content[j].Value == tok {
					return n.Content[j]
				}
			}
		}
		next := child(n, tok)
		if next == nil {
			return n
		}
		n = next
	}
	return n
}

// formatPath writes path the way the file reads: dotted map keys, [i] list
// indexes, and ["..."] for keys that aren't plain words, such as edge names.
func formatPath(root *yaml.Node, path []string) string {
	var b strings.Builder
	n := root
	for _, tok := range path {
		if n != nil && n.Kind == yaml.SequenceNode {
			fmt.Fprintf(&b, "[%s]", tok)
		} else if plainKey(tok) {
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(tok)
		} else {
			fmt.Fprintf(&b, "[%q]", tok)
		}
		if n != nil {
			n = child(n, tok)
		}
	}
	return b.String()
}

func child(n *yaml.Node, tok string) *yaml.Node {
	switch n.Kind {
	case yaml.MappingNode:
		for j := 0; j+1 < len(n.Content); j += 2 {
			if n.Content[j].Value == tok {
				return n.Content[j+1]
			}
		}
	case yaml.SequenceNode:
		if idx, err := strconv.Atoi(tok); err == nil && idx >= 0 && idx < len(n.Content) {
			return n.Content[idx]
		}
	default:
	}
	return nil
}

func plainKey(s string) bool {
	if s == "" {
		return false
	}
	return strings.IndexFunc(s, func(r rune) bool {
		word := r == '_' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		return !word
	}) < 0
}

// suggest returns ` (did you mean "x"?)` when one candidate is close to s.
func suggest(s string, candidates []string) string {
	best, bestD := "", 1<<30
	for _, c := range candidates {
		if d := distance(s, c); d < bestD {
			best, bestD = c, d
		}
	}
	limit := 2
	if len(s) <= 4 {
		limit = 1
	}
	if best == "" || bestD > limit {
		return ""
	}
	return fmt.Sprintf(" (did you mean %q?)", best)
}

// distance is the Levenshtein distance between a and b.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func quoteList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = strconv.Quote(s)
	}
	return strings.Join(q, ", ")
}
