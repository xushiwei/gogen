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

/*
import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"go/constant"
	"go/token"
	"go/types"
	"io/ioutil"
	"log"
	"math/big"
	"reflect"
	"strconv"
	"sync"

	"golang.org/x/tools/go/packages"
)

type pobj = map[string]interface{}

// ----------------------------------------------------------------------------

func toPersistNamedType(t *types.Named) interface{} {
	o := t.Obj()
	if o.IsAlias() {
		return toPersistType(o.Type())
	}
	ret := pobj{"type": "named", "name": o.Name()}
	if pkg := o.Pkg(); pkg != nil {
		ret["pkg"] = pkg.Path()
	}
	return ret
}

func fromPersistNamedType(ctx *persistPkgCtx, t pobj) *types.Named {
	name := t["name"].(string)
	if v, ok := t["pkg"]; ok {
		return ctx.ref(v.(string), name)
	}
	o := types.Universe.Lookup(name)
	return o.Type().(*types.Named)
}

func toPersistInterface(t *types.Interface) interface{} {
	n := t.NumExplicitMethods()
	methods := make([]persistFunc, n)
	for i := 0; i < n; i++ {
		methods[i] = *toPersistFunc(t.ExplicitMethod(i))
	}
	ret := pobj{"type": "interface", "methods": methods}
	if n := t.NumEmbeddeds(); n > 0 {
		embeddeds := make([]interface{}, n)
		for i := 0; i < n; i++ {
			embeddeds[i] = toPersistType(t.EmbeddedType(i))
		}
		ret["embeddeds"] = embeddeds
	}
	return ret
}

func fromPersistInterface(ctx *persistPkgCtx, t pobj) *types.Interface {
	methods := t["methods"].([]interface{})
	mthds := fromPersistInterfaceMethods(ctx, methods)
	var embeddeds []types.Type
	if v, ok := t["embeddeds"]; ok {
		in := v.([]interface{})
		embeddeds = make([]types.Type, len(in))
		for i, t := range in {
			embeddeds[i] = fromPersistType(ctx, t)
		}
	}
	i := types.NewInterfaceType(mthds, embeddeds)
	ctx.intfs = append(ctx.intfs, i)
	return i
}

func toPersistStruct(t *types.Struct) interface{} {
	n := t.NumFields()
	fields := make([]persistVar, n)
	for i := 0; i < n; i++ {
		fields[i] = toPersistField(t.Field(i))
		fields[i].Tag = t.Tag(i)
	}
	return pobj{"type": "struct", "fields": fields}
}

func fromPersistStruct(ctx *persistPkgCtx, t pobj) *types.Struct {
	in := t["fields"].([]interface{})
	fields := make([]*types.Var, len(in))
	tags := make([]string, len(in))
	for i, v := range in {
		fields[i] = fromPersistField(ctx, v)
		if tag, ok := v.(pobj)["tag"]; ok {
			tags[i] = tag.(string)
		}
	}
	return types.NewStruct(fields, tags)
}

func toPersistSignature(sig *types.Signature) interface{} {
	recv := sig.Recv()
	if recv != nil {
		switch t := recv.Type().(type) {
		case *types.Named:
			if _, ok := t.Underlying().(*types.Interface); ok {
				recv = nil
			}
		case *overloadFuncType, *templateRecvMethodType:
			return nil
		case *types.Interface:
			recv = nil
		}
	}
	params := toPersistVars(sig.Params())
	results := toPersistVars(sig.Results())
	variadic := sig.Variadic()
	ret := pobj{"type": "sig", "params": params, "results": results}
	if recv != nil {
		ret["recv"] = toPersistParam(recv)
	}
	if variadic {
		ret["variadic"] = true
	}
	return ret
}

func fromPersistSignature(ctx *persistPkgCtx, v interface{}) *types.Signature {
	sig := v.(pobj)
	if sig["type"].(string) != "sig" {
		panic("unexpected signature")
	}
	params := fromPersistVars(ctx, sig["params"].([]interface{}))
	results := fromPersistVars(ctx, sig["results"].([]interface{}))
	var variadic bool
	if v, ok := sig["variadic"]; ok {
		variadic = v.(bool)
	}
	var recv *types.Var
	if v, ok := sig["recv"]; ok {
		recv = fromPersistParam(ctx, v)
	}
	return types.NewSignature(recv, params, results, variadic)
}

func toPersistType(typ types.Type) interface{} {
	switch t := typ.(type) {
	case *types.Basic:
		return t.Name()
	case *types.Slice:
		return pobj{"type": "slice", "elem": toPersistType(t.Elem())}
	case *types.Map:
		return pobj{"type": "map", "key": toPersistType(t.Key()), "elem": toPersistType(t.Elem())}
	case *types.Named:
		return toPersistNamedType(t)
	case *types.Pointer:
		return pobj{"type": "ptr", "elem": toPersistType(t.Elem())}
	case *types.Interface:
		return toPersistInterface(t)
	case *types.Struct:
		return toPersistStruct(t)
	case *types.Signature:
		return toPersistSignature(t)
	case *types.Array:
		n := strconv.FormatInt(t.Len(), 10)
		return pobj{"type": "array", "elem": toPersistType(t.Elem()), "len": n}
	case *types.Chan:
		return pobj{"type": "chan", "elem": toPersistType(t.Elem()), "dir": int(t.Dir())}
	default:
		panic("unsupported type - " + t.String())
	}
}

func fromPersistType(ctx *persistPkgCtx, typ interface{}) types.Type {
	switch t := typ.(type) {
	case string:
		if ret := types.Universe.Lookup(t); ret != nil {
			return ret.Type()
		} else if kind, ok := typsUntyped[t]; ok {
			return types.Typ[kind]
		}
	case pobj:
		switch t["type"].(string) {
		case "slice":
			elem := fromPersistType(ctx, t["elem"])
			return types.NewSlice(elem)
		case "map":
			key := fromPersistType(ctx, t["key"])
			elem := fromPersistType(ctx, t["elem"])
			return types.NewMap(key, elem)
		case "named":
			return fromPersistNamedType(ctx, t)
		case "ptr":
			elem := fromPersistType(ctx, t["elem"])
			return types.NewPointer(elem)
		case "interface":
			return fromPersistInterface(ctx, t)
		case "struct":
			return fromPersistStruct(ctx, t)
		case "sig":
			return fromPersistSignature(ctx, t)
		case "array":
			iv, err := strconv.ParseInt(t["len"].(string), 10, 64)
			if err == nil {
				elem := fromPersistType(ctx, t["elem"])
				return types.NewArray(elem, iv)
			}
		case "chan":
			elem := fromPersistType(ctx, t["elem"])
			return types.NewChan(types.ChanDir(t["dir"].(float64)), elem)
		}
	}
	panic("unexpected type")
}

var typsUntyped = map[string]types.BasicKind{
	"untyped int":     types.UntypedInt,
	"untyped float":   types.UntypedFloat,
	"untyped string":  types.UntypedString,
	"untyped bool":    types.UntypedBool,
	"untyped rune":    types.UntypedRune,
	"untyped complex": types.UntypedComplex,
	"untyped nil":     types.UntypedNil,
	"Pointer":         types.UnsafePointer,
}

// ----------------------------------------------------------------------------

func toPersistVal(val constant.Value) interface{} {
	switch v := constant.Val(val).(type) {
	case string:
		return v
	case int64:
		return pobj{"type": "int64", "val": strconv.FormatInt(v, 10)}
	case bool:
		return v
	case *big.Int:
		return pobj{"type": "bigint", "val": v.String()}
	case *big.Rat:
		return pobj{"type": "bigrat", "num": v.Num().String(), "denom": v.Denom().String()}
	default:
		if val.Kind() == constant.Complex {
			re := constant.Real(val)
			im := constant.Imag(val)
			return pobj{"type": "complex", "re": toPersistVal(re), "im": toPersistVal(im)}
		}
		panic("unsupported constant")
	}
}

func fromPersistVal(val interface{}) constant.Value {
	var ret interface{}
	switch v := val.(type) {
	case string:
		ret = v
	case pobj:
		switch typ := v["type"].(string); typ {
		case "int64":
			iv, err := strconv.ParseInt(v["val"].(string), 10, 64)
			if err == nil {
				ret = iv
			}
		case "bigint":
			bval := v["val"].(string)
			if bv, ok := new(big.Int).SetString(bval, 10); ok {
				ret = bv
			}
		case "bigrat":
			num := v["num"].(string)
			denom := v["denom"].(string)
			bnum, ok1 := new(big.Int).SetString(num, 10)
			bdenom, ok2 := new(big.Int).SetString(denom, 10)
			if ok1 && ok2 {
				ret = new(big.Rat).SetFrac(bnum, bdenom)
			}
		case "complex":
			re := fromPersistVal(v["re"])
			im := constant.MakeImag(fromPersistVal(v["im"]))
			return constant.BinaryOp(re, token.ADD, im)
		}
	case bool:
		ret = v
	}
	if ret != nil {
		return constant.Make(ret)
	}
	panic("unsupported constant")
}

// ----------------------------------------------------------------------------

type persistVar struct {
	Name     string      `json:"name,omitempty"`
	Type     interface{} `json:"type"`
	Tag      string      `json:"tag,omitempty"`
	Embedded bool        `json:"embedded,omitempty"`
}

func toPersistParam(v *types.Var) persistVar {
	return persistVar{
		Name: v.Name(),
		Type: toPersistType(v.Type()),
	}
}

func toPersistField(v *types.Var) persistVar {
	return persistVar{
		Name:     v.Name(),
		Type:     toPersistType(v.Type()),
		Embedded: v.Embedded(),
	}
}

func fromPersistVarDecl(ctx *persistPkgCtx, v persistVar) {
	typ := fromPersistType(ctx, v.Type)
	o := types.NewVar(token.NoPos, ctx.pkg, v.Name, typ)
	ctx.scope.Insert(o)
}

func fromPersistParam(ctx *persistPkgCtx, v interface{}) *types.Var {
	obj := v.(pobj)
	var name string
	if o, ok := obj["name"]; ok {
		name = o.(string)
	}
	typ := fromPersistType(ctx, obj["type"])
	return types.NewParam(token.NoPos, ctx.pkg, name, typ)
}

func fromPersistField(ctx *persistPkgCtx, v interface{}) *types.Var {
	obj := v.(pobj)
	var name string
	if o, ok := obj["name"]; ok {
		name = o.(string)
	}
	typ := fromPersistType(ctx, obj["type"])
	_, embedded := obj["embedded"]
	return types.NewField(token.NoPos, ctx.pkg, name, typ, embedded)
}

func toPersistVars(v *types.Tuple) []persistVar {
	n := v.Len()
	vars := make([]persistVar, n)
	for i := 0; i < n; i++ {
		vars[i] = toPersistParam(v.At(i))
	}
	return vars
}

func fromPersistVars(ctx *persistPkgCtx, vars []interface{}) *types.Tuple {
	n := len(vars)
	if n == 0 {
		return nil
	}
	ret := make([]*types.Var, n)
	for i, v := range vars {
		ret[i] = fromPersistParam(ctx, v)
	}
	return types.NewTuple(ret...)
}

type persistConst struct {
	Name string      `json:"name"`
	Type interface{} `json:"type"`
	Val  interface{} `json:"val"`
}

func toPersistConst(v *types.Const) persistConst {
	return persistConst{
		Name: v.Name(),
		Type: toPersistType(v.Type()),
		Val:  toPersistVal(v.Val()),
	}
}

func fromPersistConst(ctx *persistPkgCtx, v persistConst) {
	typ := fromPersistType(ctx, v.Type)
	val := fromPersistVal(v.Val)
	o := types.NewConst(token.NoPos, ctx.pkg, v.Name, typ, val)
	ctx.scope.Insert(o)
}

// ----------------------------------------------------------------------------

type persistFunc struct {
	Name string      `json:"name"`
	Sig  interface{} `json:"sig"`
}

func toPersistFunc(v *types.Func) *persistFunc {
	sig := toPersistSignature(v.Type().(*types.Signature))
	if sig == nil {
		return nil
	}
	return &persistFunc{Name: v.Name(), Sig: sig}
}

func fromPersistFunc(ctx *persistPkgCtx, v persistFunc) *types.Func {
	sig := fromPersistSignature(ctx, v.Sig)
	return types.NewFunc(token.NoPos, ctx.pkg, v.Name, sig)
}

func fromPersistInterfaceMethod(ctx *persistPkgCtx, fn interface{}) *types.Func {
	in := fn.(pobj)
	sig := fromPersistSignature(ctx, in["sig"])
	return types.NewFunc(token.NoPos, ctx.pkg, in["name"].(string), sig)
}

func fromPersistInterfaceMethods(ctx *persistPkgCtx, funcs []interface{}) []*types.Func {
	mthds := make([]*types.Func, len(funcs))
	for i, fn := range funcs {
		mthds[i] = fromPersistInterfaceMethod(ctx, fn)
	}
	return mthds
}

func fromPersistMethods(ctx *persistPkgCtx, t *types.Named, funcs []persistFunc) {
	for _, fn := range funcs {
		ret := fromPersistFunc(ctx, fn)
		t.AddMethod(ret)
	}
}

// ----------------------------------------------------------------------------

type persistNamed struct {
	Name       string        `json:"name"`
	Underlying interface{}   `json:"underlying,omitempty"` // only for named type
	Type       interface{}   `json:"type,omitempty"`       // only for alias type
	Methods    []persistFunc `json:"methods,omitempty"`
	IsAlias    bool          `json:"alias,omitempty"`
}

func toPersistTypeName(v *types.TypeName) persistNamed {
	if v.IsAlias() {
		typ := toPersistType(v.Type())
		return persistNamed{IsAlias: true, Name: v.Name(), Type: typ}
	}
	var methods []persistFunc
	t := v.Type().(*types.Named)
	if n := t.NumMethods(); n > 0 {
		methods = make([]persistFunc, 0, n)
		for i := 0; i < n; i++ {
			if mthd := toPersistFunc(t.Method(i)); mthd != nil {
				methods = append(methods, *mthd)
			}
		}
	}
	return persistNamed{
		Name:       v.Name(),
		Underlying: toPersistType(t.Underlying()),
		Methods:    methods,
	}
}

func fromPersistTypeName(ctx *persistPkgCtx, v persistNamed) {
	if v.IsAlias {
		typ := fromPersistType(ctx, v.Type)
		o := types.NewTypeName(token.NoPos, ctx.pkg, v.Name, typ)
		ctx.scope.Insert(o)
	} else {
		var o *types.TypeName
		var t *types.Named
		obj := ctx.scope.Lookup(v.Name)
		if obj != nil {
			o = obj.(*types.TypeName)
			t = o.Type().(*types.Named)
		} else {
			o = types.NewTypeName(token.NoPos, ctx.pkg, v.Name, nil)
			t = types.NewNamed(o, nil, nil)
			ctx.scope.Insert(o)
		}
		underlying := fromPersistType(ctx, v.Underlying)
		t.SetUnderlying(underlying)
		fromPersistMethods(ctx, t, v.Methods)
	}
}

// ----------------------------------------------------------------------------

type persistPkgRef struct {
	ID        string         `json:"id"`
	PkgPath   string         `json:"pkgPath"`
	Name      string         `json:"name,omitempty"`
	Vars      []persistVar   `json:"vars,omitempty"`
	Consts    []persistConst `json:"consts,omitempty"`
	Types     []persistNamed `json:"types,omitempty"`
	Funcs     []persistFunc  `json:"funcs,omitempty"`
	Files     []string       `json:"files,omitempty"`
	Imports   []string       `json:"imports,omitempty"`
	Fingerp   string         `json:"fingerp,omitempty"`
	Versioned bool           `json:"versioned,omitempty"`
	LocalRep  bool           `json:"localrep,omitempty"`
}

func toPersistPkg(pkg *PkgRef) *persistPkgRef {
	var (
		vars   []persistVar
		consts []persistConst
		typs   []persistNamed
		funcs  []persistFunc
	)
	pkgTypes := pkg.Types
	scope := pkgTypes.Scope()
	if debugPersistCache {
		log.Println("==> Persist", pkgTypes.Path())
	}
	for _, name := range scope.Names() {
		o := scope.Lookup(name)
		// persist private types because they may be refered by public functions
		if v, ok := o.(*types.TypeName); ok {
			if _, ok := v.Type().(*overloadFuncType); !ok {
				typs = append(typs, toPersistTypeName(v))
			}
			continue
		}
		if !token.IsExported(name) {
			continue
		}
		switch v := o.(type) {
		case *types.Func:
			funcs = append(funcs, *toPersistFunc(v))
		case *types.Const:
			consts = append(consts, toPersistConst(v))
		case *types.Var:
			vars = append(vars, toPersistParam(v))
		default:
			log.Panicln("unexpected object -", reflect.TypeOf(o), o.Name())
		}
	}
	ret := &persistPkgRef{
		ID:      pkg.ID,
		PkgPath: pkgTypes.Path(),
		Name:    pkgTypes.Name(),
		Vars:    vars,
		Types:   typs,
		Funcs:   funcs,
		Consts:  consts,
		Imports: pkg.imports,
	}
	if pkgf := pkg.pkgf; pkgf != nil {
		ret.Fingerp = pkgf.getFingerp()
		ret.Files = pkgf.files
		ret.Versioned = pkgf.versioned
		ret.LocalRep = pkgf.localrep
	}
	return ret
}

func fromPersistPkg(ctx *persistPkgCtx, pkg *persistPkgRef) *PkgRef {
	ctx.pkg = types.NewPackage(pkg.PkgPath, pkg.Name)
	ctx.scope = ctx.pkg.Scope()
	ctx.checks = nil
	ctx.intfs = nil
	var pkgf *pkgFingerp
	if pkg.Fingerp != "" {
		pkgf = &pkgFingerp{
			files: pkg.Files, fingerp: pkg.Fingerp,
			versioned: pkg.Versioned, localrep: pkg.LocalRep,
		}
	}
	ret := &PkgRef{ID: pkg.ID, Types: ctx.pkg, pkgf: pkgf, imports: pkg.Imports}
	ctx.imports[pkg.PkgPath] = ret
	for _, typ := range pkg.Types {
		fromPersistTypeName(ctx, typ)
	}
	ctx.check()
	for _, c := range pkg.Consts {
		fromPersistConst(ctx, c)
	}
	for _, v := range pkg.Vars {
		fromPersistVarDecl(ctx, v)
	}
	for _, fn := range pkg.Funcs {
		o := fromPersistFunc(ctx, fn)
		ctx.scope.Insert(o)
	}
	initGopPkg(ctx.pkg)
	return ret
}

// ----------------------------------------------------------------------------

type persistPkgState struct {
	pkg    *types.Package
	scope  *types.Scope
	checks []*types.TypeName
	intfs  []*types.Interface
}

type persistPkgCtx struct {
	imports map[string]*PkgRef
	from    map[string]*persistPkgRef
	persistPkgState
}

func (ctx *persistPkgCtx) check() {
	for _, c := range ctx.checks {
		if c.Type().Underlying() == nil {
			panic("type not found - " + ctx.pkg.Path() + "." + c.Name())
		}
	}
	for _, i := range ctx.intfs {
		i.Complete()
	}
}

func (ctx *persistPkgCtx) ref(pkgPath, name string) *types.Named {
	pkg, ok := ctx.imports[pkgPath]
	if !ok {
		persist, ok := ctx.from[pkgPath]
		if !ok {
			panic("unexpected: package not found - " + pkgPath)
		}
		old := ctx.persistPkgState
		pkg = fromPersistPkg(ctx, persist)
		ctx.persistPkgState = old
	}
	if o := pkg.Types.Scope().Lookup(name); o != nil {
		return o.Type().(*types.Named)
	}
	if pkg.Types == ctx.pkg { // maybe not loaded
		o := types.NewTypeName(token.NoPos, pkg.Types, name, nil)
		t := types.NewNamed(o, nil, nil)
		ctx.scope.Insert(o)
		ctx.checks = append(ctx.checks, o)
		return t
	}
	panic("type not found: " + pkgPath + "." + name)
}

func fromPersistPkgs(from map[string]*persistPkgRef) map[string]*PkgRef {
	imports := make(map[string]*PkgRef, len(from))
	ctx := &persistPkgCtx{imports: imports, from: from}
	imports["unsafe"] = &PkgRef{
		ID:    "unsafe",
		Types: types.Unsafe,
	}
	for pkgPath, pkg := range from {
		if _, ok := imports[pkgPath]; ok {
			// already loaded
			continue
		}
		imports[pkgPath] = fromPersistPkg(ctx, pkg)
	}
	return imports
}

func toPersistPkgs(imports map[string]*PkgRef) map[string]*persistPkgRef {
	ret := make(map[string]*persistPkgRef, len(imports))
	for pkgPath, pkg := range imports {
		if pkg.Types == types.Unsafe {
			// don't persist unsafe package
			continue
		}
		ret[pkgPath] = toPersistPkg(pkg)
	}
	return ret
}

// ----------------------------------------------------------------------------

const (
	jsonFile = "go.json"
)

func savePkgsCache(file string, imports map[string]*PkgRef) (err error) {
	ret := toPersistPkgs(imports)
	buf := new(bytes.Buffer)
	zipf := zip.NewWriter(buf)
	zipw, _ := zipf.Create(jsonFile)
	err = json.NewEncoder(zipw).Encode(ret)
	if err != nil {
		return
	}
	zipf.Close()
	return ioutil.WriteFile(file, buf.Bytes(), 0666)
}

func loadPkgsCacheFrom(file string) map[string]*PkgRef {
	zipf, err := zip.OpenReader(file)
	if err == nil {
		defer zipf.Close()
		for _, item := range zipf.File {
			if item.Name == jsonFile {
				f, err := item.Open()
				if err == nil {
					defer f.Close()
					var ret map[string]*persistPkgRef
					if err = json.NewDecoder(f).Decode(&ret); err == nil {
						return fromPersistPkgs(ret)
					}
				}
				log.Println("[WARN] loadPkgsCache failed:", err)
				break
			}
		}
	}
	return make(map[string]*PkgRef)
}

// ----------------------------------------------------------------------------

func requirePkg(loaded map[string]*packages.Package, path string) *packages.Package {
	ret, ok := loaded[path]
	if !ok {
		ret = &packages.Package{}
		loaded[path] = ret
	}
	return ret
}

func importsFrom(loaded map[string]*packages.Package, imp *PkgRef) map[string]*packages.Package {
	imports := imp.imports
	if len(imports) == 0 {
		return nil
	}
	ret := make(map[string]*packages.Package, len(imports))
	for _, path := range imports {
		ret[path] = requirePkg(loaded, path)
	}
	return ret
}

func loadedPkgFrom(loaded map[string]*packages.Package, path string, imp *PkgRef) {
	log.Println("====> loadedPkgFrom:", path)
	*requirePkg(loaded, path) = packages.Package{
		ID:       imp.ID,
		Name:     imp.Types.Name(),
		PkgPath:  imp.Types.Path(),
		GoFiles:  imp.pkgf.getFileList(),
		Imports:  importsFrom(loaded, imp),
		Types:    imp.Types,
		Fset:     nil,
		IllTyped: imp.IllTyped,
		Module:   imp.pkgMod(),
	}
}

func loadedPkgsFrom(imports map[string]*PkgRef) map[string]*packages.Package {
	loaded := map[string]*packages.Package{}
	for path, imp := range imports {
		loadedPkgFrom(loaded, path, imp)
	}
	return loaded
}

/*
// LoadGoPkgs loads and returns the Go packages named by the given pkgPaths.
func LoadGoPkgs(at *Package, importPkgs map[string]*PkgRef, pkgPaths ...string) int {
	conf := at.InternalGetLoadConfig(nil)
	loadPkgs, err := packages.Load(conf, pkgPaths...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if n := packages.PrintErrors(loadPkgs); n > 0 {
		return n
	}
	for _, loadPkg := range loadPkgs {
		LoadGoPkg(at, importPkgs, loadPkg)
	}
	return 0
}

func LoadGoPkg(at *Package, imports map[string]*PkgRef, loadPkg *packages.Package) {
	for _, impPkg := range loadPkg.Imports {
		if _, ok := imports[impPkg.PkgPath]; ok { // this package is loaded
			continue
		}
		LoadGoPkg(at, imports, impPkg)
	}
	imps := makeImports(loadPkg)
	if debugImport {
		log.Println("==> Import", loadPkg.PkgPath, "- Depends:", imps)
	}
	pkg, ok := imports[loadPkg.PkgPath]
	pkgTypes := loadPkg.Types
	if ok {
		if pkg.ID == "" {
			pkg.ID = loadPkg.ID
			pkg.Types = pkgTypes
			pkg.IllTyped = loadPkg.IllTyped
			pkg.imports = imps
		}
	} else {
		pkg = &PkgRef{
			ID:       loadPkg.ID,
			Types:    pkgTypes,
			IllTyped: loadPkg.IllTyped,
			imports:  imps,
			pkg:      at,
		}
		imports[loadPkg.PkgPath] = pkg
	}
	if loadPkg.PkgPath != "unsafe" {
		initGopPkg(pkgTypes)
	}
	pkg.pkgf = newPkgFingerp(at, loadPkg)
}

type LoadPkgsCached struct {
	loaded    *loadedPkgs
	pkgsLoad  func(cfg *packages.Config, patterns ...string) ([]*packages.Package, error)
	cacheFile string
}

func (p *LoadPkgsCached) Load(at *Package, importPkgs map[string]*PkgRef, pkgPaths ...string) int {
	var nretry int
	var unimportedPaths []string
retry:
	for _, pkgPath := range pkgPaths {
		if loadPkg, ok := p.imported(at, pkgPath); ok {
			if pkg, ok := importPkgs[pkgPath]; ok {
				typs := *loadPkg.Types
				pkg.ID = loadPkg.ID
				pkg.Types = &typs // clone *types.Package instance -- TODO: maybe have bugs
				pkg.IllTyped = loadPkg.IllTyped
			}
		} else {
			unimportedPaths = append(unimportedPaths, pkgPath)
		}
	}
	if len(unimportedPaths) > 0 {
		if nretry > 1 {
			log.Println("Load packages too many times:", unimportedPaths)
			return len(unimportedPaths)
		}
		conf := at.InternalGetLoadConfig(p.loaded)
		loadPkgs, err := p.pkgsLoad(conf, unimportedPaths...)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if n := packages.PrintErrors(loadPkgs); n > 0 {
			return n
		}
		for _, loadPkg := range loadPkgs {
			LoadGoPkg(at, p.imports, loadPkg)
		}
		pkgPaths, unimportedPaths = unimportedPaths, nil
		nretry++
		goto retry
	}
	return 0
}

func (p *LoadPkgsCached) changed(at *Package, pkg *PkgRef, pkgPath string) bool {
	pkgf := pkg.pkgf
	m := at.loadMod()
	pt := m.getPkgType(pkgPath)
	switch pt {
	case ptStandardPkg:
		return false
	case ptModulePkg:
		return pkgf.localChanged()
	}
	dep, ok := m.lookupDep(pkgPath)
	if !ok {
		log.Println("[WARN] Imported package is not in modfile:", pkgPath)
		return false
	}
	if isLocalRepPkg(dep.replace) {
		return pkgf.localRepChanged(dep.replace)
	}
	return pkgf.versionChanged(dep.calcFingerp())
}

func (p *LoadPkgsCached) imported(at *Package, pkgPath string) (pkg *PkgRef, ok bool) {
	if pkg, ok = p.imports[pkgPath]; ok {
		if p.changed(at, pkg, pkgPath) {
			log.Println("==> LoadPkgsCached: package changed -", pkgPath)
			delete(p.imports, pkgPath)
			return nil, false
		}
	}
	return
}

func (p *LoadPkgsCached) Save() error {
	if p.cacheFile == "" {
		return nil
	}
	return savePkgsCache(p.cacheFile, p.imports)
}

// NewLoadPkgsCached returns a cached pkgLoader.
func NewLoadPkgsCached(
	load func(cfg *packages.Config, patterns ...string) ([]*packages.Package, error)) LoadPkgsFunc {
	if load == nil {
		load = packages.Load
	}
	imports := make(map[string]*PkgRef)
	return (&LoadPkgsCached{imports: imports, loaded: loaded, pkgsLoad: load}).Load
}

// OpenLoadPkgsCached opens cache file and returns the cached pkgLoader.
func OpenLoadPkgsCached(
	file string, load func(cfg *packages.Config, patterns ...string) ([]*packages.Package, error)) *LoadPkgsCached {
	if load == nil {
		load = packages.Load
	}
	imports := loadPkgsCacheFrom(file)
	loaded := &loadedPkgs{
		loaded: loadedPkgsFrom(imports),
	}
	return &LoadPkgsCached{imports: imports, loaded: loaded, pkgsLoad: load, cacheFile: file}
}
*/

// ----------------------------------------------------------------------------
/*

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

func (p *Package) loadMod() *module {
	if p.mod == nil {
		modRootDir := p.conf.ModRootDir
		if modRootDir != "" {
			p.mod = loadModFile(filepath.Join(modRootDir, "go.mod"))
		}
		if p.mod == nil {
			p.mod = &module{deps: map[string]*pkgdep{}}
		}
	}
	return p.mod
}

// ----------------------------------------------------------------------------

type pkgdep struct {
	path    string
	replace string
}

func (p *pkgdep) calcFingerp() string {
	if p.replace != "" {
		return p.replace
	}
	return p.path
}

type module struct {
	*modfile.Module
	deps map[string]*pkgdep
}

func (p *module) lookupDep(pkgPath string) (dep *pkgdep, ok bool) {
	for modPath, dep := range p.deps {
		if isPkgInModule(pkgPath, modPath) {
			return dep, true
		}
	}
	return
}

func isPkgInModule(pkgPath, modPath string) bool {
	if strings.HasPrefix(pkgPath, modPath) {
		suffix := pkgPath[len(modPath):]
		return suffix == "" || suffix[0] == '/'
	}
	return false
}

type pkgType int

const (
	ptStandardPkg pkgType = iota
	ptModulePkg
	ptLocalPkg
	ptExternPkg
	ptInvalidPkg = -1
)

func (p *module) getPkgType(pkgPath string) pkgType {
	if pkgPath == "" {
		return ptInvalidPkg
	}
	if p.Module != nil {
		if isPkgInModule(pkgPath, p.Module.Mod.Path) {
			return ptModulePkg
		}
	}
	c := pkgPath[0]
	if c == '/' || c == '.' {
		return ptLocalPkg
	}
	pos := strings.Index(pkgPath, "/")
	if pos > 0 {
		pkgPath = pkgPath[:pos]
	}
	if strings.Contains(pkgPath, ".") {
		return ptExternPkg
	}
	return ptStandardPkg
}

func loadModFile(file string) (m *module) {
	src, err := ioutil.ReadFile(file)
	if err != nil {
		log.Println("Modfile not found:", file)
		return
	}
	f, err := modfile.Parse(file, src, nil)
	if err != nil {
		log.Println("modfile.Parse:", err)
		return
	}
	deps := map[string]*pkgdep{}
	for _, v := range f.Require {
		deps[v.Mod.Path] = &pkgdep{
			path: v.Mod.String(),
		}
	}
	for _, v := range f.Replace {
		if dep, ok := deps[v.Old.Path]; ok {
			dep.replace = v.New.String()
		}
	}
	return &module{deps: deps, Module: f.Module}
}

func isLocalRepPkg(replace string) bool {
	if replace == "" {
		return false
	}
	c := replace[0]
	return c == '/' || c == '.'
}
*/

// ----------------------------------------------------------------------------
