//go:build test_dep

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fixturePaths returns absolute paths to the analyzer's shared fixture
// corpus (real Go packages in the server module with known workflows,
// activities, and resources) and its annotations file, resolved from the
// package directory so they work no matter where the test binary runs.
func fixturePaths(t *testing.T) (fixtures string, annotations string) {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Join(wd, "../../../tools/depvis/testdata/fixtures"),
		filepath.Join(wd, "../../../tools/depvis/testdata/annotations/precedence.json")
}

// buildDepvis builds the depvis binary once and returns its path.
func buildDepvis(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "depvis")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	require.NoError(t, err, "building depvis binary: %s", out)
	return bin
}

// runDepvis runs the built binary with args and returns its stdout, stderr,
// and process error (nil on exit 0).
func runDepvis(t *testing.T, bin string, args ...string) (stdout string, stderr string, err error) {
	t.Helper()
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd := exec.Command(bin, args...)
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err = cmd.Run()
	return stdoutBuf.String(), stderrBuf.String(), err
}

// runDepvisExpectOutput builds and runs the binary against the fixture
// corpus and asserts a clean exit, returning stdout.
func runDepvisExpectOutput(t *testing.T, bin string, args ...string) string {
	t.Helper()
	stdout, stderr, err := runDepvis(t, bin, args...)
	require.NoError(t, err, "depvis %v: stderr: %s", args, stderr)
	require.Empty(t, stderr, "depvis %v wrote to stderr on success", args)
	return stdout
}

func TestMainFormatOutput(t *testing.T) {
	bin := buildDepvis(t)
	fixturesDir, _ := fixturePaths(t)

	cases := []struct {
		format   string
		contains []string
	}{
		{"json", []string{`"schemaVersion": 1`, `"name": "BatchActivity"`, `"edges"`, `"unresolved"`}},
		{"mermaid", []string{"flowchart TD", "BatchActivity"}},
		{"dot", []string{"digraph depvis {", `label="BatchActivity"`}},
		{"html", []string{"<!DOCTYPE html>", "flowchart TD", "unresolved"}},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			out := runDepvisExpectOutput(t, bin, "--dir", fixturesDir, "--format", tc.format)
			for _, want := range tc.contains {
				require.Contains(t, out, want, "format %s: missing %q", tc.format, want)
			}
		})
	}
}

func TestMainOutFlagWritesFile(t *testing.T) {
	bin := buildDepvis(t)
	fixturesDir, _ := fixturePaths(t)

	for _, format := range []string{"json", "mermaid", "dot", "html"} {
		outFile := filepath.Join(t.TempDir(), "deps."+format)
		runDepvisExpectOutput(t, bin, "--dir", fixturesDir, "--format", format, "--out", outFile)

		data, err := os.ReadFile(outFile)
		require.NoError(t, err, "format %s: --out file not written", format)
		require.NotEmpty(t, data, "format %s: --out file is empty", format)
		switch format {
		case "json":
			var g struct {
				SchemaVersion int `json:"schemaVersion"`
			}
			require.NoError(t, json.Unmarshal(data, &g), "json output must parse")
			require.Equal(t, 1, g.SchemaVersion)
		case "html":
			require.True(t, bytes.HasPrefix(data, []byte("<!DOCTYPE html>")))
		case "mermaid":
			require.True(t, bytes.HasPrefix(data, []byte("flowchart TD")))
		case "dot":
			require.True(t, bytes.HasPrefix(data, []byte("digraph depvis {")))
		default:
			t.Fatalf("unhandled format %s", format)
		}
	}
}

func TestMainAnnotationsFlag(t *testing.T) {
	bin := buildDepvis(t)
	fixturesDir, annotationsPath := fixturePaths(t)

	out := runDepvisExpectOutput(t, bin, "--dir", fixturesDir,
		"--annotations", annotationsPath, "--format", "json")
	var g struct {
		Nodes []struct {
			ID       string `json:"id"`
			Inferred bool   `json:"inferred"`
		} `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &g))
	require.NotEmpty(t, g.Nodes)
}

func TestMainBadFormatFailsReadable(t *testing.T) {
	bin := buildDepvis(t)
	fixturesDir, _ := fixturePaths(t)

	_, stderr, err := runDepvis(t, bin, "--dir", fixturesDir, "--format", "svg")
	require.Error(t, err, "unknown format must exit non-zero")
	require.Contains(t, stderr, `unknown format "svg"`)
	require.Contains(t, stderr, "json, mermaid, dot, html")
}

func TestMainBadDirFailsReadable(t *testing.T) {
	bin := buildDepvis(t)

	_, stderr, err := runDepvis(t, bin, "--dir", "/nonexistent/dir")
	require.Error(t, err, "bad directory must exit non-zero")
	require.Contains(t, stderr, "target dir")
}

func TestMainMissingDirFailsReadable(t *testing.T) {
	bin := buildDepvis(t)

	_, stderr, err := runDepvis(t, bin, "--format", "json")
	require.Error(t, err, "missing --dir must exit non-zero")
	require.Contains(t, stderr, "--dir is required")
}

func TestMainPilotSubcommand(t *testing.T) {
	bin := buildDepvis(t)
	fixturesDir, _ := fixturePaths(t)

	out := runDepvisExpectOutput(t, bin, "pilot", "--dir", fixturesDir)
	var report struct {
		TargetDir string `json:"targetDir"`
		Packages  []any  `json:"packages"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	require.NotEmpty(t, report.TargetDir)
	require.NotEmpty(t, report.Packages)
}
