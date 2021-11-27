package gox

import (
	"go/token"
	"go/types"
	"log"
	"strings"
)

// ----------------------------------------------------------------------------

func initGopPkg(pkg *types.Package) {
	if pkg.Scope().Lookup("GopPackage") == nil { // not is a Go+ package
		return
	}
	type omthd struct {
		named *types.Named
		mthd  string
	}
	scope := pkg.Scope()
	overloads := make(map[string][]types.Object)
	moverloads := make(map[omthd][]types.Object)
	names := scope.Names()
	for _, name := range names {
		o := scope.Lookup(name)
		if n := len(name); n > 3 && name[n-3:n-1] == "__" { // overload function
			key := name[:n-3]
			overloads[key] = append(overloads[key], o)
		} else if named, ok := o.Type().(*types.Named); ok {
			for i, n := 0, named.NumMethods(); i < n; i++ {
				m := named.Method(i)
				mName := m.Name()
				if n := len(mName); n > 3 && mName[n-3:n-1] == "__" { // overload method
					mthd := mName[:n-3]
					key := omthd{named, mthd}
					moverloads[key] = append(moverloads[key], m)
				}
			}
		} else {
			checkTemplateMethod(pkg, name, o)
		}
	}
	for key, items := range overloads {
		off := len(key) + 2
		fns := overloadFuncs(off, items)
		if debugImport {
			log.Println("==> NewOverloadFunc", key)
		}
		o := NewOverloadFunc(token.NoPos, pkg, key, fns...)
		scope.Insert(o)
		checkTemplateMethod(pkg, key, o)
	}
	for key, items := range moverloads {
		off := len(key.mthd) + 2
		fns := overloadFuncs(off, items)
		if debugImport {
			log.Println("==> NewOverloadMethod", key.named.Obj().Name(), key.mthd)
		}
		NewOverloadMethod(key.named, token.NoPos, pkg, key.mthd, fns...)
	}
}

func checkTemplateMethod(pkg *types.Package, name string, o types.Object) {
	const (
		goptPrefix = "Gopt_"
	)
	if strings.HasPrefix(name, goptPrefix) {
		name = name[len(goptPrefix):]
		if pos := strings.Index(name, "_"); pos > 0 {
			tname, mname := name[:pos], name[pos+1:]
			if tobj := pkg.Scope().Lookup(tname); tobj != nil {
				if tn, ok := tobj.(*types.TypeName); ok {
					if t, ok := tn.Type().(*types.Named); ok {
						if debugImport {
							log.Println("==> NewTemplateRecvMethod", tname, mname)
						}
						NewTemplateRecvMethod(t, token.NoPos, pkg, mname, o)
					}
				}
			}
		}
	}
}

func overloadFuncs(off int, items []types.Object) []types.Object {
	fns := make([]types.Object, len(items))
	for _, item := range items {
		idx := toIndex(item.Name()[off])
		if idx >= len(items) {
			log.Panicln("overload function must be from 0 to N:", item.Name(), len(fns))
		}
		if fns[idx] != nil {
			panic("overload function exists?")
		}
		fns[idx] = item
	}
	return fns
}

func toIndex(c byte) int {
	if c >= '0' && c <= '9' {
		return int(c - '0')
	}
	if c >= 'a' && c <= 'z' {
		return int(c - ('a' - 10))
	}
	panic("invalid character out of [0-9,a-z]")
}

// ----------------------------------------------------------------------------
