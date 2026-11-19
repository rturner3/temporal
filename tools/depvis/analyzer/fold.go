// Package analyzer implements the depvis analyzer core: a type-aware static
// analysis that reconstructs the dependency graph of Temporal Go SDK
// workflows from source. It loads a target module with
// golang.org/x/tools/go/packages, finds workflow and activity registration
// sites, walks the bodies of registered workflows (plus the local helpers
// they call), and records one edge per ExecuteActivity / ExecuteChildWorkflow
// / Nexus-client call site with a file:line location. Names are recovered by
// constant folding (string literals, referenced constants, simple
// concatenations) and by typed function references; anything non-foldable is
// reported as an explicit unresolved entry — never dropped.
package analyzer

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

// SDKWorkflowPkgPath is the import path of the Temporal Go SDK workflow
// package whose calls depvis tracks.
const SDKWorkflowPkgPath = "go.temporal.io/sdk/workflow"

// SelectorObject resolves the object a selector expression refers to, whether
// it is a package-level identifier or a method/field selection.
func SelectorObject(info *types.Info, sel *ast.SelectorExpr) types.Object {
	if selection := info.Selections[sel]; selection != nil {
		return selection.Obj()
	}
	return info.Uses[sel.Sel]
}

// FoldName folds the name argument of a workflow.Execute* call to a plain
// string. String literals, referenced constants, constant concatenations, and
// typed function references (whose default registered name is the function's
// own name) all resolve; anything else returns a reason for the unresolved
// entry.
func FoldName(expr ast.Expr, info *types.Info) (name string, reason string) {
	if folded, ok := FoldExpr(expr, info); ok {
		return folded, ""
	}
	return "", fmt.Sprintf("non-constant name expression (%s): cannot fold %s to a string",
		DescribeExpr(expr, info), types.ExprString(expr))
}

// FoldExpr constant-folds a name expression: string literals, identifiers and
// selectors bound to constants or function references, and concatenations of
// the above.
func FoldExpr(expr ast.Expr, info *types.Info) (string, bool) {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return FoldExpr(e.X, info)
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			v, err := strconv.Unquote(e.Value)
			if err != nil {
				return e.Value, true // raw literal is already the string value
			}
			return v, true
		}
		return "", false
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			lhs, lok := FoldExpr(e.X, info)
			rhs, rok := FoldExpr(e.Y, info)
			if lok && rok {
				return lhs + rhs, true
			}
		}
	case *ast.Ident:
		if obj := info.Uses[e]; obj != nil {
			return FoldObject(obj)
		}
	case *ast.SelectorExpr:
		if obj := SelectorObject(info, e); obj != nil {
			return FoldObject(obj)
		}
	default:
		// Calls, index expressions, etc.: not foldable names.
	}
	return "", false
}

// FoldObject folds a resolved object to a name: constants to their value,
// functions and signature-typed variables (typed activity references) to
// their identifier, which is the default registered name for such references.
func FoldObject(obj types.Object) (string, bool) {
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

// DescribeExpr names the shape of an expression for unresolved reasons.
func DescribeExpr(expr ast.Expr, info *types.Info) string {
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

// ModuleRootFor walks up from dir to the enclosing module root (the directory
// holding go.mod), so reported locations are module-relative.
func ModuleRootFor(dir string) (string, error) {
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

// unwrapParen strips any wrapping parenthesis expressions.
func unwrapParen(expr ast.Expr) ast.Expr {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			return expr
		}
		expr = paren.X
	}
}

// sortStrings sorts a string slice in place.
func sortStrings(s []string) {
	slices.Sort(s)
}

// FormatLocation renders a position as a module-relative file:line.
func FormatLocation(fset *token.FileSet, moduleRoot string, pos token.Pos) string {
	p := fset.Position(pos)
	rel, err := filepath.Rel(moduleRoot, p.Filename)
	if err != nil {
		rel = p.Filename
	}
	return fmt.Sprintf("%s:%d", filepath.ToSlash(rel), p.Line)
}
