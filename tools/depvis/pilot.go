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
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/tools/go/packages"
)

const (
	// sdkWorkflowPkgPath is the import path of the Temporal Go SDK workflow package.
	sdkWorkflowPkgPath = "go.temporal.io/sdk/workflow"

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

	moduleRoot, err := moduleRootFor(absDir)
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
		loc := formatLocation(fset, moduleRoot, call.Lparen)

		nameArg := call.Args[1] // all three calls take (ctx, name/activity, ...)
		name, reason := resolveName(nameArg, pkg.TypesInfo)
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
// which edge kind it produces.
func sdkCallKind(info *types.Info, sel *ast.SelectorExpr) (kind string, ok bool) {
	obj := selectorObject(info, sel)
	if obj == nil {
		return "", false
	}
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != sdkWorkflowPkgPath {
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

// selectorObject resolves the object a selector expression refers to, whether
// it is a package-level identifier or a method/field selection.
func selectorObject(info *types.Info, sel *ast.SelectorExpr) types.Object {
	if selection := info.Selections[sel]; selection != nil {
		return selection.Obj()
	}
	return info.Uses[sel.Sel]
}

// resolveName folds the name argument of a workflow.Execute* call to a plain
// string. String literals, referenced constants, constant concatenations, and
// typed function references (whose default registered name is the function's
// own name) all resolve; anything else returns a reason for the unresolved
// entry.
func resolveName(expr ast.Expr, info *types.Info) (name string, reason string) {
	if folded, ok := foldExpr(expr, info); ok {
		return folded, ""
	}
	return "", fmt.Sprintf("non-constant name expression (%s): cannot fold %s to a string", types.ExprString(expr), describeExpr(expr, info))
}

// foldExpr constant-folds a name expression: string literals, identifiers and
// selectors bound to constants or function references, and concatenations of
// the above.
func foldExpr(expr ast.Expr, info *types.Info) (string, bool) {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return foldExpr(e.X, info)
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			v, err := strconv.Unquote(e.Value)
			if err != nil {
				return e.Value, true // raw literal is already the string value
			}
			return v, true
		}
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			lhs, lok := foldExpr(e.X, info)
			rhs, rok := foldExpr(e.Y, info)
			if lok && rok {
				return lhs + rhs, true
			}
		}
	case *ast.Ident:
		if obj := info.Uses[e]; obj != nil {
			return foldObject(obj)
		}
	case *ast.SelectorExpr:
		if obj := selectorObject(info, e); obj != nil {
			return foldObject(obj)
		}
	default:
		return "", false
	}
	return "", false
}

// foldObject folds a resolved object to a name: constants to their value,
// functions and signature-typed variables (typed activity references) to
// their identifier, which is the default registered name for such references.
func foldObject(obj types.Object) (string, bool) {
	switch o := obj.(type) {
	case *types.Const:
		v := o.Val()
		if v != nil && v.Kind() == constant.String {
			return constant.StringVal(v), true
		}
		return "", false
	case *types.Func:
		return o.Name(), true
	case *types.Var:
		if _, isFunc := o.Type().Underlying().(*types.Signature); isFunc {
			return o.Name(), true
		}
		return "", false
	}
	return "", false
}

// describeExpr names the shape of an expression for unresolved reasons.
func describeExpr(expr ast.Expr, info *types.Info) string {
	if ident, ok := expr.(*ast.Ident); ok {
		if obj := info.Uses[ident]; obj != nil {
			if _, isVar := obj.(*types.Var); isVar {
				return fmt.Sprintf("variable %q is not a constant or function reference", ident.Name)
			}
		}
		return fmt.Sprintf("identifier %q", ident.Name)
	}
	return types.ExprString(expr)
}

// moduleRootFor walks up from dir to the enclosing module root (the directory
// holding go.mod), so reported locations are module-relative.
func moduleRootFor(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, "go.mod")); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("no go.mod found at or above %s", dir)
		}
		abs = parent
	}
}

// formatLocation renders a position as a module-relative file:line.
func formatLocation(fset *token.FileSet, moduleRoot string, pos token.Pos) string {
	p := fset.Position(pos)
	rel, err := filepath.Rel(moduleRoot, p.Filename)
	if err != nil {
		rel = p.Filename
	}
	return fmt.Sprintf("%s:%d", filepath.ToSlash(rel), p.Line)
}
