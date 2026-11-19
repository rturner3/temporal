// Package depvis implements depvis, the workflow dependency visualizer: a
// static-analysis tool that reconstructs the dependency graph of Temporal Go
// SDK workflows — child workflows, activities, Nexus operations, and the
// resources they touch — directly from Go source code. It never connects to a
// Temporal server and consumes no runtime data.
//
// This file contains the pilot analyzer: a feasibility spike that loads a
// target Go module with golang.org/x/tools/go/packages and reports how every
// workflow.ExecuteActivity / ExecuteLocalActivity / ExecuteChildWorkflow
// call-site name resolves. Names that cannot be resolved to a constant or a
// typed function reference are reported as explicit `unresolved` entries with
// a file:line location and a reason — never dropped.
package depvis

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"

	"go.temporal.io/server/tools/depvis/analyzer"
	"golang.org/x/tools/go/packages"
)

const (
	// schemaVersion is the version of the emitted report schema.
	schemaVersion = 1

	// Edge kinds emitted for resolved call sites.
	EdgeExecutesActivity      = "executes_activity"
	EdgeExecutesLocalActivity = "executes_local_activity"
	EdgeExecutesChildWorkflow = "executes_child_workflow"
)

// PilotReport is the result of a pilot run over one target directory.
type PilotReport struct {
	SchemaVersion int               `json:"schemaVersion"`
	TargetDir     string            `json:"targetDir"`
	Packages      []string          `json:"packages"`
	Edges         []PilotEdge       `json:"edges"`
	Unresolved    []PilotUnresolved `json:"unresolved"`
}

// PilotEdge is one resolved workflow.Execute* call site.
type PilotEdge struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Location string `json:"location"`
}

// PilotUnresolved is one call site whose name expression could not be folded
// to a constant or a typed function reference.
type PilotUnresolved struct {
	Kind     string `json:"kind"`
	Call     string `json:"call"`
	Reason   string `json:"reason"`
	Location string `json:"location"`
}

// RunPilot loads the Go packages under dir (as pattern ./... relative to dir,
// which must live inside a Go module) and reports every Temporal SDK
// workflow.Execute* call site found there: resolved edges with file:line
// locations, and non-foldable name expressions as unresolved entries.
func RunPilot(dir string) (*PilotReport, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving target dir: %w", err)
	}
	if _, err := os.Stat(absDir); err != nil {
		return nil, fmt.Errorf("target dir: %w", err)
	}

	moduleRoot, err := analyzer.ModuleRootFor(absDir)
	if err != nil {
		return nil, err
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps,
		Dir:   absDir,
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("loading packages under %s: %w", absDir, err)
	}

	report := &PilotReport{
		SchemaVersion: schemaVersion,
		TargetDir:     absDir,
		Packages:      make([]string, 0, len(pkgs)),
		Edges:         []PilotEdge{},
		Unresolved:    []PilotUnresolved{},
	}
	for _, pkg := range pkgs {
		for _, err := range pkg.Errors {
			return nil, fmt.Errorf("package %s failed to load: %v", pkg.PkgPath, err)
		}
		report.Packages = append(report.Packages, pkg.PkgPath)
	}

	// Use pkg.Fset: the file set the package's syntax was parsed with.
	for _, pkg := range pkgs {
		fset := pkg.Fset
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				walkFuncBody(report, fset, moduleRoot, pkg, fn)
			}
		}
	}
	return report, nil
}

// walkFuncBody inspects one function declaration for Temporal SDK workflow
// call sites and appends the result to the report.
func walkFuncBody(report *PilotReport, fset *token.FileSet, moduleRoot string, pkg *packages.Package, fn *ast.FuncDecl) {
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		kind, ok := sdkCallKind(pkg.TypesInfo, sel)
		if !ok {
			return true
		}
		loc := analyzer.FormatLocation(fset, moduleRoot, call.Lparen)

		nameArg := call.Args[1] // all three calls take (ctx, name/activity, ...)
		name, reason := analyzer.FoldName(nameArg, pkg.TypesInfo)
		if reason == "" {
			report.Edges = append(report.Edges, PilotEdge{
				Kind:     kind,
				Name:     name,
				Location: loc,
			})
			return true
		}
		report.Unresolved = append(report.Unresolved, PilotUnresolved{
			Kind:     kind,
			Call:     "workflow." + sel.Sel.Name,
			Reason:   reason,
			Location: loc,
		})
		return true
	})
}

// sdkCallKind reports whether sel names a workflow.Execute* call we track, and
// which edge kind it produces. Name folding and location formatting live in
// the analyzer package, shared with the full analyzer.
func sdkCallKind(info *types.Info, sel *ast.SelectorExpr) (kind string, ok bool) {
	obj := analyzer.SelectorObject(info, sel)
	if obj == nil {
		return "", false
	}
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != analyzer.SDKWorkflowPkgPath {
		return "", false
	}
	switch fn.Name() {
	case "ExecuteActivity":
		return EdgeExecutesActivity, true
	case "ExecuteLocalActivity":
		return EdgeExecutesLocalActivity, true
	case "ExecuteChildWorkflow":
		return EdgeExecutesChildWorkflow, true
	}
	return "", false
}

// Folding, object resolution, and location formatting now live in the
// analyzer package; the pilot delegates to them. See tools/depvis/analyzer.
