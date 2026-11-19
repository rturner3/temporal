package analyzer

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"

	"go.temporal.io/server/tools/depvis/graph"
	"go.temporal.io/server/tools/depvis/resources"
	"golang.org/x/tools/go/packages"
)

// Options controls an analyzer run.
type Options struct {
	// Annotations is an optional path to an annotations file (YAML or JSON)
	// mapping activity/workflow names to resources. Annotations always win
	// over import/API heuristics.
	Annotations string
}

// Analyze extracts the dependency graph of the Go module tree under dir.
func Analyze(dir string, opts Options) (*graph.DepGraph, error) {
	loaded, err := Load(dir)
	if err != nil {
		return nil, err
	}

	g := graph.NewDepGraph(loaded.ModulePath, relTargetDir(dir))
	a := &analyzer{loaded: loaded, graph: g, evidenceByOwner: make(map[string]*resources.Evidence)}

	regs := findRegistrations(loaded)
	a.emitRegistrationNodes(regs)

	var activityRegs []activityRegistration
	for _, reg := range regs {
		if reg.fn == nil {
			// Already reported as unresolved by emitRegistrationNodes.
			continue
		}
		if reg.nodeType == nodeTypeActivityRegistration {
			activityRegs = append(activityRegs, activityRegistration{
				id:   graph.NodeID(reg.nodeType, reg.name),
				name: reg.name,
				fn:   reg.fn,
			})
			continue
		}
		a.walkFunction(graph.NodeID(reg.nodeType, reg.name), reg.name, reg.fn)
	}

	// Activity bodies are walked for edges too: an activity can execute
	// further activities or child workflows.
	for _, ar := range activityRegs {
		a.walkFunction(ar.id, ar.name, ar.fn)
	}

	if err := a.applyResources(opts); err != nil {
		return nil, err
	}

	a.graph.Sort()
	return g, nil
}

// activityRegistration carries an activity registration into the body walk.
type activityRegistration struct {
	id   string
	name string
	fn   *types.Func
}

// analyzer holds one run's state.
type analyzer struct {
	loaded *Loaded
	graph  *graph.DepGraph

	// evidence accumulates resource-relevant facts per owner node, in
	// discovery order for deterministic output.
	evidence        []*resources.Evidence
	evidenceByOwner map[string]*resources.Evidence
}

// evidenceFor returns the evidence bucket for one owner, creating it on first
// use.
func (a *analyzer) evidenceFor(ownerID, ownerName string) *resources.Evidence {
	if ev, ok := a.evidenceByOwner[ownerID]; ok {
		return ev
	}
	ev := &resources.Evidence{OwnerID: ownerID, OwnerName: ownerName}
	a.evidenceByOwner[ownerID] = ev
	a.evidence = append(a.evidence, ev)
	return ev
}

// emitRegistrationNodes adds a node for every registration with a resolvable
// name and function, or exactly one unresolved entry per failed site.
func (a *analyzer) emitRegistrationNodes(regs []registration) {
	for _, reg := range regs {
		if reg.fn == nil {
			a.graph.AddUnresolved(graph.Unresolved{
				Call:     reg.regCall,
				Reason:   "registration argument could not be resolved to a named function reference",
				Location: reg.location,
			})
			continue
		}
		if reg.name == "" {
			a.graph.AddUnresolved(graph.Unresolved{
				Call:     reg.regCall,
				Reason:   "registered name could not be resolved to a constant or function reference",
				Location: reg.location,
			})
			continue
		}
		location := reg.location
		if reg.declPos != token.NoPos {
			location = FormatLocation(a.loaded.Fset, a.loaded.ModuleRoot, reg.declPos)
		}
		a.graph.AddNode(graph.Node{
			ID:       graph.NodeID(reg.nodeType, reg.name),
			Type:     reg.nodeType,
			Name:     reg.name,
			Location: location,
		})
	}
}

// walkFunction walks fn's body plus transitively-called local functions with
// a per-owner visited set, so helpers shared across workflows are attributed
// to each owner once.
func (a *analyzer) walkFunction(ownerID, ownerName string, fn *types.Func) {
	visited := make(map[*types.Func]bool)
	stack := []*types.Func{fn}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[current] {
			continue
		}
		visited[current] = true

		entry, ok := a.loaded.declOf(current)
		if !ok {
			continue
		}
		a.recordEvidence(ownerID, ownerName, entry)
		stack = a.walkBody(ownerID, ownerName, entry, stack)
	}
}

// walkBody inspects one function body for SDK call sites and local callees to
// enqueue, returning the grown work stack. SDK call sites become edges or
// unresolved entries; every other call whose callee is declared in the
// analyzed packages is pushed for a transitively-attributed walk.
func (a *analyzer) walkBody(ownerID, ownerName string, entry funcDeclEntry, stack []*types.Func) []*types.Func {
	info := entry.pkg.TypesInfo
	ast.Inspect(entry.decl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if a.handleSDKCall(ownerID, ownerName, info, fun, call) {
				return true
			}
			if a.handleNexusCall(ownerID, info, fun, call) {
				return true
			}
			stack = a.enqueueLocalCallee(info, fun, stack)
		case *ast.Ident:
			stack = a.enqueueIdentCallee(info, fun, stack)
		default:
			// Method values, builtin calls, conversions: nothing to track.
		}
		a.recordCall(ownerID, ownerName, info, call)
		return true
	})
	return stack
}

// recordEvidence adds a walked function's file imports to the owner's
// evidence.
func (a *analyzer) recordEvidence(ownerID, ownerName string, entry funcDeclEntry) {
	ev := a.evidenceFor(ownerID, ownerName)
	ev.Imports = append(ev.Imports, a.loaded.importsOf(entry.file)...)
}

// recordCall folds a call's string-constant arguments and records the call
// for resource heuristics.
func (a *analyzer) recordCall(ownerID, ownerName string, info *types.Info, call *ast.CallExpr) {
	var callee *types.Func
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		if fn, ok := SelectorObject(info, fun).(*types.Func); ok {
			callee = fn
		}
	case *ast.Ident:
		if fn, ok := info.Uses[fun].(*types.Func); ok {
			callee = fn
		}
	default:
		// Not a plain function call: nothing to record.
	}
	if callee == nil {
		return
	}

	constArgs := make([]string, len(call.Args))
	for i, arg := range call.Args {
		constArgs[i], _ = FoldExpr(arg, info)
	}
	ev := a.evidenceFor(ownerID, ownerName)
	ev.Calls = append(ev.Calls, resources.CallSite{
		CalleePath: callFullName(callee),
		ConstArgs:  constArgs,
		Location:   FormatLocation(a.loaded.Fset, a.loaded.ModuleRoot, call.Lparen),
	})
}

// applyResources adds resource nodes and touches_resource edges: annotations
// for an owner when present (always winning), heuristic hints otherwise.
func (a *analyzer) applyResources(opts Options) error {
	var annotationsByName map[string][]resources.Annotation
	if opts.Annotations != "" {
		annotations, err := resources.LoadAnnotations(opts.Annotations)
		if err != nil {
			return err
		}
		annotationsByName = make(map[string][]resources.Annotation)
		for _, ann := range annotations {
			annotationsByName[ann.Name] = append(annotationsByName[ann.Name], ann)
		}
	}

	inferred := resources.Infer(a.evidence)
	ownersWithHints := make(map[string]bool, len(inferred))
	for _, owner := range inferred {
		ownersWithHints[owner.OwnerName] = true
		if anns, ok := annotationsByName[owner.OwnerName]; ok {
			a.addAnnotationResources(owner.OwnerID, anns)
			continue
		}
		for _, hint := range owner.Hints {
			a.addHintResources(owner.OwnerID, hint)
		}
	}

	// Owners with no heuristic hints may still have annotations.
	for _, ev := range a.evidence {
		if ownersWithHints[ev.OwnerName] {
			continue
		}
		if anns, ok := annotationsByName[ev.OwnerName]; ok {
			a.addAnnotationResources(ev.OwnerID, anns)
		}
	}
	return nil
}

// addAnnotationResources emits annotation-derived nodes and edges — explicit
// user mapping, so never marked inferred.
func (a *analyzer) addAnnotationResources(ownerID string, anns []resources.Annotation) {
	for _, ann := range anns {
		a.graph.AddNode(graph.Node{
			ID:   graph.NodeID(ann.Type, ann.Label),
			Type: ann.Type,
			Name: ann.Label,
			Via:  ann.Technology,
		})
		a.graph.AddEdge(graph.Edge{
			From: ownerID,
			To:   graph.NodeID(ann.Type, ann.Label),
			Kind: graph.EdgeTouchesResource,
		})
	}
}

// addHintResources emits heuristic-derived nodes and edges.
func (a *analyzer) addHintResources(ownerID string, hint resources.Hint) {
	a.graph.AddNode(graph.Node{
		ID:       graph.NodeID(hint.Type, hint.Name),
		Type:     hint.Type,
		Name:     hint.Name,
		Location: hint.Location,
		Inferred: true,
		Via:      hint.Via,
	})
	a.graph.AddEdge(graph.Edge{
		From:     ownerID,
		To:       graph.NodeID(hint.Type, hint.Name),
		Kind:     graph.EdgeTouchesResource,
		Location: hint.Location,
		Inferred: true,
	})
}

// handleSDKCall records an ExecuteActivity / ExecuteLocalActivity /
// ExecuteChildWorkflow edge (or unresolved entry) and reports whether the
// call was one of those shapes.
func (a *analyzer) handleSDKCall(ownerID, ownerName string, info *types.Info, sel *ast.SelectorExpr, call *ast.CallExpr) bool {
	obj := SelectorObject(info, sel)
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != SDKWorkflowPkgPath {
		return false
	}

	var edgeKind, nodeType string
	switch fn.Name() {
	case "ExecuteActivity", "ExecuteLocalActivity", "ExecuteActivityWithOption":
		edgeKind, nodeType = graph.EdgeExecutesActivity, graph.TypeActivity
	case "ExecuteChildWorkflow":
		edgeKind, nodeType = graph.EdgeExecutesChild, graph.TypeChildWorkflow
	default:
		return false
	}

	// All tracked shapes take (ctx, name-or-function-reference, ...).
	nameArg := call.Args[1]
	location := FormatLocation(a.loaded.Fset, a.loaded.ModuleRoot, call.Lparen)
	callShape := "workflow." + fn.Name()

	name, reason := FoldName(nameArg, info)
	if reason != "" {
		a.graph.AddUnresolved(graph.Unresolved{
			From:     ownerID,
			Call:     callShape,
			Reason:   reason,
			Location: location,
		})
		return true
	}

	targetFn, _ := nameArgFunc(a.loaded, info, nameArg)
	a.graph.AddNode(graph.Node{
		ID:       graph.NodeID(nodeType, name),
		Type:     nodeType,
		Name:     name,
		Location: declLocation(a.loaded, targetFn),
	})
	a.graph.AddEdge(graph.Edge{
		From:     ownerID,
		To:       graph.NodeID(nodeType, name),
		Kind:     edgeKind,
		Location: location,
	})

	// The target's own body is walked under its node: activities can execute
	// further activities, and an unregistered child's body would otherwise
	// never be visited.
	if targetFn != nil {
		a.walkFunction(graph.NodeID(nodeType, name), name, targetFn)
	}
	return true
}

// handleNexusCall records a workflow.NewNexusClient edge (or unresolved
// entry) and reports whether the call was that shape.
func (a *analyzer) handleNexusCall(ownerID string, info *types.Info, sel *ast.SelectorExpr, call *ast.CallExpr) bool {
	obj := SelectorObject(info, sel)
	fn, ok := obj.(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != SDKWorkflowPkgPath || fn.Name() != "NewNexusClient" {
		return false
	}

	location := FormatLocation(a.loaded.Fset, a.loaded.ModuleRoot, call.Lparen)
	if len(call.Args) < 2 {
		a.graph.AddUnresolved(graph.Unresolved{
			From:     ownerID,
			Call:     "workflow.NewNexusClient",
			Reason:   fmt.Sprintf("expected endpoint and service arguments, found %d", len(call.Args)),
			Location: location,
		})
		return true
	}

	endpoint, eok := FoldExpr(call.Args[0], info)
	service, sok := FoldExpr(call.Args[1], info)
	if !eok || !sok {
		a.graph.AddUnresolved(graph.Unresolved{
			From:     ownerID,
			Call:     "workflow.NewNexusClient",
			Reason:   "endpoint and service arguments must be constants or function references",
			Location: location,
		})
		return true
	}

	name := endpoint + "/" + service
	a.graph.AddNode(graph.Node{
		ID:       graph.NodeID(graph.TypeNexusOperation, name),
		Type:     graph.TypeNexusOperation,
		Name:     name,
		Location: location,
	})
	a.graph.AddEdge(graph.Edge{
		From:     ownerID,
		To:       graph.NodeID(graph.TypeNexusOperation, name),
		Kind:     graph.EdgeNexusOperation,
		Location: location,
	})
	return true
}

// enqueueLocalCallee pushes a selector-call callee onto the walk stack when
// its body is in the analyzed packages.
func (a *analyzer) enqueueLocalCallee(info *types.Info, sel *ast.SelectorExpr, stack []*types.Func) []*types.Func {
	obj := SelectorObject(info, sel)
	if fn, ok := obj.(*types.Func); ok {
		return a.enqueueCallee(fn, stack)
	}
	return stack
}

// enqueueIdentCallee pushes a plain-identifier callee onto the walk stack.
func (a *analyzer) enqueueIdentCallee(info *types.Info, ident *ast.Ident, stack []*types.Func) []*types.Func {
	if fn, ok := info.Uses[ident].(*types.Func); ok {
		return a.enqueueCallee(fn, stack)
	}
	return stack
}

// enqueueCallee appends a callee with a body in the analyzed packages. SDK
// workflow-package functions are call shapes, not callees to recurse into.
func (a *analyzer) enqueueCallee(fn *types.Func, stack []*types.Func) []*types.Func {
	if fn.Pkg() != nil && fn.Pkg().Path() == SDKWorkflowPkgPath {
		return stack
	}
	if _, ok := a.loaded.declOf(fn); ok {
		return append(stack, fn)
	}
	return stack
}

// nameArgFunc resolves a folded name argument back to its function object, so
// the target node can carry the function's declaration location.
func nameArgFunc(loaded *Loaded, info *types.Info, expr ast.Expr) (*types.Func, bool) {
	switch e := unwrapParen(expr).(type) {
	case *ast.Ident:
		if fn, ok := info.Uses[e].(*types.Func); ok {
			return fn, true
		}
	case *ast.SelectorExpr:
		if fn, ok := SelectorObject(info, e).(*types.Func); ok {
			return fn, true
		}
	default:
		// Not a function reference.
	}
	return nil, false
}

// declLocation renders a function's declaration location, or "" when unknown.
func declLocation(loaded *Loaded, fn *types.Func) string {
	if fn == nil {
		return ""
	}
	entry, ok := loaded.declOf(fn)
	if !ok {
		return ""
	}
	return FormatLocation(loaded.Fset, loaded.ModuleRoot, entry.decl.Pos())
}

// relTargetDir renders the user-facing target directory relative to the
// module root (usually "."), falling back to the absolute path.
func relTargetDir(dir string) string {
	moduleRoot, err := ModuleRootFor(dir)
	if err != nil {
		return dir
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	if abs == moduleRoot {
		return "."
	}
	rel, err := filepath.Rel(moduleRoot, abs)
	if err != nil {
		return dir
	}
	return filepath.ToSlash(rel)
}

// sortedByPath returns packages ordered by path for deterministic walks.
func sortedByPath(pkgs []*packages.Package) []*packages.Package {
	sorted := append([]*packages.Package(nil), pkgs...)
	slices.SortFunc(sorted, func(a, b *packages.Package) int { return strings.Compare(a.PkgPath, b.PkgPath) })
	return sorted
}
