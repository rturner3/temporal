// Command depvis is a static-analysis tool that extracts the dependency graph
// of Temporal Go SDK workflows — child workflows, activities, Nexus
// operations, and the resources they touch — directly from Go source code,
// and renders it as JSON, Mermaid, DOT, or a self-contained HTML page. It
// never connects to a Temporal server: all inputs are parsed Go source files.
//
// The pilot subcommand validates the tool's load-bearing assumption on a
// target codebase before the full analyzer is built out: it reports how every
// workflow.ExecuteActivity / ExecuteLocalActivity / ExecuteChildWorkflow
// call-site name resolves, surfacing non-constant names as explicit
// "unresolved" entries with file:line locations instead of dropping them.
package main

import (
	"fmt"
	"os"

	"go.temporal.io/server/tools/depvis"
)

func main() {
	app := depvis.NewCliApp()
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "depvis:", err)
		os.Exit(1)
	}
}
