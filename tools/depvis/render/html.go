package render

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"text/template"

	"go.temporal.io/server/tools/depvis/graph"
)

//go:embed viewer.tmpl
var viewerTemplate string

// viewerPayload is the JSON blob the HTML viewer bootstraps from: the
// canonical graph plus the Mermaid source for the full graph, which the
// viewer renders client-side before any filtering.
type viewerPayload struct {
	Graph   json.RawMessage `json:"graph"`
	Mermaid string          `json:"mermaid"`
}

type htmlData struct {
	Title           string
	Payload         string
	Types           []string
	UnresolvedCount int
}

// HTML renders the graph as a single self-contained page: the JSON payload
// embedded in a script element plus a small client-side viewer that renders
// the Mermaid source via the Mermaid CDN and filters nodes by type. No
// server, no build step; the Mermaid CDN script is the page's only external
// reference.
func HTML(g *graph.DepGraph) (string, error) {
	sorted := sortedCopy(g)

	tpl, err := template.New("viewer").Parse(viewerTemplate)
	if err != nil {
		return "", fmt.Errorf("parsing viewer template: %w", err)
	}

	// json.Marshal HTML-escapes <, > and & inside the payload, so a node
	// name containing "</script>" cannot break out of the script element.
	payload, err := json.Marshal(viewerPayload{
		Graph:   json.RawMessage(trimNewline(JSON(sorted))),
		Mermaid: Mermaid(sorted),
	})
	if err != nil {
		return "", fmt.Errorf("marshaling viewer payload: %w", err)
	}

	// One filter checkbox per node type present in the graph, in render
	// order, so the controls exist in the static file.
	types := make([]string, 0, len(sorted.Nodes))
	seen := make(map[string]bool, len(sorted.Nodes))
	for _, n := range sorted.Nodes {
		if !seen[n.Type] {
			seen[n.Type] = true
			types = append(types, n.Type)
		}
	}

	var buf bytes.Buffer
	err = tpl.Execute(&buf, htmlData{
		Title:           fmt.Sprintf("%s (%s)", sorted.Target.ModulePath, sorted.Target.Dir),
		Payload:         string(payload),
		Types:           types,
		UnresolvedCount: len(sorted.Unresolved),
	})
	if err != nil {
		return "", fmt.Errorf("rendering viewer: %w", err)
	}
	return buf.String(), nil
}

func trimNewline(s string) string {
	if len(s) > 0 && s[len(s)-1] == '\n' {
		return s[:len(s)-1]
	}
	return s
}
