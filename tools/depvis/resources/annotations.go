// Package resources infers the resource nodes of a dependency graph: which
// databases, queues, and external services the analyzed workflows and
// activities touch. Two layers, in precedence order:
//
//  1. Annotations — an explicit file (--annotations, YAML or JSON) mapping
//     activity or workflow names to typed resources. Annotations always win.
//  2. Import/API heuristics — database/sql opens, known queue/messaging
//     client packages, and HTTP client construction to a constant host.
//     Heuristic-derived nodes carry inferred: true and the enabling import,
//     so a reviewer can audit every guess.
package resources

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"go.temporal.io/server/tools/depvis/graph"
	"gopkg.in/yaml.v3"
)

// Annotation maps one activity or workflow name to one resource it touches.
type Annotation struct {
	// Name is the activity or workflow (registered) name the resource is
	// attached to.
	Name string `yaml:"name" json:"name"`
	// Type is the resource type: external_service, database, or queue.
	Type string `yaml:"type" json:"type"`
	// Label is the resource's name in the graph, e.g. "orders-db".
	Label string `yaml:"label" json:"label"`
	// Technology is optional free text, e.g. "postgres", "kafka", "stripe".
	Technology string `yaml:"technology,omitempty" json:"technology,omitempty"`
}

// annotationsFile is the on-disk shape of an annotations file.
type annotationsFile struct {
	Annotations []Annotation `yaml:"annotations" json:"annotations"`
}

// LoadAnnotations reads and validates an annotations file (by extension:
// .json, .yaml, or .yml). Every entry must carry a name, a valid resource
// type, and a label; an invalid entry is an error, never a silent skip.
func LoadAnnotations(path string) ([]Annotation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading annotations file: %w", err)
	}

	var file annotationsFile
	switch ext := filepath.Ext(path); ext {
	case ".json":
		if err := json.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("parsing annotations file %s: %w", path, err)
		}
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("parsing annotations file %s: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("annotations file %s must be .json, .yaml, or .yml", path)
	}

	for i, a := range file.Annotations {
		if a.Name == "" {
			return nil, fmt.Errorf("annotations file %s: entry %d has no name", path, i)
		}
		if a.Label == "" {
			return nil, fmt.Errorf("annotations file %s: entry %d (%s) has no label", path, i, a.Name)
		}
		switch a.Type {
		case graph.TypeExternalService, graph.TypeDatabase, graph.TypeQueue:
		default:
			return nil, fmt.Errorf(
				"annotations file %s: entry %d (%s) has invalid type %q (want %q, %q, or %q)",
				path, i, a.Name, a.Type,
				graph.TypeExternalService, graph.TypeDatabase, graph.TypeQueue)
		}
	}
	return file.Annotations, nil
}
