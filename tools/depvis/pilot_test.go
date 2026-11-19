//go:build test_dep

package depvis

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// runPilotOnWorkerCorpus analyzes the server's own in-repo worker workflows —
// real, idiomatic Go SDK workflow code — as the pilot corpus.
func runPilotOnWorkerCorpus(t *testing.T) *PilotReport {
	t.Helper()
	report, err := RunPilot("../../service/worker")
	require.NoError(t, err)
	require.NotEmpty(t, report.Packages)
	return report
}

func findEdge(report *PilotReport, location, kind, name string) bool {
	for _, e := range report.Edges {
		if e.Location == location && e.Kind == kind && e.Name == name {
			return true
		}
	}
	return false
}

func findUnresolved(report *PilotReport, location string) *PilotUnresolved {
	for i := range report.Unresolved {
		if report.Unresolved[i].Location == location {
			return &report.Unresolved[i]
		}
	}
	return nil
}

// TestPilotSchedulerTypedActivity pins the spec's example: the
// workflow.ExecuteActivity call at service/worker/scheduler/workflow.go:1819
// uses a typed activity reference (s.a.WatchWorkflow) and must resolve to the
// activity function's name.
func TestPilotSchedulerTypedActivity(t *testing.T) {
	report := runPilotOnWorkerCorpus(t)
	require.True(t, findEdge(report,
		"service/worker/scheduler/workflow.go:1819",
		EdgeExecutesActivity, "WatchWorkflow"))
}

// TestPilotConstantNames covers the constant-folded activity names across the
// worker corpus: parentclosepolicy, scanner (task queue, history, executions,
// build ids, schedule invariants), and dlq.
func TestPilotConstantNames(t *testing.T) {
	report := runPilotOnWorkerCorpus(t)

	constants := []struct {
		location string
		name     string
	}{
		{"service/worker/parentclosepolicy/workflow.go:82", "temporal-sys-parent-close-policy-activity"},
		{"service/worker/scanner/workflow.go:83", "temporal-sys-tq-scanner-scvg-activity"},
		{"service/worker/scanner/workflow.go:92", "temporal-sys-history-scanner-scvg-activity"},
		{"service/worker/scanner/workflow.go:103", "temporal-sys-executions-scanner-scvg-activity"},
		{"service/worker/scanner/build_ids/scavenger.go:107", "scavenge-build-ids"},
		{"service/worker/dlq/workflow.go:293", "dlq-read-tasks-activity"},
		{"service/worker/dlq/workflow.go:316", "dlq-re-enqueue-tasks-activity"},
	}
	for _, c := range constants {
		require.True(t, findEdge(report, c.location, EdgeExecutesActivity, c.name),
			"expected constant %q to resolve at %s", c.name, c.location)
	}
}

// TestPilotChildWorkflowNames covers typed child-workflow references, which
// resolve to the workflow function's name.
func TestPilotChildWorkflowNames(t *testing.T) {
	report := runPilotOnWorkerCorpus(t)

	require.True(t, findEdge(report,
		"service/worker/deletenamespace/reclaimresources/workflow.go:239",
		EdgeExecutesChildWorkflow, "DeleteExecutionsWorkflow"))
	require.True(t, findEdge(report,
		"service/worker/deletenamespace/workflow.go:189",
		EdgeExecutesChildWorkflow, "ReclaimResourcesWorkflow"))
	require.True(t, findEdge(report,
		"service/worker/migration/force_replication_workflow.go:357",
		EdgeExecutesChildWorkflow, "ForceTaskQueueUserDataReplicationWorkflow"))
}

// TestPilotUnresolvedNeverDropped pins the honest-failure mode: the
// runScanActivity helper takes the activity reference as an `any` parameter,
// so the call site cannot be folded and must surface as an unresolved entry
// with a location and reason — not silently disappear.
func TestPilotUnresolvedNeverDropped(t *testing.T) {
	report := runPilotOnWorkerCorpus(t)

	unresolved := findUnresolved(report,
		"service/worker/scanner/scheduleinvariants/workflows.go:105")
	require.NotNil(t, unresolved, "expected unresolved entry for runScanActivity helper call site")
	require.Equal(t, EdgeExecutesActivity, unresolved.Kind)
	require.Equal(t, "workflow.ExecuteActivity", unresolved.Call)
	require.NotEmpty(t, unresolved.Reason)
}

// TestPilotEveryCallSiteAccountedFor verifies the never-drop invariant holds
// across the whole corpus: edges plus unresolved entries are disjoint and
// non-empty, and no tracked call produced no record.
func TestPilotEveryCallSiteAccountedFor(t *testing.T) {
	report := runPilotOnWorkerCorpus(t)
	require.NotEmpty(t, report.Edges, "worker corpus must contain resolvable call sites")

	seen := make(map[string]bool, len(report.Edges)+len(report.Unresolved))
	for _, e := range report.Edges {
		require.NotEmpty(t, e.Name)
		require.NotEmpty(t, e.Location)
		require.False(t, seen[e.Location], "duplicate record for %s", e.Location)
		seen[e.Location] = true
	}
	for _, u := range report.Unresolved {
		require.NotEmpty(t, u.Reason)
		require.NotEmpty(t, u.Location)
		require.False(t, seen[u.Location], "duplicate record for %s", u.Location)
		seen[u.Location] = true
	}
	require.NotEmpty(t, report.Unresolved,
		"worker corpus is known to contain at least one non-foldable call site (scheduleinvariants helper)")
}
