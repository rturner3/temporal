package analyzer

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Registration call shapes depvis recognizes, beyond the SDK's own
// workflow.Register / workflow.RegisterWithName: the worker-registry wrapper
// pattern the server's own workers use (methods on go.temporal.io/sdk/worker
// Registry and repo-local helpers with the same names). Raw workflow.Register
// is absent from the server's worker corpus — everything there goes through
// these wrappers.
const (
	regWorkflow                  = "RegisterWorkflow"
	regWorkflowWithOptions       = "RegisterWorkflowWithOptions"
	regActivity                  = "RegisterActivity"
	regActivityWithOptions       = "RegisterActivityWithOptions"
	sdkRegister                  = "Register"
	sdkRegisterWithName          = "RegisterWithName"
	nodeTypeWorkflowRegistration = "workflow"
	nodeTypeActivityRegistration = "activity"
)

// registration is one detected workflow or activity registration site.
type registration struct {
	fn       *types.Func // resolved registered function reference (nil if unresolved)
	nodeType string      // graph node type the registered function becomes
	name     string      // registered name (register-options name, or function name)
	regCall  string      // call shape, e.g. "workflow.Register" or "registry.RegisterWorkflowWithOptions"
	location string      // registration call site, module-relative file:line
	declPos  token.Pos   // position of the registered function's declaration, if known
}

// findRegistrations scans every function body in the loaded packages for
// registration call sites, in deterministic package and file order.
func findRegistrations(loaded *Loaded) []registration {
	var regs []registration
	for _, pkg := range sortedByPath(loaded.Pkgs) {
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				reg, ok := matchRegistration(loaded, pkg, sel, call)
				if !ok {
					return true
				}
				regs = append(regs, reg)
				return true
			})
		}
	}
	slices.SortStableFunc(regs, func(a, b registration) int { return strings.Compare(a.location, b.location) })
	return regs
}

// matchRegistration reports whether a call is a registration site depvis
// tracks, and extracts the registered function, name, and location.
func matchRegistration(loaded *Loaded, pkg *packages.Package, sel *ast.SelectorExpr, call *ast.CallExpr) (registration, bool) {
	obj := SelectorObject(pkg.TypesInfo, sel)
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil {
		return registration{}, false
	}

	nameArg, fnArg, isActivity := "", -1, false
	switch {
	case fn.Pkg().Path() == SDKWorkflowPkgPath && fn.Name() == sdkRegister && len(call.Args) >= 1:
		fnArg = 0
	case fn.Pkg().Path() == SDKWorkflowPkgPath && fn.Name() == sdkRegisterWithName && len(call.Args) >= 2:
		nameArg, fnArg = "0", 1
	case fn.Name() == regWorkflow && fn.Pkg().Path() != SDKWorkflowPkgPath && len(call.Args) >= 1:
		fnArg = 0
	case fn.Name() == regWorkflowWithOptions && fn.Pkg().Path() != SDKWorkflowPkgPath && len(call.Args) >= 2:
		nameArg, fnArg = "opts", 0
	case fn.Name() == regActivity && fn.Pkg().Path() != SDKWorkflowPkgPath && len(call.Args) >= 1:
		isActivity, fnArg = true, 0
	case fn.Name() == regActivityWithOptions && fn.Pkg().Path() != SDKWorkflowPkgPath && len(call.Args) >= 2:
		isActivity, nameArg, fnArg = true, "opts", 0
	default:
		return registration{}, false
	}

	nodeType := nodeTypeWorkflowRegistration
	if isActivity {
		nodeType = nodeTypeActivityRegistration
	}
	reg := registration{
		nodeType: nodeType,
		regCall:  fmt.Sprintf("%s.%s", calleePkgAlias(fn), fn.Name()),
		location: FormatLocation(loaded.Fset, loaded.ModuleRoot, call.Lparen),
	}
	if pos, _, found := registeredFuncDecl(loaded, pkg, call.Args[fnArg]); found {
		reg.fn, reg.declPos = pos, loaded.funcDecls[pos].decl.Pos()
	}

	reg.name = registeredName(pkg, nameArg, call, reg)
	return reg, true
}

// registeredName resolves the registered type name: an explicit name argument
// (RegisterWithName) or RegisterOptions.Name (the *WithOptions shapes), else
// the registered function's own name.
func registeredName(pkg *packages.Package, nameArg string, call *ast.CallExpr, reg registration) string {
	switch nameArg {
	case "0":
		if name, ok := FoldExpr(call.Args[0], pkg.TypesInfo); ok {
			return name
		}
	case "opts":
		if name, ok := registerOptionsName(call.Args[1], pkg.TypesInfo); ok {
			return name
		}
	default:
		// No explicit name argument; fall through to the function name.
	}
	if reg.fn != nil {
		return reg.fn.Name()
	}
	return ""
}

// registerOptionsName extracts and folds the Name field of a
// workflow.RegisterOptions (or compatible) composite literal.
func registerOptionsName(expr ast.Expr, info *types.Info) (string, bool) {
	lit, ok := unwrapParen(expr).(*ast.CompositeLit)
	if !ok {
		return "", false
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Name" {
			return FoldExpr(kv.Value, info)
		}
	}
	return "", false
}

// registeredFuncDecl resolves a registration argument to a function object
// with a known declaration.
func registeredFuncDecl(loaded *Loaded, pkg *packages.Package, expr ast.Expr) (*types.Func, token.Pos, bool) {
	switch e := unwrapParen(expr).(type) {
	case *ast.Ident:
		if fn, ok := pkg.TypesInfo.Uses[e].(*types.Func); ok {
			if entry, ok := loaded.declOf(fn); ok {
				return fn, entry.decl.Pos(), true
			}
			return fn, token.NoPos, true
		}
	case *ast.SelectorExpr:
		if fn, ok := SelectorObject(pkg.TypesInfo, e).(*types.Func); ok {
			if entry, ok := loaded.declOf(fn); ok {
				return fn, entry.decl.Pos(), true
			}
			return fn, token.NoPos, true
		}
	default:
		// Composite literals, calls, etc.: not a function reference.
	}
	return nil, token.NoPos, false
}

// calleePkgAlias renders the call prefix for unresolved-entry reporting.
func calleePkgAlias(fn *types.Func) string {
	if fn.Pkg() == nil {
		return fn.Name()
	}
	// A short package name is friendlier in reports than the full path; use
	// the path's last element, which matches how the calls are written.
	return lastPathElement(fn.Pkg().Path())
}

func lastPathElement(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:]
		}
	}
	return path
}
