package depvis

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/urfave/cli/v2"
	"go.temporal.io/server/tools/depvis/analyzer"
)

// NewCliApp instantiates a new instance of the depvis CLI application.
func NewCliApp() *cli.App {
	return &cli.App{
		Name:  "depvis",
		Usage: "Extract and visualize the dependency graph of Temporal Go SDK workflows via static analysis",
		Commands: []*cli.Command{
			newPilotCommand(),
			newAnalyzeCommand(),
		},
	}
}

// newAnalyzeCommand builds the analyze subcommand: the full analyzer run over
// a Go module tree, emitting the canonical DepGraph as JSON.
func newAnalyzeCommand() *cli.Command {
	return &cli.Command{
		Name:  "analyze",
		Usage: "Extract the workflow dependency graph of a Go module directory",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "dir",
				Usage:    "directory of the Go module or package tree to analyze (loaded as ./...)",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "annotations",
				Usage: "optional annotations file (YAML or JSON) mapping activity/workflow names to resources; overrides heuristics",
			},
			&cli.StringFlag{
				Name:  "out",
				Usage: "write the JSON graph to this file instead of stdout",
			},
		},
		Action: runAnalyzeAction,
	}
}

// runAnalyzeAction runs the analyzer and emits the canonical graph as JSON.
func runAnalyzeAction(c *cli.Context) error {
	g, err := analyzer.Analyze(c.String("dir"), analyzer.Options{
		Annotations: c.String("annotations"),
	})
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling graph: %w", err)
	}
	data = append(data, '\n')

	out := c.String("out")
	if out == "" {
		if _, err := c.App.Writer.Write(data); err != nil {
			return fmt.Errorf("writing graph: %w", err)
		}
		return nil
	}
	return os.WriteFile(out, data, 0o644)
}

// newPilotCommand builds the pilot subcommand: the feasibility spike that
// validates name resolution over a target codebase before the full analyzer
// is built out.
func newPilotCommand() *cli.Command {
	return &cli.Command{
		Name:  "pilot",
		Usage: "Run the pilot analyzer over a Go module directory and report how workflow call-site names resolve",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "dir",
				Usage:    "directory of the Go module or package tree to analyze (loaded as ./...)",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "out",
				Usage: "write the JSON report to this file instead of stdout",
			},
		},
		Action: runPilotAction,
	}
}

// runPilotAction runs the pilot analyzer and emits the report as JSON.
func runPilotAction(c *cli.Context) error {
	report, err := RunPilot(c.String("dir"))
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling report: %w", err)
	}
	data = append(data, '\n')

	out := c.String("out")
	if out == "" {
		if _, err := c.App.Writer.Write(data); err != nil {
			return fmt.Errorf("writing report: %w", err)
		}
		return nil
	}
	return os.WriteFile(out, data, 0o644)
}
