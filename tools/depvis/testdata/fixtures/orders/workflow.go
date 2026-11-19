// Package orders is a depvis test fixture covering the analyzer's name
// resolution: typed function references, string constants, constant
// concatenations, dynamic names, child workflows, Nexus clients, and a
// workflow with no dependencies.
//
// This package is a depvis test fixture and is excluded from normal builds
// by living under testdata/.
package orders

import (
	"context"

	"go.temporal.io/sdk/workflow"
	"go.temporal.io/server/tools/depvis/testdata/fixtures/registry"
)

// legacyAuditActivity is a string-constant activity name.
const legacyAuditActivity = "legacy-audit-activity"

// OrderWorkflow executes a typed activity and a typed child workflow.
func OrderWorkflow(ctx workflow.Context) error {
	future := workflow.ExecuteActivity(ctx, ChargeCardActivity)
	_ = future
	child := workflow.ExecuteChildWorkflow(ctx, ShipOrderWorkflow)
	_ = child
	return nil
}

// ShipOrderWorkflow runs as a child and executes an activity by constant name.
func ShipOrderWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, legacyAuditActivity).Get(ctx, nil)
}

// ChargeCardActivity is a typed activity reference.
func ChargeCardActivity(ctx context.Context) error {
	return nil
}

// DynamicNameWorkflow cannot resolve its activity names: a runtime value and
// a constant-concatenated dynamic suffix must both surface as unresolved.
func DynamicNameWorkflow(ctx workflow.Context) error {
	name := runtimeActivityName()
	_ = workflow.ExecuteActivity(ctx, name)
	_ = workflow.ExecuteActivity(ctx, "orders-"+name)
	return nil
}

// runtimeActivityName models a name chosen at runtime.
func runtimeActivityName() string {
	return "chosen-at-runtime"
}

// NoDependenciesWorkflow has no SDK call sites at all.
func NoDependenciesWorkflow(ctx workflow.Context) error {
	return nil
}

// BillingWorkflow invokes a Nexus operation through the SDK Nexus client.
func BillingWorkflow(ctx workflow.Context) error {
	client := workflow.NewNexusClient("billing-endpoint", "billing")
	_ = client
	return nil
}

// RegisterAll registers the package's workflows through the repo-local
// registry wrapper — the shape the server's own workers use (this SDK
// version has no workflow.Register). Names default to the function's own
// name when RegisterOptions carries no Name field.
func RegisterAll() {
	registry.RegisterWorkflowWithOptions(OrderWorkflow, workflow.RegisterOptions{})
	registry.RegisterWorkflowWithOptions(NoDependenciesWorkflow, workflow.RegisterOptions{})
	registry.RegisterWorkflowWithOptions(DynamicNameWorkflow, workflow.RegisterOptions{})
	registry.RegisterWorkflowWithOptions(BillingWorkflow, workflow.RegisterOptions{})
}
