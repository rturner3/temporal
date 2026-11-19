package resources

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferHeuristics is table-driven over synthetic evidence: one case per
// heuristic plus negative cases (relative URL, unknown import).
func TestInferHeuristics(t *testing.T) {
	cases := []struct {
		name     string
		evidence []*Evidence
		want     []OwnerResources // exact match on the result set
	}{
		{
			name: "known queue import produces a queue node",
			evidence: []*Evidence{{
				OwnerID:   "workflow:W",
				OwnerName: "W",
				Imports:   []string{"database/sql", "github.com/segmentio/kafka-go"},
			}},
			want: []OwnerResources{{
				OwnerID:   "workflow:W",
				OwnerName: "W",
				Hints:     []Hint{{Type: "queue", Name: "kafka-go", Via: "github.com/segmentio/kafka-go"}},
			}},
		},
		{
			name: "unknown import produces nothing",
			evidence: []*Evidence{{
				OwnerID:   "workflow:W",
				OwnerName: "W",
				Imports:   []string{"os", "encoding/json"},
			}},
			want: nil,
		},
		{
			name: "database/sql open with constant driver produces a database node",
			evidence: []*Evidence{{
				OwnerID:   "activity:A",
				OwnerName: "A",
				Calls:     []CallSite{{CalleePath: "database/sql.Open", ConstArgs: []string{"postgres", "dsn"}}, {CalleePath: "(*sql.DB).Query"}},
			}},
			want: []OwnerResources{{
				OwnerID:   "activity:A",
				OwnerName: "A",
				Hints:     []Hint{{Type: "database", Name: "postgres", Via: "database/sql"}},
			}},
		},
		{
			name: "net/http to a constant absolute URL produces an external_service node",
			evidence: []*Evidence{{
				OwnerID:   "activity:A",
				OwnerName: "A",
				Calls:     []CallSite{{CalleePath: "net/http.Get", ConstArgs: []string{"https://api.example.com/v1/orders"}}},
			}},
			want: []OwnerResources{{
				OwnerID:   "activity:A",
				OwnerName: "A",
				Hints:     []Hint{{Type: "external_service", Name: "api.example.com", Via: "net/http"}},
			}},
		},
		{
			name: "net/http to a non-constant or relative URL produces nothing",
			evidence: []*Evidence{{
				OwnerID:   "activity:A",
				OwnerName: "A",
				Calls:     []CallSite{{CalleePath: "net/http.Get", ConstArgs: []string{""}}, {CalleePath: "net/http.Get", ConstArgs: []string{"/v1/orders"}}},
			}},
			want: nil,
		},
		{
			name: "hints of different kinds sort by type then name",
			evidence: []*Evidence{{
				OwnerID:   "activity:A",
				OwnerName: "A",
				Imports:   []string{"github.com/nats-io/nats.go"},
				Calls:     []CallSite{{CalleePath: "database/sql.Open", ConstArgs: []string{"mysql"}}},
			}},
			want: []OwnerResources{{
				OwnerID:   "activity:A",
				OwnerName: "A",
				Hints: []Hint{
					{Type: "database", Name: "mysql", Via: "database/sql"},
					{Type: "queue", Name: "nats.go", Via: "github.com/nats-io/nats.go"},
				},
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, Infer(tc.evidence))
		})
	}
}

// TestLoadAnnotations covers the annotations loader: both file formats, and
// the validation errors that must fail loudly instead of skipping entries.
func TestLoadAnnotations(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		return path
	}

	t.Run("valid yaml", func(t *testing.T) {
		path := write("plan.yaml", `annotations:
  - name: ChargeCard
    type: database
    label: orders-db
    technology: postgres
`)
		anns, err := LoadAnnotations(path)
		require.NoError(t, err)
		require.Equal(t, []Annotation{{
			Name: "ChargeCard", Type: "database", Label: "orders-db", Technology: "postgres",
		}}, anns)
	})

	t.Run("valid json", func(t *testing.T) {
		path := write("plan.json", `{"annotations": [{"name": "Ship", "type": "queue", "label": "ship-events"}]}`)
		anns, err := LoadAnnotations(path)
		require.NoError(t, err)
		require.Equal(t, []Annotation{{Name: "Ship", Type: "queue", Label: "ship-events"}}, anns)
	})

	t.Run("invalid resource type", func(t *testing.T) {
		path := write("bad-type.yaml", `annotations:
  - name: X
    type: widget
    label: some-widget
`)
		_, err := LoadAnnotations(path)
		require.ErrorContains(t, err, "invalid type")
	})

	t.Run("missing label", func(t *testing.T) {
		path := write("bad-label.yaml", `annotations:
  - name: X
    type: queue
`)
		_, err := LoadAnnotations(path)
		require.ErrorContains(t, err, "has no label")
	})

	t.Run("missing name", func(t *testing.T) {
		path := write("bad-name.yaml", `annotations:
  - type: queue
    label: q
`)
		_, err := LoadAnnotations(path)
		require.ErrorContains(t, err, "has no name")
	})

	t.Run("unsupported extension", func(t *testing.T) {
		_, err := LoadAnnotations(write("plan.toml", "annotations = []"))
		require.ErrorContains(t, err, "must be .json, .yaml, or .yml")
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := LoadAnnotations(filepath.Join(dir, "nope.yaml"))
		require.ErrorContains(t, err, "reading annotations file")
	})
}
