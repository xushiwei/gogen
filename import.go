/*
 Copyright 2021 The GoPlus Authors (goplus.org)
 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at
     http://www.apache.org/licenses/LICENSE-2.0
 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package gox

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"strconv"
	"sync"

	"golang.org/x/tools/go/packages"
)

// ----------------------------------------------------------------------------

// Ref type
type Ref = types.Object

// PkgRef type is a subset of golang.org/x/tools/go/packages.Package
type PkgRef struct {
	// ID is a unique identifier for a package,
	// in a syntax provided by the underlying build system.
	ID string

	name string // may be different from Types.Name(), if it conflicts with other packages

	// Types provides type information for the package.
	// The NeedTypes LoadMode bit sets this field for packages matching the
	// patterns; type information for dependencies may be missing or incomplete,
	// unless NeedDeps and NeedImports are also set.
	Types *types.Package

	file *file // to import packages anywhere
	pkg  *Package

	// IllTyped indicates whether the package or any dependency contains errors.
	// It is set only when Types is set.
	IllTyped bool

	inTestingFile bool // this package is refered in a testing file.
	isForceUsed   bool // this package is force-used

	isUsed   bool
	nameRefs []*ast.Ident // for internal use
}

func (p *PkgRef) markUsed(v *ast.Ident) {
	if p.isUsed {
		return
	}
	for _, ref := range p.nameRefs {
		if ref == v {
			p.isUsed = true
			return
		}
	}
}

// Ref returns the object in this package with the given name if such an
// object exists; otherwise it panics.
func (p *PkgRef) Ref(name string) Ref {
	if o := p.TryRef(name); o != nil {
		return o
	}
	panic(p.Types.Path() + "." + name + " not found")
}

// TryRef returns the object in this package with the given name if such an
// object exists; otherwise it returns nil.
func (p *PkgRef) TryRef(name string) Ref {
	p.EnsureImported()
	return p.Types.Scope().Lookup(name)
}

// MarkForceUsed marks this package is force-used.
func (p *PkgRef) MarkForceUsed() {
	p.isForceUsed = true
}

// EnsureImported ensures this package is imported.
func (p *PkgRef) EnsureImported() {
	if p.Types == nil {
		p.file.endImport(p.pkg, p.inTestingFile)
	}
}

// ----------------------------------------------------------------------------

type loadedPkgs struct {
	loaded map[string]*packages.Package
	mutex  sync.Mutex
}

var (
	gblLoaded = &loadedPkgs{
		loaded: map[string]*packages.Package{},
	}
)

func (p *loadedPkgs) Lookup(pkgPath string) (pkg *packages.Package, ok bool) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	pkg, ok = p.loaded[pkgPath]
	return
}

func (p *loadedPkgs) Add(pkg *packages.Package) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.loaded[pkg.PkgPath] = pkg
}

// ----------------------------------------------------------------------------

const (
	loadTypes = packages.NeedImports | packages.NeedDeps | packages.NeedTypes
	loadModes = loadTypes | packages.NeedName | packages.NeedModule | packages.NeedFiles
)

// internalGetLoadConfig is a internal function. don't use it.
func (p *Package) internalGetLoadConfig(loaded packages.LoadedPkgs) *packages.Config {
	conf := p.conf
	return &packages.Config{
		Loaded:     loaded,
		Mode:       loadModes,
		Context:    conf.Context,
		Logf:       conf.Logf,
		Dir:        conf.Dir,
		Env:        conf.Env,
		BuildFlags: conf.BuildFlags,
		Fset:       conf.Fset,
		ParseFile:  conf.ParseFile,
	}
}

// loadPkgs loads and returns the Go/Go+ packages named by the given pkgPaths.
func loadPkgs(at *Package, importPkgs map[string]*PkgRef, pkgPaths ...string) int {
	conf := at.internalGetLoadConfig(gblLoaded)
	loadPkgs, err := packages.Load(conf, pkgPaths...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if n := packages.PrintErrors(loadPkgs); n > 0 {
		return n
	}
	for _, loadPkg := range loadPkgs {
		if pkg, ok := importPkgs[loadPkg.PkgPath]; ok {
			pkg.ID = loadPkg.ID
			pkg.IllTyped = loadPkg.IllTyped
			pkg.name = loadPkg.Name
			pkg.Types = loadPkg.Types
			if loadPkg.PkgPath != "unsafe" {
				initGopPkg(loadPkg.Types)
			}
		}
	}
	return 0
}

// Import func
func (p *Package) Import(pkgPath string) *PkgRef {
	return p.files[p.testingFile].importPkg(p, pkgPath, p.testingFile != 0)
}

func (p *Package) big() *PkgRef {
	return p.files[p.testingFile].big(p, p.testingFile != 0)
}

func (p *Package) unsafe() *PkgRef {
	return p.files[p.testingFile].unsafe(p, p.testingFile != 0)
}

// ----------------------------------------------------------------------------

type null struct{}
type autoNames struct {
	gbl     *types.Scope
	builtin *types.Scope
	names   map[string]null
	idx     int
}

const (
	goxAutoPrefix = "_autoGo_"
)

func (p *Package) autoName() string {
	p.autoIdx++
	return goxAutoPrefix + strconv.Itoa(p.autoIdx)
}

func (p *Package) newAutoNames() *autoNames {
	return &autoNames{
		gbl:     p.Types.Scope(),
		builtin: p.builtin.Scope(),
		names:   make(map[string]null),
	}
}

func scopeHasName(at *types.Scope, name string) bool {
	if at.Lookup(name) != nil {
		return true
	}
	for i := at.NumChildren(); i > 0; {
		i--
		if scopeHasName(at.Child(i), name) {
			return true
		}
	}
	return false
}

func (p *autoNames) importHasName(name string) bool {
	_, ok := p.names[name]
	return ok
}

func (p *autoNames) hasName(name string) bool {
	return scopeHasName(p.gbl, name) || p.importHasName(name) ||
		p.builtin.Lookup(name) != nil || types.Universe.Lookup(name) != nil
}

func (p *autoNames) RequireName(name string) (ret string, renamed bool) {
	ret = name
	for p.hasName(ret) {
		p.idx++
		ret = name + strconv.Itoa(p.idx)
		renamed = true
	}
	p.names[name] = null{}
	return
}

// ----------------------------------------------------------------------------
