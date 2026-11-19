package render

import (
	"fmt"
	"strings"

	"go.temporal.io/server/tools/depvis/graph"
)

// DOT renders the graph as Graphviz DOT with the same type styling as the
// Mermaid emitter: workflow nodes are rounded boxes, activities are stadium
// (oval), databases are cylinders, queues are cds shapes, Nexus operations
// are hexagons, external services are parallelograms. Unresolved-flagged
// nodes render dashed; inferred nodes are lightly filled; inferred edges are
// dashed; every edge carries its kind as a label. Unresolved call sites are
// emitted as comments — DOT cannot express them as graph elements.
func DOT(g *graph.DepGraph) string {
	sorted := sortedCopy(g)

	var b strings.Builder
	b.WriteString("digraph depvis {\n")
	b.WriteString("    rankdir=LR;\n")
	for _, n := range sorted.Nodes {
		fmt.Fprintf(&b, "    %s;\n", dotNode(n))
	}
	for _, e := range sorted.Edges {
		fmt.Fprintf(&b, "    %s;\n", dotEdge(e))
	}
	for _, u := range sorted.Unresolved {
		fmt.Fprintf(&b, "    // unresolved: %s\n", dotEscape(unresolvedSummary(u)))
	}
	b.WriteString("}\n")
	return b.String()
}

// dotNode renders one node statement with the shape for its type.
func dotNode(n graph.Node) string {
	attrs := []string{
		fmt.Sprintf("label=%s", dotQuote(n.Name)),
		fmt.Sprintf("shape=%s", dotQuote(dotShape(n.Type))),
	}

	// One style attribute: rounded for workflows, dashed on unresolved
	// nodes, filled (light blue) on inferred nodes.
	var style []string
	if n.Type == graph.TypeWorkflow || n.Type == graph.TypeChildWorkflow {
		style = append(style, "rounded")
	}
	if n.Unresolved {
		style = append(style, "dashed")
	}
	if n.Inferred {
		style = append(style, "filled")
		attrs = append(attrs, `fillcolor="#eef3fb"`)
		if n.Via != "" {
			attrs = append(attrs, fmt.Sprintf("tooltip=%s", dotQuote(n.Via)))
		}
	}
	if len(style) > 0 {
		attrs = append(attrs, fmt.Sprintf("style=%s", dotQuote(strings.Join(style, ","))))
	}
	return fmt.Sprintf("%q [%s]", n.ID, strings.Join(attrs, ", "))
}

// dotShape maps a node type to a Graphviz shape. Workflows — registered or
// child — and unknown types render as plain boxes.
func dotShape(nodeType string) string {
	switch nodeType {
	case graph.TypeActivity:
		return "oval"
	case graph.TypeDatabase:
		return "cylinder"
	case graph.TypeQueue:
		return "cds"
	case graph.TypeNexusOperation:
		return "hexagon"
	case graph.TypeExternalService:
		return "parallelogram"
	default:
		return "box"
	}
}

// dotEdge renders one edge statement with its kind label.
func dotEdge(e graph.Edge) string {
	attrs := []string{fmt.Sprintf("label=%s", dotQuote(e.Kind))}
	if e.Inferred {
		attrs = append(attrs, `style="dashed"`)
	}
	return fmt.Sprintf("%q -> %q [%s]", e.From, e.To, strings.Join(attrs, ", "))
}

// dotEscape makes a string safe inside a DOT quoted attribute value.
func dotEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

func dotQuote(s string) string {
	return `"` + dotEscape(s) + `"`
}
