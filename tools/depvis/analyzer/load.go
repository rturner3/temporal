package analyzer

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/tools/go/packages"
)

// funcDeclEntry binds a resolved function object to the AST declaration that
// defines it and the package it belongs to.
type funcDeclEntry struct {
	decl *ast.FuncDecl
	file *ast.File
	pkg  *packages.Package
}

// Loaded is the result of loading a target directory's Go packages, with the
// indexes the analyzer passes need.
type Loaded struct {
	Pkgs       []*packages.Package
	Fset       *token.FileSet
	ModulePath string
	ModuleRoot string

	// funcDecls maps every function and method defined in the loaded
	// packages to its declaration, enabling the intra-module call-graph walk.
	funcDecls map[*types.Func]funcDeclEntry
}

// Load loads the Go packages under dir (as pattern ./... relative to dir,
// which must live inside a Go module) with full syntax and type information.
func Load(dir string) (*Loaded, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving target dir: %w", err)
	}
	if _, err := os.Stat(absDir); err != nil {
		return nil, fmt.Errorf("target dir: %w", err)
	}

	moduleRoot, err := ModuleRootFor(absDir)
	if err != nil {
		return nil, err
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps |
			packages.NeedModule,
		Dir:   absDir,
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("loading packages under %s: %w", absDir, err)
	}

	loaded := &Loaded{
		Fset:       token.NewFileSet(),
		ModuleRoot: moduleRoot,
		funcDecls:  make(map[*types.Func]funcDeclEntry),
	}
	for _, pkg := range pkgs {
		if pkg.Module != nil && loaded.ModulePath == "" {
			loaded.ModulePath = pkg.Module.Path
		}
		for _, err := range pkg.Errors {
			return nil, fmt.Errorf("package %s failed to load: %v", pkg.PkgPath, err)
		}
		loaded.Pkgs = append(loaded.Pkgs, pkg)
	}

	loaded.Fset = pkgs[0].Fset
	loaded.indexFuncDecls()
	return loaded, nil
}

// indexFuncDecls builds the function-object -> declaration index over every
// loaded package, in deterministic package and file order.
func (l *Loaded) indexFuncDecls() {
	for _, pkg := range sortedByPath(l.Pkgs) {
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name == nil {
					continue
				}
				obj, ok := pkg.TypesInfo.Defs[fn.Name].(*types.Func)
				if !ok {
					continue
				}
				l.funcDecls[obj] = funcDeclEntry{decl: fn, file: file, pkg: pkg}
			}
		}
	}
}

// declOf returns the declaration entry for a function object, if its body is
// in the analyzed packages.
func (l *Loaded) declOf(fn *types.Func) (funcDeclEntry, bool) {
	entry, ok := l.funcDecls[fn]
	return entry, ok
}

// importsOf returns the import paths of one file, sorted.
func (l *Loaded) importsOf(file *ast.File) []string {
	paths := make([]string, 0, len(file.Imports))
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		paths = append(paths, path)
	}
	sortStrings(paths)
	return paths
}

// callFullName renders the fully qualified name of a called function object,
// e.g. "database/sql.Open" or "example.com/pkg.(*T).Method".
func callFullName(fn *types.Func) string {
	return fn.FullName()
}
