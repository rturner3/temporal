package depvis

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/urfave/cli/v2"
	"go.temporal.io/server/tools/depvis/analyzer"
	"go.temporal.io/server/tools/depvis/render"
)

// NewCliApp instantiates a new instance of the depvis CLI application.
//
// The default (subcommand-less) action is the full analysis run:
//
//	depvis --dir ./service/worker --format mermaid
//	depvis --dir ./service/worker --format html --out deps.html
//	depvis --dir ./service/worker --annotations plan.yaml --format json
//
// The pilot subcommand is the feasibility spike that validates name
// resolution over a target codebase.
func NewCliApp() *cli.App {
	return &cli.App{
		Name:  "depvis",
		Usage: "Extract and visualize the dependency graph of Temporal Go SDK workflows via static analysis",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "dir",
				Usage: "directory of the Go module or package tree to analyze (loaded as ./...)",
			},
			&cli.StringFlag{
				Name:  "format",
				Usage: "output format: json, mermaid, dot, or html",
				Value: render.FormatJSON,
			},
			&cli.StringFlag{
				Name:  "out",
				Usage: "write the rendered graph to this file instead of stdout",
			},
			&cli.StringFlag{
				Name:  "annotations",
				Usage: "optional annotations file (YAML or JSON) mapping activity/workflow names to resources; overrides heuristics",
			},
		},
		Action:   runAnalyzeAction,
		Commands: []*cli.Command{newPilotCommand()},
	}
}

// runAnalyzeAction runs the analyzer over the target directory and emits the
// canonical graph in the requested format.
func runAnalyzeAction(c *cli.Context) error {
	dir := c.String("dir")
	if dir == "" {
		return errors.New("--dir is required: pass the Go module directory to analyze")
	}
	format := c.String("format")
	if !render.IsValidFormat(format) {
		return fmt.Errorf("unknown format %q (want one of: json, mermaid, dot, html)", format)
	}

	g, err := analyzer.Analyze(dir, analyzer.Options{
		Annotations: c.String("annotations"),
	})
	if err != nil {
		return err
	}

	data, err := render.Render(g, format)
	if err != nil {
		return err
	}
	data += "\n"

	out := c.String("out")
	if out == "" {
		if _, err := fmt.Fprint(c.App.Writer, data); err != nil {
			return fmt.Errorf("writing graph: %w", err)
		}
		return nil
	}
	return os.WriteFile(out, []byte(data), 0o644)
}

// newPilotCommand builds the pilot subcommand: the feasibility spike that
// validates name resolution over a target codebase before the full analyzer
// is trusted.
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
