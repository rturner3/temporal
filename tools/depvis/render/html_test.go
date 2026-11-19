//go:build test_dep

package render

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHTMLContainsGraphAndMermaid unmarshals the embedded viewer payload and
// requires it to carry the canonical graph and the Mermaid source the
// standalone emitters produce, byte for byte.
func TestHTMLContainsGraphAndMermaid(t *testing.T) {
	g := fixtureGraph()
	page, err := HTML(g)
	require.NoError(t, err)

	require.Contains(t, page, `id="depvis-data"`, "embedded payload script missing")
	payloadStart := strings.Index(page, `<script type="application/json" id="depvis-data">`)
	require.GreaterOrEqual(t, payloadStart, 0)
	contentStart := payloadStart + strings.Index(page[payloadStart:], ">") + 1
	payloadEnd := strings.Index(page[contentStart:], "</script>")
	require.GreaterOrEqual(t, payloadEnd, 0)
	embedded := page[contentStart : contentStart+payloadEnd]

	var payload struct {
		Graph   json.RawMessage `json:"graph"`
		Mermaid string          `json:"mermaid"`
	}
	require.NoError(t, json.Unmarshal([]byte(embedded), &payload))

	// The embedded graph must equal the canonical graph of a sorted fixture,
	// modulo JSON formatting.
	sortedFixture := fixtureGraph()
	sortedFixture.Sort()
	require.JSONEq(t, trimNewline(JSON(sortedFixture)), string(payload.Graph))
	require.Equal(t, strings.TrimSpace(Mermaid(g)), strings.TrimSpace(payload.Mermaid))
}

// TestHTMLFilterControls requires a filter checkbox per node type present in
// the graph, in the static file — not injected at runtime.
func TestHTMLFilterControls(t *testing.T) {
	g := fixtureGraph()
	page, err := HTML(g)
	require.NoError(t, err)

	for _, nodeType := range []string{
		"activity", "child_workflow", "database", "external_service",
		"nexus_operation", "queue", "workflow",
	} {
		require.Contains(t, page, `data-type="`+nodeType+`"`,
			"filter control missing for node type "+nodeType)
	}
	require.Equal(t, 7, strings.Count(page, `class="depvis-type"`),
		"one checkbox per distinct node type, deduplicated")
}

// TestHTMLOpenStandalone requires the page to carry no external reference
// other than the Mermaid CDN script: no other http(s) resource, no base64
// data blobs, no sandbox URLs.
func TestHTMLOpenStandalone(t *testing.T) {
	page, err := HTML(fixtureGraph())
	require.NoError(t, err)

	srcs := []string{}
	rest := page
	for {
		idx := strings.Index(rest, `src="`)
		if idx < 0 {
			break
		}
		rest = rest[idx+len(`src="`):]
		end := strings.Index(rest, `"`)
		require.GreaterOrEqual(t, end, 0)
		srcs = append(srcs, rest[:end])
	}
	require.Len(t, srcs, 1, "exactly one external src (the Mermaid CDN)")
	require.Contains(t, srcs[0], "https://cdn.jsdelivr.net/npm/mermaid@11/", "Mermaid CDN reference")

	require.NotContains(t, page, "data:", "no base64 blobs")
	require.NotContains(t, page, "localhost", "no sandbox URLs")
	require.NotContains(t, page, "http://", "no plaintext-HTTP references")
}
