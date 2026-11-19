// Package render turns a canonical graph.DepGraph into the four output
// formats depvis emits: JSON, Mermaid, Graphviz DOT, and a self-contained
// HTML viewer. Every renderer is a pure function of the graph: it never
// mutates its input, and the same graph always renders to byte-identical
// output (renderers work on a sorted deep copy, never on the original).
package render

import (
	"fmt"
	"slices"

	"go.temporal.io/server/tools/depvis/graph"
)

// Output formats accepted by Render.
const (
	FormatJSON    = "json"
	FormatMermaid = "mermaid"
	FormatDOT     = "dot"
	FormatHTML    = "html"
)

// IsValidFormat reports whether format is one of the formats Render
// accepts. Callers validate the format before an expensive analysis run so
// a typo fails fast with a readable error.
func IsValidFormat(format string) bool {
	switch format {
	case FormatJSON, FormatMermaid, FormatDOT, FormatHTML:
		return true
	default:
		return false
	}
}

// Render renders g in the named format. JSON, Mermaid and DOT return the
// rendered text directly; HTML returns a complete standalone page.
func Render(g *graph.DepGraph, format string) (string, error) {
	switch format {
	case FormatJSON:
		return JSON(g), nil
	case FormatMermaid:
		return Mermaid(g), nil
	case FormatDOT:
		return DOT(g), nil
	case FormatHTML:
		return HTML(g)
	default:
		return "", fmt.Errorf("unknown format %q (want one of: json, mermaid, dot, html)", format)
	}
}

// sortedCopy deep-copies g and sorts the copy so rendering order is
// deterministic regardless of how the analyzer accumulated the graph. The
// input graph is never touched.
func sortedCopy(g *graph.DepGraph) *graph.DepGraph {
	c := *g
	c.Nodes = slices.Clone(g.Nodes)
	c.Edges = slices.Clone(g.Edges)
	c.Unresolved = slices.Clone(g.Unresolved)
	c.Sort()
	return &c
}
