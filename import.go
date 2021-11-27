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
	"strings"
	"time"

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

	// Types provides type information for the package.
	// The NeedTypes LoadMode bit sets this field for packages matching the
	// patterns; type information for dependencies may be missing or incomplete,
	// unless NeedDeps and NeedImports are also set.
	Types *types.Package

	file *file // to import packages anywhere
	pkg  *Package

	pkgf *pkgFingerp

	// IllTyped indicates whether the package or any dependency contains errors.
	// It is set only when Types is set.
	IllTyped bool

	inTestingFile bool // this package is refered in a testing file.
	isForceUsed   bool // this package is force-used

	isUsed   bool
	nameRefs []*ast.Ident // for internal use
}

func (p *PkgRef) pkgMod() (mod *packages.Module) {
	if pkgf := p.pkgf; pkgf != nil {
		mod = &packages.Module{}
		mod.Path = p.Types.Path()
		if pkgf.versioned {
			pos := strings.Index(pkgf.fingerp, "@")
			if pos < 0 {
				panic("PkgRef: invalid fingerp - " + pkgf.fingerp)
			}
			path, ver := pkgf.fingerp[:pos], pkgf.fingerp[pos+1:]
			if path == mod.Path {
				mod.Version = ver
				return
			}
			mod.Replace = &packages.Module{Path: path, Version: ver}
		} else if pkgf.localrep {
			mod.Replace = &packages.Module{Path: pkgf.files[0]}
		}
	}
	return
}

// 1) Standard packages: nil
// 2) Packages in module: {files, fingerp}
// 3) External versioned packages: {fingerp=ver, versioned=true}
// 4) Replaced packages with versioned packages: {fingerp=pkgPath@ver, versioned=true}
// 5) Replaced packages with local packages: {files[0], files[1:], fingerp, localrep=true}
//
type pkgFingerp struct {
	files     []string // files to generate fingerprint,when isVersion==true
	fingerp   string   // package code fingerprint, or empty (delay calc)
	updated   bool     // dirty flag is valid
	dirty     bool
	versioned bool
	localrep  bool
}

func newPkgFingerp(at *Package, loadPkg *packages.Package) *pkgFingerp {
	mod := at.loadMod().Module
	if mod == nil { // no modfile
		return nil
	}
	loadMod := loadPkg.Module
	if loadMod == nil { // Standard packages
		return nil
	}
	if mod.Mod.Path == loadMod.Path { // Packages in module
		return &pkgFingerp{files: fileList(loadPkg), updated: true}
	}
	if rep := loadMod.Replace; rep != nil {
		if isLocalRepPkg(rep.Path) { // Replaced packages with local packages
			files := localRepFileList(rep.Path, loadPkg)
			return &pkgFingerp{files: files, updated: true, localrep: true}
		}
		// Replaced packages with versioned packages
		loadMod = rep
	}
	// External versioned packages
	return &pkgFingerp{fingerp: loadMod.Path + "@" + loadMod.Version, versioned: true}
}

func (p *pkgFingerp) getFileList() []string {
	if p != nil {
		if p.localrep {
			return p.files[1:]
		}
		return p.files
	}
	return nil
}

func (p *pkgFingerp) getFingerp() string {
	if p.fingerp == "" {
		files := p.files
		if p.localrep {
			files = files[1:]
		}
		p.fingerp = calcFingerp(files)
	}
	return p.fingerp
}

func (p *pkgFingerp) localChanged() bool {
	if p == nil { // cache not exists
		return true
	}
	if !p.updated {
		p.dirty = calcFingerp(p.files) != p.fingerp
		p.updated = true
	}
	return p.dirty
}

func (p *pkgFingerp) localRepChanged(localDir string) bool {
	if p == nil || !p.localrep { // cache is not a local-replaced package
		return true
	}
	if p.files[0] != localDir { // localDir is changed
		return true
	}
	if !p.updated {
		p.dirty = calcFingerp(p.files[1:]) != p.fingerp
		p.updated = true
	}
	return p.dirty
}

func (p *pkgFingerp) versionChanged(fingerp string) bool {
	if p == nil || !p.versioned { // cache is not a versioned package
		return true
	}
	return p.fingerp != fingerp
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

func makeImports(loadPkg *packages.Package) []string {
	imports := loadPkg.Imports
	if len(imports) == 0 {
		return nil
	}
	ret := make([]string, 0, len(imports))
	for _, pkg := range imports {
		ret = append(ret, pkg.PkgPath)
	}
	return ret
}

func fileList(loadPkg *packages.Package) []string {
	return loadPkg.GoFiles
}

func localRepFileList(localDir string, loadPkg *packages.Package) []string {
	files := make([]string, len(loadPkg.GoFiles)+1)
	files[0] = localDir
	copy(files[1:], loadPkg.GoFiles)
	return files
}

func calcFingerp(files []string) string {
	var gopTime time.Time
	for _, file := range files {
		if fi, err := os.Stat(file); err == nil {
			modTime := fi.ModTime()
			if modTime.After(gopTime) {
				gopTime = modTime
			}
		}
	}
	return strconv.FormatInt(gopTime.UnixNano()/1000, 36)
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
			pkg.Types = loadPkg.Types
			if loadPkg.PkgPath != "unsafe" {
				initGopPkg(loadPkg.Types)
			}
			pkg.pkgf = newPkgFingerp(at, loadPkg)
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
