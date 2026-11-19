package depvis

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/urfave/cli/v2"
)

// NewCliApp instantiates a new instance of the depvis CLI application.
func NewCliApp() *cli.App {
	return &cli.App{
		Name:  "depvis",
		Usage: "Extract and visualize the dependency graph of Temporal Go SDK workflows via static analysis",
		Commands: []*cli.Command{
			newPilotCommand(),
		},
	}
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
