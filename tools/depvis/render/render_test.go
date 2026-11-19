//go:build test_dep

package render

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/server/tools/depvis/graph"
)

var update = flag.Bool("update", false, "rewrite golden files")

// fixtureGraph builds a fixed graph covering every node type, both resource
// flags (inferred node/edge, unresolved node) and an unresolved call site.
// Nodes and edges are added out of sorted order on purpose: emitters must be
// independent of insertion order.
func fixtureGraph() *graph.DepGraph {
	g := graph.NewDepGraph("example.com/app", "order")

	parentID := graph.NodeID(graph.TypeWorkflow, "OrderParent")
	refundID := graph.NodeID(graph.TypeActivity, "RefundOrder")
	g.AddNode(graph.Node{ID: graph.NodeID(graph.TypeActivity, "ChargeCard"), Type: graph.TypeActivity, Name: "ChargeCard", Location: "order/activities.go:41"})
	g.AddNode(graph.Node{ID: parentID, Type: graph.TypeWorkflow, Name: "OrderParent", Location: "order/workflow.go:22"})
	g.AddNode(graph.Node{ID: graph.NodeID(graph.TypeDatabase, "orders-db"), Type: graph.TypeDatabase, Name: "orders-db", Inferred: true, Via: "database/sql"})
	g.AddNode(graph.Node{ID: graph.NodeID(graph.TypeExternalService, "stripe-api"), Type: graph.TypeExternalService, Name: "stripe-api", Inferred: true, Via: "net/http"})
	g.AddNode(graph.Node{ID: graph.NodeID(graph.TypeChildWorkflow, "Fulfillment"), Type: graph.TypeChildWorkflow, Name: "Fulfillment", Location: "fulfillment/workflow.go:10"})
	g.AddNode(graph.Node{ID: refundID, Type: graph.TypeActivity, Name: "RefundOrder", Location: "order/activities.go:58"})
	g.AddNode(graph.Node{ID: graph.NodeID(graph.TypeNexusOperation, "GetShippingQuote"), Type: graph.TypeNexusOperation, Name: "GetShippingQuote", Location: "order/workflow.go:90"})
	g.AddNode(graph.Node{ID: graph.NodeID(graph.TypeQueue, "billing-events"), Type: graph.TypeQueue, Name: "billing-events", Inferred: true, Via: "github.com/segmentio/kafka-go"})
	g.AddNode(graph.Node{ID: graph.NodeID(graph.TypeActivity, "DynamicExport"), Type: graph.TypeActivity, Name: "DynamicExport", Unresolved: true})

	g.AddEdge(graph.Edge{From: parentID, To: refundID, Kind: graph.EdgeExecutesActivity, Location: "order/workflow.go:64"})
	g.AddEdge(graph.Edge{From: graph.NodeID(graph.TypeActivity, "ChargeCard"), To: graph.NodeID(graph.TypeExternalService, "stripe-api"), Kind: graph.EdgeTouchesResource, Inferred: true})
	g.AddEdge(graph.Edge{From: parentID, To: graph.NodeID(graph.TypeChildWorkflow, "Fulfillment"), Kind: graph.EdgeExecutesChild, Location: "order/workflow.go:50"})
	g.AddEdge(graph.Edge{From: parentID, To: graph.NodeID(graph.TypeActivity, "ChargeCard"), Kind: graph.EdgeExecutesActivity, Location: "order/workflow.go:58"})
	g.AddEdge(graph.Edge{From: parentID, To: graph.NodeID(graph.TypeNexusOperation, "GetShippingQuote"), Kind: graph.EdgeNexusOperation, Location: "order/workflow.go:90"})
	g.AddEdge(graph.Edge{From: refundID, To: graph.NodeID(graph.TypeQueue, "billing-events"), Kind: graph.EdgeTouchesResource, Inferred: true})
	g.AddEdge(graph.Edge{From: refundID, To: graph.NodeID(graph.TypeDatabase, "orders-db"), Kind: graph.EdgeTouchesResource, Inferred: true})
	g.AddEdge(graph.Edge{From: parentID, To: graph.NodeID(graph.TypeActivity, "DynamicExport"), Kind: graph.EdgeExecutesActivity, Location: "order/workflow.go:77"})

	g.AddUnresolved(graph.Unresolved{
		From:     parentID,
		Call:     "workflow.ExecuteActivity",
		Reason:   "non-constant name expression",
		Location: "order/workflow.go:82",
	})
	return g
}

// requireGolden compares got against the golden file, rewriting it under
// -update.
func requireGolden(t *testing.T, name string, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "golden file missing; run tests with -update to seed it")
	require.Equal(t, string(want), got)
}

func TestJSONGolden(t *testing.T) {
	requireGolden(t, "fixture.json", JSON(fixtureGraph()))
}

func TestMermaidGolden(t *testing.T) {
	requireGolden(t, "fixture.mmd", Mermaid(fixtureGraph()))
}

func TestDOTGolden(t *testing.T) {
	requireGolden(t, "fixture.dot", DOT(fixtureGraph()))
}

func TestHTMLGolden(t *testing.T) {
	got, err := HTML(fixtureGraph())
	require.NoError(t, err)
	requireGolden(t, "fixture.html", got)
}

// TestEmittersDeterministicAndPure renders every format twice — over the
// same graph and over an insertion-order-scrambled clone — and requires all
// outputs byte-identical, and the input graph untouched.
func TestEmittersDeterministicAndPure(t *testing.T) {
	g := fixtureGraph()
	beforeJSON := JSON(g)

	renderers := map[string]func(*graph.DepGraph) (string, error){
		FormatJSON:    func(g *graph.DepGraph) (string, error) { return JSON(g), nil },
		FormatMermaid: func(g *graph.DepGraph) (string, error) { return Mermaid(g), nil },
		FormatDOT:     func(g *graph.DepGraph) (string, error) { return DOT(g), nil },
		FormatHTML:    HTML,
	}

	for name, render := range renderers {
		first, err := render(g)
		require.NoError(t, err, name)
		second, err := render(g)
		require.NoError(t, err, name)
		require.Equal(t, first, second, "%s: repeated renders differ", name)
		require.JSONEq(t, beforeJSON, JSON(g), "%s: renderer mutated the input graph", name)
		// Insertion order must not leak into the output.
		scrambled := fixtureGraph()
		require.Equal(t, first, mustRender(t, render, scrambled), "%s: output depends on graph construction order", name)
	}
}

// mustRender is a helper for renderers that cannot fail in these tests.
func mustRender(t *testing.T, render func(*graph.DepGraph) (string, error), g *graph.DepGraph) string {
	t.Helper()
	out, err := render(g)
	require.NoError(t, err)
	return out
}

// TestRenderDispatch checks that Render dispatches to the right emitter and
// rejects unknown formats with a readable error.
func TestRenderDispatch(t *testing.T) {
	g := fixtureGraph()
	for _, format := range []string{FormatJSON, FormatMermaid, FormatDOT, FormatHTML} {
		out, err := Render(g, format)
		require.NoError(t, err, format)
		require.NotEmpty(t, out, format)
	}
	_, err := Render(g, "svg")
	require.ErrorContains(t, err, `unknown format "svg"`)
}
