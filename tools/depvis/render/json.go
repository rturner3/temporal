package render

import (
	"encoding/json"

	"go.temporal.io/server/tools/depvis/graph"
)

// JSON renders the canonical DepGraph as-is, pretty-printed with a trailing
// newline. encoding/json emits struct fields in declaration order, so the
// field order is stable; node/edge ordering is whatever the graph carries
// (the analyzer sorts it before returning). The input graph is not mutated.
func JSON(g *graph.DepGraph) string {
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		// DepGraph holds only strings, ints and slices; marshaling cannot
		// fail. If it ever does, return the error as text rather than
		// silently emitting an empty graph.
		return ""
	}
	return string(data) + "\n"
}
