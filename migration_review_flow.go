package bubblelint

import (
	"go/ast"
	"go/token"
	"sort"
	"strconv"

	"golang.org/x/tools/go/cfg"
)

// Record reachable source statements, including selects whose channel
// expressions go/cfg evaluates in the preceding block.
func (c *checker) reviewReachable(body *ast.BlockStmt) map[ast.Node]bool {
	reachable := make(map[ast.Node]bool)
	c.migrationNodes(body, func(_ *cfg.Block, node ast.Node) {
		reachable[node] = true
		i := sort.Search(len(body.List), func(i int) bool { return body.List[i].End() >= node.End() })
		if i < len(body.List) && body.List[i].Pos() <= node.Pos() {
			reachable[body.List[i]] = true
		}
	})
	return reachable
}

type reviewDependencies map[string]map[string]bool

func (d reviewDependencies) edge(from, to string) {
	if from == "" || to == "" || from == to {
		return
	}
	if d[from] == nil {
		d[from] = make(map[string]bool)
	}
	d[from][to] = true
}

// Immutable aliases share an identity. Field paths retain their base so two
// instances of the same struct do not share channel dependencies.
func (c *checker) reviewPath(expr ast.Expr) string {
	expr = c.resolve(expr)
	switch expr := expr.(type) {
	case *ast.Ident:
		if obj := c.pass.TypesInfo.ObjectOf(expr); obj != nil {
			return "v" + strconv.Itoa(int(obj.Pos()))
		}
	case *ast.SelectorExpr:
		if base := c.reviewPath(expr.X); base != "" {
			return base + "." + expr.Sel.Name
		}
	case *ast.UnaryExpr:
		if expr.Op == token.AND {
			return c.reviewPath(expr.X)
		}
	case *ast.StarExpr:
		return c.reviewPath(expr.X)
	case *ast.CompositeLit, *ast.CallExpr:
		return "a" + strconv.Itoa(int(expr.Pos()))
	}
	return ""
}

// This graph establishes accessibility, not channel ownership or a guaranteed
// signal. In particular, assigning a gate to a worker's field makes its local
// channels accessible without assuming that an interface call reaches them.
func (c *checker) reviewDependencies(body *ast.BlockStmt, before token.Pos) reviewDependencies {
	dependencies := make(reviewDependencies)
	c.migrationNodes(body, func(_ *cfg.Block, node ast.Node) {
		if node.Pos() >= before {
			return
		}
		ast.Inspect(node, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.FuncLit:
				return false
			case *ast.SelectorExpr:
				dependencies.edge(c.reviewPath(n.X), c.reviewPath(n))
			case *ast.AssignStmt:
				if len(n.Lhs) == len(n.Rhs) {
					for i := range n.Lhs {
						dependencies.edge(c.reviewPath(n.Lhs[i]), c.reviewPath(n.Rhs[i]))
					}
				}
			case *ast.CompositeLit:
				base := c.reviewPath(n)
				for _, elt := range n.Elts {
					if field, ok := elt.(*ast.KeyValueExpr); ok {
						if key, ok := field.Key.(*ast.Ident); ok {
							dependencies.edge(base, base+"."+key.Name)
							dependencies.edge(base+"."+key.Name, c.reviewPath(field.Value))
						}
					}
				}
			}
			return true
		})
	})
	return dependencies
}

func (d reviewDependencies) workerReaches(c *checker, body *ast.BlockStmt, channel ast.Expr) bool {
	target := c.reviewChannel(channel)
	if target == nil {
		return false
	}
	var pending []string
	seenBodies := make(map[*ast.BlockStmt]bool)
	bodies := []*ast.BlockStmt{body}
	for i := 0; i < len(bodies); i++ {
		body := bodies[i]
		if seenBodies[body] {
			continue
		}
		seenBodies[body] = true
		c.migrationNodes(body, func(_ *cfg.Block, node ast.Node) {
			ast.Inspect(node, func(node ast.Node) bool {
				if _, ok := node.(*ast.FuncLit); ok {
					return false
				}
				if expr, ok := node.(ast.Expr); ok {
					if key := c.reviewPath(expr); key != "" {
						pending = append(pending, key)
					}
				}
				return true
			})
			c.migrationCalls(node, func(call *ast.CallExpr, _ bool) {
				if helper, ok := c.callable(call.Fun); ok && !seenBodies[helper.body] {
					bodies = append(bodies, helper.body)
				}
			})
		})
	}
	seen := make(map[string]bool)
	want := c.reviewPath(target)
	for i := 0; i < len(pending); i++ {
		key := pending[i]
		if key == want {
			return true
		}
		if !seen[key] {
			seen[key] = true
			for next := range d[key] {
				pending = append(pending, next)
			}
		}
	}
	return false
}
