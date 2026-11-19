//go:build test_dep

package graph

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDepGraphJSONShape pins the serialized graph to the canonical shape from
// the spec: nodes carry id/type/name/location (+inferred/via on resources),
// edges carry from/to/kind/location (+inferred on resource edges), and
// unresolved entries carry from/call/reason/location.
func TestDepGraphJSONShape(t *testing.T) {
	g := NewDepGraph("example.com/app", ".")
	g.AddNode(Node{ID: NodeID(TypeWorkflow, "OrderParent"), Type: TypeWorkflow, Name: "OrderParent", Location: "order/workflow.go:22"})
	g.AddNode(Node{ID: NodeID(TypeActivity, "ChargeCard"), Type: TypeActivity, Name: "ChargeCard", Location: "order/activities.go:41"})
	g.AddNode(Node{ID: NodeID(TypeDatabase, "orders-db"), Type: TypeDatabase, Name: "orders-db", Inferred: true, Via: "database/sql"})
	g.AddEdge(Edge{From: NodeID(TypeWorkflow, "OrderParent"), To: NodeID(TypeActivity, "ChargeCard"), Kind: EdgeExecutesActivity, Location: "order/workflow.go:58"})
	g.AddEdge(Edge{From: NodeID(TypeActivity, "ChargeCard"), To: NodeID(TypeDatabase, "orders-db"), Kind: EdgeTouchesResource, Inferred: true})
	g.AddUnresolved(Unresolved{
		From:     NodeID(TypeWorkflow, "OrderParent"),
		Call:     "workflow.ExecuteActivity",
		Reason:   "non-constant name expression",
		Location: "order/workflow.go:77",
	})

	data, err := json.Marshal(g)
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	require.InDelta(t, float64(SchemaVersion), doc["schemaVersion"], 0)
	require.Equal(t, map[string]any{"modulePath": "example.com/app", "dir": "."}, doc["target"])

	nodes, ok := doc["nodes"].([]any)
	require.True(t, ok)
	workflowNode := nodes[0].(map[string]any)
	require.Equal(t, "workflow:OrderParent", workflowNode["id"])
	require.Equal(t, "workflow", workflowNode["type"])
	require.Equal(t, "OrderParent", workflowNode["name"])
	require.Equal(t, "order/workflow.go:22", workflowNode["location"])
	require.NotContains(t, workflowNode, "inferred")

	resourceNode := nodes[2].(map[string]any)
	require.Equal(t, "database:orders-db", resourceNode["id"])
	require.Equal(t, true, resourceNode["inferred"])
	require.Equal(t, "database/sql", resourceNode["via"])
	require.NotContains(t, resourceNode, "location")

	edges, ok := doc["edges"].([]any)
	require.True(t, ok)
	codeEdge := edges[0].(map[string]any)
	require.Equal(t, "workflow:OrderParent", codeEdge["from"])
	require.Equal(t, "activity:ChargeCard", codeEdge["to"])
	require.Equal(t, "executes_activity", codeEdge["kind"])
	require.Equal(t, "order/workflow.go:58", codeEdge["location"])
	require.NotContains(t, codeEdge, "inferred")

	resourceEdge := edges[1].(map[string]any)
	require.Equal(t, true, resourceEdge["inferred"])
	require.NotContains(t, resourceEdge, "location")

	unresolved, ok := doc["unresolved"].([]any)
	require.True(t, ok)
	entry := unresolved[0].(map[string]any)
	require.Equal(t, "workflow:OrderParent", entry["from"])
	require.Equal(t, "workflow.ExecuteActivity", entry["call"])
	require.Equal(t, "non-constant name expression", entry["reason"])
	require.Equal(t, "order/workflow.go:77", entry["location"])
}

// TestAddNodeProvenWinsOverInferred checks annotation precedence at the model
// level: a heuristic node loses to an annotation-proven node with the same id.
func TestAddNodeProvenWinsOverInferred(t *testing.T) {
	g := NewDepGraph("example.com/app", ".")
	g.AddNode(Node{ID: "database:orders-db", Type: TypeDatabase, Name: "orders-db", Inferred: true, Via: "database/sql"})
	g.AddNode(Node{ID: "database:orders-db", Type: TypeDatabase, Name: "orders-db", Location: "plan.yaml"})

	require.Len(t, g.Nodes, 1)
	require.False(t, g.Nodes[0].Inferred)
	require.Empty(t, g.Nodes[0].Via)

	// The proven node is never demoted back to inferred.
	g.AddNode(Node{ID: "database:orders-db", Type: TypeDatabase, Name: "orders-db", Inferred: true, Via: "database/sql"})
	require.False(t, g.Nodes[0].Inferred)
}

// TestSortIsDeterministic checks that Sort produces stable, ordered output.
func TestSortIsDeterministic(t *testing.T) {
	g := NewDepGraph("example.com/app", ".")
	g.AddNode(Node{ID: "workflow:B", Type: TypeWorkflow, Name: "B"})
	g.AddNode(Node{ID: "workflow:A", Type: TypeWorkflow, Name: "A"})
	g.AddEdge(Edge{From: "workflow:B", To: "activity:X", Kind: EdgeExecutesActivity})
	g.AddEdge(Edge{From: "workflow:A", To: "activity:X", Kind: EdgeExecutesActivity})
	g.AddUnresolved(Unresolved{From: "workflow:A", Call: "workflow.ExecuteActivity", Reason: "r", Location: "a.go:9"})
	g.AddUnresolved(Unresolved{From: "workflow:A", Call: "workflow.ExecuteActivity", Reason: "r", Location: "a.go:2"})
	g.Sort()

	require.Equal(t, []string{"workflow:A", "workflow:B"}, []string{g.Nodes[0].ID, g.Nodes[1].ID})
	require.Equal(t, []string{"workflow:A", "workflow:B"}, []string{g.Edges[0].From, g.Edges[1].From})
	require.Equal(t, []string{"a.go:2", "a.go:9"}, []string{g.Unresolved[0].Location, g.Unresolved[1].Location})
}
