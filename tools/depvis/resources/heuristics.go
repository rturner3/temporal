package resources

import (
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"go.temporal.io/server/tools/depvis/graph"
)

// CallSite is one recorded function call with its string-constant arguments
// folded ("" where an argument is not a constant). CalleePath is the callee's
// fully qualified name, e.g. "database/sql.Open".
type CallSite struct {
	CalleePath string
	ConstArgs  []string
	Location   string
}

// Evidence is the resource-relevant facts collected from one owner's body —
// a workflow or activity function plus the local helpers it calls.
type Evidence struct {
	OwnerID   string
	OwnerName string
	Imports   []string
	Calls     []CallSite
}

// Hint is one heuristic-derived resource candidate for an owner.
type Hint struct {
	Type     string // graph node type: external_service, database, or queue
	Name     string // resource name in the graph
	Via      string // the enabling import, for auditability
	Location string // call-site location, or "" for import-derived hints
}

// OwnerResources pairs one owner with the hints inferred from its evidence.
type OwnerResources struct {
	OwnerID   string
	OwnerName string
	Hints     []Hint
}

// queuePackages are the known queue/messaging client packages depvis
// recognizes by import path.
var queuePackages = map[string]bool{
	"github.com/segmentio/kafka-go":            true,
	"github.com/IBM/sarama":                    true,
	"cloud.google.com/go/pubsub":               true,
	"github.com/Azure/azure-service-bus-go":    true,
	"github.com/nats-io/nats.go":               true,
	"github.com/nats-io/stan.go":               true,
	"github.com/rabbitmq/amqp091-go":           true,
	"github.com/aws/aws-sdk-go-v2/service/sqs": true,
}

// databaseSQLPath is the import path whose opens map to database nodes.
const databaseSQLPath = "database/sql"

// netHTTPPath is the import path of the standard HTTP client.
const netHTTPPath = "net/http"

// httpURLArgIndex maps net/http callee full names to the argument position
// holding the request URL.
var httpURLArgIndex = map[string]int{
	"net/http.NewRequest":            1,
	"net/http.NewRequestWithContext": 1,
	"net/http.Get":                   0,
	"net/http.Head":                  0,
	"net/http.Post":                  0,
	"net/http.PostForm":              0,
}

// Infer runs the import/API heuristics over collected evidence and returns
// the resource hints per owner. Owners with no hints are omitted; unknown
// imports produce nothing.
func Infer(evidence []*Evidence) []OwnerResources {
	var results []OwnerResources
	for _, ev := range evidence {
		hints := inferOwner(ev)
		if len(hints) == 0 {
			continue
		}
		slices.SortStableFunc(hints, func(a, b Hint) int {
			if c := strings.Compare(a.Type, b.Type); c != 0 {
				return c
			}
			return strings.Compare(a.Name, b.Name)
		})
		results = append(results, OwnerResources{
			OwnerID:   ev.OwnerID,
			OwnerName: ev.OwnerName,
			Hints:     hints,
		})
	}
	return results
}

// inferOwner applies every heuristic to one owner's evidence.
func inferOwner(ev *Evidence) []Hint {
	var hints []Hint
	hints = append(hints, inferQueues(ev.Imports)...)
	hints = append(hints, inferDatabases(ev.Calls)...)
	hints = append(hints, inferExternalServices(ev.Calls)...)
	return hints
}

// inferQueues maps known queue/messaging client imports to queue resources.
func inferQueues(imports []string) []Hint {
	var hints []Hint
	for _, imp := range imports {
		if !queuePackages[imp] {
			continue
		}
		hints = append(hints, Hint{
			Type: graph.TypeQueue,
			Name: filepath.Base(imp),
			Via:  imp,
		})
	}
	return hints
}

// inferDatabases maps database/sql.Open calls with a constant driver to
// database resources named after the driver.
func inferDatabases(calls []CallSite) []Hint {
	var hints []Hint
	for _, call := range calls {
		if call.CalleePath != "database/sql.Open" || len(call.ConstArgs) < 1 || call.ConstArgs[0] == "" {
			continue
		}
		hints = append(hints, Hint{
			Type:     graph.TypeDatabase,
			Name:     call.ConstArgs[0],
			Via:      databaseSQLPath,
			Location: call.Location,
		})
	}
	return hints
}

// inferExternalServices maps net/http client construction to a constant URL
// host to external_service resources named after the host.
func inferExternalServices(calls []CallSite) []Hint {
	var hints []Hint
	for _, call := range calls {
		argIndex, tracked := httpURLArgIndex[call.CalleePath]
		if !tracked || len(call.ConstArgs) <= argIndex || call.ConstArgs[argIndex] == "" {
			continue
		}
		host := constantURLHost(call.ConstArgs[argIndex])
		if host == "" {
			continue
		}
		hints = append(hints, Hint{
			Type:     graph.TypeExternalService,
			Name:     host,
			Via:      netHTTPPath,
			Location: call.Location,
		})
	}
	return hints
}

// constantURLHost extracts the host of a constant URL, or "" if the string
// is not an absolute URL with a host.
func constantURLHost(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Hostname()
}
