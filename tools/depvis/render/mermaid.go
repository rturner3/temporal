package render

import (
	"fmt"
	"strconv"
	"strings"

	"go.temporal.io/server/tools/depvis/graph"
)

// Mermaid renders the graph as a Mermaid flowchart. Node shapes encode node
// type (rounded = workflow, stadium = activity, cylinder = database,
// parallelogram = queue, hexagon = nexus operation, circle = external
// service); nodes flagged unresolved get a dashed class, inferred edges are
// dashed, and each edge carries its kind as a label. Unresolved call sites
// are emitted as comments — Mermaid cannot express them as graph elements.
// Mermaid has no native queue shape; the parallelogram is the documented
// stand-in.
func Mermaid(g *graph.DepGraph) string {
	sorted := sortedCopy(g)
	ids := newIDAllocator()

	var b strings.Builder
	b.WriteString("flowchart TD\n")
	for _, n := range sorted.Nodes {
		fmt.Fprintf(&b, "    %s\n", mermaidNode(ids.get(n.ID), n))
	}
	for _, e := range sorted.Edges {
		fmt.Fprintf(&b, "    %s -->|\"%s\"| %s\n",
			ids.get(e.From), mermaidEscape(e.Kind), ids.get(e.To))
	}

	// Unresolved-flagged nodes render dashed via a class definition.
	var dashed []string
	for _, n := range sorted.Nodes {
		if n.Unresolved {
			dashed = append(dashed, ids.get(n.ID))
		}
	}
	if len(dashed) > 0 {
		b.WriteString("    classDef unresolved stroke-dasharray: 5 5\n")
		fmt.Fprintf(&b, "    class %s unresolved\n", strings.Join(dashed, ","))
	}

	// linkStyle indices follow edge emission order, which sorting fixes.
	for i, e := range sorted.Edges {
		if e.Inferred {
			fmt.Fprintf(&b, "    linkStyle %d stroke-dasharray: 5 5\n", i)
		}
	}

	// No unresolved call sites here: Mermaid's handling of %% comments
	// inside flowcharts is unreliable (a comment after class/linkStyle
	// statements can fail to parse in Mermaid 11), so call sites are not
	// rendered into Mermaid at all. They remain in the canonical JSON, as
	// DOT comments, and in the HTML viewer's unresolved panel.
	return b.String()
}

// mermaidNode renders one node statement with the shape for its type.
func mermaidNode(id string, n graph.Node) string {
	label := mermaidEscape(n.Name)
	switch n.Type {
	case graph.TypeWorkflow, graph.TypeChildWorkflow:
		return id + `("` + label + `")` // rounded
	case graph.TypeActivity:
		return id + `(["` + label + `"])` // stadium
	case graph.TypeDatabase:
		return id + `[("` + label + `")]` // cylinder
	case graph.TypeQueue:
		return id + `[/"` + label + `"/]` // parallelogram
	case graph.TypeNexusOperation:
		return id + `{"` + label + `"}` // hexagon
	case graph.TypeExternalService:
		return id + `(("` + label + `"))` // circle
	default:
		return id + `["` + label + `"]`
	}
}

// mermaidEscape makes a string safe inside a Mermaid quoted label: embedded
// quotes become Mermaid's #quot; escape and line breaks are collapsed.
func mermaidEscape(s string) string {
	s = strings.ReplaceAll(s, `"`, "#quot;")
	return strings.ReplaceAll(s, "\n", " ")
}

// idAllocator assigns deterministic, Mermaid-safe node ids. Ids are the
// sanitized node id ("workflow:OrderParent" -> "workflow_OrderParent");
// collisions after sanitization get a numeric suffix in first-seen order,
// which is stable because nodes are processed in sorted order.
type idAllocator struct {
	assigned map[string]string
	used     map[string]bool
}

func newIDAllocator() *idAllocator {
	return &idAllocator{assigned: map[string]string{}, used: map[string]bool{}}
}

func (a *idAllocator) get(id string) string {
	if got, ok := a.assigned[id]; ok {
		return got
	}
	base := sanitizeID(id)
	name, i := base, 2
	for a.used[name] {
		name = base + "_" + strconv.Itoa(i)
		i++
	}
	a.used[name] = true
	a.assigned[id] = name
	return name
}

// sanitizeID maps an id to [A-Za-z0-9_], the characters Mermaid accepts in
// identifiers. The leading type segment is always alphabetic, so the result
// never starts with a digit.
func sanitizeID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// unresolvedSummary renders an unresolved entry as a one-line comment body.
func unresolvedSummary(u graph.Unresolved) string {
	from := u.From
	if from == "" {
		from = "-"
	}
	return fmt.Sprintf("from %s call %s at %s (%s)",
		mermaidEscape(from), mermaidEscape(u.Call), u.Location, mermaidEscape(u.Reason))
}
