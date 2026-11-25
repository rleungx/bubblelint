package bubblelint

import (
	"go/ast"
	"go/token"
	"slices"

	"golang.org/x/tools/go/cfg"
)

// A possible effect is useful evidence, but it must not be presented as an
// unconditional obstacle. Required blocks dominate every reachable exit.
func (c *checker) migrationEffectNodes(body *ast.BlockStmt, visit func(*cfg.Block, ast.Node)) []*cfg.Block {
	type item struct {
		block *cfg.Block
		node  ast.Node
	}
	var nodes []item
	blocks := c.migrationNodes(body, func(block *cfg.Block, node ast.Node) {
		nodes = append(nodes, item{block, node})
	})
	required := c.migrationRequiredBlocks(blocks)
	for _, item := range nodes {
		c.effectConditional = !required[item.block]
		visit(item.block, item.node)
	}
	c.effectConditional = false
	return blocks
}

func (c *checker) migrationRequiredBlocks(blocks []*cfg.Block) map[*cfg.Block]bool {
	return requiredMigrationGraphBlocks(blocks, c.flowSuccessors)
}

func requiredMigrationGraphBlocks[T comparable](blocks []T, successorsOf func(T) []T) map[T]bool {
	if len(blocks) == 0 {
		return nil
	}
	index := make(map[T]int, len(blocks))
	for i, block := range blocks {
		index[block] = i
	}
	predecessors := make([][]int, len(blocks))
	var exits []int
	for i, block := range blocks {
		successors := successorsOf(block)
		if len(successors) == 0 {
			exits = append(exits, i)
		}
		for _, next := range successors {
			if j, ok := index[next]; ok {
				predecessors[j] = append(predecessors[j], i)
			}
		}
	}
	words := (len(blocks) + 63) / 64
	dominators := make([][]uint64, len(blocks))
	for i := range blocks {
		dominators[i] = make([]uint64, words)
		if i == 0 {
			dominators[i][0] = 1
		} else {
			for j := range dominators[i] {
				dominators[i][j] = ^uint64(0)
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for i := 1; i < len(blocks); i++ {
			next := make([]uint64, words)
			if len(predecessors[i]) != 0 {
				copy(next, dominators[predecessors[i][0]])
				for _, predecessor := range predecessors[i][1:] {
					for j := range next {
						next[j] &= dominators[predecessor][j]
					}
				}
			}
			next[i/64] |= uint64(1) << (i % 64)
			if !slices.Equal(next, dominators[i]) {
				dominators[i], changed = next, true
			}
		}
	}
	common := slices.Clone(dominators[0])
	if len(exits) != 0 {
		copy(common, dominators[exits[0]])
		for _, exit := range exits[1:] {
			for j := range common {
				common[j] &= dominators[exit][j]
			}
		}
	}
	required := make(map[T]bool)
	for i, block := range blocks {
		required[block] = common[i/64]&(uint64(1)<<(i%64)) != 0
	}
	return required
}

func (c *checker) migrationConditionalOperands(node ast.Node) map[*ast.CallExpr]bool {
	conditional := make(map[*ast.CallExpr]bool)
	ast.Inspect(node, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		if binary, ok := node.(*ast.BinaryExpr); ok && (binary.Op == token.LAND || binary.Op == token.LOR) && c.pass.TypesInfo.Types[binary.X].Value == nil {
			ast.Inspect(binary.Y, func(node ast.Node) bool {
				if _, ok := node.(*ast.FuncLit); ok {
					return false
				}
				if call, ok := node.(*ast.CallExpr); ok {
					conditional[call] = true
				}
				return true
			})
		}
		return true
	})
	return conditional
}
