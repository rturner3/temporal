// Package registry mimics the repo-local worker registration wrappers the
// server's own workers use (methods on go.temporal.io/sdk/worker Registry and
// repo-local helpers with the same names). depvis must treat these wrappers
// as registration sites just like the SDK's workflow.Register.
//
// This package is a depvis test fixture and is excluded from normal builds
// by living under testdata/.
package registry

import "go.temporal.io/sdk/workflow"

// RegisterWorkflowWithOptions mirrors the SDK worker-registry wrapper shape.
func RegisterWorkflowWithOptions(wfFunc any, opts workflow.RegisterOptions) {
	_ = wfFunc
	_ = opts
}

// RegisterActivity mirrors the SDK worker-registry wrapper shape.
func RegisterActivity(activity any) {
	_ = activity
}
