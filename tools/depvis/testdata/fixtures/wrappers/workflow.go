// Package wrappers is a depvis test fixture for the repo-local registration
// wrapper pattern: registrations go through a registry wrapper rather than
// the SDK's workflow.Register.
//
// This package is a depvis test fixture and is excluded from normal builds
// by living under testdata/.
package wrappers

import (
	"context"

	"go.temporal.io/sdk/workflow"
	"go.temporal.io/server/tools/depvis/testdata/fixtures/registry"
)

// processingWorkflowName is a wrapper-registered workflow name constant.
const processingWorkflowName = "wrapped-processing-workflow"

// ProcessingWorkflow is registered through the repo-local wrapper.
func ProcessingWorkflow(ctx workflow.Context) error {
	return workflow.ExecuteActivity(ctx, TransformActivity).Get(ctx, nil)
}

// TransformActivity is registered through the repo-local wrapper.
func TransformActivity(ctx context.Context) error {
	return nil
}

// BatchActivity is registered through the repo-local wrapper.
func BatchActivity(ctx context.Context) error {
	return nil
}

// RegisterAll registers through the wrapper shapes the server's own workers
// use.
func RegisterAll() {
	registry.RegisterWorkflowWithOptions(ProcessingWorkflow, workflow.RegisterOptions{Name: processingWorkflowName})
	registry.RegisterActivity(TransformActivity)
	registry.RegisterActivity(BatchActivity)
}
