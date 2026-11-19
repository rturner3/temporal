// Package infra is a depvis test fixture for resource heuristics: one
// activity opening a database connection, one issuing an HTTP request to a
// constant host, and one whose imports produce no resource at all.
//
// This package is a depvis test fixture and is excluded from normal builds
// by living under testdata/.
package infra

import (
	"context"
	"database/sql"
	"net/http"
	"os"

	"go.temporal.io/sdk/workflow"
	"go.temporal.io/server/tools/depvis/testdata/fixtures/registry"
)

// ordersDSN is a constant database DSN.
const ordersDSN = "postgres://user@localhost:5432/orders"

// LoadOrdersActivity opens a database connection with a constant driver.
func LoadOrdersActivity(ctx context.Context) error {
	db, err := sql.Open("postgres", ordersDSN)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return nil
}

// PublishActivity issues an HTTP request to a constant host.
func PublishActivity(ctx context.Context) error {
	req, err := http.NewRequest(http.MethodGet, "https://api.example.com/v1/orders", nil)
	if err != nil {
		return err
	}
	_ = req
	return nil
}

// UntraceableActivity imports nothing depvis recognizes, so it must produce
// no resource nodes at all.
func UntraceableActivity(ctx context.Context) error {
	_ = os.Getenv("ORDERS_API")
	return nil
}

// InfraWorkflow executes the activities above so their bodies are walked.
func InfraWorkflow(ctx workflow.Context) error {
	_ = workflow.ExecuteActivity(ctx, LoadOrdersActivity)
	_ = workflow.ExecuteActivity(ctx, PublishActivity)
	_ = workflow.ExecuteActivity(ctx, UntraceableActivity)
	return nil
}

// RegisterAll registers the package's workflow through the repo-local
// registry wrapper — the shape the server's own workers use.
func RegisterAll() {
	registry.RegisterWorkflowWithOptions(InfraWorkflow, workflow.RegisterOptions{})
}
