package gox

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"log"
	"reflect"
	"strconv"
	"strings"

	"github.com/goplus/gox/internal"
)

// ----------------------------------------------------------------------------

var (
	underscore ast.Expr = &ast.Ident{Name: "_"}
)

var (
	identTrue  = ident("true")
	identFalse = ident("false")
	identNil   = ident("nil")
)

func ident(name string) *ast.Ident {
	return &ast.Ident{Name: name}
}

func boolean(v bool) *ast.Ident {
	if v {
		return identTrue
	}
	return identFalse
}

func newField(pkg *Package, name string, typ types.Type) *ast.Field {
	return &ast.Field{
		Names: []*ast.Ident{ident(name)},
		Type:  toType(pkg, typ),
	}
}

func toRecv(pkg *Package, recv *types.Var) *ast.FieldList {
	return &ast.FieldList{List: []*ast.Field{newField(pkg, recv.Name(), recv.Type())}}
}

// -----------------------------------------------------------------------------
// function type

func toFieldList(pkg *Package, t *types.Tuple) []*ast.Field {
	if t == nil {
		return nil
	}
	n := t.Len()
	flds := make([]*ast.Field, n)
	for i := 0; i < n; i++ {
		item := t.At(i)
		name := item.Name()
		var names []*ast.Ident
		if name != "" {
			names = []*ast.Ident{ident(name)}
		}
		typ := toType(pkg, item.Type())
		flds[i] = &ast.Field{Names: names, Type: typ}
	}
	return flds
}

func toVariadic(fld *ast.Field) {
	t, ok := fld.Type.(*ast.ArrayType)
	if !ok {
		panic("TODO: not a slice type")
	}
	fld.Type = &ast.Ellipsis{Elt: t.Elt}
}

func toFuncType(pkg *Package, sig *types.Signature) *ast.FuncType {
	params := toFieldList(pkg, sig.Params())
	results := toFieldList(pkg, sig.Results())
	if sig.Variadic() {
		n := len(params)
		if n == 0 {
			panic("TODO: toFuncType error")
		}
		toVariadic(params[n-1])
	}
	return &ast.FuncType{
		Params:  &ast.FieldList{List: params},
		Results: &ast.FieldList{List: results},
	}
}

// -----------------------------------------------------------------------------

func toType(pkg *Package, typ types.Type) ast.Expr {
retry:
	switch t := typ.(type) {
	case *types.Basic: // bool, int, etc
		return toBasicType(pkg, t)
	case *types.Pointer:
		return &ast.StarExpr{X: toType(pkg, t.Elem())}
	case *types.Named:
		return toNamedType(pkg, t)
	case *types.Interface:
		return toInterface(pkg, t)
	case *types.Slice:
		return toSliceType(pkg, t)
	case *types.Array:
		return toArrayType(pkg, t)
	case *types.Map:
		return toMapType(pkg, t)
	case *types.Chan:
		return toChanType(pkg, t)
	case *types.Signature:
		return toFuncType(pkg, t)
	case *unboundType:
		if t.tBound == nil {
			panic("TODO: unbound type")
		}
		typ = t.tBound
		goto retry
	}
	log.Panicln("TODO: toType -", reflect.TypeOf(typ))
	return nil
}

func toBasicType(pkg *Package, t *types.Basic) ast.Expr {
	if t.Kind() == types.UnsafePointer {
		return toObjectExpr(pkg, pkg.Import("unsafe").Ref("Pointer"))
	}
	if (t.Info() & types.IsUntyped) != 0 {
		panic("unexpected: untyped type")
	}
	return &ast.Ident{Name: t.Name()}
}

func isUntyped(typ types.Type) bool {
	if t, ok := typ.(*types.Basic); ok {
		return (t.Info() & types.IsUntyped) != 0
	}
	return false
}

func toNamedType(pkg *Package, t *types.Named) ast.Expr {
	return toObjectExpr(pkg, t.Obj())
}

func toChanType(pkg *Package, t *types.Chan) ast.Expr {
	return &ast.ChanType{Value: toType(pkg, t.Elem()), Dir: chanDirs[t.Dir()]}
}

var (
	chanDirs = [...]ast.ChanDir{
		types.SendRecv: ast.SEND | ast.RECV,
		types.SendOnly: ast.SEND,
		types.RecvOnly: ast.RECV,
	}
)

func toArrayType(pkg *Package, t *types.Array) ast.Expr {
	var len ast.Expr
	if n := t.Len(); n < 0 {
		len = &ast.Ellipsis{}
	} else {
		len = &ast.BasicLit{Kind: token.INT, Value: strconv.FormatInt(t.Len(), 10)}
	}
	return &ast.ArrayType{Len: len, Elt: toType(pkg, t.Elem())}
}

func toSliceType(pkg *Package, t *types.Slice) ast.Expr {
	return &ast.ArrayType{Elt: toType(pkg, t.Elem())}
}

func toMapType(pkg *Package, t *types.Map) ast.Expr {
	return &ast.MapType{Key: toType(pkg, t.Key()), Value: toType(pkg, t.Elem())}
}

func toInterface(pkg *Package, t *types.Interface) ast.Expr {
	var flds []*ast.Field
	for i, n := 0, t.NumExplicitMethods(); i < n; i++ {
		fn := t.ExplicitMethod(i)
		name := ident(fn.Name())
		typ := toFuncType(pkg, fn.Type().(*types.Signature))
		fld := &ast.Field{Names: []*ast.Ident{name}, Type: typ}
		flds = append(flds, fld)
	}
	for i, n := 0, t.NumEmbeddeds(); i < n; i++ {
		panic("TODO: interface embedded")
	}
	return &ast.InterfaceType{Methods: &ast.FieldList{List: flds}}
}

// -----------------------------------------------------------------------------
// expression

func toExpr(pkg *Package, val interface{}) internal.Elem {
	if val == nil {
		return internal.Elem{
			Val:  identNil,
			Type: types.Typ[types.UntypedNil],
		}
	}
	switch v := val.(type) {
	case *ast.BasicLit:
		return internal.Elem{
			Val:  v,
			Type: types.Typ[toBasicKind(v.Kind)],
			CVal: constant.MakeFromLiteral(v.Value, v.Kind, 0),
		}
	case *types.Builtin:
		if o := pkg.builtin.Scope().Lookup(v.Name()); o != nil {
			return toObject(pkg, o)
		}
		log.Panicln("TODO: unsupported builtin -", v.Name())
	case *types.TypeName:
		if typ := v.Type(); isType(typ) {
			return internal.Elem{Val: toType(pkg, typ), Type: NewTypeType(typ)}
		} else {
			return toObject(pkg, v)
		}
	case types.Object:
		return toObject(pkg, v)
	case *Element:
		return *v
	case int:
		return internal.Elem{
			Val:  &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(v)},
			Type: types.Typ[types.UntypedInt],
			CVal: constant.MakeInt64(int64(v)),
		}
	case string:
		return internal.Elem{
			Val:  &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(v)},
			Type: types.Typ[types.UntypedString],
			CVal: constant.MakeString(v),
		}
	case bool:
		return internal.Elem{
			Val:  boolean(v),
			Type: types.Typ[types.UntypedBool],
			CVal: constant.MakeBool(v),
		}
	case rune:
		return internal.Elem{
			Val:  &ast.BasicLit{Kind: token.CHAR, Value: strconv.QuoteRune(v)},
			Type: types.Typ[types.UntypedRune],
			CVal: constant.MakeInt64(int64(v)),
		}
	case float64:
		return internal.Elem{
			Val:  &ast.BasicLit{Kind: token.FLOAT, Value: strconv.FormatFloat(v, 'g', -1, 64)},
			Type: types.Typ[types.UntypedFloat],
			CVal: constant.MakeFloat64(v),
		}
	}
	panic("TODO: toExpr")
}

func toBasicKind(tok token.Token) types.BasicKind {
	return tok2BasicKinds[tok]
}

var (
	tok2BasicKinds = [...]types.BasicKind{
		token.INT:    types.UntypedInt,
		token.STRING: types.UntypedString,
		token.CHAR:   types.UntypedRune,
		token.FLOAT:  types.UntypedFloat,
		token.IMAG:   types.UntypedComplex,
	}
)

func toObject(pkg *Package, v types.Object) internal.Elem {
	return internal.Elem{
		Val:  toObjectExpr(pkg, v),
		Type: realType(v.Type()),
	}
}

func toObjectExpr(pkg *Package, v types.Object) ast.Expr {
	atPkg, name := v.Pkg(), v.Name()
	if atPkg == nil || atPkg == pkg.Types { // at universe or at this package
		return ident(name)
	}
	if atPkg == pkg.builtin { // at builtin package
		if strings.HasPrefix(name, pkg.prefix) {
			opName := name[len(pkg.prefix):]
			if op, ok := nameToOps[opName]; ok {
				switch op.Arity {
				case 2:
					return &ast.BinaryExpr{Op: op.Tok}
				case 1:
					return &ast.UnaryExpr{Op: op.Tok}
				}
			}
		}
		return ident(name)
	}
	importPkg, ok := pkg.importPkgs[atPkg.Path()]
	if !ok {
		log.Panicln("TODO: package not found -", atPkg.Name(), atPkg.Path())
	}
	x := ident(atPkg.Name())
	importPkg.nameRefs = append(importPkg.nameRefs, x)
	return &ast.SelectorExpr{
		X:   x,
		Sel: ident(v.Name()),
	}
}

type operator struct {
	Tok   token.Token
	Arity int
}

var (
	nameToOps = map[string]operator{
		"Add":    {token.ADD, 2},
		"Sub":    {token.SUB, 2},
		"Mul":    {token.MUL, 2},
		"Quo":    {token.QUO, 2},
		"Rem":    {token.REM, 2},
		"Or":     {token.OR, 2},
		"Xor":    {token.XOR, 2},
		"And":    {token.AND, 2},
		"AndNot": {token.AND_NOT, 2},

		"Lsh": {token.SHL, 2},
		"Rsh": {token.SHR, 2},

		"LT": {token.LSS, 2},
		"LE": {token.LEQ, 2},
		"GT": {token.GTR, 2},
		"GE": {token.GEQ, 2},
		"EQ": {token.EQL, 2},
		"NE": {token.NEQ, 2},

		"Neg":  {token.SUB, 1},
		"Not":  {token.XOR, 1},
		"Recv": {token.ARROW, 1},
	}
)

func toFuncCall(pkg *Package, fn internal.Elem, args []internal.Elem, flags InstrFlags) internal.Elem {
	ret, err := matchFuncCall(pkg, fn, args, flags)
	if err != nil {
		panic(err)
	}
	return ret
}

func unaryOp(tok token.Token, args []internal.Elem) constant.Value {
	if len(args) == 1 {
		if a := args[0].CVal; a != nil {
			return constant.UnaryOp(tok, a, 0) // TODO: prec should not be 0
		}
	}
	return nil
}

func binaryOp(tok token.Token, args []internal.Elem) constant.Value {
	if len(args) == 2 {
		if a, b := args[0].CVal, args[1].CVal; a != nil && b != nil {
			return constant.BinaryOp(a, tok, b)
		}
	}
	return nil
}

func getParamLen(sig *types.Signature) int {
	n := sig.Params().Len()
	if sig.Recv() != nil {
		n++
	}
	return n
}

func getParam(sig *types.Signature, i int) *types.Var {
	if sig.Recv() != nil {
		i--
	}
	if i < 0 {
		return sig.Recv()
	}
	return sig.Params().At(i)
}

func getParam1st(sig *types.Signature) int {
	if sig.Recv() != nil {
		return 1
	}
	return 0
}

func matchFuncCall(pkg *Package, fn internal.Elem, args []internal.Elem, flags InstrFlags) (ret internal.Elem, err error) {
	var it *instantiated
	var sig *types.Signature
	var cval constant.Value
	switch t := fn.Type.(type) {
	case *types.Signature:
		sig = t
	case *TypeType: // type convert
		valArgs := make([]ast.Expr, len(args))
		for i, v := range args { // TODO: type check
			valArgs[i] = v.Val
		}
		ret = internal.Elem{
			Val:  &ast.CallExpr{Fun: fn.Val, Args: valArgs, Ellipsis: flags & InstrFlagEllipsis},
			Type: t.Type(),
		}
		return
	case *TemplateSignature: // template function
		sig, it = t.instantiate()
		if (t.tokFlag & tokUnaryFlag) != 0 {
			cval = unaryOp(t.tokFlag&^tokUnaryFlag, args)
		} else if t.tokFlag != 0 {
			cval = binaryOp(t.tokFlag, args)
		}
	case *overloadFuncType:
		for _, o := range t.funcs {
			if ret, err = matchFuncCall(pkg, toObject(pkg, o), args, flags); err == nil {
				return
			}
		}
		return
	case *instructionType:
		return t.instr.Call(pkg, args, flags)
	default:
		log.Panicln("TODO: call to non function -", t)
	}
	if err = matchFuncType(pkg, args, (flags&InstrFlagEllipsis) != token.NoPos, sig); err != nil {
		log.Println("matchFuncType:", args[0].Type.(*unboundType).tBound, err) // TODO: remove (debug only)
		return
	}
	tyRet := toRetType(sig.Results(), it)
	switch t := fn.Val.(type) {
	case *ast.BinaryExpr:
		t.X, t.Y = args[0].Val, args[1].Val
		return internal.Elem{Val: t, Type: tyRet, CVal: cval}, nil
	case *ast.UnaryExpr:
		t.X = args[0].Val
		return internal.Elem{Val: t, Type: tyRet, CVal: cval}, nil
	}
	var valArgs []ast.Expr
	var recv = getParam1st(sig)
	if n := len(args); n > recv { // for method, args[0] is already in fn.Val
		valArgs = make([]ast.Expr, n-recv)
		for i := recv; i < n; i++ {
			valArgs[i-recv] = args[i].Val
		}
	}
	return internal.Elem{
		Val:  &ast.CallExpr{Fun: fn.Val, Args: valArgs, Ellipsis: flags & InstrFlagEllipsis},
		Type: tyRet, CVal: cval,
	}, nil
}

func toRetType(t *types.Tuple, it *instantiated) types.Type {
	if t == nil {
		return nil
	} else if t.Len() == 1 {
		return it.normalize(t.At(0).Type())
	}
	return it.normalizeTuple(t)
}

func matchFuncType(pkg *Package, args []internal.Elem, ellipsis bool, sig *types.Signature) error {
	n := len(args)
	if sig.Variadic() {
		if !ellipsis {
			n1 := getParamLen(sig) - 1
			if n < n1 {
				return errors.New("TODO: not enough function parameters")
			}
			tyVariadic, ok := getParam(sig, n1).Type().(*types.Slice)
			if !ok {
				return errors.New("TODO: tyVariadic not a slice")
			}
			if err := matchFuncArgs(pkg, args[:n1], sig); err != nil {
				return err
			}
			return matchElemType(pkg, args[n1:], tyVariadic.Elem())
		}
	} else if ellipsis {
		return errors.New("TODO: call with ... to non variadic function")
	}
	if nreq := getParamLen(sig); nreq != n {
		return fmt.Errorf("TODO: unmatched function parameters count, requires %v but got %v", nreq, n)
	}
	return matchFuncArgs(pkg, args, sig)
}

func matchFuncArgs(pkg *Package, args []internal.Elem, sig *types.Signature) error {
	for i, arg := range args {
		if err := matchType(pkg, arg.Type, getParam(sig, i).Type()); err != nil {
			return err
		}
	}
	return nil
}

func matchFuncResults(pkg *Package, rets []internal.Elem, results *types.Tuple) error {
	n := len(rets)
	need := results.Len()
	switch n {
	case 0:
		if need > 0 && isUnnamedParams(results) {
			return errors.New("TODO: return without value")
		}
		return nil
	case 1:
		if need > 1 {
			if t, ok := rets[0].Type.(*types.Tuple); ok {
				if n1 := t.Len(); n1 != need {
					return fmt.Errorf("TODO: require %d results, but got %d", need, n1)
				}
				for i := 0; i < need; i++ {
					if err := matchType(pkg, t.At(i).Type(), results.At(i).Type()); err != nil {
						return err
					}
				}
				return nil
			}
		}
	}
	if n == need {
		for i := 0; i < need; i++ {
			if err := matchType(pkg, rets[i].Type, results.At(i).Type()); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("TODO: require %d results, but got %d", need, n)
}

func isUnnamedParams(t *types.Tuple) bool {
	if t == nil {
		return true
	}
	for i, n := 0, t.Len(); i < n; i++ {
		if t.At(i).Name() == "" {
			return true
		}
	}
	return false
}

func matchElemType(pkg *Package, vals []internal.Elem, elt types.Type) error {
	for _, val := range vals {
		if err := matchType(pkg, val.Type, elt); err != nil {
			return err
		}
	}
	return nil
}

func assignMatchType(pkg *Package, varRef types.Type, val types.Type) {
	if rt, ok := varRef.(*refType); ok {
		if err := matchType(pkg, val, rt.typ); err != nil {
			panic(err)
		}
	} else if varRef == nil { // underscore
		// do nothing
	} else {
		panic("TODO: unassignable")
	}
}

func matchType(pkg *Package, arg, param types.Type) error {
	switch t := param.(type) {
	case *unboundType: // variable to bound type
		if t2, ok := arg.(*unboundType); ok {
			log.Println("matchType:", t2.tBound) // TODO: remove (debug only)
			if t2.tBound == nil {
				if t == t2 {
					return nil
				}
				return fmt.Errorf("TODO: can't match two unboundTypes")
			}
			arg = t2.tBound
		}
		if t.tBound == nil {
			arg = types.Default(arg)
			t.boundTo(pkg, arg)
		}
		param = t.tBound
	case *unboundMapElemType:
		if t2, ok := arg.(*unboundType); ok {
			if t2.tBound == nil {
				panic("TODO: don't pass unbound variables")
			}
			arg = t2.tBound
		}
		arg = types.Default(arg)
		mapTy := types.NewMap(types.Default(t.key), arg)
		t.typ.boundTo(pkg, mapTy)
		return nil
	default:
		if isUnboundParam(param) {
			if t, ok := arg.(*unboundType); ok {
				if t.tBound == nil {
					// panic("TODO: don't pass unbound variables as template function params.")
					return nil
				}
				arg = t.tBound
			}
			return boundType(pkg, arg, param)
		}
	}
	if AssignableTo(arg, param) {
		return nil
	}
	return fmt.Errorf("TODO: can't pass %v to %v", arg, param)
}

// -----------------------------------------------------------------------------

func boundElementType(elts []internal.Elem, base, max, step int) types.Type {
	var tBound types.Type
	for i := base; i < max; i += step {
		e := elts[i]
		if tBound == e.Type {
			// nothing to do
		} else if tBound == nil || AssignableTo(tBound, e.Type) {
			tBound = e.Type
		} else if !AssignableTo(e.Type, tBound) {
			return TyEmptyInterface
		}
	}
	return tBound
}

// -----------------------------------------------------------------------------
