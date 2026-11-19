package analyzer

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/server/tools/depvis/graph"
)

// fixturesDir is the fixture tree's path relative to this test's package
// directory (go test runs with cwd set to the package dir).
const fixturesDir = "../testdata/fixtures"

// analyzeFixtures runs the analyzer over the fixture tree.
func analyzeFixtures(t *testing.T, opts Options) *graph.DepGraph {
	t.Helper()
	abs, err := filepath.Abs(fixturesDir)
	require.NoError(t, err)
	g, err := Analyze(abs, opts)
	require.NoError(t, err)
	return g
}

// nodeIDs returns the id set of the graph's nodes.
func nodeIDs(g *graph.DepGraph) map[string]bool {
	ids := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		ids[n.ID] = true
	}
	return ids
}

// edgeSet returns the set of "from -> to [kind]" strings of the graph's
// edges.
func edgeSet(g *graph.DepGraph) map[string]bool {
	set := make(map[string]bool, len(g.Edges))
	for _, e := range g.Edges {
		set[e.From+" -> "+e.To+" ["+e.Kind+"]"] = true
	}
	return set
}

// TestAnalyzeEdges verifies one edge per SDK call site with file:line
// locations, covering typed function references, string-const folding, child
// workflows, and Nexus client calls.
func TestAnalyzeEdges(t *testing.T) {
	g := analyzeFixtures(t, Options{})

	edges := edgeSet(g)
	for _, want := range []string{
		"workflow:OrderWorkflow -> activity:ChargeCardActivity [executes_activity]",
		"workflow:OrderWorkflow -> child_workflow:ShipOrderWorkflow [executes_child]",
		"child_workflow:ShipOrderWorkflow -> activity:legacy-audit-activity [executes_activity]",
		"workflow:BillingWorkflow -> nexus_operation:billing-endpoint/billing [nexus_operation]",
		"workflow:InfraWorkflow -> activity:LoadOrdersActivity [executes_activity]",
		"workflow:wrapped-processing-workflow -> activity:TransformActivity [executes_activity]",
	} {
		require.Contains(t, edges, want, "missing edge %q", want)
	}

	// Every edge carries a file:line location.
	for _, e := range g.Edges {
		require.NotEmpty(t, e.Location, "edge %s -> %s has no location", e.From, e.To)
		require.Regexp(t, `:[0-9]+$`, e.Location, "edge location is not file:line")
	}
}

// TestAnalyzeRegistrations verifies registration detection: typed function
// references, folded constant names through RegisterOptions.Name, and the
// repo-local registry wrappers the server's own workers use.
func TestAnalyzeRegistrations(t *testing.T) {
	g := analyzeFixtures(t, Options{})

	ids := nodeIDs(g)
	for _, want := range []string{
		"workflow:OrderWorkflow",               // fn-name default
		"workflow:wrapped-processing-workflow", // folded const via RegisterOptions.Name
		"activity:ChargeCardActivity",          // registered activity
		"activity:TransformActivity",           // wrapper-registered activity
		"activity:legacy-audit-activity",       // unregistered, string-const call site
	} {
		require.Contains(t, ids, want)
	}

	byID := make(map[string]graph.Node, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	n := byID["workflow:OrderWorkflow"]
	require.Equal(t, "workflow", n.Type)
	require.Regexp(t, `orders/workflow\.go:[0-9]+$`, n.Location)
}

// TestAnalyzeUnresolved verifies non-foldable call-site names surface as
// unresolved entries with an owner, reason, and file:line location — never
// silently dropped.
func TestAnalyzeUnresolved(t *testing.T) {
	g := analyzeFixtures(t, Options{})

	var dynamic []graph.Unresolved
	for _, u := range g.Unresolved {
		if u.From == "workflow:DynamicNameWorkflow" {
			dynamic = append(dynamic, u)
		}
	}
	require.Len(t, dynamic, 2, "want two unresolved dynamic-name call sites")
	for _, u := range dynamic {
		require.Equal(t, "workflow.ExecuteActivity", u.Call)
		require.Contains(t, u.Reason, "non-constant name expression")
		require.Regexp(t, `orders/workflow\.go:(43|44)$`, u.Location)
	}
}

// TestAnalyzeNoDependencyWorkflow verifies a workflow whose body touches
// nothing produces a node and no outgoing edges.
func TestAnalyzeNoDependencyWorkflow(t *testing.T) {
	g := analyzeFixtures(t, Options{})

	require.Contains(t, nodeIDs(g), "workflow:NoDependenciesWorkflow")
	for _, e := range g.Edges {
		require.NotEqual(t, "workflow:NoDependenciesWorkflow", e.From,
			"no-dependency workflow must have no outgoing edges")
	}
}

// TestResourceInference verifies heuristic-derived resource nodes carry
// inferred: true and the enabling import, with unknown imports producing
// nothing.
func TestResourceInference(t *testing.T) {
	g := analyzeFixtures(t, Options{})

	byID := make(map[string]graph.Node, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}

	db, ok := byID["database:postgres"]
	require.True(t, ok, "want a database:postgres node from the database/sql open")
	require.Equal(t, "database", db.Type)
	require.True(t, db.Inferred)
	require.Equal(t, "database/sql", db.Via)

	svc, ok := byID["external_service:api.example.com"]
	require.True(t, ok, "want an external_service node from the constant-host HTTP call")
	require.True(t, svc.Inferred)
	require.Equal(t, "net/http", svc.Via)

	// No queue imports exist in the fixture tree: no queue nodes, and the
	// relative-URL HTTP call produces nothing.
	for id := range byID {
		require.NotContains(t, id, "queue:")
		require.NotEqual(t, "external_service:localhost", id)
	}
}

// TestAnnotationsPrecedence verifies annotations always win: an annotated
// owner emits the annotated resources instead of its heuristic hints, and an
// owner with no heuristics still gets its annotated resources.
func TestAnnotationsPrecedence(t *testing.T) {
	annotationsPath, err := filepath.Abs("../testdata/annotations/precedence.json")
	require.NoError(t, err)

	g := analyzeFixtures(t, Options{Annotations: annotationsPath})

	byID := make(map[string]graph.Node, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}

	// The annotation replaces PublishActivity's heuristic external service.
	ann, ok := byID["external_service:payments-gateway"]
	require.True(t, ok, "annotated resource missing")
	require.Equal(t, "stripe", ann.Via)
	require.False(t, ann.Inferred, "annotation-derived nodes are never marked inferred")
	require.NotContains(t, byID, "external_service:api.example.com",
		"heuristic node must lose to the annotation")
	edges := edgeSet(g)
	require.Contains(t, edges, "activity:PublishActivity -> external_service:payments-gateway [touches_resource]")

	// The annotated no-dependency workflow gains a resource edge it has no
	// heuristic for.
	queueAnn, ok := byID["queue:audit-events"]
	require.True(t, ok, "annotation on an owner without heuristics is missing")
	require.Equal(t, "queue", queueAnn.Type)
	require.Contains(t, edges, "workflow:NoDependenciesWorkflow -> queue:audit-events [touches_resource]")
}
