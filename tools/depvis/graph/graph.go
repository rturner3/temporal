// Package graph defines the canonical dependency-graph model that depvis
// emits. The model is plain data — JSON-serializable, no graph library — and
// is the single source of truth shared by the analyzer and every renderer
// (JSON, Mermaid, DOT, HTML).
package graph

import (
	"slices"
	"strings"
)

// SchemaVersion is the version of the emitted graph schema. Bump on breaking
// model changes so downstream consumers can detect the shape they read.
const SchemaVersion = 1

// Node types.
const (
	TypeWorkflow        = "workflow"
	TypeChildWorkflow   = "child_workflow"
	TypeActivity        = "activity"
	TypeNexusOperation  = "nexus_operation"
	TypeExternalService = "external_service"
	TypeDatabase        = "database"
	TypeQueue           = "queue"
)

// Edge kinds.
const (
	EdgeExecutesActivity = "executes_activity"
	EdgeExecutesChild    = "executes_child"
	EdgeNexusOperation   = "nexus_operation"
	EdgeTouchesResource  = "touches_resource"
)

// DepGraph is the canonical dependency graph of the analyzed target.
type DepGraph struct {
	SchemaVersion int          `json:"schemaVersion"`
	Target        Target       `json:"target"`
	Nodes         []Node       `json:"nodes"`
	Edges         []Edge       `json:"edges"`
	Unresolved    []Unresolved `json:"unresolved"`
}

// Target identifies the module and directory the graph was extracted from.
type Target struct {
	ModulePath string `json:"modulePath"`
	Dir        string `json:"dir"`
}

// Node is one vertex of the graph: a workflow, child workflow, activity,
// Nexus operation, or an external resource. Inferred and Via are set only on
// heuristic-derived resource nodes; Unresolved marks nodes depvis could not
// fully prove (rendered dashed downstream).
type Node struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	Location   string `json:"location,omitempty"`
	Inferred   bool   `json:"inferred,omitempty"`
	Via        string `json:"via,omitempty"`
	Unresolved bool   `json:"unresolved,omitempty"`
}

// Edge is one directed dependency between two nodes. Inferred is set on
// heuristic-derived resource edges; Location is the call site for code edges.
type Edge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"`
	Location string `json:"location,omitempty"`
	Inferred bool   `json:"inferred,omitempty"`
}

// Unresolved is one call site (or registration) whose target depvis could not
// resolve to a node. Unresolved entries are never dropped: the graph shows
// what it could not prove instead of silently hiding it.
type Unresolved struct {
	From     string `json:"from,omitempty"`
	Call     string `json:"call"`
	Reason   string `json:"reason"`
	Location string `json:"location"`
}

// NodeID renders the stable node identifier for a typed node: "<type>:<name>".
func NodeID(nodeType, name string) string {
	return nodeType + ":" + name
}

// NewDepGraph returns an empty graph for the given target, with non-nil
// slices so JSON output is never null.
func NewDepGraph(modulePath, dir string) *DepGraph {
	return &DepGraph{
		SchemaVersion: SchemaVersion,
		Target:        Target{ModulePath: modulePath, Dir: dir},
		Nodes:         []Node{},
		Edges:         []Edge{},
		Unresolved:    []Unresolved{},
	}
}

// AddNode appends a node if no node with the same ID exists. When an
// annotation-proven node (not inferred) and a heuristic node (inferred) would
// collide, the proven one wins and the heuristic flag is dropped.
func (g *DepGraph) AddNode(n Node) {
	for i := range g.Nodes {
		if g.Nodes[i].ID != n.ID {
			continue
		}
		if !g.Nodes[i].Inferred {
			return
		}
		n.Inferred = false
		g.Nodes[i] = n
		return
	}
	g.Nodes = append(g.Nodes, n)
}

// AddEdge appends an edge, keeping every call site as its own edge.
func (g *DepGraph) AddEdge(e Edge) {
	g.Edges = append(g.Edges, e)
}

// AddUnresolved appends an unresolved entry.
func (g *DepGraph) AddUnresolved(u Unresolved) {
	g.Unresolved = append(g.Unresolved, u)
}

// Sort orders nodes, edges, and unresolved entries deterministically so
// repeated runs over the same source emit byte-identical graphs.
func (g *DepGraph) Sort() {
	sortNodes(g.Nodes)
	sortEdges(g.Edges)
	slices.SortStableFunc(g.Unresolved, func(a, b Unresolved) int {
		return lessUnresolved(a, b)
	})
}

func sortNodes(nodes []Node) {
	slices.SortStableFunc(nodes, func(a, b Node) int { return strings.Compare(a.ID, b.ID) })
}

func sortEdges(edges []Edge) {
	slices.SortStableFunc(edges, func(a, b Edge) int {
		if c := strings.Compare(a.From, b.From); c != 0 {
			return c
		}
		if c := strings.Compare(a.To, b.To); c != 0 {
			return c
		}
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return strings.Compare(a.Location, b.Location)
	})
}

// lessUnresolved orders unresolved entries deterministically.
func lessUnresolved(a, b Unresolved) int {
	if c := strings.Compare(a.Location, b.Location); c != 0 {
		return c
	}
	if c := strings.Compare(a.From, b.From); c != 0 {
		return c
	}
	return strings.Compare(a.Call, b.Call)
}
